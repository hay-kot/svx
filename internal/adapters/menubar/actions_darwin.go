//go:build darwin && cgo

package menubar

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

func (b *Bar) interval() time.Duration {
	if b.Interval <= 0 {
		return 2 * time.Second
	}
	return b.Interval
}

func (b *Bar) snapshot(ctx context.Context) Menu {
	views, err := b.App.List(ctx)
	if err != nil {
		return ErrorMenu(err)
	}
	return Build(views)
}

// watch draws the menu, then redraws whenever it changes. A send on
// refresh re-reads state at once instead of on the next tick.
func (b *Bar) watch(ctx context.Context, refresh <-chan struct{}, draw func(Menu)) {
	tick := time.NewTicker(b.interval())
	defer tick.Stop()
	var last Menu
	drawn := false
	for {
		m := b.snapshot(ctx)
		if !drawn || !m.Equal(last) {
			draw(m)
			last, drawn = m, true
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-refresh:
		}
	}
}

func (b *Bar) perform(ctx context.Context, a Action) error {
	switch a.Kind {
	case ActionOpenURL:
		return b.App.Browser.Open(ctx, a.URL)
	case ActionAttach:
		return b.attach(ctx, a.Stack, a.Service)
	case ActionStop:
		_, err := b.App.Down(ctx, app.DownRequest{Target: app.Target{Project: a.Stack.Project, Stack: a.Stack.Stack}})
		return err
	case ActionRestart:
		target := app.Target{Project: a.Stack.Project, Stack: a.Stack.Stack}
		services := []string{a.Service}
		if a.Service == "" {
			view, err := b.App.Status(ctx, target)
			if err != nil {
				return err
			}
			services = RestartTargets(view)
		}
		_, err := b.App.Restart(ctx, app.RestartRequest{Target: target, Services: services})
		return err
	case ActionNone:
	}
	return nil
}

// attach opens the terminal app on a script that runs svx attach. A
// .command file is what Terminal and iTerm both run when opened.
func (b *Bar) attach(ctx context.Context, id domain.Identity, service string) error {
	dir := filepath.Join(b.App.Store.Dir(), "menubar")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, id.Key()+".command")
	if err := os.WriteFile(path, []byte(AttachScript(b.Executable, id, service, os.Getenv)), 0o700); err != nil { //nolint:gosec // the terminal must execute it
		return err
	}
	terminal := b.Terminal
	if terminal == "" {
		terminal = "Terminal"
	}
	if out, err := exec.CommandContext(ctx, "open", "-a", terminal, path).CombinedOutput(); err != nil { //nolint:gosec // user-configured terminal app
		return fmt.Errorf("opening %s: %s", terminal, strings.TrimSpace(string(out)))
	}
	return nil
}
