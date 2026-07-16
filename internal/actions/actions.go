// Package actions dispatches control-server actions to local handlers.
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/config"
	"github.com/Xiemo911/vpn-provision-agent/internal/protocol"
	"github.com/Xiemo911/vpn-provision-agent/internal/system"
	"github.com/Xiemo911/vpn-provision-agent/internal/xray"
)

// Handler executes one action type. It returns a JSON-encodable output.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Dispatcher routes actions to handlers.
type Dispatcher struct {
	cfg      *config.Config
	xray     *xray.Manager
	handlers map[string]Handler
}

// NewDispatcher wires up all supported action handlers.
func NewDispatcher(cfg *config.Config, mgr *xray.Manager) *Dispatcher {
	d := &Dispatcher{cfg: cfg, xray: mgr}
	d.handlers = map[string]Handler{
		protocol.ActionInstallXray:   d.installXray,
		protocol.ActionUninstallXray: d.uninstallXray,
		protocol.ActionRestartXray:   d.restartXray,
		protocol.ActionAddUser:       d.addUser,
		protocol.ActionRemoveUser:    d.removeUser,
		protocol.ActionListUsers:     d.listUsers,
		protocol.ActionApplyConfig:   d.applyConfig,
		protocol.ActionGetStatus:     d.getStatus,
		protocol.ActionRunCommand:    d.runCommand,
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
	Config json.RawMessage `json:"config"`
	Restart *bool          `json:"restart"`
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

// --- helpers ---

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
