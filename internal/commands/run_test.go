package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hay-kot/svx/internal/app"
)

// counterPlan runs a command that appends a line to a file each run and
// exits with the code script picks from the run count in $n.
func counterPlan(t *testing.T, retries int, script string) (app.RunPlan, func() int) {
	t.Helper()
	runs := filepath.Join(t.TempDir(), "runs")
	cmd := `echo x >> "$RUNS"; n=$(wc -l < "$RUNS" | tr -d ' '); ` + script
	plan := app.RunPlan{
		Argv:       []string{"/bin/sh", "-c", cmd},
		Env:        []string{"RUNS=" + runs, "PATH=" + os.Getenv("PATH")},
		Retries:    retries,
		StopSignal: syscall.SIGTERM,
	}
	count := func() int {
		data, _ := os.ReadFile(runs) //nolint:gosec // test temp file
		return strings.Count(string(data), "x")
	}
	return plan, count
}

func TestRunWithRetriesGivesUp(t *testing.T) {
	plan, runs := counterPlan(t, 2, "exit 3")
	var out bytes.Buffer
	if code := runWithRetries(t.Context(), &out, plan, nil, 0); code != 3 {
		t.Fatalf("code = %d, want 3", code)
	}
	if runs() != 3 {
		t.Fatalf("runs = %d, want 3", runs())
	}
	if !strings.Contains(out.String(), "retry 2 of 2") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestRunWithRetriesStopsOnSuccess(t *testing.T) {
	plan, runs := counterPlan(t, 5, `[ "$n" -ge 2 ]`)
	if code := runWithRetries(t.Context(), &bytes.Buffer{}, plan, nil, 0); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if runs() != 2 {
		t.Fatalf("runs = %d, want 2", runs())
	}
}

// An exit that follows svx's stop signal is not a crash.
func TestRunWithRetriesNoRetryAfterStop(t *testing.T) {
	plan, runs := counterPlan(t, 5, "exit 1")
	stop := make(chan os.Signal, 1)
	stop <- syscall.SIGTERM
	if code := runWithRetries(t.Context(), &bytes.Buffer{}, plan, stop, 0); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if runs() != 1 {
		t.Fatalf("runs = %d, want 1", runs())
	}
}
