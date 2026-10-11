package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hay-kot/svx/internal/app"
)

const testStack = "proj--stack"

// newTestClient starts nothing yet: the first call that creates a session
// starts a private server on its own socket with no config, and cleanup
// kills it. The developer's own server is never touched.
func newTestClient(t *testing.T) *Client {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	c := &Client{Bin: "tmux", Socket: "svx-test-" + hex.EncodeToString(buf), Config: "/dev/null"}
	t.Cleanup(func() {
		// tmux leaves its socket file behind when the server exits.
		socket, _ := c.run(context.Background(), "display-message", "-p", "#{socket_path}")
		_, _ = c.run(context.Background(), "kill-server")
		if socket = strings.TrimSpace(socket); socket != "" {
			_ = os.Remove(socket)
		}
	})
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func findWindow(t *testing.T, c *Client, session, name string) (app.Window, bool) {
	t.Helper()
	ws, err := c.Windows(t.Context(), session, testStack)
	if err != nil {
		t.Fatalf("Windows: %v", err)
	}
	for _, w := range ws {
		if w.Name == name {
			return w, true
		}
	}
	return app.Window{}, false
}

// gated runs script once the window is set up, the way the _run shim
// waits for PaneReady.
func gated(c *Client, script string) []string {
	wait := `until [ -n "$(tmux -L ` + c.Socket + ` display-message -p -t "$TMUX_PANE" '#{@svx_service}')" ]; do sleep 0.01; done; `
	return []string{"sh", "-c", wait + script}
}

func startWindow(t *testing.T, c *Client, session, service string, cmd ...string) string {
	t.Helper()
	id, err := c.StartWindow(t.Context(), app.WindowSpec{
		Session: session, Stack: testStack, Service: service, Command: cmd,
	})
	if err != nil {
		t.Fatalf("StartWindow: %v", err)
	}
	return id
}

// userSession makes a session with one plain window, standing in for the
// user's own, and returns its pane id.
func userSession(t *testing.T, c *Client, name string) string {
	t.Helper()
	out, err := c.run(t.Context(), "new-session", "-d", "-s", name, "-n", "shell", "-P", "-F", "#{pane_id}", "sleep", "300")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// A command that exits as soon as setup allows still leaves its window,
// with its exit status readable.
func TestFastExitKeepsWindow(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s1")
	startWindow(t, c, "s1", "first", gated(c, "exit 7")...)
	startWindow(t, c, "s1", "second", gated(c, "exit 3")...)

	for name, code := range map[string]int{"first": 7, "second": 3} {
		waitFor(t, name+" to die", func() bool {
			w, ok := findWindow(t, c, "s1", name)
			return ok && w.Dead && w.ExitCode == code
		})
	}
}

// The pane's process leads its own process group, so signalling the
// negative pane pid reaches the command's children too.
func TestPanePidLeadsProcessGroup(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s3")
	startWindow(t, c, "s3", "svc", "sh", "-c", "sleep 300 & wait")

	w, _ := findWindow(t, c, "s3", "svc")
	pgid, err := syscall.Getpgid(w.PanePID)
	if err != nil {
		t.Fatal(err)
	}
	if pgid != w.PanePID {
		t.Fatalf("pgid = %d, want pane pid %d", pgid, w.PanePID)
	}

	var child int
	waitFor(t, "child sleep to start", func() bool {
		out, _ := exec.CommandContext(t.Context(), "pgrep", "-P", strconv.Itoa(w.PanePID)).Output() //nolint:gosec // a pid from tmux
		child, _ = strconv.Atoi(strings.TrimSpace(string(out)))
		return child > 0
	})

	if err := syscall.Kill(-w.PanePID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "pane to die", func() bool {
		w, ok := findWindow(t, c, "s3", "svc")
		return ok && w.Dead
	})
	waitFor(t, "child to exit", func() bool {
		return syscall.Kill(child, 0) != nil
	})
}

// Windows returns only the stack's own windows, by service, even beside a
// user window of the same name and another stack's windows.
func TestWindowsFindsStackByTag(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s5")
	if _, err := c.run(t.Context(), "new-window", "-d", "-t", "=s5:", "-n", "web", "sleep", "300"); err != nil {
		t.Fatal(err)
	}
	id := startWindow(t, c, "s5", "web", "sleep", "300")
	if _, err := c.StartWindow(t.Context(), app.WindowSpec{Session: "s5", Stack: "proj--other", Service: "web", Command: []string{"sleep", "300"}}); err != nil {
		t.Fatal(err)
	}

	ws, err := c.Windows(t.Context(), "s5", testStack)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].ID != id || ws[0].Name != "web" {
		t.Fatalf("Windows = %+v, want only %s", ws, id)
	}
}

// A session is matched by exact name, where tmux alone would match a
// prefix.
func TestExactSessionTarget(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "work-2")
	startWindow(t, c, "work-2", "svc", "sleep", "300")

	if _, err := c.run(t.Context(), "has-session", "-t", "work"); err != nil {
		t.Fatalf("bare prefix should match: %v", err)
	}
	if ws, err := c.Windows(t.Context(), "work", testStack); err != nil || len(ws) != 0 {
		t.Fatalf("Windows(work) = %v, %v; want none from work-2", ws, err)
	}
	if _, err := c.StartWindow(t.Context(), app.WindowSpec{Session: "work", Stack: testStack, Service: "svc", Command: []string{"sleep", "300"}}); err == nil {
		t.Fatal("StartWindow(work) created a window in work-2")
	}
}

func TestPipePaneLogsAndEnv(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s6")
	log := filepath.Join(t.TempDir(), "out.log")
	id, err := c.StartWindow(t.Context(), app.WindowSpec{
		Session: "s6", Stack: testStack, Service: "svc", Dir: t.TempDir(),
		Command: gated(c, `echo "hello $SVX_TEST_VAR"; sleep 30`),
		Env:     map[string]string{"SVX_TEST_VAR": "world"},
		LogFile: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "log output", func() bool {
		data, _ := os.ReadFile(log) //nolint:gosec // test temp file
		return strings.Contains(string(data), "hello world")
	})

	capture, err := c.Capture(t.Context(), id, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(capture, "hello world") {
		t.Fatalf("capture = %q", capture)
	}
}

func TestMissingSessionIsAbsence(t *testing.T) {
	c := newTestClient(t)
	ws, err := c.Windows(t.Context(), "nope", testStack)
	if err != nil || len(ws) != 0 {
		t.Fatalf("Windows = %v, %v", ws, err)
	}
	if err := c.KillWindow(t.Context(), "@9999"); err != nil {
		t.Fatal(err)
	}
}

func TestHasSessionMatchesExactName(t *testing.T) {
	c := newTestClient(t)
	if ok, err := c.HasSession(t.Context(), "work"); err != nil || ok {
		t.Fatalf("HasSession with no server = %v, %v; want false", ok, err)
	}
	userSession(t, c, "work-2")
	if ok, err := c.HasSession(t.Context(), "work"); err != nil || ok {
		t.Fatalf("HasSession(work) = %v, %v; want no prefix match", ok, err)
	}
	if ok, err := c.HasSession(t.Context(), "work-2"); err != nil || !ok {
		t.Fatalf("HasSession(work-2) = %v, %v; want true", ok, err)
	}
}

// A respawned pane keeps its window and keeps logging to the same file.
func TestRespawnKeepsWindowAndLogging(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s7")
	log := filepath.Join(t.TempDir(), "out.log")
	spec := app.WindowSpec{
		Session: "s7", Stack: testStack, Service: "svc", LogFile: log,
		Command: gated(c, "echo run1; sleep 30"),
	}
	id, err := c.StartWindow(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first run in log", func() bool {
		data, _ := os.ReadFile(log) //nolint:gosec // test temp file
		return strings.Contains(string(data), "run1")
	})
	before, _ := findWindow(t, c, "s7", "svc")

	spec.Window = id
	spec.Command = gated(c, "echo run2; sleep 30")
	if err := c.RespawnWindow(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second run in log", func() bool {
		data, _ := os.ReadFile(log) //nolint:gosec // test temp file
		return strings.Contains(string(data), "run2")
	})
	after, ok := findWindow(t, c, "s7", "svc")
	if !ok || after.ID != before.ID || after.PanePID == before.PanePID {
		t.Fatalf("pane not respawned in place: before %+v after %+v", before, after)
	}
}

// The window name is display only and taken literally: "." and ":" stay
// because windows are targeted by id, "#" does not expand as a format, and
// the stack still finds the window by its service tag, through a respawn.
func TestWindowNameIsLiteralAndDisplayOnly(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s8")
	spec := app.WindowSpec{
		Session: "s8", Stack: testStack, Service: "api", Name: "rcp.api:#{session_name}",
		Command: gated(c, "sleep 30"),
	}
	id, err := c.StartWindow(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	windowName := func() string {
		out, err := c.run(t.Context(), "display-message", "-p", "-t", id, "#{window_name}")
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	if got := windowName(); got != spec.Name {
		t.Fatalf("window name = %q, want %q", got, spec.Name)
	}
	if w, ok := findWindow(t, c, "s8", "api"); !ok || w.ID != id {
		t.Fatalf("api window by tag = %+v, %v; want %s", w, ok, id)
	}

	spec.Window = id
	if err := c.RespawnWindow(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	if got := windowName(); got != spec.Name {
		t.Fatalf("window name after respawn = %q, want %q", got, spec.Name)
	}
	if _, err := c.run(t.Context(), "select-window", "-t", id); err != nil {
		t.Fatalf("select-window by id: %v", err)
	}
}

// Without a name, the window is named after its service.
func TestWindowNameDefaultsToService(t *testing.T) {
	c := newTestClient(t)
	userSession(t, c, "s9")
	id := startWindow(t, c, "s9", "web", "sleep", "300")
	out, err := c.run(t.Context(), "display-message", "-p", "-t", id, "#{window_name}")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "web" {
		t.Fatalf("window name = %q, want web", got)
	}
}

// Run from a pane on the same server, svx adds its windows to that pane's
// session in the background and leaves the user's own windows as they
// were: no pipe, no remain-on-exit, no tags, and still the active window.
func TestStartWindowInCallerSessionLeavesCallerAlone(t *testing.T) {
	c := newTestClient(t)
	pane := userSession(t, c, "user")
	socket, err := c.run(t.Context(), "display-message", "-p", "#{socket_path}")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", strings.TrimSpace(socket)+",1,0")
	t.Setenv("TMUX_PANE", pane)

	session, inside, err := c.CurrentSession(t.Context())
	if err != nil || !inside || session != "user" {
		t.Fatalf("CurrentSession = %q, %v, %v; want user", session, inside, err)
	}

	_, err = c.StartWindow(t.Context(), app.WindowSpec{
		Session: "user", Stack: testStack, Service: "svc",
		Command: gated(c, "sleep 30"),
		LogFile: filepath.Join(t.TempDir(), "svc.log"),
	})
	if err != nil {
		t.Fatal(err)
	}

	out, err := c.run(t.Context(), "display-message", "-p", "-t", pane,
		"#{pane_pipe}|#{remain-on-exit}|#{@svx_stack}|#{@svx_service}|#{window_active}")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "0|off|||1" {
		t.Fatalf("caller pane pipe|remain|stack|service|active = %q, want 0|off|||1", got)
	}
	if _, ok := findWindow(t, c, "user", "svc"); !ok {
		t.Fatal("svc window missing from the caller's session")
	}
}

// TMUX for another server means svx is not inside this one, even when a
// pane with the same id exists here.
func TestCurrentSessionIgnoresOtherServer(t *testing.T) {
	c := newTestClient(t)
	pane := userSession(t, c, "user")
	t.Setenv("TMUX", "/tmp/some-other-server,1,0")
	t.Setenv("TMUX_PANE", pane)
	if session, inside, err := c.CurrentSession(t.Context()); err != nil || inside {
		t.Fatalf("CurrentSession = %q, %v, %v; want outside", session, inside, err)
	}
}
