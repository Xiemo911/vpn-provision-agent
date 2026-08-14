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

// Install builds/installs amneziawg-go (userspace implementation) and the
// awg/awg-quick control tools, then registers the awg-quick@.service systemd
// template unit.
//
// NOTE: the exact amneziawg-go / amneziawg-tools source locations and build
// steps below should be re-verified against the current upstream READMEs
// before relying on this in production — those repos move independently of
// this agent's release cadence. The rest of the Manager's surface does not
// depend on these exact commands.
func (m *Manager) Install(ctx context.Context) (system.Result, error) {
	return system.RunShell(ctx, installScript)
}

// Uninstall removes AmneziaWG: stops the service, deletes the systemd unit,
// the installed binaries, and the config directory.
func (m *Manager) Uninstall(ctx context.Context) (system.Result, error) {
	return system.RunShell(ctx, uninstallScript)
}

// Restart enables and restarts the awg-quick@awg0 service.
func (m *Manager) Restart(ctx context.Context) (system.Result, error) {
	if res, err := system.Run(ctx, "systemctl", "enable", systemdUnit); err != nil {
		return res, fmt.Errorf("enable %s: %s", systemdUnit, res.Combined())
	}
	res, err := system.Run(ctx, "systemctl", "restart", systemdUnit)
	if err != nil {
		return res, fmt.Errorf("restart %s: %s", systemdUnit, res.Combined())
	}
	return res, nil
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

const installScript = `set -e
if ! command -v go >/dev/null 2>&1; then
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64) GOARCH=amd64 ;;
    aarch64|arm64) GOARCH=arm64 ;;
    armv7l) GOARCH=arm ;;
    *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
  esac
  GOVER=1.23.4
  curl -fsSL "https://go.dev/dl/go${GOVER}.linux-${GOARCH}.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm -f /tmp/go.tar.gz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
fi
export PATH=$PATH:/usr/local/go/bin

GOBIN=/usr/local/bin go install github.com/amnezia-vpn/amneziawg-go@latest

if ! command -v awg >/dev/null 2>&1 || ! command -v awg-quick >/dev/null 2>&1; then
  (apt-get update -y && apt-get install -y build-essential libmnl-dev pkg-config git) >/dev/null 2>&1 || \
    (yum install -y gcc make libmnl-devel pkgconfig git) >/dev/null 2>&1 || true
  rm -rf /tmp/amneziawg-tools
  git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-tools.git /tmp/amneziawg-tools
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

const uninstallScript = `systemctl disable --now ` + systemdUnit + ` 2>/dev/null || true
rm -f /etc/systemd/system/awg-quick@.service
systemctl daemon-reload 2>/dev/null || true
rm -f /usr/local/bin/awg /usr/local/bin/awg-quick /usr/local/bin/amneziawg-go
rm -rf /etc/amnezia
`
