// Package protocol defines the wire types exchanged between the agent and the
// control server. Keep this in sync with the server implementation.
package protocol

import "encoding/json"

// PollRequest is sent by the agent on every polling tick.
type PollRequest struct {
	AgentID   string      `json:"agent_id"`
	Hostname  string      `json:"hostname"`
	Version   string      `json:"version"`
	Timestamp int64       `json:"timestamp"`
	Status    AgentStatus `json:"status"`
}

// AgentStatus is a snapshot of the VPS / Xray state reported with each poll.
type AgentStatus struct {
	XrayInstalled bool   `json:"xray_installed"`
	XrayRunning   bool   `json:"xray_running"`
	XrayVersion   string `json:"xray_version,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds,omitempty"`
}

// PollResponse is returned by the control server with any pending work.
type PollResponse struct {
	Actions []Action `json:"actions"`
}

// Action is a single unit of work handed to the agent.
type Action struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ActionResult is reported back after an action is executed.
type ActionResult struct {
	ID        string          `json:"id"`
	AgentID   string          `json:"agent_id"`
	Success   bool            `json:"success"`
	Error     string          `json:"error,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
	Timestamp int64           `json:"timestamp"`
}

// Action type identifiers.
const (
	ActionInstallXray   = "install_xray"
	ActionUninstallXray = "uninstall_xray"
	ActionRestartXray   = "restart_xray"
	ActionAddUser       = "add_vless_user"
	ActionRemoveUser    = "remove_vless_user"
	ActionListUsers     = "list_vless_users"
	ActionApplyConfig   = "apply_config"
	ActionGetStatus     = "get_status"
	ActionRunCommand    = "run_command"
)
