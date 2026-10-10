package app

import (
	"context"
	"fmt"
	"slices"
	"syscall"
	"time"

	"github.com/hay-kot/appkit/concurrency"

	"github.com/hay-kot/svx/internal/domain"
)

// DownRequest asks for one stack, or all of them, to stop.
type DownRequest struct {
	Target Target
	All    bool
	// Purge deletes the stack's data directory.
	Purge bool
}

// DownResult lists the stacks that were stopped.
type DownResult struct {
	Stopped []domain.Identity
	// Forced lists services that ignored their stop signal and were killed.
	Forced []string
}

// Down stops stacks in reverse dependency order, then closes their windows.
// The session itself is left alone: it may be the user's own, and tmux ends
// a stack's own session when its last window closes.
// A stack's slot is released but kept on record as its preference.
func (a *App) Down(ctx context.Context, req DownRequest) (DownResult, error) {
	var stacks []domain.Stack
	if req.All {
		records, err := a.Store.List()
		if err != nil {
			return DownResult{}, err
		}
		for _, rec := range records {
			if rec.Status != domain.StackUp {
				continue
			}
			stack, found, err := a.Store.Load(rec.ID)
			if err != nil {
				return DownResult{}, err
			}
			if found {
				stacks = append(stacks, stack)
			}
		}
	} else {
		r, err := a.resolve(req.Target, false)
		if err != nil {
			return DownResult{}, err
		}
		stack, err := a.loadStack(r.id)
		if err != nil {
			return DownResult{}, err
		}
		stacks = append(stacks, stack)
	}

	var result DownResult
	for _, stack := range stacks {
		forced, err := a.downOne(ctx, stack, req.Purge)
		if err != nil {
			return result, fmt.Errorf("stopping %s: %w", stack.ID, err)
		}
		result.Stopped = append(result.Stopped, stack.ID)
		result.Forced = append(result.Forced, forced...)
	}

	return result, nil
}

func (a *App) downOne(ctx context.Context, stack domain.Stack, purge bool) ([]string, error) {
	alive, err := a.stackAlive(ctx, &stack)
	if err != nil {
		return nil, err
	}

	var forced []string
	if alive {
		forced, err = a.stopServices(ctx, stack, stack.ServiceNames())
		if err != nil {
			return forced, err
		}
		windows, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
		if err != nil {
			return forced, err
		}
		for _, w := range windows {
			if err := a.Mux.KillWindow(ctx, w.ID); err != nil {
				return forced, err
			}
		}
	}

	if err := a.Store.SetStatus(stack.ID, domain.StackDown); err != nil {
		return forced, err
	}
	if purge {
		if err := a.Files.RemoveAll(stack.Dir); err != nil {
			return forced, fmt.Errorf("removing %s: %w", stack.Dir, err)
		}
	}
	return forced, nil
}

// stopServices stops the named services, dependents before their
// dependencies. Services within one dependency level stop together.
func (a *App) stopServices(ctx context.Context, stack domain.Stack, names []string) ([]string, error) {
	levels, err := domain.Levels(stack.ServiceDefs())
	if err != nil {
		return nil, err
	}
	windows, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
	if err != nil {
		return nil, err
	}
	byName := map[string]Window{}
	for _, w := range windows {
		byName[w.Name] = w
	}

	var forced []string
	forcedCh := make(chan string, len(names))
	for _, level := range slices.Backward(levels) {
		var batch []domain.Service
		for _, name := range level {
			if !slices.Contains(names, name) {
				continue
			}
			w, ok := byName[name]
			if !ok || w.Dead {
				continue
			}
			svc, _ := stack.Service(name)
			batch = append(batch, svc)
		}
		err := concurrency.ForEach(ctx, len(batch), len(batch)+1, func(ctx context.Context, i int) error {
			svc := batch[i]
			if !a.stopOne(ctx, stack, svc, byName[svc.Name]) {
				forcedCh <- svc.Name
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	close(forcedCh)
	for name := range forcedCh {
		forced = append(forced, name)
	}
	return forced, nil
}

// stopOne signals a pane's process group and waits for the pane to die. The
// pane's process leads its own group, so the negative pid reaches the
// command and everything it spawned. It reports false if SIGKILL was needed.
func (a *App) stopOne(ctx context.Context, stack domain.Stack, svc domain.Service, w Window) bool {
	a.emit(Event{Kind: EventStopping, Service: svc.Name, Detail: "", Elapsed: 0})
	sig, err := domain.ParseSignal(svc.Stop.Signal)
	if err != nil {
		sig = syscall.SIGTERM
	}
	timeout := svc.Stop.Timeout
	if timeout <= 0 {
		timeout = domain.DefaultStopTimeout
	}

	start := a.Now()
	_ = a.Signals.SignalGroup(w.PanePID, sig)
	if a.waitDead(ctx, stack, svc.Name, timeout) {
		a.emit(Event{Kind: EventStopped, Service: svc.Name, Detail: "", Elapsed: a.Now().Sub(start)})
		return true
	}

	_ = a.Signals.SignalGroup(w.PanePID, syscall.SIGKILL)
	a.waitDead(ctx, stack, svc.Name, 2*time.Second)
	a.emit(Event{
		Kind:    EventStopped,
		Service: svc.Name,
		Detail:  fmt.Sprintf("did not stop within %s of %s, killed", timeout, svc.Stop.Signal),
		Elapsed: a.Now().Sub(start),
	})
	return false
}

func (a *App) waitDead(ctx context.Context, stack domain.Stack, name string, timeout time.Duration) bool {
	deadline := a.Now().Add(timeout)
	ticker := time.NewTicker(a.poll())
	defer ticker.Stop()
	for {
		w, found, err := a.window(ctx, stack, name)
		if err == nil && (!found || w.Dead) {
			return true
		}
		if a.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}
