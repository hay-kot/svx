//go:build darwin && cgo

package menubar

import (
	"context"
	"time"

	"github.com/hay-kot/svx/internal/app"
)

// pollInterval is how often state is re-read. tmux, not svx, notices a
// service dying, so the menu has to poll to see it.
const pollInterval = 2 * time.Second

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
	tick := time.NewTicker(pollInterval)
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
	case ActionStop:
		_, err := b.App.Down(ctx, app.DownRequest{Target: app.Target{Project: a.Stack.Project, Stack: a.Stack.Stack}})
		return err
	case ActionRestart:
		req := app.RestartRequest{Target: app.Target{Project: a.Stack.Project, Stack: a.Stack.Stack}, All: a.Service == ""}
		if !req.All {
			req.Services = []string{a.Service}
		}
		_, err := b.App.Restart(ctx, req)
		return err
	case ActionNone:
	}
	return nil
}
