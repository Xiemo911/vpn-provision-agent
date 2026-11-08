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
	"strconv"
	"strings"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/system"
)

const (
	defaultConfigPath = "/etc/amnezia/amneziawg/awg0.conf"
	ifaceName         = "awg0"
	systemdUnit       = "awg-quick@" + ifaceName
	openRCService     = "awg-quick"
)

// service runs awg-quick for awg0: a systemd template instance, or on
// OpenRC (Alpine) the init script installScript writes.
var service = system.Service{Systemd: systemdUnit, OpenRC: openRCService}

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

// IsRunning reports whether the awg0 service is active.
func (m *Manager) IsRunning(ctx context.Context) bool {
	return service.IsActive(ctx)
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
// template unit (or on OpenRC, the awg-quick init script).
func (m *Manager) Install(ctx context.Context) (system.Result, error) {
	return system.RunShell(ctx, installScript())
}

// Uninstall removes AmneziaWG: stops the service, deletes its unit or init
// script, the installed binaries, and the config directory.
func (m *Manager) Uninstall(ctx context.Context) (system.Result, error) {
	return system.RunShell(ctx, uninstallScript)
}

// Restart enables and restarts the awg0 service, and confirms it actually
// reaches the active state rather than trusting the init system's exit
// code alone.
func (m *Manager) Restart(ctx context.Context) (system.Result, error) {
	res, err := service.EnableAndRestart(ctx)
	if err != nil {
		return res, err
	}
	if !m.waitRunning(ctx) {
		return res, fmt.Errorf("restart %s: service did not reach active state", service.Name())
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

// goRoot is where Install keeps its own Go toolchain: a directory of ours,
// so a Go the VPS owner put in /usr/local/go is never replaced or removed.
const (
	nullsifyDir = "/opt/nullsify"
	goRoot      = nullsifyDir + "/go"
)

// Disk space Install needs (MB): Go (~350 with its download), build tools
// (~250), build caches (~100), plus headroom; and the temporary swap file
// on a small VPS.
const (
	installDiskMB = 1200
	swapMB        = 1024
)

func installScript() string {
	return system.PkgHelpers + `
set -e
export HOME="${HOME:-/root}"

# amneziawg-go is a userspace daemon and needs a TUN device, which OpenVZ and
# many LXC containers only provide once it's enabled in the provider's panel.
progress "Checking the VPS"
modprobe tun >/dev/null 2>&1 || true
[ -c /dev/net/tun ] || fail "This VPS has no TUN device (/dev/net/tun), which AmneziaWG and WireGuard need. It's usually an OpenVZ or LXC container: enable TUN/TAP in your provider's control panel, or use a KVM VPS."

# The VPN turns on IP forwarding when it starts; containers often forbid
# that. Writing back the current value tests it without changing anything.
sysctl -w net.ipv4.ip_forward="$(sysctl -n net.ipv4.ip_forward 2>/dev/null || echo 0)" >/dev/null 2>&1 ||
  fail "This VPS doesn't allow changing its network settings (net.ipv4.ip_forward), which the VPN needs to route traffic. That's common on LXC/OpenVZ containers: use a KVM VPS, or ask your provider to allow it."

case "$(uname -m)" in
  x86_64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) fail "This VPS's CPU architecture ($(uname -m)) isn't supported. Use a 64-bit (x86-64 or ARM64) VPS." ;;
esac

need_disk ` + strconv.Itoa(installDiskMB) + `

# Build tools, plus what awg-quick needs at runtime: ip (iproute2) to bring
# the interface up and iptables for the NAT rules in the server config.
# Minimal Debian images ship without iptables, and awg-quick up then fails.
# awg-quick is a bash script, which Alpine doesn't ship. libmnl isn't needed
# by the tools any more; RHEL 9 only has its headers in the CRB repository,
# so they're not asked for there.
progress "Installing build tools"
if has_apt; then
  pkg_install build-essential libmnl-dev pkg-config git ca-certificates curl iptables iproute2
elif has_apk; then
  pkg_install build-base linux-headers git ca-certificates curl iptables iproute2 bash tar
else
  pkg_install gcc make git ca-certificates curl iproute tar gzip
  # RHEL 9 and later call the package iptables-nft; 8 still calls it iptables.
  if ! command -v iptables >/dev/null 2>&1; then
    { command -v dnf >/dev/null 2>&1 && dnf install -y iptables-nft; } || pkg_install iptables
  fi
fi
for tool in make git iptables ip; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is still missing on the VPS after installing packages."
done
iptables -t nat -L -n >/dev/null 2>&1 ||
  fail "This VPS doesn't allow NAT in its firewall (iptables' nat table), which the VPN needs to give clients internet access. That's common on LXC/OpenVZ containers: use a KVM VPS."

progress "Checking the VPS can reach the download sites"
need_hosts "AmneziaWG" go.dev dl.google.com proxy.golang.org github.com

# Build in a throwaway directory (Go's module and build caches, about 100
# MB), deleted on exit whether or not the build works. /var/tmp rather than
# /tmp, which is often a small RAM-backed tmpfs.
build_dir=$(mktemp -d /var/tmp/awg-build.XXXXXX)
swap_file=""
cleanup() {
  if [ -n "$swap_file" ]; then swapoff "$swap_file" 2>/dev/null || true; rm -f "$swap_file"; fi
  rm -rf "$build_dir"
}
trap cleanup EXIT

# Always our own pinned Go, never a distro go found on PATH: Debian 12 and
# Ubuntu 22.04 ship versions too old to build amneziawg-go.
GO=` + goRoot + `/bin/go
if [ "$("$GO" env GOVERSION 2>/dev/null)" != "` + goVersion + `" ]; then
  progress "Downloading Go (needed to build AmneziaWG)"
  for attempt in 1 2 3; do
    curl -fsSL --connect-timeout 20 --max-time 600 -o "$build_dir/go.tar.gz" "https://go.dev/dl/` + goVersion + `.linux-${GOARCH}.tar.gz" && break
    [ "$attempt" = 3 ] && fail "Couldn't download the Go toolchain from go.dev to build AmneziaWG. Try again in a few minutes."
    sleep 5
  done
  rm -rf ` + goRoot + `
  mkdir -p ` + nullsifyDir + `
  tar -C ` + nullsifyDir + ` -xzf "$build_dir/go.tar.gz"
  rm -f "$build_dir/go.tar.gz"
fi

# Compiling amneziawg-go peaks around 1 GB of memory. On a smaller VPS,
# build one package at a time and add a temporary swap file (removed again
# by cleanup), or the kernel kills the build.
build_flags=""
avail_mb=$(awk '/^MemAvailable:/ {a=$2} /^SwapFree:/ {s=$2} END {print int((a+s)/1024)}' /proc/meminfo)
if [ "$avail_mb" -lt 1536 ]; then
  build_flags="-p 1"
  free_mb=$(df -Pm /var/tmp | awk 'NR==2 {print $4}')
  if [ "$free_mb" -gt $((` + strconv.Itoa(swapMB) + ` + 500)) ]; then
    swap_file=/var/tmp/nullsify-build.swap
    if { fallocate -l ` + strconv.Itoa(swapMB) + `M "$swap_file" 2>/dev/null ||
         dd if=/dev/zero of="$swap_file" bs=1M count=` + strconv.Itoa(swapMB) + ` status=none 2>/dev/null; } &&
       chmod 600 "$swap_file" && mkswap "$swap_file" >/dev/null 2>&1 && swapon "$swap_file" 2>/dev/null; then
      :
    else
      rm -f "$swap_file"; swap_file=""
    fi
  fi
fi

progress "Building AmneziaWG (this takes a minute or two, longer on a small VPS)"
GOPATH="$build_dir/gopath" GOCACHE="$build_dir/cache" GOFLAGS=-modcacherw GOTOOLCHAIN=local GOBIN=/usr/local/bin \
  "$GO" install $build_flags github.com/amnezia-vpn/amneziawg-go@` + amneziaWGGoVersion + ` ||
  fail "Building AmneziaWG on the VPS failed. On a VPS with 1 GB of memory or less that's usually the build running out of memory: try a VPS with more RAM. Otherwise, try again."

if ! command -v awg >/dev/null 2>&1 || ! command -v awg-quick >/dev/null 2>&1; then
  progress "Building the AmneziaWG tools"
  # -c protocol.version=1: GitHub's anonymous git-upload-pack over protocol
  # v2 (git >=2.29 default) intermittently 401s on the stateless-RPC POST
  # even though the repo is public, which surfaces as a confusing "could not
  # read Username" prompt error. Protocol v1 clones the same repo fine.
  # Retried a few times on top of that in case of a plain transient network
  # blip (unrelated to the protocol-v2 issue above).
  for attempt in 1 2 3; do
    rm -rf /tmp/amneziawg-tools
    git -c protocol.version=1 clone --depth 1 --branch ` + amneziaWGToolsVersion + ` https://github.com/amnezia-vpn/amneziawg-tools.git /tmp/amneziawg-tools && break
    [ "$attempt" = 3 ] && fail "Couldn't download amneziawg-tools from github.com. Try again in a few minutes."
    sleep 5
  done
  make -C /tmp/amneziawg-tools/src
  install -m 0755 /tmp/amneziawg-tools/src/wg /usr/local/bin/awg
  install -m 0755 /tmp/amneziawg-tools/src/wg-quick/linux.bash /usr/local/bin/awg-quick
  rm -rf /tmp/amneziawg-tools
fi

progress "Finishing the install"
if is_openrc; then
  cat > /etc/init.d/` + openRCService + ` <<'INIT'
` + openRCInitScript + `INIT
  chmod 0755 /etc/init.d/` + openRCService + `
else
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
fi

# firewalld (RHEL family) drops forwarded traffic and NATs nothing by
# default, whatever iptables rules awg-quick adds: trust the VPN interface
# so clients' traffic is forwarded, and masquerade it out of the default
# zone. Both now and in the saved config, so it survives a reboot.
if firewalld_running; then
  for args in "--zone=trusted --add-interface=` + ifaceName + `" "--add-masquerade"; do
    firewall-cmd $args >/dev/null 2>&1 || true
    firewall-cmd --permanent $args >/dev/null 2>&1 || true
  done
fi

# Marks this AmneziaWG as Nullsify's, so a later install on this VPS can tell
# it from someone else's (which it must not overwrite). Uninstall removes it
# with the rest of /etc/amnezia.
mkdir -p ` + markerDir + ` && touch ` + markerDir + `/.nullsify
`
}

// openRCInitScript brings awg0 up and down with awg-quick on OpenRC, like
// the awg-quick@.service template does under systemd.
const openRCInitScript = `#!/sbin/openrc-run
description="AmneziaWG via awg-quick for ` + ifaceName + `"

depend() {
  need net
  after firewall iptables
}

start() {
  ebegin "Starting AmneziaWG ` + ifaceName + `"
  export PATH="/usr/local/sbin:/usr/local/bin:$PATH"
  export WG_ENDPOINT_RESOLUTION_RETRIES=infinity
  export WG_QUICK_USERSPACE_IMPLEMENTATION=/usr/local/bin/amneziawg-go
  /usr/local/bin/awg-quick up ` + ifaceName + `
  eend $?
}

stop() {
  ebegin "Stopping AmneziaWG ` + ifaceName + `"
  export PATH="/usr/local/sbin:/usr/local/bin:$PATH"
  /usr/local/bin/awg-quick down ` + ifaceName + `
  eend $?
}
`

// markerDir holds the marker file that says Nullsify installed this
// AmneziaWG; the control server's pre-install check looks for it.
const markerDir = "/etc/amnezia/amneziawg"

// uninstallScript also removes the Go toolchain Install keeps in goRoot
// (about 270 MB). /usr/local/go is never touched: it may be the VPS
// owner's own Go.
const uninstallScript = `if [ ! -d /run/systemd/system ] && [ -x /sbin/openrc-run ]; then
  rc-service ` + openRCService + ` stop >/dev/null 2>&1 || true
  rc-update del ` + openRCService + ` default >/dev/null 2>&1 || true
  rm -f /etc/init.d/` + openRCService + `
else
  systemctl disable --now ` + systemdUnit + ` 2>/dev/null || true
  rm -f /etc/systemd/system/awg-quick@.service
  systemctl daemon-reload 2>/dev/null || true
fi
if command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  firewall-cmd --zone=trusted --remove-interface=` + ifaceName + ` >/dev/null 2>&1 || true
  firewall-cmd --permanent --zone=trusted --remove-interface=` + ifaceName + ` >/dev/null 2>&1 || true
fi
rm -f /usr/local/bin/awg /usr/local/bin/awg-quick /usr/local/bin/amneziawg-go
rm -rf /etc/amnezia
rm -rf ` + goRoot + `
rmdir ` + nullsifyDir + ` 2>/dev/null || true
`
