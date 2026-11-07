package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xiemo911/vpn-provision-agent/internal/config"
	"github.com/Xiemo911/vpn-provision-agent/internal/protocol"
)

// fakeServer is a control server that hands out queued actions one per poll
// and records every request in order.
type fakeServer struct {
	mu         sync.Mutex
	queue      []protocol.Action
	resultCode int // status returned for /result; 0 means 200
	events     []string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/api/agent/poll":
		f.events = append(f.events, "poll")
		resp := protocol.PollResponse{Actions: []protocol.Action{}}
		if len(f.queue) > 0 {
			resp.Actions = f.queue[:1]
			f.queue = f.queue[1:]
		}
		_ = json.NewEncoder(w).Encode(resp)
	case "/api/agent/result":
		var res protocol.ActionResult
		_ = json.NewDecoder(r.Body).Decode(&res)
		code := f.resultCode
		if code == 0 {
			code = http.StatusOK
		}
		f.events = append(f.events, "result "+res.ID+" "+http.StatusText(code))
		w.WriteHeader(code)
		_, _ = w.Write([]byte("{}"))
	case "/api/agent/progress":
		var p protocol.ProgressReport
		_ = json.NewDecoder(r.Body).Decode(&p)
		f.events = append(f.events, "progress "+p.ID+" "+p.Message)
		_, _ = w.Write([]byte("{}"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeServer) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeServer) takeEvents() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ev := f.events
	f.events = nil
	return ev
}

func newTestAgent(t *testing.T, f *fakeServer) *Agent {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	old := reportRetryDelay
	reportRetryDelay = time.Millisecond
	t.Cleanup(func() { reportRetryDelay = old })
	dir := t.TempDir()
	return New(&config.Config{
		ServerURL:           srv.URL,
		Token:               "token",
		AgentID:             "agent",
		PollIntervalSeconds: 60,
		XrayConfigPath:      dir + "/xray.json",
		AmneziaWGConfigPath: dir + "/awg0.conf",
	}, "test", "host")
}

// Unknown action types fail at once without touching the system, which is
// all these tests need from the dispatcher.
func action(id string) protocol.Action {
	return protocol.Action{ID: id, Type: "test_noop"}
}

func expectEvents(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n  got  %q\n  want %q", got, want)
	}
}

func TestDrainRunsQueuedActionsBackToBack(t *testing.T) {
	f := &fakeServer{queue: []protocol.Action{action("a1"), action("a2")}}
	a := newTestAgent(t, f)

	a.drain(context.Background())

	expectEvents(t, f.takeEvents(),
		"poll", "result a1 OK",
		"poll", "result a2 OK",
		"poll")
}

func TestUnreportedResultIsResentBeforeNextPoll(t *testing.T) {
	f := &fakeServer{queue: []protocol.Action{action("a1"), action("a2")}, resultCode: http.StatusBadGateway}
	a := newTestAgent(t, f)

	// The server can't take results: a1's is retried, queued, and polling
	// stops instead of fetching a2 ahead of it.
	a.drain(context.Background())
	expectEvents(t, f.takeEvents(),
		"poll",
		"result a1 Bad Gateway", "result a1 Bad Gateway", "result a1 Bad Gateway",
		"result a1 Bad Gateway")
	if len(a.unsent) != 1 {
		t.Fatalf("unsent = %d, want 1", len(a.unsent))
	}

	// Once it recovers, a1's result goes in first, then work resumes.
	f.set(func() { f.resultCode = 0 })
	a.drain(context.Background())
	expectEvents(t, f.takeEvents(),
		"result a1 OK",
		"poll", "result a2 OK",
		"poll")
	if len(a.unsent) != 0 {
		t.Fatalf("unsent = %d after recovery, want 0", len(a.unsent))
	}
}

func TestRejectedResultIsNotQueued(t *testing.T) {
	f := &fakeServer{queue: []protocol.Action{action("a1")}, resultCode: http.StatusUnauthorized}
	a := newTestAgent(t, f)

	a.drain(context.Background())

	expectEvents(t, f.takeEvents(), "poll", "result a1 Unauthorized", "poll")
	if len(a.unsent) != 0 {
		t.Fatalf("unsent = %d, want 0", len(a.unsent))
	}
}

func TestStaleQueuedResultIsDropped(t *testing.T) {
	f := &fakeServer{}
	a := newTestAgent(t, f)
	a.unsent = []protocol.ActionResult{{ID: "old", Timestamp: time.Now().Add(-2 * maxUnsentAge).Unix()}}

	a.drain(context.Background())

	expectEvents(t, f.takeEvents(), "poll")
	if len(a.unsent) != 0 {
		t.Fatalf("unsent = %d, want 0", len(a.unsent))
	}
}

// A script's progress lines reach the control server while the action runs,
// before its result.
func TestProgressLinesAreSentDuringTheAction(t *testing.T) {
	params, _ := json.Marshal(map[string]string{
		"command": "echo 'NULLSIFY_PROGRESS: Step one'; echo noise; echo 'NULLSIFY_PROGRESS: Step two'",
	})
	f := &fakeServer{queue: []protocol.Action{{ID: "a1", Type: protocol.ActionRunCommand, Params: params}}}
	a := newTestAgent(t, f)
	a.cfg.AllowRunCommand = true

	a.drain(context.Background())

	expectEvents(t, f.takeEvents(),
		"poll",
		"progress a1 Step one", "progress a1 Step two",
		"result a1 OK",
		"poll")
}
