// Package actions dispatches control-server actions to local handlers.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/amneziawg"
	"github.com/Xiemo911/vpn-provision-agent/internal/config"
	"github.com/Xiemo911/vpn-provision-agent/internal/protocol"
	"github.com/Xiemo911/vpn-provision-agent/internal/system"
	"github.com/Xiemo911/vpn-provision-agent/internal/xray"
)

// Handler executes one action type. It returns a JSON-encodable output.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Dispatcher routes actions to handlers.
type Dispatcher struct {
	cfg       *config.Config
	xray      *xray.Manager
	amneziawg *amneziawg.Manager
	handlers  map[string]Handler
}

// NewDispatcher wires up all supported action handlers.
func NewDispatcher(cfg *config.Config, mgr *xray.Manager, awgMgr *amneziawg.Manager) *Dispatcher {
	d := &Dispatcher{cfg: cfg, xray: mgr, amneziawg: awgMgr}
	d.handlers = map[string]Handler{
		protocol.ActionInstallXray:    d.installXray,
		protocol.ActionUninstallXray:  d.uninstallXray,
		protocol.ActionRestartXray:    d.restartXray,
		protocol.ActionAddUser:        d.addUser,
		protocol.ActionRemoveUser:     d.removeUser,
		protocol.ActionListUsers:      d.listUsers,
		protocol.ActionApplyConfig:    d.applyConfig,
		protocol.ActionGetStatus:      d.getStatus,
		protocol.ActionRunCommand:     d.runCommand,
		protocol.ActionUninstallAgent: d.uninstallAgent,
		protocol.ActionUpdateAgent:    d.updateAgent,

		protocol.ActionInstallAmneziaWG:     d.installAmneziaWG,
		protocol.ActionUninstallAmneziaWG:   d.uninstallAmneziaWG,
		protocol.ActionRestartAmneziaWG:     d.restartAmneziaWG,
		protocol.ActionApplyAmneziaWGConfig: d.applyAmneziaWGConfig,
		protocol.ActionAddAmneziaWGPeer:     d.addAmneziaWGPeer,
		protocol.ActionRemoveAmneziaWGPeer:  d.removeAmneziaWGPeer,
	}
	return d
}

// Execute runs an action and packages the result.
func (d *Dispatcher) Execute(ctx context.Context, action protocol.Action) protocol.ActionResult {
	res := protocol.ActionResult{
		ID:        action.ID,
		AgentID:   d.cfg.AgentID,
		Timestamp: time.Now().Unix(),
	}

	handler, ok := d.handlers[action.Type]
	if !ok {
		res.Error = fmt.Sprintf("unknown action type %q", action.Type)
		return res
	}

	out, err := handler(ctx, action.Params)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Success = true
	if out != nil {
		if encoded, mErr := json.Marshal(out); mErr == nil {
			res.Output = encoded
		}
	}
	return res
}

// --- handlers ---

type installParams struct {
	Version string `json:"version"`
}

func (d *Dispatcher) installXray(ctx context.Context, params json.RawMessage) (any, error) {
	var p installParams
	_ = unmarshal(params, &p)
	res, err := d.xray.Install(ctx, p.Version)
	if err != nil {
		return nil, fmt.Errorf("install failed: %s", res.Combined())
	}
	return map[string]string{"output": res.Combined()}, nil
}

func (d *Dispatcher) uninstallXray(ctx context.Context, _ json.RawMessage) (any, error) {
	res, err := d.xray.Uninstall(ctx)
	if err != nil {
		return nil, fmt.Errorf("uninstall failed: %s", res.Combined())
	}
	return map[string]string{"output": res.Combined()}, nil
}

func (d *Dispatcher) restartXray(ctx context.Context, _ json.RawMessage) (any, error) {
	if _, err := d.xray.Restart(ctx); err != nil {
		return nil, err
	}
	return map[string]bool{"restarted": true}, nil
}

type addUserParams struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
	Email      string `json:"email"`
	Flow       string `json:"flow"`
	Restart    *bool  `json:"restart"`
}

func (d *Dispatcher) addUser(ctx context.Context, params json.RawMessage) (any, error) {
	var p addUserParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	client, err := d.xray.AddUser(p.InboundTag, xray.Client{ID: p.ID, Email: p.Email, Flow: p.Flow})
	if err != nil {
		return nil, err
	}
	if restartWanted(p.Restart) {
		if _, err := d.xray.Restart(ctx); err != nil {
			return nil, fmt.Errorf("user added but restart failed: %w", err)
		}
	}
	return client, nil
}

type removeUserParams struct {
	InboundTag string `json:"inbound_tag"`
	ID         string `json:"id"`
	Email      string `json:"email"`
	Restart    *bool  `json:"restart"`
}

func (d *Dispatcher) removeUser(ctx context.Context, params json.RawMessage) (any, error) {
	var p removeUserParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	target := p.ID
	if target == "" {
		target = p.Email
	}
	if target == "" {
		return nil, fmt.Errorf("either id or email is required")
	}
	removed, err := d.xray.RemoveUser(p.InboundTag, target)
	if err != nil {
		return nil, err
	}
	if restartWanted(p.Restart) {
		if _, err := d.xray.Restart(ctx); err != nil {
			return nil, fmt.Errorf("user removed but restart failed: %w", err)
		}
	}
	return map[string]int{"removed": removed}, nil
}

type listUsersParams struct {
	InboundTag string `json:"inbound_tag"`
}

func (d *Dispatcher) listUsers(_ context.Context, params json.RawMessage) (any, error) {
	var p listUsersParams
	_ = unmarshal(params, &p)
	clients, err := d.xray.ListUsers(p.InboundTag)
	if err != nil {
		return nil, err
	}
	return map[string]any{"clients": clients, "count": len(clients)}, nil
}

type applyConfigParams struct {
	Config  json.RawMessage `json:"config"`
	Restart *bool           `json:"restart"`
}

func (d *Dispatcher) applyConfig(ctx context.Context, params json.RawMessage) (any, error) {
	var p applyConfigParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if len(p.Config) == 0 {
		return nil, fmt.Errorf("config is required")
	}
	if err := d.xray.ApplyConfig(ctx, p.Config); err != nil {
		return nil, err
	}
	if restartWanted(p.Restart) {
		if _, err := d.xray.Restart(ctx); err != nil {
			return nil, fmt.Errorf("config applied but restart failed: %w", err)
		}
	}
	return map[string]bool{"applied": true}, nil
}

func (d *Dispatcher) getStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	return protocol.AgentStatus{
		XrayInstalled: d.xray.IsInstalled(),
		XrayRunning:   d.xray.IsRunning(ctx),
		XrayVersion:   d.xray.Version(ctx),

		AmneziaWGInstalled: d.amneziawg.IsInstalled(),
		AmneziaWGRunning:   d.amneziawg.IsRunning(ctx),
		AmneziaWGVersion:   d.amneziawg.Version(ctx),
	}, nil
}

type runCommandParams struct {
	Command string `json:"command"`
}

func (d *Dispatcher) runCommand(ctx context.Context, params json.RawMessage) (any, error) {
	if !d.cfg.AllowRunCommand {
		return nil, fmt.Errorf("run_command is disabled (set allow_run_command=true to enable)")
	}
	var p runCommandParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Command == "" {
		return nil, fmt.Errorf("command is required")
	}
	res, err := system.RunShell(ctx, p.Command)
	out := map[string]any{
		"stdout":    res.Stdout,
		"stderr":    res.Stderr,
		"exit_code": res.ExitCode,
	}
	if err != nil {
		return out, fmt.Errorf("command exited non-zero (%d)", res.ExitCode)
	}
	return out, nil
}

// uninstallAgent removes Xray, the systemd service, the binary, and the
// config directory, i.e. everything install.sh and installXray put on the
// host. Because this action must kill the very process running it, the
// teardown runs as a detached systemd-run transient unit: that lets this
// handler return success (so the result gets reported to the control server)
// before the agent's own service is stopped and its files deleted.
func (d *Dispatcher) uninstallAgent(ctx context.Context, _ json.RawMessage) (any, error) {
	inner := `sleep 2; ` +
		`systemctl stop vpn-agent 2>/dev/null || true; ` +
		`systemctl disable vpn-agent 2>/dev/null || true; ` +
		`bash -c "$(curl -fsSL https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ remove --purge 2>/dev/null || true; ` +
		`rm -f /etc/systemd/system/vpn-agent.service; ` +
		`systemctl daemon-reload 2>/dev/null || true; ` +
		`rm -rf /etc/vpn-agent; ` +
		`rm -f /usr/local/bin/vpn-agent`

	script := "systemd-run --unit=vpn-agent-uninstall --collect " +
		"--description='vpn-agent self-uninstall' bash -c " + shellQuote(inner)

	res, err := system.RunShell(ctx, script)
	if err != nil {
		return nil, fmt.Errorf("failed to schedule uninstall: %s", res.Combined())
	}
	return map[string]string{"status": "uninstall scheduled"}, nil
}

// --- AmneziaWG handlers ---

func (d *Dispatcher) installAmneziaWG(ctx context.Context, _ json.RawMessage) (any, error) {
	res, err := d.amneziawg.Install(ctx)
	if err != nil {
		return nil, fmt.Errorf("install failed: %s", res.Combined())
	}
	return map[string]string{"output": res.Combined()}, nil
}

func (d *Dispatcher) uninstallAmneziaWG(ctx context.Context, _ json.RawMessage) (any, error) {
	res, err := d.amneziawg.Uninstall(ctx)
	if err != nil {
		return nil, fmt.Errorf("uninstall failed: %s", res.Combined())
	}
	return map[string]string{"output": res.Combined()}, nil
}

func (d *Dispatcher) restartAmneziaWG(ctx context.Context, _ json.RawMessage) (any, error) {
	if _, err := d.amneziawg.Restart(ctx); err != nil {
		return nil, err
	}
	return map[string]bool{"restarted": true}, nil
}

type applyAmneziaWGConfigParams struct {
	Config  string `json:"config"`
	Restart *bool  `json:"restart"`
}

func (d *Dispatcher) applyAmneziaWGConfig(ctx context.Context, params json.RawMessage) (any, error) {
	var p applyAmneziaWGConfigParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.Config == "" {
		return nil, fmt.Errorf("config is required")
	}
	if err := d.amneziawg.ApplyConfig(ctx, p.Config); err != nil {
		return nil, err
	}
	if restartWanted(p.Restart) {
		if _, err := d.amneziawg.Restart(ctx); err != nil {
			return nil, fmt.Errorf("config applied but restart failed: %w", err)
		}
	}
	return map[string]bool{"applied": true}, nil
}

type addAmneziaWGPeerParams struct {
	PeerConfigText string `json:"peer_config_text"`
	PublicKey      string `json:"public_key"`
	Email          string `json:"email"`
	AllowedIP      string `json:"allowed_ip"`
	// PrivateKey is opaque to the agent (a [Peer] block never contains a
	// private key) — it's only round-tripped in the result so the backend
	// can persist the peer into its stored config once the add is confirmed,
	// mirroring how add_vless_user's echoed client is used.
	PrivateKey string `json:"private_key"`
}

func (d *Dispatcher) addAmneziaWGPeer(ctx context.Context, params json.RawMessage) (any, error) {
	var p addAmneziaWGPeerParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PeerConfigText == "" {
		return nil, fmt.Errorf("peer_config_text is required")
	}
	if err := d.amneziawg.AddPeer(ctx, p.PeerConfigText); err != nil {
		return nil, err
	}
	return map[string]string{
		"public_key":  p.PublicKey,
		"email":       p.Email,
		"allowed_ip":  p.AllowedIP,
		"private_key": p.PrivateKey,
	}, nil
}

type removeAmneziaWGPeerParams struct {
	PublicKey string `json:"public_key"`
}

func (d *Dispatcher) removeAmneziaWGPeer(ctx context.Context, params json.RawMessage) (any, error) {
	var p removeAmneziaWGPeerParams
	if err := unmarshal(params, &p); err != nil {
		return nil, err
	}
	if p.PublicKey == "" {
		return nil, fmt.Errorf("public_key is required")
	}
	if err := d.amneziawg.RemovePeer(ctx, p.PublicKey); err != nil {
		return nil, err
	}
	return map[string]string{"public_key": p.PublicKey}, nil
}

// --- helpers ---

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func unmarshal(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	return json.Unmarshal(params, v)
}

// restartWanted defaults to true when the param is omitted, since most user/
// config changes only take effect after a restart.
func restartWanted(p *bool) bool {
	return p == nil || *p
}
