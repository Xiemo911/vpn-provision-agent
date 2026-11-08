// Package xray manages the installation, service state, and VLESS user list of
// an Xray-core instance on the local host.
package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/system"
)

const defaultConfigPath = "/usr/local/etc/xray/config.json"

// service is Xray's service: the official installer's systemd unit, or on
// OpenRC (Alpine) the init script openRCInstall writes.
var service = system.Service{Systemd: "xray", OpenRC: "xray"}

// The official installer and the Xray release it installs are pinned, so an
// upstream change can't break every install at once. Bump them together
// after testing an install. Naming a version also stops the installer from
// looking up the latest release through GitHub's rate-limited API.
const (
	// InstallScriptURL is XTLS/Xray-install's install-release.sh at a fixed
	// commit.
	InstallScriptURL = "https://raw.githubusercontent.com/XTLS/Xray-install/04e6f3bd9534811ed901a48c47b2c042dd64eb3a/install-release.sh"
	// DefaultVersion is the Xray-core release installed when the control
	// server doesn't ask for one.
	DefaultVersion = "v26.10.31"
)

// installerScript runs the pinned installer with args. It's downloaded to a
// file first: with bash -c "$(curl ...)" a failed download just ran an empty
// script and "succeeded".
func installerScript(args string) string {
	return `set -e
installer=$(mktemp)
trap 'rm -f "$installer"' EXIT
curl -fsSL --retry 3 --connect-timeout 20 --max-time 120 -o "$installer" ` + shellQuote(InstallScriptURL) + `
bash "$installer" ` + args + `
`
}

// Manager performs Xray operations against a single host.
type Manager struct {
	ConfigPath string
}

// New returns a Manager. An empty configPath falls back to the default.
func New(configPath string) *Manager {
	if configPath == "" {
		configPath = defaultConfigPath
	}
	return &Manager{ConfigPath: configPath}
}

// IsInstalled reports whether the xray binary is present.
func (m *Manager) IsInstalled() bool {
	if _, err := exec.LookPath("xray"); err == nil {
		return true
	}
	_, err := os.Stat("/usr/local/bin/xray")
	return err == nil
}

// IsRunning reports whether the xray service is active.
func (m *Manager) IsRunning(ctx context.Context) bool {
	return service.IsActive(ctx)
}

// Version returns the installed Xray version string, or "" if unavailable.
func (m *Manager) Version(ctx context.Context) string {
	res, err := system.Run(ctx, "xray", "version")
	if err != nil {
		return ""
	}
	if line := firstLine(res.Stdout); line != "" {
		return line
	}
	return ""
}

// Install installs Xray. An empty version installs DefaultVersion.
//
// On systemd hosts it runs the official Xray-install script. The tools the
// installer needs are installed first, waiting out the dpkg lock: the
// installer runs apt itself with no retry, so on a fresh VPS still busy
// with its first-boot updates it failed straight away.
//
// The official installer only supports systemd, so on OpenRC (Alpine) the
// release is installed directly instead (see openRCInstall), to the same
// paths.
func (m *Manager) Install(ctx context.Context, version string) (system.Result, error) {
	return system.RunShell(ctx, installScript(version, system.IsOpenRC()))
}

func installScript(version string, openrc bool) string {
	if version == "" {
		version = DefaultVersion
	}
	install := installerScript("install --version " + shellQuote(version))
	if openrc {
		install = openRCInstall(version)
	}
	return system.PkgHelpers + `
progress "Checking the VPS"
need_disk 300
missing=""
for tool in curl unzip gzip; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
if [ -n "$missing" ]; then
  progress "Installing download tools"
  pkg_install $missing || exit 1
fi
need_hosts "Xray" raw.githubusercontent.com github.com
progress "Downloading and installing Xray"
` + install + `
# Marks this Xray as Nullsify's, so a later install on this VPS can tell it
# from someone else's (which it must not overwrite). The installer's
# "remove --purge" deletes it along with the rest of the directory.
mkdir -p ` + markerDir + ` && touch ` + markerDir + `/.nullsify
`
}

// openRCInstall installs an Xray release from GitHub where the official
// installer would (/usr/local/bin/xray, geo files in shareDir, config in
// /usr/local/etc/xray), checking the download against the release's .dgst
// checksums, and writes an OpenRC init script equivalent to the
// installer's systemd unit: Xray runs as nobody, allowed to bind ports
// below 1024.
func openRCInstall(version string) string {
	return `set -e
case "$(uname -m)" in
  x86_64|amd64) asset=Xray-linux-64.zip ;;
  aarch64|arm64) asset=Xray-linux-arm64-v8a.zip ;;
  *) fail "This VPS's CPU architecture ($(uname -m)) isn't supported. Use a 64-bit (x86-64 or ARM64) VPS." ;;
esac
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
url="https://github.com/XTLS/Xray-core/releases/download/"` + shellQuote(version) + `"/$asset"
curl -fsSL --retry 3 --connect-timeout 20 --max-time 300 -o "$tmp/xray.zip" "$url" &&
  curl -fsSL --retry 3 --connect-timeout 20 --max-time 60 -o "$tmp/xray.zip.dgst" "$url.dgst" ||
  fail "Couldn't download Xray from github.com. Try again in a few minutes."
want=$(awk -F '= ' '/256=/ {print $2; exit}' "$tmp/xray.zip.dgst")
got=$(sha256sum "$tmp/xray.zip" | awk '{print $1}')
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  fail "The Xray download from github.com didn't match its checksum. Try again in a few minutes."
fi
mkdir -p "$tmp/x" ` + shareDir + ` ` + markerDir + `
unzip -q -o "$tmp/xray.zip" -d "$tmp/x"
install -m 0755 "$tmp/x/xray" /usr/local/bin/xray
for f in geoip.dat geosite.dat; do
  if [ -f "$tmp/x/$f" ]; then install -m 0644 "$tmp/x/$f" ` + shareDir + `/$f; fi
done
if [ ! -f ` + defaultConfigPath + ` ]; then echo '{}' > ` + defaultConfigPath + `; fi
cat > /etc/init.d/xray <<'INIT'
` + openRCInitScript + `INIT
chmod 0755 /etc/init.d/xray
`
}

// shareDir holds Xray's geoip/geosite files; Xray looks there by default.
const shareDir = "/usr/local/share/xray"

const openRCInitScript = `#!/sbin/openrc-run
name="xray"
description="Xray"
command="/usr/local/bin/xray"
command_args="run -config ` + defaultConfigPath + `"
command_user="nobody:nobody"
capabilities="^cap_net_bind_service"
supervisor=supervise-daemon
respawn_delay=5
respawn_max=0

depend() {
  need net
  after firewall
}
`

// OpenRCUninstallScript removes what openRCInstall installed, config
// included (like the official installer's "remove --purge").
const OpenRCUninstallScript = `rc-service xray stop >/dev/null 2>&1 || true
rc-update del xray default >/dev/null 2>&1 || true
rm -f /etc/init.d/xray /usr/local/bin/xray
rm -rf ` + shareDir + ` ` + markerDir + ` /var/log/xray
`

// markerDir holds the marker file that says Nullsify installed this Xray;
// the control server's pre-install check looks for it.
const markerDir = "/usr/local/etc/xray"

// Uninstall removes Xray: via the official installer, or on OpenRC with
// OpenRCUninstallScript.
func (m *Manager) Uninstall(ctx context.Context) (system.Result, error) {
	if system.IsOpenRC() {
		return system.RunShell(ctx, OpenRCUninstallScript)
	}
	return system.RunShell(ctx, installerScript("remove --purge"))
}

// Restart restarts (and enables) the xray service, and confirms it actually
// reaches the active state rather than trusting the init system's exit
// code alone (a Type=simple unit can report a successful restart and then
// crash-loop).
func (m *Manager) Restart(ctx context.Context) (system.Result, error) {
	res, err := service.EnableAndRestart(ctx)
	if err != nil {
		return res, err
	}
	if !m.waitRunning(ctx) {
		return res, fmt.Errorf("restart xray: service did not reach active state")
	}
	return res, nil
}

// waitRunning polls IsRunning for a few seconds to give the service time to
// come up (or crash) after a restart before reporting success.
func (m *Manager) waitRunning(ctx context.Context) bool {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m.IsRunning(ctx) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// AddUser adds a VLESS client to the target inbound (empty tag = first VLESS
// inbound). If client.ID is empty a new UUID is generated and returned. The
// resulting config is validated with `xray -test` before being kept; a bad
// result is rolled back so a malformed add can't break a running xray.
func (m *Manager) AddUser(ctx context.Context, inboundTag string, client Client) (Client, error) {
	if client.ID == "" {
		id, err := NewUUID()
		if err != nil {
			return client, err
		}
		client.ID = id
	}
	err := modifyClients(ctx, m.ConfigPath, inboundTag, func(clients []Client) ([]Client, error) {
		for _, c := range clients {
			if c.ID == client.ID {
				return nil, fmt.Errorf("client id %s already exists", client.ID)
			}
			if client.Email != "" && c.Email == client.Email {
				return nil, fmt.Errorf("client email %s already exists", client.Email)
			}
		}
		return append(clients, client), nil
	})
	return client, err
}

// RemoveUser deletes clients matching idOrEmail from the target inbound.
// It returns the number of clients removed. See AddUser for the validation
// and rollback behavior applied to the written config.
func (m *Manager) RemoveUser(ctx context.Context, inboundTag, idOrEmail string) (int, error) {
	removed := 0
	err := modifyClients(ctx, m.ConfigPath, inboundTag, func(clients []Client) ([]Client, error) {
		kept := clients[:0:0]
		for _, c := range clients {
			if c.ID == idOrEmail || (c.Email != "" && c.Email == idOrEmail) {
				removed++
				continue
			}
			kept = append(kept, c)
		}
		if removed == 0 {
			return nil, fmt.Errorf("no client matching %q", idOrEmail)
		}
		return kept, nil
	})
	return removed, err
}

// ListUsers returns the VLESS clients of the target inbound.
func (m *Manager) ListUsers(inboundTag string) ([]Client, error) {
	return listClients(m.ConfigPath, inboundTag)
}

// ApplyConfig validates and installs a full replacement Xray config. The
// previous config is backed up and restored if the new one fails validation.
func (m *Manager) ApplyConfig(ctx context.Context, raw json.RawMessage) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("invalid config json: %w", err)
	}

	backup, hadOld := "", false
	if old, err := os.ReadFile(m.ConfigPath); err == nil {
		backup = m.ConfigPath + ".bak"
		hadOld = true
		if err := writeFileAtomic(backup, old); err != nil {
			return fmt.Errorf("backup existing config: %w", err)
		}
	}

	pretty, _ := json.MarshalIndent(probe, "", "  ")
	if err := writeFileAtomic(m.ConfigPath, pretty); err != nil {
		return err
	}

	res, err := system.Run(ctx, "xray", "-test", "-config", m.ConfigPath)
	if err != nil {
		if hadOld {
			if old, rerr := os.ReadFile(backup); rerr == nil {
				_ = writeFileAtomic(m.ConfigPath, old)
			}
		}
		return fmt.Errorf("config failed validation: %s", res.Combined())
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
