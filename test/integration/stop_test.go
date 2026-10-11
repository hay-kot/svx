//go:build integration

package integration

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// buildLeaky builds testdata/leaky into a temp directory.
func buildLeaky(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "leaky")
	out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./testdata/leaky").CombinedOutput() //nolint:gosec // the test helper
	if err != nil {
		t.Fatalf("building leaky: %v\n%s", err, out)
	}
	return bin
}

// leakyRun is one run of the leaky stack: the wrapper, supervisor and
// worker pids.
type leakyRun [3]int

func readRun(t *testing.T, dir string) leakyRun {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "pids")) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatalf("reading pids: %v", err)
	}
	var run leakyRun
	for i, f := range strings.Fields(string(data)) {
		run[i], _ = strconv.Atoi(f)
	}
	t.Cleanup(func() {
		for _, pid := range run {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return run
}

// assertStopped fails when any process of run is still alive. The short
// poll only covers the reaping of processes that already exited.
func assertStopped(t *testing.T, run leakyRun) {
	t.Helper()
	alive := func() []string {
		var left []string
		for i, name := range []string{"wrapper", "supervisor", "worker"} {
			if err := syscall.Kill(run[i], 0); !errors.Is(err, syscall.ESRCH) {
				left = append(left, fmt.Sprintf("%s (%d)", name, run[i]))
			}
		}
		return left
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for len(alive()) > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if left := alive(); len(left) > 0 {
		t.Errorf("still running: %s", strings.Join(left, ", "))
	}
}

// assertTornDown fails unless the supervisor had finished its teardown by
// the time svx returned.
func assertTornDown(t *testing.T, dir string, run leakyRun) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "done-"+strconv.Itoa(run[1]))); err != nil {
		t.Errorf("svx returned before the supervisor finished its teardown: %v", err)
	}
}

// The wrapper exits at once on SIGTERM while the supervisor behind it
// still has to stop a worker in a process group of its own. Nothing may
// survive restart or down.
//
// When the wrapper exits, the kernel sends SIGHUP to the pane's
// foreground group, since the wrapper leads the pane's session. A
// supervisor that dies on SIGHUP never stops its worker, so svx has to.
// One that survives it must get its whole teardown before svx returns.
// With the default SIGINT the wrapper stays up and the teardown always
// completes.
func TestStopWaitsForWholeProcessTree(t *testing.T) {
	leaky := buildLeaky(t)
	cases := []struct {
		name       string
		signal     string
		supervisor string
		tornDown   bool
	}{
		{name: "sigterm", signal: "SIGTERM", supervisor: "supervisor", tornDown: false},
		{name: "sigterm-nohup", signal: "SIGTERM", supervisor: "supervisor -nohup", tornDown: true},
		{name: "default", signal: "", supervisor: "supervisor", tornDown: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHarness(t)
			dir := t.TempDir()
			stopSignal := ""
			if tc.signal != "" {
				stopSignal = "stop_signal: " + tc.signal
			}
			config := fmt.Sprintf(`
project: leaky
services:
  app:
    run: '%[1]s wrapper %[1]s %[2]s %[3]s'
    ready: { output: 'supervisor up', timeout: 10s }
    stop_timeout: 4s
    %[4]s
`, leaky, tc.supervisor, dir, stopSignal)
			if err := os.WriteFile(filepath.Join(dir, "svx.yaml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}

			if out, err := h.Run(dir, "up"); err != nil {
				t.Fatalf("up: %v\n%s", err, out)
			}
			first := readRun(t, dir)

			if out, err := h.Run(dir, "restart", "app"); err != nil {
				t.Fatalf("restart: %v\n%s", err, out)
			}
			if tc.tornDown {
				assertTornDown(t, dir, first)
			}
			assertStopped(t, first)
			second := readRun(t, dir)
			if second == first {
				t.Fatalf("restart did not start a new run: %v", second)
			}

			if out, err := h.Run(dir, "down"); err != nil {
				t.Fatalf("down: %v\n%s", err, out)
			}
			if tc.tornDown {
				assertTornDown(t, dir, second)
			}
			assertStopped(t, second)
		})
	}
}
