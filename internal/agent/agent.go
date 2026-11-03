// Package agent contains the main polling loop.
package agent

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/actions"
	"github.com/Xiemo911/vpn-provision-agent/internal/amneziawg"
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
	amneziawg  *amneziawg.Manager
	version    string
	hostname   string
	startTime  time.Time

	// unsent holds results the control server hasn't acknowledged (it was
	// unreachable, e.g. mid-deploy), oldest first. They're resent before the
	// next poll, so a backend restart doesn't lose an install's outcome.
	// Kept in memory only: an agent restart drops them.
	unsent []protocol.ActionResult
}

// maxUnsent bounds the resend queue; past it the oldest result is dropped.
const maxUnsent = 100

// maxUnsentAge is how long a queued result keeps being resent. By then the
// control server has failed the action as stalled and ignores it anyway, and
// a result it keeps rejecting must not block polling forever.
const maxUnsentAge = time.Hour

// reportAttempts and reportRetryDelay set how hard a fresh result is pushed
// before it's queued for the next poll instead.
const reportAttempts = 3

var reportRetryDelay = 5 * time.Second

// New builds an Agent from config.
func New(cfg *config.Config, version, hostname string) *Agent {
	mgr := xray.New(cfg.XrayConfigPath)
	awgMgr := amneziawg.New(cfg.AmneziaWGConfigPath)
	return &Agent{
		cfg:        cfg,
		api:        api.New(cfg.ServerURL, cfg.Token, cfg.AgentID),
		dispatcher: actions.NewDispatcher(cfg, mgr, awgMgr),
		xray:       mgr,
		amneziawg:  awgMgr,
		version:    version,
		hostname:   hostname,
		startTime:  time.Now(),
	}
}

// Run polls until the context is cancelled.
func (a *Agent) Run(ctx context.Context) error {
	log.Printf("agent %s started, polling %s every %s", a.cfg.AgentID, a.cfg.ServerURL, a.cfg.PollInterval())

	// Poll once immediately so a fresh agent registers without waiting.
	a.drain(ctx)

	ticker := time.NewTicker(a.cfg.PollInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("agent shutting down")
			return nil
		case <-ticker.C:
			a.drain(ctx)
		}
	}
}

// drain polls until the control server has nothing more queued. The server
// hands out one action per poll, so this runs a multi-step install's steps
// back to back instead of one per poll interval.
func (a *Agent) drain(ctx context.Context) {
	for ctx.Err() == nil && a.tick(ctx) {
	}
}

// tick resends any unacknowledged results, then polls and runs what it gets.
// It reports whether it ran any action.
func (a *Agent) tick(ctx context.Context) bool {
	// Results go back in the order the actions ran; polling before they're
	// in would let the server hand out a step whose predecessor failed.
	if !a.flushUnsent(ctx) {
		return false
	}

	req := protocol.PollRequest{
		AgentID:   a.cfg.AgentID,
		Hostname:  a.hostname,
		Version:   a.version,
		Timestamp: time.Now().Unix(),
		Status:    a.status(ctx),
	}

	resp, err := a.api.Poll(ctx, req)
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			log.Printf("poll error: %v (check token/agent_id in the agent config)", err)
		} else {
			log.Printf("poll error: %v", err)
		}
		return false
	}
	if len(resp.Actions) == 0 {
		return false
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
		a.report(ctx, result)
	}
	return true
}

// report sends a result, retrying briefly, and queues it for resending if
// the control server still can't take it.
func (a *Agent) report(ctx context.Context, result protocol.ActionResult) {
	var err error
	for attempt := 1; attempt <= reportAttempts; attempt++ {
		if err = a.api.ReportResult(ctx, result); err == nil {
			return
		}
		if errors.Is(err, api.ErrUnauthorized) {
			// The server no longer knows this agent (e.g. its server was just
			// deleted); no retry will change that.
			log.Printf("dropping result for %s: %v", result.ID, err)
			return
		}
		if attempt < reportAttempts {
			select {
			case <-ctx.Done():
				attempt = reportAttempts
			case <-time.After(reportRetryDelay):
			}
		}
	}
	log.Printf("failed to report result for %s, will resend: %v", result.ID, err)
	a.unsent = append(a.unsent, result)
	if len(a.unsent) > maxUnsent {
		log.Printf("dropping result for %s: resend queue full", a.unsent[0].ID)
		a.unsent = a.unsent[1:]
	}
}

// flushUnsent resends queued results in order and reports whether all of
// them got through.
func (a *Agent) flushUnsent(ctx context.Context) bool {
	for len(a.unsent) > 0 {
		result := a.unsent[0]
		if time.Since(time.Unix(result.Timestamp, 0)) > maxUnsentAge {
			log.Printf("dropping result for %s: not accepted within %s", result.ID, maxUnsentAge)
			a.unsent = a.unsent[1:]
			continue
		}
		err := a.api.ReportResult(ctx, result)
		if err != nil && !errors.Is(err, api.ErrUnauthorized) {
			log.Printf("still unable to report result for %s: %v", result.ID, err)
			return false
		}
		if err != nil {
			log.Printf("dropping result for %s: %v", result.ID, err)
		} else {
			log.Printf("reported queued result for %s", result.ID)
		}
		a.unsent = a.unsent[1:]
	}
	return true
}

func (a *Agent) status(ctx context.Context) protocol.AgentStatus {
	return protocol.AgentStatus{
		XrayInstalled: a.xray.IsInstalled(),
		XrayRunning:   a.xray.IsRunning(ctx),
		XrayVersion:   a.xray.Version(ctx),

		AmneziaWGInstalled: a.amneziawg.IsInstalled(),
		AmneziaWGRunning:   a.amneziawg.IsRunning(ctx),
		AmneziaWGVersion:   a.amneziawg.Version(ctx),

		UptimeSeconds: int64(time.Since(a.startTime).Seconds()),
	}
}
