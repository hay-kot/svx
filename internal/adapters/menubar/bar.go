package menubar

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"

	"github.com/rs/zerolog/log"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// ErrUnsupported is returned by Run on builds without the Cocoa UI.
var ErrUnsupported = errors.New("the menu bar needs macOS and an svx built with cgo")

// errRunning means another menu bar process holds the lock.
var errRunning = errors.New("the svx menu bar is already running")

// Bar is the menu bar app.
type Bar struct {
	App *app.App
	// Executable is the svx binary the Terminal attach script runs.
	Executable string
	// Terminal is the app, by name, that runs the attach script, such as
	// Terminal or iTerm.
	Terminal string
	// AttachURI, when set, replaces Terminal: attach opens the URI it
	// renders. See ParseAttachURI.
	AttachURI *template.Template
}

// Run shows the menu bar item until ctx ends or the user picks Quit. It
// must be called from the main goroutine. When another menu bar already
// runs, Run returns nil: launchd restarts an agent that exits with an
// error, which would retry forever against the held lock.
func (b *Bar) Run(ctx context.Context) error {
	unlock, err := b.lock()
	if errors.Is(err, errRunning) {
		log.Info().Msg(err.Error())
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	return runUI(ctx, b)
}

func (b *Bar) lock() (func(), error) {
	path := filepath.Join(b.App.Store.Dir(), "menubar.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // inside svx's state directory
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errRunning
		}
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

// forwardedEnv is the environment the attach script needs from the menu
// bar: a terminal starts with a fresh one, and tmux must be on PATH and
// svx must find the same server and state.
var forwardedEnv = []string{"PATH", "SVX_STATE_DIR", "SVX_TMUX_SOCKET", "SVX_TMUX_CONFIG"}

// AttachScript is the shell script that attaches a terminal to a stack, at
// service's window when service is set.
func AttachScript(svx string, id domain.Identity, service string, getenv func(string) string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, k := range forwardedEnv {
		if v := getenv(k); v != "" {
			fmt.Fprintf(&b, "export %s=%s\n", k, shellQuote(v))
		}
	}
	b.WriteString("exec " + attachCommand(svx, id, service) + "\n")
	return b.String()
}

func attachCommand(svx string, id domain.Identity, service string) string {
	cmd := fmt.Sprintf("%s attach --project %s --name %s", shellQuote(svx), shellQuote(id.Project), shellQuote(id.Stack))
	if service != "" {
		cmd += " " + shellQuote(service)
	}
	return cmd
}

// AttachURIData is what an attach URI template sees: .project, .stack,
// .service (empty for the whole stack), .session, .command (the quoted svx
// attach command line) and .script (an executable file that runs .command
// with svx's environment, the safer choice when the terminal starts a fresh
// shell).
type AttachURIData = map[string]string

// ParseAttachURI parses an attach URI template. Besides text/template's
// builtins, such as urlquery, it has pathescape. A key outside
// AttachURIData fails when rendered.
func ParseAttachURI(text string) (*template.Template, error) {
	t, err := template.New("attach_uri").Option("missingkey=error").Funcs(template.FuncMap{"pathescape": url.PathEscape}).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parsing menubar.attach_uri: %w", err)
	}
	return t, nil
}

func renderAttachURI(t *template.Template, data AttachURIData) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("rendering menubar.attach_uri: %w", err)
	}
	return strings.TrimSpace(b.String()), nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
