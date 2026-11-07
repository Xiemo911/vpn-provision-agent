// Package system provides small helpers for running external commands.
package system

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"time"
)

// waitDelay bounds how long Run keeps reading output after the command's
// context ends: a killed shell's children could otherwise hold the output
// pipes open, and Run would never return.
const waitDelay = 10 * time.Second

// Result captures the output of a command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Combined returns stdout and stderr joined, trimmed.
func (r Result) Combined() string {
	return strings.TrimSpace(r.Stdout + r.Stderr)
}

// Run executes name with args and returns the captured output. A non-zero exit
// status is returned as a non-nil error along with a populated Result. When
// ctx ends, the command and everything it started are killed.
func Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	killGroupOnCancel(cmd)
	cmd.WaitDelay = waitDelay
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	if report := progressFrom(ctx); report != nil {
		cmd.Stdout = io.MultiWriter(&out, &progressWriter{report: report})
	}
	cmd.Stderr = &errBuf

	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errBuf.String()}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
		}
	}
	return res, err
}

// RunShell executes a shell script via bash -c.
func RunShell(ctx context.Context, script string) (Result, error) {
	return Run(ctx, "bash", "-c", script)
}
