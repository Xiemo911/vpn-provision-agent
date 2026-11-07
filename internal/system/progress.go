package system

import (
	"bytes"
	"context"
	"strings"
)

// ProgressMarker starts a line a script prints to say what it's doing now
// (see progress in PkgHelpers). Run passes those lines on to the reporter in
// its context, which the agent forwards to the control server, so the task
// page of a long install shows it's still moving.
const ProgressMarker = "NULLSIFY_PROGRESS: "

// maxProgressLine bounds how much of an unfinished output line is kept
// while waiting for its newline.
const maxProgressLine = 4096

type progressKey struct{}

// WithProgress returns a context whose commands report their progress lines
// to report.
func WithProgress(ctx context.Context, report func(msg string)) context.Context {
	return context.WithValue(ctx, progressKey{}, report)
}

func progressFrom(ctx context.Context) func(string) {
	report, _ := ctx.Value(progressKey{}).(func(string))
	return report
}

// progressWriter scans a command's stdout as it's written and reports each
// progress line as soon as it's complete.
type progressWriter struct {
	report  func(string)
	pending []byte
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.pending[:i]))
		w.pending = w.pending[i+1:]
		if msg, ok := strings.CutPrefix(line, ProgressMarker); ok && strings.TrimSpace(msg) != "" {
			w.report(strings.TrimSpace(msg))
		}
	}
	// A progress line is short; a longer unfinished line is other output
	// (a download's progress bar, say) and isn't worth keeping.
	if len(w.pending) > maxProgressLine {
		w.pending = nil
	}
	return len(p), nil
}
