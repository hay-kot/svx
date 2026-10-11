package menubar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rs/zerolog/log"

	"github.com/hay-kot/svx/internal/app"
)

// ErrUnsupported is returned by Run on builds without the Cocoa UI.
var ErrUnsupported = errors.New("the menu bar needs macOS and an svx built with cgo")

// errRunning means another menu bar process holds the lock.
var errRunning = errors.New("the svx menu bar is already running")

// Bar is the menu bar app.
type Bar struct {
	App *app.App
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
