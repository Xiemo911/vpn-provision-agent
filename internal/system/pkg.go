package system

// UserErrorMarker starts a line of script output written for the user. The
// control server shows the last such line on the task page instead of a
// generic error, so scripts use it (through fail) for problems the VPS
// owner has to fix. Keep in sync with the control server's usererr.Marker.
const UserErrorMarker = "NULLSIFY_ERROR: "

// PkgHelpers is a shell snippet for install scripts. It defines:
//
//   - fail MESSAGE: print MESSAGE for the user (see UserErrorMarker), exit 1.
//   - progress MESSAGE: say what the script is doing now (see
//     ProgressMarker); shown on the task page.
//   - pkg_install PKG...: install packages with apt-get, dnf, yum or apk
//     (the caller passes names for the package manager in use, telling
//     them apart with has_apt, has_dnf_or_yum and has_apk).
//   - is_openrc: whether the host runs OpenRC (Alpine) instead of systemd.
//   - firewalld_running: whether firewalld (RHEL family) is active, so
//     ports and interfaces must be opened with firewall-cmd too.
//   - need_disk MB: fail unless / has MB megabytes free.
//   - need_hosts WHAT HOST...: fail unless HTTPS to every HOST works; WHAT
//     says what they're needed for.
//
// A fresh VPS's first-boot cloud-init and unattended-upgrades runs hold the
// dpkg lock for many minutes, so apt-get waits for it (DPkg::Lock::Timeout)
// and the whole install is retried for a few minutes more before giving up
// with a clear error, rather than failing at once or carrying on without
// the packages. dnf, yum and apk are retried the same way (dnf waits for
// another dnf's lock itself, but a mirror can be briefly unreachable). A dpkg run cut short (a crash or reboot mid-upgrade) is
// finished first: until it is, every install fails with "dpkg was
// interrupted".
const PkgHelpers = `
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a

fail() { echo "` + UserErrorMarker + `$*" >&2; exit 1; }

progress() { echo "` + ProgressMarker + `$*"; }

has_apt() { command -v apt-get >/dev/null 2>&1; }
has_dnf_or_yum() { command -v dnf >/dev/null 2>&1 || command -v yum >/dev/null 2>&1; }
has_apk() { command -v apk >/dev/null 2>&1; }

is_openrc() { [ ! -d /run/systemd/system ] && [ -x /sbin/openrc-run ]; }

firewalld_running() {
  command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1
}

# Whether something else (usually the VPS's automatic updates) is using the
# package manager right now, so apt-get is about to wait for it.
apt_busy() {
  if command -v fuser >/dev/null 2>&1; then
    fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock >/dev/null 2>&1 && return 0
  fi
  pgrep -x apt-get >/dev/null 2>&1 || pgrep -x apt >/dev/null 2>&1 || pgrep -x dpkg >/dev/null 2>&1
}

repair_dpkg() {
  if [ -n "$(ls -A /var/lib/dpkg/updates 2>/dev/null)" ] || [ -n "$(dpkg --audit 2>/dev/null)" ]; then
    dpkg --force-confdef --force-confold --configure -a || true
  fi
}

pkg_install() {
  if has_apt; then
    if command -v cloud-init >/dev/null 2>&1; then
      timeout 600 cloud-init status --wait >/dev/null 2>&1 || true
    fi
    for attempt in 1 2 3 4 5 6; do
      if apt_busy; then
        progress "Waiting for the VPS to finish its automatic updates (this can take 10-20 minutes on a new VPS)"
      fi
      repair_dpkg
      apt-get -q -o DPkg::Lock::Timeout=300 update >/dev/null 2>&1 || true
      if apt-get -q -y -o DPkg::Lock::Timeout=300 install --no-install-recommends "$@"; then
        return 0
      fi
      sleep 20
    done
    fail "Couldn't install $* on the VPS: its package manager stayed busy (often automatic updates on a new VPS) or its package mirror is unreachable. Try again in a few minutes."
  fi
  if command -v dnf >/dev/null 2>&1; then pm="dnf install -y"
  elif command -v yum >/dev/null 2>&1; then pm="yum install -y"
  elif has_apk; then pm="apk add --no-cache"
  else fail "The VPS has no supported package manager (apt-get, dnf, yum or apk) to install $*."
  fi
  for attempt in 1 2 3 4; do
    $pm "$@" && return 0
    sleep 20
  done
  fail "Couldn't install $* on the VPS ($pm failed): its package manager stayed busy or its package mirror is unreachable. Try again in a few minutes."
}

need_disk() {
  free_mb=$(df -Pm / | awk 'NR==2 {print $4}')
  if [ -n "$free_mb" ] && [ "$free_mb" -lt "$1" ]; then
    fail "The VPS has only ${free_mb} MB of free disk space; installing needs about $1 MB. Free some space, then try again."
  fi
}

need_hosts() {
  what=$1; shift
  for host in "$@"; do
    if ! curl -sS -o /dev/null --connect-timeout 10 --max-time 20 "https://$host/" 2>/dev/null; then
      if command -v ip >/dev/null 2>&1 && [ -z "$(ip -4 route show default 2>/dev/null)" ]; then
        fail "The VPS has no IPv4 internet access, and $host (needed to download $what) only works over IPv4. Use a VPS with an IPv4 address."
      fi
      fail "The VPS can't reach $host, which is needed to download $what. Check its DNS and that outbound HTTPS isn't blocked (GitHub, for one, is blocked in some countries)."
    fi
  done
}
`
