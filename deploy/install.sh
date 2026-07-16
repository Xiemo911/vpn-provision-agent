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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Installing binary to /usr/local/bin/vpn-agent"
install -m 0755 "${SCRIPT_DIR}/vpn-agent" /usr/local/bin/vpn-agent

echo "Writing config to /etc/vpn-agent/config.json"
mkdir -p /etc/vpn-agent
cat > /etc/vpn-agent/config.json <<EOF
{
  "server_url": "${SERVER_URL}",
  "agent_id": "${AGENT_ID}",
  "token": "${TOKEN}",
  "poll_interval_seconds": 60,
  "xray_config_path": "/usr/local/etc/xray/config.json",
  "allow_run_command": false
}
EOF
chmod 0600 /etc/vpn-agent/config.json

echo "Installing systemd unit"
install -m 0644 "${SCRIPT_DIR}/vpn-agent.service" /etc/systemd/system/vpn-agent.service

systemctl daemon-reload
systemctl enable --now vpn-agent
echo "Done. Follow logs with: journalctl -u vpn-agent -f"
