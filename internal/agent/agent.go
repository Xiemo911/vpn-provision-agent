// Package agent contains the main polling loop.
package agent

import (
	"context"
	"log"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/actions"
	"github.com/Xiemo911/vpn-provision-agent/internal/api"
	"github.com/Xiemo911/vpn-provision-agent/internal/config"
	"github.com/Xiemo911/vpn-provision-agent/internal/protocol"
	"github.com/Xiemo911/vpn-provision-agent/internal/xray"
)

// Agent ties together config, the API client, and the action dispatcher.
type Agent struct {
	cfg        *config.Config
	api        *api.Client
	dispatcher *actions.Dispatcher
	xray       *xray.Manager
	version    string
	hostname   string
	startTime  time.Time
}

// New builds an Agent from config.
func New(cfg *config.Config, version, hostname string) *Agent {
	mgr := xray.New(cfg.XrayConfigPath)
	return &Agent{
		cfg:        cfg,
		api:        api.New(cfg.ServerURL, cfg.Token, cfg.AgentID),
		dispatcher: actions.NewDispatcher(cfg, mgr),
		xray:       mgr,
		version:    version,
		hostname:   hostname,
		startTime:  time.Now(),
	}
}

// Run polls until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("agent %s started, polling %s every %s", a.cfg.AgentID, a.cfg.ServerURL, a.cfg.PollInterval())

	// Poll once immediately so a fresh agent registers without waiting.
	a.tick(ctx)

	ticker := time.NewTicker(a.cfg.PollInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("agent shutting down")
			return nil
		case <-ticker.C:
			a.tick(ctx)
		}
	}
}

func (a *Agent) tick(ctx context.Context) {
	req := protocol.PollRequest{
		AgentID:   a.cfg.AgentID,
		Hostname:  a.hostname,
		Version:   a.version,
		Timestamp: time.Now().Unix(),
		Status:    a.status(ctx),
	}

	resp, err := a.api.Poll(ctx, req)
	if err != nil {
		log.Printf("poll error: %v", err)
		return
	}
	if len(resp.Actions) == 0 {
		return
	}

	log.Printf("received %d action(s)", len(resp.Actions))
	for _, action := range resp.Actions {
		log.Printf("executing action %s (%s)", action.ID, action.Type)
		result := a.dispatcher.Execute(ctx, action)
		if result.Success {
			log.Printf("action %s succeeded", action.ID)
		} else {
			log.Printf("action %s failed: %s", action.ID, result.Error)
		}
		if err := a.api.ReportResult(ctx, result); err != nil {
			log.Printf("failed to report result for %s: %v", action.ID, err)
		}
	}
}

func (a *Agent) status(ctx context.Context) protocol.AgentStatus {
	return protocol.AgentStatus{
		XrayInstalled: a.xray.IsInstalled(),
		XrayRunning:   a.xray.IsRunning(ctx),
		XrayVersion:   a.xray.Version(ctx),
		UptimeSeconds: int64(time.Since(a.startTime).Seconds()),
	}
}
