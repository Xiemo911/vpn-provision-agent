// Package amneziawg manages the installation, service state, and config of an
// AmneziaWG (awg-quick/amneziawg-go) instance on the local host. It mirrors
// the shape of internal/xray, but AmneziaWG's config is a plain INI text file
// rather than JSON, and the "install" surface is a userspace Go daemon plus
// the awg/awg-quick control tools instead of a single prebuilt binary.
package amneziawg

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/system"
)

const (
	defaultConfigPath = "/etc/amnezia/amneziawg/awg0.conf"
	ifaceName         = "awg0"
	systemdUnit       = "awg-quick@" + ifaceName
)

// Manager performs AmneziaWG operations against a single host.
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

// IsInstalled reports whether the awg and awg-quick tools are present.
func (m *Manager) IsInstalled() bool {
	_, errAwg := exec.LookPath("awg")
	_, errQuick := exec.LookPath("awg-quick")
	return errAwg == nil && errQuick == nil
}

// IsRunning reports whether the awg-quick@awg0 systemd service is active.
func (m *Manager) IsRunning(ctx context.Context) bool {
	res, _ := system.Run(ctx, "systemctl", "is-active", systemdUnit)
	return strings.TrimSpace(res.Stdout) == "active"
}

// Version returns the installed awg (control tool) version string, or "" if
// unavailable.
func (m *Manager) Version(ctx context.Context) string {
	res, err := system.Run(ctx, "awg", "--version")
	if err != nil {
		return ""
	}
	return firstLine(res.Stdout)
}

// Pinned upstream versions, so an upstream release can't break every install
// at once. Bump them together after testing an install.
//
// amneziawg-go and amneziawg-tools are the pair installs got before pinning
// (the daemon via "@latest", which resolves to the last pre-v3 release since
// the module moved to .../amneziawg-go/v3; the tools from the default
// branch). Moving to the v3 daemon is a separate, deliberate upgrade.
const (
	goVersion             = "go1.26.10"
	amneziaWGGoVersion    = "v0.2.19"
	amneziaWGToolsVersion = "v3.1.20260812"
)

// Install builds/installs amneziawg-go (userspace implementation) and the
// awg/awg-quick control tools, then registers the awg-quick@.service systemd
// template unit.
func (m *Manager) Install(ctx context.Context) (system.Result, error) {
	return system.RunShell(ctx, installScript())
}

// Uninstall removes AmneziaWG: stops the service, deletes the systemd unit,
// the installed binaries, and the config directory.
func (m *Manager) Uninstall(ctx context.Context) (system.Result, error) {
	return system.RunShell(ctx, uninstallScript)
}

// Restart enables and restarts the awg-quick@awg0 service, and confirms it
// actually reaches the active state rather than trusting systemctl's exit
// code alone.
func (m *Manager) Restart(ctx context.Context) (system.Result, error) {
	if res, err := system.Run(ctx, "systemctl", "enable", systemdUnit); err != nil {
		return res, fmt.Errorf("enable %s: %s", systemdUnit, res.Combined())
	}
	res, err := system.Run(ctx, "systemctl", "restart", systemdUnit)
	if err != nil {
		return res, fmt.Errorf("restart %s: %s", systemdUnit, res.Combined())
	}
	if !m.waitRunning(ctx) {
		return res, fmt.Errorf("restart %s: service did not reach active state", systemdUnit)
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

// ApplyConfig validates and installs a full replacement awg0.conf. The
// previous config is backed up and restored if the new one fails validation.
// It does not restart the service; call Restart afterward if desired.
func (m *Manager) ApplyConfig(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("config is empty")
	}

	backup, hadOld := "", false
	if old, err := readFile(m.ConfigPath); err == nil {
		backup = m.ConfigPath + ".bak"
		hadOld = true
		if err := writeFileAtomic(backup, old, 0o600); err != nil {
			return fmt.Errorf("backup existing config: %w", err)
		}
	}

	if err := writeFileAtomic(m.ConfigPath, []byte(text), 0o600); err != nil {
		return err
	}

	res, err := system.Run(ctx, "awg-quick", "strip", m.ConfigPath)
	if err != nil {
		if hadOld {
			if old, rerr := readFile(backup); rerr == nil {
				_ = writeFileAtomic(m.ConfigPath, old, 0o600)
			}
		}
		return fmt.Errorf("config failed validation: %s", res.Combined())
	}
	return nil
}

// AddPeer appends a rendered "[Peer]" block to the config file and hot-applies
// it via `awg syncconf`, without disrupting existing peers.
func (m *Manager) AddPeer(ctx context.Context, peerBlock string) error {
	if strings.TrimSpace(peerBlock) == "" {
		return fmt.Errorf("peer config is empty")
	}
	if err := appendToFile(m.ConfigPath, "\n"+strings.TrimRight(peerBlock, "\n")+"\n"); err != nil {
		return fmt.Errorf("append peer block: %w", err)
	}

	script := fmt.Sprintf(`awg syncconf %s <(awg-quick strip %s)`, ifaceName, shellQuote(m.ConfigPath))
	res, err := system.RunShell(ctx, script)
	if err != nil {
		return fmt.Errorf("apply peer live: %s", res.Combined())
	}
	return nil
}

// RemovePeer removes a peer live via `awg set ... remove` and strips the
// matching "[Peer]" block from the persisted config file.
func (m *Manager) RemovePeer(ctx context.Context, publicKey string) error {
	if publicKey == "" {
		return fmt.Errorf("public key is required")
	}
	res, err := system.Run(ctx, "awg", "set", ifaceName, "peer", publicKey, "remove")
	if err != nil {
		return fmt.Errorf("remove peer live: %s", res.Combined())
	}
	if err := removePeerBlock(m.ConfigPath, publicKey); err != nil {
		return fmt.Errorf("peer removed live but config file not updated: %w", err)
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

func installScript() string {
	return system.PkgHelpers + `
set -e
export HOME="${HOME:-/root}"

# amneziawg-go is a userspace daemon and needs a TUN device, which OpenVZ and
# many LXC containers only provide once it's enabled in the provider's panel.
modprobe tun >/dev/null 2>&1 || true
if [ ! -c /dev/net/tun ]; then
  echo "This VPS has no TUN device (/dev/net/tun), which AmneziaWG needs. It is usually an OpenVZ or LXC container: enable TUN/TAP in your provider's control panel, or use a KVM VPS." >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

# Build tools, plus what awg-quick needs at runtime: ip (iproute2) to bring
# the interface up and iptables for the NAT rules in the server config.
# Minimal Debian images ship without iptables, and awg-quick up then fails.
if has_apt; then
  pkg_install build-essential libmnl-dev pkg-config git ca-certificates curl iptables iproute2
else
  pkg_install gcc make libmnl-devel pkgconfig git ca-certificates curl iptables iproute
fi
for tool in make git iptables ip; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "$tool is still missing after installing packages" >&2
    exit 1
  fi
done

# Always the pinned Go in /usr/local/go, never a distro go found on PATH:
# Debian 12 and Ubuntu 22.04 ship versions too old to build amneziawg-go.
GO=/usr/local/go/bin/go
if [ "$("$GO" env GOVERSION 2>/dev/null)" != "` + goVersion + `" ]; then
  for attempt in 1 2 3; do
    curl -fsSL --connect-timeout 20 --max-time 600 -o /tmp/go.tar.gz "https://go.dev/dl/` + goVersion + `.linux-${GOARCH}.tar.gz" && break
    if [ "$attempt" = 3 ]; then
      echo "Go toolchain download failed after 3 attempts" >&2
      exit 1
    fi
    sleep 5
  done
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm -f /tmp/go.tar.gz
fi

GOTOOLCHAIN=local GOBIN=/usr/local/bin "$GO" install github.com/amnezia-vpn/amneziawg-go@` + amneziaWGGoVersion + `

if ! command -v awg >/dev/null 2>&1 || ! command -v awg-quick >/dev/null 2>&1; then
  # -c protocol.version=1: GitHub's anonymous git-upload-pack over protocol
  # v2 (git >=2.29 default) intermittently 401s on the stateless-RPC POST
  # even though the repo is public, which surfaces as a confusing "could not
  # read Username" prompt error. Protocol v1 clones the same repo fine.
  # Retried a few times on top of that in case of a plain transient network
  # blip (unrelated to the protocol-v2 issue above).
  for attempt in 1 2 3; do
    rm -rf /tmp/amneziawg-tools
    git -c protocol.version=1 clone --depth 1 --branch ` + amneziaWGToolsVersion + ` https://github.com/amnezia-vpn/amneziawg-tools.git /tmp/amneziawg-tools && break
    if [ "$attempt" = 3 ]; then
      echo "amneziawg-tools clone failed after 3 attempts" >&2
      exit 1
    fi
    sleep 5
  done
  make -C /tmp/amneziawg-tools/src
  install -m 0755 /tmp/amneziawg-tools/src/wg /usr/local/bin/awg
  install -m 0755 /tmp/amneziawg-tools/src/wg-quick/linux.bash /usr/local/bin/awg-quick
  rm -rf /tmp/amneziawg-tools
fi

mkdir -p /etc/systemd/system
cat > /etc/systemd/system/awg-quick@.service <<'UNIT'
[Unit]
Description=AmneziaWG via awg-quick(8) for %I
After=network-online.target nss-lookup.target
Wants=network-online.target nss-lookup.target
PartOf=awg-quick.target
Documentation=man:awg-quick(8)

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/bin/awg-quick up %i
ExecStop=/usr/local/bin/awg-quick down %i
Environment=WG_ENDPOINT_RESOLUTION_RETRIES=infinity
Environment=WG_QUICK_USERSPACE_IMPLEMENTATION=/usr/local/bin/amneziawg-go

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
`
}

const uninstallScript = `systemctl disable --now ` + systemdUnit + ` 2>/dev/null || true
rm -f /etc/systemd/system/awg-quick@.service
systemctl daemon-reload 2>/dev/null || true
rm -f /usr/local/bin/awg /usr/local/bin/awg-quick /usr/local/bin/amneziawg-go
rm -rf /etc/amnezia
`
