package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/hay-kot/svx/internal/domain"
)

// RestartRequest names services to restart in place.
type RestartRequest struct {
	Target   Target
	Services []string
	NoWait   bool
}

// Restart stops and respawns services in their windows, keeping their ports.
// When the project config is reachable it is re-rendered against the
// recorded slot, so config edits apply without moving any port.
func (a *App) Restart(ctx context.Context, req RestartRequest) (UpResult, error) {
	if len(req.Services) == 0 {
		return UpResult{}, usage(errors.New("name at least one service to restart"))
	}

	r, err := a.resolve(req.Target, false)
	if err != nil {
		return UpResult{}, err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return UpResult{}, err
	}
	alive, err := a.stackAlive(ctx, &stack)
	if err != nil {
		return UpResult{}, err
	}
	if !alive {
		return UpResult{}, usage(fmt.Errorf("stack %s is not running, start it with `svx up`", stack.ID))
	}

	if r.project != nil {
		stack, err = a.rerender(ctx, r, stack)
		if err != nil {
			return UpResult{}, err
		}
	}

	for _, name := range req.Services {
		if _, ok := stack.Service(name); !ok {
			return UpResult{}, usage(fmt.Errorf("unknown service %q, known services: %s", name, strings.Join(stack.ServiceNames(), ", ")))
		}
	}

	if err := a.stopServices(ctx, stack, req.Services); err != nil {
		return UpResult{}, err
	}
	windows, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
	if err != nil {
		return UpResult{}, err
	}
	ids := map[string]string{}
	for _, w := range windows {
		ids[w.Name] = w.ID
	}

	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		outcomes = map[string]Outcome{}
	)
	// Restarted services start level by level, as up starts them, so a
	// whole-stack restart brings db back before the migration that needs
	// it. Services in one level start together, and a service whose
	// restarted dependency failed is skipped.
	levels, err := domain.Levels(stack.ServiceDefs())
	if err != nil {
		return UpResult{}, err
	}
	for _, level := range levels {
		for _, name := range level {
			if !slices.Contains(req.Services, name) {
				continue
			}
			svc, _ := stack.Service(name)
			mu.Lock()
			dep, failed := failedDependency(svc, outcomes)
			mu.Unlock()
			if failed {
				o := a.skip(svc, "dependency "+dep+" failed")
				mu.Lock()
				outcomes[name] = o
				mu.Unlock()
				continue
			}
			wg.Go(func() {
				o := a.launch(ctx, stack, svc, ids[name], req.NoWait)
				mu.Lock()
				outcomes[name] = o
				mu.Unlock()
			})
		}
		wg.Wait()
	}

	result := UpResult{Stack: stack, Outcomes: outcomes, Moved: nil}
	if failed := failedServices(stack, outcomes); len(failed) > 0 {
		return result, &ServiceError{Services: failed}
	}
	return result, nil
}

// failedDependency reports a dependency of svc that failed or was skipped in
// an earlier level.
func failedDependency(svc domain.Service, outcomes map[string]Outcome) (string, bool) {
	for _, dep := range svc.Depends {
		if o, ok := outcomes[dep]; ok && (o.State == OutcomeFailed || o.State == OutcomeSkipped) {
			return dep, true
		}
	}
	return "", false
}

func (a *App) rerender(ctx context.Context, r resolved, prev domain.Stack) (domain.Stack, error) {
	if _, err := domain.Validate(r.project.Def); err != nil {
		return domain.Stack{}, err
	}
	unlock, err := a.Store.Lock(ctx)
	if err != nil {
		return domain.Stack{}, err
	}
	defer unlock()

	ports, err := a.slotPorts(r.project.Def, prev.Slot, prev.Ports())
	if err != nil {
		return domain.Stack{}, err
	}
	stack, err := a.render(r, prev.Slot, prev.Session, ports, prev.Snapshot)
	if err != nil {
		return domain.Stack{}, err
	}
	stack.StartedAt = prev.StartedAt
	if err := a.Store.Save(stack); err != nil {
		return domain.Stack{}, err
	}
	return stack, nil
}
