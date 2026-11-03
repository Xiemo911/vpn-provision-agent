package system

// PkgHelpers is a shell snippet for install scripts. It defines pkg_install,
// which installs packages with apt-get, dnf or yum (the caller passes names
// for the package manager in use, telling them apart with has_apt).
//
// A fresh VPS's first-boot cloud-init and unattended-upgrades runs hold the
// dpkg lock for several minutes, so apt-get waits for it (DPkg::Lock::Timeout)
// and the whole install is retried for a few minutes more before giving up
// with a clear error, rather than failing at once or carrying on without
// the packages.
const PkgHelpers = `
export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a

has_apt() { command -v apt-get >/dev/null 2>&1; }

pkg_install() {
  if has_apt; then
    if command -v cloud-init >/dev/null 2>&1; then
      timeout 600 cloud-init status --wait >/dev/null 2>&1 || true
    fi
    for attempt in 1 2 3 4 5 6; do
      apt-get -q -o DPkg::Lock::Timeout=300 update >/dev/null 2>&1 || true
      if apt-get -q -y -o DPkg::Lock::Timeout=300 install --no-install-recommends "$@"; then
        return 0
      fi
      sleep 20
    done
    echo "apt-get could not install $* (the dpkg lock stayed busy or the package mirror is unreachable)" >&2
    return 1
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y "$@" && return 0
    echo "dnf could not install $*" >&2
    return 1
  elif command -v yum >/dev/null 2>&1; then
    yum install -y "$@" && return 0
    echo "yum could not install $*" >&2
    return 1
  fi
  echo "no supported package manager (apt-get, dnf or yum) to install $*" >&2
  return 1
}
`
