# vpn-provision-agent

A small, dependency-free Go agent that runs on each Linux VPS, polls your
control server once a minute, and executes VPN-management actions — installing
and managing [Xray-core](https://github.com/XTLS/Xray-core) VLESS servers.

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

Copy `vpn-agent`, `deploy/install.sh`, and `deploy/vpn-agent.service` to the
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
| `allow_run_command`     | `AGENT_ALLOW_RUN_COMMAND`      | `false`                               |

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

Notes:

- `inbound_tag` is optional; when omitted the first VLESS inbound is targeted.
- For `add_vless_user`, omit `id` to have the agent generate a UUID and return it.
- `restart` defaults to `true` for user/config changes so they take effect
  immediately; set `false` to batch several changes and restart once at the end.
- `apply_config` validates the new config with `xray -test` and rolls back to the
  previous file if validation fails.

## Security notes

- Each VPS should have its own `token`. Treat the token file (`0600`) as a
  secret; rotating it means updating the server's record and the file.
- `run_command` is arbitrary remote shell execution and is disabled by default.
  Only enable it on hosts where you accept that the control server can run any
  command as root.
- Serve the control server over HTTPS only — the token travels in the header
```
