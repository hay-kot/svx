// Package tmux implements app.Multiplexer by running the tmux binary. svx
// never keeps a tmux process of its own alive: every call is one short tmux
// invocation.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/hay-kot/svx/internal/app"
)

// Client runs tmux. The zero value uses "tmux" on PATH and the default
// server.
type Client struct {
	// Bin is the tmux binary.
	Bin string
	// Socket selects a server by name, as `tmux -L`. Tests use it to keep
	// away from the developer's own server.
	Socket string
	// Config is passed as `tmux -f` and only matters when the call starts
	// the server.
	Config string
}

var _ app.Multiplexer = (*Client)(nil)

// FromEnv builds a client honouring SVX_TMUX_SOCKET and SVX_TMUX_CONFIG.
func FromEnv() *Client {
	return &Client{Bin: "tmux", Socket: os.Getenv("SVX_TMUX_SOCKET"), Config: os.Getenv("SVX_TMUX_CONFIG")}
}

func (c *Client) bin() string {
	if c.Bin == "" {
		return "tmux"
	}
	return c.Bin
}

func (c *Client) args(args ...string) []string {
	var pre []string
	if c.Socket != "" {
		pre = append(pre, "-L", c.Socket)
	}
	if c.Config != "" {
		pre = append(pre, "-f", c.Config)
	}
	return append(pre, args...)
}

func (c *Client) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin(), c.args(args...)...) //nolint:gosec // tmux with arguments svx builds itself
	cmd.Env = detachedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		if _, ok := errors.AsType[*exec.Error](err); ok {
			return "", fmt.Errorf("tmux is not installed or not on PATH: %w", err)
		}
		return string(out), &Error{Args: args, Output: strings.TrimSpace(string(out)), Err: err}
	}
	return string(out), nil
}

// detachedEnv drops TMUX and TMUX_PANE. With them set, tmux resolves an
// untargeted command to the caller's own pane, and a missed -t would act
// on the pane svx was run from. Only Attach needs them, to switch clients.
func detachedEnv() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=")
	})
}

// Error is a failed tmux call.
type Error struct {
	Args   []string
	Output string
	Err    error
}

func (e *Error) Error() string {
	cmd := ""
	if len(e.Args) > 0 {
		cmd = e.Args[0]
	}
	if e.Output != "" {
		return fmt.Sprintf("tmux %s: %s", cmd, e.Output)
	}
	return fmt.Sprintf("tmux %s: %v", cmd, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// gone reports errors that mean the session or the whole server does not
// exist, which callers treat as absence rather than failure.
func gone(err error) bool {
	te, ok := errors.AsType[*Error](err)
	if !ok {
		return false
	}
	for _, s := range []string{"can't find session", "no server running", "can't find window", "error connecting to"} {
		if strings.Contains(te.Output, s) {
			return true
		}
	}
	return false
}

// sessionTarget matches a session by exact name. Without "=", tmux falls
// back to prefix matching and "work" would find "work-2".
func sessionTarget(session string) string { return "=" + session }

// Version returns `tmux -V` output, such as "tmux 3.4".
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, c.bin(), "-V").Output() //nolint:gosec // the configured tmux binary
	if err != nil {
		return "", fmt.Errorf("running tmux -V: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Window user options svx tags its windows with. Windows are found by tag,
// not name, because a stack can share a session with the user's own
// windows.
const (
	stackOption   = "@svx_stack"
	serviceOption = "@svx_service"
)

// CurrentSession returns the session of the pane svx runs in. TMUX names
// the caller's server; it has to be this client's server, or the pane id
// would point at an unrelated pane.
func (c *Client) CurrentSession(ctx context.Context) (string, bool, error) {
	tmuxEnv, pane := os.Getenv("TMUX"), os.Getenv("TMUX_PANE")
	if tmuxEnv == "" || pane == "" {
		return "", false, nil
	}
	out, err := c.run(ctx, "display-message", "-p", "-t", pane, "#{socket_path}|#{session_name}")
	if err != nil {
		return "", false, nil //nolint:nilerr // a pane this server does not know means svx runs outside it
	}
	socket, session, _ := strings.Cut(strings.TrimSpace(out), "|")
	if callerSocket, _, _ := strings.Cut(tmuxEnv, ","); socket != callerSocket {
		return "", false, nil
	}
	return session, true, nil
}

// HasSession reports whether session exists. A missing server means it
// does not.
func (c *Client) HasSession(ctx context.Context, session string) (bool, error) {
	_, err := c.run(ctx, "has-session", "-t", sessionTarget(session))
	switch {
	case err == nil:
		return true, nil
	case gone(err):
		return false, nil
	default:
		return false, err
	}
}

// StartWindow creates a detached window in an existing session, then tags
// and configures it. remain-on-exit is set per window
// because a session-level setting does not reach windows created later.
//
// tmux cannot target a window created earlier in the same invocation, so
// setup is a second call. A fast command could exit before it, taking its
// window and exit status with it. svx windows run the _run shim, which
// waits for PaneReady first, and setup sets the service tag last.
func (c *Client) StartWindow(ctx context.Context, spec app.WindowSpec) (string, error) {
	args := []string{"new-window", "-d", "-t", sessionTarget(spec.Session) + ":", "-n", windowName(spec), "-P", "-F", "#{window_id}"}
	if spec.Dir != "" {
		args = append(args, "-c", spec.Dir)
	}
	args = append(args, envArgs(spec.Env)...)
	args = append(args, spec.Command...)

	out, err := c.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("starting window %s in %s: %w", spec.Service, spec.Session, err)
	}
	id := strings.TrimSpace(out)

	setup := []string{
		"set-option", "-w", "-t", id, "remain-on-exit", "on",
		";", "set-option", "-w", "-t", id, stackOption, spec.Stack,
	}
	if spec.LogFile != "" {
		setup = append(setup, ";", "pipe-pane", "-t", id, pipeCommand(spec.LogFile))
	}
	setup = append(setup, ";", "set-option", "-w", "-t", id, serviceOption, spec.Service)
	if _, err := c.run(ctx, setup...); err != nil {
		_ = c.KillWindow(ctx, id)
		return "", fmt.Errorf("setting up window %s in %s: %w", spec.Service, spec.Session, err)
	}
	return id, nil
}

// windowName is the literal name for spec's window. new-window expands
// formats in -n, so "#" is doubled to keep "#{...}" from rendering.
func windowName(spec app.WindowSpec) string {
	name := spec.Name
	if name == "" {
		name = spec.Service
	}
	return strings.ReplaceAll(name, "#", "##")
}

// RespawnWindow restarts a window's command in place. -k kills whatever is
// still running there first.
func (c *Client) RespawnWindow(ctx context.Context, spec app.WindowSpec) error {
	args := []string{"respawn-pane", "-k", "-t", spec.Window}
	if spec.Dir != "" {
		args = append(args, "-c", spec.Dir)
	}
	args = append(args, envArgs(spec.Env)...)
	args = append(args, spec.Command...)
	// respawn-pane drops the pane's pipe, and pipe-pane -o in the same call
	// still sees the old one and does nothing. Closing it explicitly and
	// opening a fresh one is what keeps a restarted service logging.
	if spec.LogFile != "" {
		args = append(args, ";", "pipe-pane", "-t", spec.Window, ";", "pipe-pane", "-t", spec.Window, pipeCommand(spec.LogFile))
	}
	if _, err := c.run(ctx, args...); err != nil {
		return fmt.Errorf("respawning %s in %s: %w", spec.Service, spec.Session, err)
	}
	return nil
}

func envArgs(env map[string]string) []string {
	var args []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		args = append(args, "-e", k+"="+env[k])
	}
	return args
}

// pipeCommand is run by tmux through /bin/sh, so the path is single-quoted.
func pipeCommand(path string) string {
	return "cat >> '" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

// windowFormat separates fields with "|" rather than a tab: without a UTF-8
// locale, tmux sanitises control characters in format output, and a tab
// comes back as "_".
const windowFormat = "#{window_id}|#{" + stackOption + "}|#{" + serviceOption + "}|#{pane_pid}|#{pane_dead}|#{pane_dead_status}"

// Windows lists the windows in session tagged with stack, named by their
// service. A missing session has none.
func (c *Client) Windows(ctx context.Context, session, stack string) ([]app.Window, error) {
	out, err := c.run(ctx, "list-windows", "-t", sessionTarget(session)+":", "-F", windowFormat)
	if err != nil {
		if gone(err) {
			return nil, nil
		}
		return nil, err
	}
	return parseWindows(out, stack), nil
}

// parseWindows keeps windows carrying both tags. A user option set on the
// session shows up in every window's format, so the stack tag alone could
// match the user's windows; the service tag is only ever set per window.
func parseWindows(out, stack string) []app.Window {
	var windows []app.Window
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		f := strings.Split(line, "|")
		if len(f) != 6 || f[1] != stack || f[2] == "" {
			continue
		}
		pid, _ := strconv.Atoi(f[3])
		code, _ := strconv.Atoi(f[5])
		windows = append(windows, app.Window{
			ID:       f[0],
			Name:     f[2],
			PanePID:  pid,
			Dead:     f[4] == "1",
			ExitCode: code,
		})
	}
	return windows
}

// KillWindow kills a window. One that is already gone is not an error.
func (c *Client) KillWindow(ctx context.Context, window string) error {
	if _, err := c.run(ctx, "kill-window", "-t", window); err != nil && !gone(err) {
		return err
	}
	return nil
}

// Capture returns the last lines of a window's history, or all of it when
// lines is 0, without blank lines.
func (c *Client) Capture(ctx context.Context, window string, lines int) (string, error) {
	start := "-"
	if lines > 0 {
		start = "-" + strconv.Itoa(lines*4)
	}
	out, err := c.run(ctx, "capture-pane", "-p", "-J", "-t", window, "-S", start)
	if err != nil {
		return "", err
	}
	// A detached pane is 50 rows tall and a short-lived command fills only
	// the top few, so blank rows and tmux's own "Pane is dead" line are
	// dropped before taking the tail.
	var kept []string
	for l := range strings.SplitSeq(out, "\n") {
		l = strings.TrimRight(l, " ")
		if l == "" || strings.HasPrefix(l, "Pane is dead") {
			continue
		}
		kept = append(kept, l)
	}
	if lines > 0 && len(kept) > lines {
		kept = kept[len(kept)-lines:]
	}
	return strings.Join(kept, "\n"), nil
}

// PaneReady reports whether StartWindow has finished setting up the pane's
// window, given as a pane id such as %3.
func (c *Client) PaneReady(ctx context.Context, pane string) (bool, error) {
	out, err := c.run(ctx, "display-message", "-p", "-t", pane, "#{"+serviceOption+"}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}
