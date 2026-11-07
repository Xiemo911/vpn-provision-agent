package system

import (
	"context"
	"reflect"
	"testing"
)

func TestProgressLinesAreReportedAsTheyArrive(t *testing.T) {
	var got []string
	w := &progressWriter{report: func(m string) { got = append(got, m) }}
	for _, chunk := range []string{"apt noise\nNULLSIFY_PRO", "GRESS: Installing build tools\n", "NULLSIFY_PROGRESS:   \n", "NULLSIFY_PROGRESS: Building\nunfinished"} {
		_, _ = w.Write([]byte(chunk))
	}
	if want := []string{"Installing build tools", "Building"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRunForwardsProgressFromContext(t *testing.T) {
	var got []string
	ctx := WithProgress(context.Background(), func(m string) { got = append(got, m) })
	res, err := RunShell(ctx, "echo hello; echo 'NULLSIFY_PROGRESS: Step one'; echo 'NULLSIFY_PROGRESS: Step two' >&2")
	if err != nil {
		t.Skipf("bash not usable here: %v", err)
	}
	if want := []string{"Step one"}; !reflect.DeepEqual(got, want) {
		t.Errorf("progress %q, want %q (stdout only)", got, want)
	}
	if res.Stdout == "" {
		t.Error("stdout is still captured")
	}
}

func TestLongUnfinishedLinesAreDropped(t *testing.T) {
	var got []string
	w := &progressWriter{report: func(m string) { got = append(got, m) }}
	noise := make([]byte, 3*maxProgressLine)
	for i := range noise {
		noise[i] = '#'
	}
	for i := 0; i < 10; i++ {
		_, _ = w.Write(noise)
		if len(w.pending) > maxProgressLine {
			t.Fatalf("kept %d bytes of an unfinished line", len(w.pending))
		}
	}
	_, _ = w.Write([]byte("\nNULLSIFY_PROGRESS: Still here\n"))
	if want := []string{"Still here"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
