//go:build !windows

package system

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A timed-out shell must take its children down with it and return promptly,
// even though the children hold its output pipes open.
func TestRunKillsWholeProcessGroupOnTimeout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := RunShell(ctx, `sleep 60 & echo $! > `+pidFile+`; wait`)
	if err == nil {
		t.Fatal("expected an error from a timed-out command")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run returned after %s; want shortly after the timeout", elapsed)
	}

	raw, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("read child pid: %v", rerr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(2 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("child %d still running after the shell was killed", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
