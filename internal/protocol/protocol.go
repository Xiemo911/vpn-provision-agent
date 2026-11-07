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

// AgentStatus is a snapshot of the VPS / Xray / AmneziaWG state reported with
// each poll.
type AgentStatus struct {
	XrayInstalled bool   `json:"xray_installed"`
	XrayRunning   bool   `json:"xray_running"`
	XrayVersion   string `json:"xray_version,omitempty"`

	AmneziaWGInstalled bool   `json:"amneziawg_installed"`
	AmneziaWGRunning   bool   `json:"amneziawg_running"`
	AmneziaWGVersion   string `json:"amneziawg_version,omitempty"`

	UptimeSeconds int64 `json:"uptime_seconds,omitempty"`

	Capabilities []string `json:"capabilities,omitempty"`
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

// ProgressReport says what a running action is doing right now; the control
// server shows it on the action's task page.
type ProgressReport struct {
	ID      string `json:"id"` // action id
	AgentID string `json:"agent_id"`
	Message string `json:"message"`
}

// Action type identifiers.
const (
	ActionInstallXray    = "install_xray"
	ActionUninstallXray  = "uninstall_xray"
	ActionRestartXray    = "restart_xray"
	ActionAddUser        = "add_vless_user"
	ActionRemoveUser     = "remove_vless_user"
	ActionListUsers      = "list_vless_users"
	ActionApplyConfig    = "apply_config"
	ActionGetStatus      = "get_status"
	ActionRunCommand     = "run_command"
	ActionUninstallAgent = "uninstall_agent"
	ActionUpdateAgent    = "update_agent"

	ActionInstallAmneziaWG     = "install_amneziawg"
	ActionUninstallAmneziaWG   = "uninstall_amneziawg"
	ActionRestartAmneziaWG     = "restart_amneziawg"
	ActionApplyAmneziaWGConfig = "apply_amneziawg_config"
	ActionAddAmneziaWGPeer     = "add_amneziawg_peer"
	ActionRemoveAmneziaWGPeer  = "remove_amneziawg_peer"

	// ActionPickUDPPort finds a free UDP port for WireGuard/AmneziaWG
	// before a protocol switch to it (see Capabilities).
	ActionPickUDPPort = "pick_udp_port"
)

// Capabilities are the actions newer than the first agents, reported with
// every poll so the control server only queues what this agent can run.
var Capabilities = []string{ActionPickUDPPort}
