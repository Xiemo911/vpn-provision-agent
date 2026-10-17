#!/usr/bin/env bash
# Installs the vpn-provision-agent binary and systemd service on a Linux VPS.
#
# Usage:
#   SERVER_URL=https://panel.example.com TOKEN=xxxx ./install.sh
#
# Expects the compiled `vpn-agent` binary to sit next to this script.
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "This script must be run as root." >&2
  exit 1
fi

SERVER_URL="${SERVER_URL:?set SERVER_URL}"
TOKEN="${TOKEN:?set TOKEN}"
AGENT_ID="${AGENT_ID:-}"
# Set to force-regenerate an existing config.json (e.g. when SERVER_URL/TOKEN
# actually changed). By default a reinstall/upgrade leaves it alone so any
# local config drift (allow_run_command, custom paths, etc.) survives.
FORCE_CONFIG="${FORCE_CONFIG:-false}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_PATH=/etc/vpn-agent/config.json

echo "Installing binary to /usr/local/bin/vpn-agent"
install -m 0755 "${SCRIPT_DIR}/vpn-agent" /usr/local/bin/vpn-agent

if [[ -f "${CONFIG_PATH}" && "${FORCE_CONFIG}" != "true" ]]; then
  echo "Config already exists at ${CONFIG_PATH}, leaving it as-is (set FORCE_CONFIG=true to regenerate it)"
else
  echo "Writing config to ${CONFIG_PATH}"
  mkdir -p /etc/vpn-agent
  cat > "${CONFIG_PATH}" <<EOF
{
  "server_url": "${SERVER_URL}",
  "agent_id": "${AGENT_ID}",
  "token": "${TOKEN}",
  "poll_interval_seconds": 60,
  "xray_config_path": "/usr/local/etc/xray/config.json",
  "allow_run_command": false
}
EOF
  chmod 0600 "${CONFIG_PATH}"
fi

echo "Installing systemd unit"
install -m 0644 "${SCRIPT_DIR}/vpn-agent.service" /etc/systemd/system/vpn-agent.service

systemctl daemon-reload
systemctl enable vpn-agent
# restart (not just enable --now) so a reinstall actually loads the new
# binary/unit even if the service was already running.
systemctl restart vpn-agent

echo "Waiting for vpn-agent to come up..."
for _ in 1 2 3 4 5; do
  if systemctl is-active --quiet vpn-agent; then
    echo "Done. Follow logs with: journalctl -u vpn-agent -f"
    exit 0
  fi
  sleep 1
done

echo "vpn-agent did not reach the active state; recent logs:" >&2
journalctl -u vpn-agent -n 50 --no-pager >&2
exit 1
