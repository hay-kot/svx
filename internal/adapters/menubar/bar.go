package menubar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// ErrUnsupported is returned by Run on builds without the Cocoa UI.
var ErrUnsupported = errors.New("the menu bar needs macOS and an svx built with cgo")

// ErrRunning is returned by Run when another menu bar process holds the
// lock.
var ErrRunning = errors.New("the svx menu bar is already running")

// Bar is the menu bar app.
type Bar struct {
	App *app.App
	// Executable is the svx binary the Terminal attach script runs.
	Executable string
	// Terminal is the app, by name, that runs the attach script, such as
	// Terminal or iTerm.
	Terminal string
	// Interval is how often state is re-read. tmux, not svx, notices a
	// service dying, so the menu has to poll to see it.
	Interval time.Duration
}

// Run shows the menu bar item until ctx ends or the user picks Quit. It
// must be called from the main goroutine.
func (b *Bar) Run(ctx context.Context) error {
	if !Supported {
		return ErrUnsupported
	}
	unlock, err := b.lock()
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
			return nil, ErrRunning
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
	fmt.Fprintf(&b, "exec %s attach --project %s --name %s", shellQuote(svx), shellQuote(id.Project), shellQuote(id.Stack))
	if service != "" {
		b.WriteString(" " + shellQuote(service))
	}
	b.WriteString("\n")
	return b.String()
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
