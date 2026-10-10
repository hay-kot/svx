//go:build integration

package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type statusJSON struct {
	Version  int    `json:"version"`
	Project  string `json:"project"`
	Stack    string `json:"stack"`
	Slot     int    `json:"slot"`
	Status   string `json:"status"`
	Session  string `json:"session"`
	Services []struct {
		Name  string         `json:"name"`
		State string         `json:"state"`
		Ports map[string]int `json:"ports"`
	} `json:"services"`
}

func status(t *testing.T, h *Harness, dir string) statusJSON {
	t.Helper()
	out, err := h.Stdout(dir, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	var s statusJSON
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	return s
}

func (s statusJSON) port(service string) int {
	for _, svc := range s.Services {
		if svc.Name == service {
			return svc.Ports["main"]
		}
	}
	return 0
}

func (s statusJSON) key() string { return s.Project + "--" + s.Stack }

func (s statusJSON) state(service string) string {
	for _, svc := range s.Services {
		if svc.Name == service {
			return svc.State
		}
	}
	return ""
}

func exitCode(err error) int {
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

// The milestone 1 check: two checkouts of one project run at once on
// different ports, ls shows both, and down in one leaves the other running.
func TestTwoCheckoutsRunSideBySide(t *testing.T) {
	h := NewHarness(t)
	a := h.CopySample("checkout-a")
	b := h.CopySample("checkout-b")

	for _, dir := range []string{a, b} {
		if out, err := h.Run(dir, "up"); err != nil {
			t.Fatalf("up in %s: %v\n%s", dir, err, out)
		}
	}

	sa, sb := status(t, h, a), status(t, h, b)
	if sa.Stack == sb.Stack {
		t.Fatalf("both checkouts got stack name %q", sa.Stack)
	}
	if sa.Slot == sb.Slot || sa.port("web") == sb.port("web") || sa.port("api") == sb.port("api") {
		t.Fatalf("stacks share a slot or ports: a=%+v b=%+v", sa, sb)
	}
	for _, s := range []statusJSON{sa, sb} {
		if s.state("api") != "running" || s.state("web") != "running" || s.state("worker") != "idle" {
			t.Fatalf("unexpected states: %+v", s.Services)
		}
	}

	ls, err := h.Stdout(a, "ls")
	if err != nil {
		t.Fatalf("ls: %v", err)
	}
	if !strings.Contains(ls, sa.Stack) || !strings.Contains(ls, sb.Stack) {
		t.Fatalf("ls does not list both stacks:\n%s", ls)
	}

	if out, err := h.Run(a, "down"); err != nil {
		t.Fatalf("down: %v\n%s", err, out)
	}
	windows := h.StackWindows()
	if strings.Contains(windows, sa.key()) {
		t.Fatalf("a's windows survived down:\n%s", windows)
	}
	if !strings.Contains(windows, sb.key()) {
		t.Fatalf("down in a stopped b:\n%s", windows)
	}
	if got := status(t, h, b); got.Status != "up" || got.state("web") != "running" {
		t.Fatalf("b after a's down: %+v", got)
	}

	// The slot is remembered, so a comes back on the same ports.
	if out, err := h.Run(a, "up"); err != nil {
		t.Fatalf("second up: %v\n%s", err, out)
	}
	if again := status(t, h, a); again.Slot != sa.Slot || again.port("web") != sa.port("web") {
		t.Fatalf("slot moved on re-up: before %d after %d", sa.Slot, again.Slot)
	}
}

func TestNamedStacksFromOneCheckout(t *testing.T) {
	h := NewHarness(t)
	dir := h.CopySample("checkout")
	for _, name := range []string{"one", "two"} {
		if out, err := h.Run(dir, "up", "--name", name, "--no-wait"); err != nil {
			t.Fatalf("up %s: %v\n%s", name, err, out)
		}
	}
	if windows := h.StackWindows(); !strings.Contains(windows, "sample--one") || !strings.Contains(windows, "sample--two") {
		t.Fatalf("windows:\n%s", windows)
	}
}

// The milestone 2 checks: a failing dependency makes up exit 1 naming it,
// and exec sees the stack's ports.
func TestFailingDependencyAndExec(t *testing.T) {
	h := NewHarness(t)
	dir := t.TempDir()
	config := `
project: deps
services:
  db:
    run: 'echo "db cannot start"; exit 3'
    port: 25432
  api:
    run: 'sleep 300'
    port: 28080
    depends: [db]
`
	if err := os.WriteFile(filepath.Join(dir, "svx.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := h.Run(dir, "up")
	if code := exitCode(err); code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	for _, want := range []string{"service db failed", "db cannot start", "api not started: dependency db failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("up output lacks %q:\n%s", want, out)
		}
	}

	s := status(t, h, dir)
	if s.state("db") != "exited" || s.state("api") != "missing" {
		t.Fatalf("states after failure: %+v", s.Services)
	}

	env, err := h.Stdout(dir, "exec", "--", "env")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	for _, want := range []string{"SVX_PORT_DB=", "SVX_PORT_API=", "SVX_PROJECT=deps"} {
		if !strings.Contains(env, want) {
			t.Errorf("exec env lacks %s", want)
		}
	}
}

func TestReadyOutputLogsAndRestart(t *testing.T) {
	for _, logs := range []bool{false, true} {
		t.Run(fmt.Sprintf("logs=%t", logs), func(t *testing.T) {
			h := NewHarness(t)
			dir := t.TempDir()
			config := fmt.Sprintf(`
project: logs
settings:
  logs: %t
services:
  app:
    run: 'echo "booting"; sleep 0.5; printf "\033[32mserver ready\033[0m on %%s\n" "$PORT"; sleep 300'
    port: 29000
    ready: { output: 'server ready on \d+', timeout: 10s }
`, logs)
			if err := os.WriteFile(filepath.Join(dir, "svx.yaml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if out, err := h.Run(dir, "up"); err != nil {
				t.Fatalf("up: %v\n%s", err, out)
			}
			port := status(t, h, dir).port("app")
			if found, _ := filepath.Glob(filepath.Join(dir, ".svx", "*", "logs", "app.log")); (len(found) > 0) != logs {
				t.Errorf("log files = %v, want any = %t", found, logs)
			}

			out, err := h.Stdout(dir, "logs", "app")
			if err != nil {
				t.Fatalf("logs: %v", err)
			}
			if !strings.Contains(out, "booting") || !strings.Contains(out, "server ready on") || strings.Contains(out, "\x1b[") {
				t.Fatalf("logs:\n%q", out)
			}

			if out, err := h.Run(dir, "restart", "app"); err != nil {
				t.Fatalf("restart: %v\n%s", err, out)
			}
			if after := status(t, h, dir).port("app"); after != port {
				t.Fatalf("restart moved the port: %d -> %d", port, after)
			}
			// respawn-pane clears the visible screen, so only the log file
			// keeps the earlier run.
			want := 1
			if logs {
				want = 2
			}
			out, _ = h.Stdout(dir, "logs", "app")
			if got := strings.Count(out, "server ready on 29000"); got != want {
				t.Fatalf("expected %d runs in the log, got %d:\n%s", want, got, out)
			}
		})
	}
}

func TestConfigRendersWithoutStarting(t *testing.T) {
	h := NewHarness(t)
	dir := h.CopySample("checkout")
	out, err := h.Stdout(dir, "config")
	if err != nil {
		t.Fatalf("config: %v\n%s", err, out)
	}
	if !strings.Contains(out, "API_URL: http://127.0.0.1:18080") || !strings.Contains(out, "status: down") {
		t.Fatalf("config output:\n%s", out)
	}
	if windows := h.StackWindows(); strings.Contains(windows, "sample--") {
		t.Fatalf("config started something:\n%s", windows)
	}
}
