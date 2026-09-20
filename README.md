# vpn-provision-agent

A small, dependency-free Go agent that runs on each Linux VPS, polls your
control server once a minute, and executes VPN-management actions — installing
and managing [Xray-core](https://github.com/XTLS/Xray-core) VLESS servers and
[AmneziaWG](https://github.com/amnezia-vpn/amneziawg-go) peers.

```
┌────────────┐   1. POST /api/agent/poll (heartbeat + status)
│            │ ───────────────────────────────────────────────►  ┌─────────────┐
│  vpn-agent │                                                    │   Control   │
│  (on VPS)  │ ◄─────────────────────────────────────────────── │   server    │
│            │   2. { actions: [...] }                            │ (your site) │
│            │                                                    │             │
│            │   3. POST /api/agent/result (per action)           │             │
│            │ ───────────────────────────────────────────────►  └─────────────┘
└────────────┘
```

## How it works

1. Every `poll_interval_seconds` (default 60) the agent `POST`s its status to
   `/api/agent/poll`.
2. The server responds with a list of pending **actions**.
3. The agent executes each action and `POST`s the outcome to
   `/api/agent/result`.

The agent holds no state of its own — the control server is the source of
truth. Actions are idempotent-friendly where practical.

## Build

```bash
go build -ldflags "-X main.version=1.0.0" -o vpn-agent .
# cross-compile for a Linux VPS from any OS:
GOOS=linux GOARCH=amd64 go build -ldflags "-X main.version=1.0.0" -o vpn-agent .
```

## Install on a VPS

Download a binary from the GitHub release (`vpn-agent-linux-amd64` or
`vpn-agent-linux-arm64`) or build one, and name it `vpn-agent`. Copy it,
`deploy/install.sh`, and `deploy/vpn-agent.service` to the
server, then:

```bash
SERVER_URL=https://panel.example.com TOKEN=the-per-agent-token ./install.sh
journalctl -u vpn-agent -f
```

## Configuration

`/etc/vpn-agent/config.json` (see `deploy/config.example.json`). Every field can
also be set via environment variable:

| JSON key                | Env var                        | Default                               |
|-------------------------|--------------------------------|---------------------------------------|
| `server_url`            | `AGENT_SERVER_URL`             | *(required)*                          |
| `agent_id`              | `AGENT_ID`                     | hostname                              |
| `token`                 | `AGENT_TOKEN`                  | *(required)*                          |
| `poll_interval_seconds` | `AGENT_POLL_INTERVAL_SECONDS`  | `60`                                  |
| `xray_config_path`      | `AGENT_XRAY_CONFIG_PATH`       | `/usr/local/etc/xray/config.json`     |
| `amneziawg_config_path` | `AGENT_AMNEZIAWG_CONFIG_PATH`  | `/etc/amnezia/amneziawg/awg0.conf`    |
| `allow_run_command`     | `AGENT_ALLOW_RUN_COMMAND`      | `false`                               |

The config path itself defaults to `/etc/vpn-agent/config.json` and can be
changed with `--config`. `vpn-agent --version` prints the build version.

## Server API contract

Both endpoints require these request headers:

```
Authorization: Bearer <token>
X-Agent-Id: <agent_id>
Content-Type: application/json
```

### `POST /api/agent/poll`

Request body:

```json
{
  "agent_id": "vps-fra-01",
  "hostname": "vps-fra-01",
  "version": "1.0.0",
  "timestamp": 1752600000,
  "status": {
    "xray_installed": true,
    "xray_running": true,
    "xray_version": "Xray 1.8.4 (Xray, Penetrates Everything.)",
    "amneziawg_installed": true,
    "amneziawg_running": true,
    "amneziawg_version": "amneziawg-tools v1.0.0",
    "uptime_seconds": 3600
  }
}
```

Response body:

```json
{
  "actions": [
    { "id": "act_123", "type": "add_vless_user", "params": { "email": "alice" } }
  ]
}
```

Return `{"actions": []}` when there's nothing to do.

### `POST /api/agent/result`

Sent once per executed action:

```json
{
  "id": "act_123",
  "agent_id": "vps-fra-01",
  "success": true,
  "output": { "id": "3f9c...", "email": "alice" },
  "timestamp": 1752600001
}
```

On failure, `success` is `false` and `error` holds the message.

## Action catalog

| Type                | Params                                                        | Output                          |
|---------------------|--------------------------------------------------------------|---------------------------------|
| `install_xray`      | `{ "version"?: "1.8.4" }`                                     | `{ "output": "..." }`           |
| `uninstall_xray`    | —                                                            | `{ "output": "..." }`           |
| `restart_xray`      | —                                                            | `{ "restarted": true }`         |
| `add_vless_user`    | `{ "inbound_tag"?, "id"?, "email"?, "flow"?, "restart"? }`    | the created client (with `id`)  |
| `remove_vless_user` | `{ "inbound_tag"?, "id"?, "email"?, "restart"? }`            | `{ "removed": 1 }`              |
| `list_vless_users`  | `{ "inbound_tag"? }`                                          | `{ "clients": [...], "count" }` |
| `apply_config`      | `{ "config": { ...full xray config... }, "restart"? }`       | `{ "applied": true }`           |
| `get_status`        | —                                                            | `AgentStatus`                   |
| `run_command`       | `{ "command": "..." }` *(requires `allow_run_command`)*       | `{ "stdout", "stderr", "exit_code" }` |
| `uninstall_agent`   | —                                                            | `{ "status": "uninstall scheduled" }` |
| `update_agent`      | `{ "url": "...", "sha256": "...", "version"? }`               | `{ "status": "update installed, restarting", "version" }` |
| `install_amneziawg` | —                                                            | `{ "output": "..." }`           |
| `uninstall_amneziawg` | —                                                          | `{ "output": "..." }`           |
| `restart_amneziawg` | —                                                            | `{ "restarted": true }`         |
| `apply_amneziawg_config` | `{ "config": "<awg0.conf text>", "restart"? }`          | `{ "applied": true }`           |
| `add_amneziawg_peer` | `{ "peer_config_text", "public_key"?, "email"?, "allowed_ip"?, "private_key"? }` | `{ "public_key", "email", "allowed_ip", "private_key" }` |
| `remove_amneziawg_peer` | `{ "public_key" }`                                       | `{ "public_key" }`              |

Notes:

- `inbound_tag` is optional; when omitted the first VLESS inbound is targeted.
- For `add_vless_user`, omit `id` to have the agent generate a UUID and return it.
- `restart` defaults to `true` for user/config changes so they take effect
  immediately; set `false` to batch several changes and restart once at the end.
- `apply_config` validates the new config with `xray -test` and rolls back to the
  previous file if validation fails.
- `apply_amneziawg_config` takes the config as plain INI text (not JSON),
  validates it with `awg-quick strip`, and restores the previous file
  (kept as `awg0.conf.bak`) if validation fails.
- `add_amneziawg_peer` appends `peer_config_text` (a rendered `[Peer]` block) to
  the config and hot-applies it with `awg syncconf`, without dropping existing
  peers. `remove_amneziawg_peer` removes the peer live and strips its block from
  the file. The other params are only echoed back in the result so the server
  can persist them.
- `install_amneziawg` installs Go (if missing), builds `amneziawg-go` and
  `amneziawg-tools` from source, and registers an `awg-quick@.service` unit.
- `uninstall_agent` purges Xray (it does **not** remove AmneziaWG; run
  `uninstall_amneziawg` first if needed), then stops/disables/removes the `vpn-agent`
  systemd unit, config directory, and binary. Because it tears down the very
  process handling it, the actual teardown runs a couple seconds later as a
  detached `systemd-run` unit — this is a one-way trip, the VPS won't be
  manageable by this agent again until reinstalled by hand.
- `update_agent` downloads `url`, verifies it against `sha256`, and replaces
  the running binary. It then exits after a couple seconds (giving the
  action result time to reach the control server) and relies on
  `Restart=always` in `vpn-agent.service` to relaunch it as the new version.
  The previous binary is kept alongside it as `vpn-agent.bak` for manual
  rollback.

## Security notes

- Each VPS should have its own `token`. Treat the token file (`0600`) as a
  secret; rotating it means updating the server's record and the file.
- `run_command` is arbitrary remote shell execution and is disabled by default.
  Only enable it on hosts where you accept that the control server can run any
  command as root.
- Serve the control server over HTTPS only — the token travels in the header.
- `uninstall_agent` is irreversible and self-destructive: it removes Xray,
  the systemd service, the binary, and `/etc/vpn-agent`. Once it runs, the
  control server has no way to reach that VPS again.
- `update_agent` runs whatever binary is at `url` as root on every VPS that
  receives the action. The `sha256` check only guards against corruption or a
  compromised transport, not a malicious control server — treat the ability
  to issue this action as equivalent to root on the fleet, same as
  `run_command`.
