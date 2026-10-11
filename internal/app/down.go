package app

import (
	"context"
	"fmt"
	"slices"

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
}

// Down stops stacks in reverse dependency order, then closes their windows.
// The session itself is left alone: it is the user's.
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
		r, err := a.resolve(ctx, req.Target, false)
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
		if err := a.downOne(ctx, stack, req.Purge); err != nil {
			return result, fmt.Errorf("stopping %s: %w", stack.ID, err)
		}
		result.Stopped = append(result.Stopped, stack.ID)
	}

	return result, nil
}

func (a *App) downOne(ctx context.Context, stack domain.Stack, purge bool) error {
	alive, err := a.stackAlive(ctx, &stack)
	if err != nil {
		return err
	}

	if alive {
		if err := a.stopServices(ctx, stack, stack.ServiceNames()); err != nil {
			return err
		}
		windows, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
		if err != nil {
			return err
		}
		for _, w := range windows {
			if err := a.Mux.KillWindow(ctx, w.ID); err != nil {
				return err
			}
		}
	}

	if err := a.Store.SetStatus(stack.ID, domain.StackDown); err != nil {
		return err
	}
	if purge {
		if err := a.Files.RemoveAll(stack.Dir); err != nil {
			return fmt.Errorf("removing %s: %w", stack.Dir, err)
		}
	}
	return nil
}

// stopServices stops the named services, dependents before their
// dependencies. Services within one dependency level stop together.
func (a *App) stopServices(ctx context.Context, stack domain.Stack, names []string) error {
	levels, err := domain.Levels(stack.ServiceDefs())
	if err != nil {
		return err
	}
	windows, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
	if err != nil {
		return err
	}
	byName := map[string]Window{}
	for _, w := range windows {
		byName[w.Name] = w
	}

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
			a.stopOne(ctx, stack, batch[i], byName[batch[i].Name])
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
