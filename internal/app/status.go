package app

import (
	"context"

	"github.com/hay-kot/svx/internal/domain"
)

// ServiceState is what tmux says about a service right now.
type ServiceState string

const (
	StateRunning ServiceState = "running"
	StateExited  ServiceState = "exited"
	// StateMissing means the stack is up but the window is gone, usually
	// because someone closed it.
	StateMissing ServiceState = "missing"
	// StateIdle is a service that is not set to autostart and was not
	// started.
	StateIdle    ServiceState = "idle"
	StateStopped ServiceState = "stopped"
)

// ServiceView is a service with its live state.
type ServiceView struct {
	Service  domain.Service
	State    ServiceState
	ExitCode int
}

// StackView is a stack with the live state of each service.
type StackView struct {
	Stack    domain.Stack
	Services []ServiceView
}

// Running counts services that are alive.
func (v StackView) Running() int {
	n := 0
	for _, s := range v.Services {
		if s.State == StateRunning {
			n++
		}
	}
	return n
}

// Status reports one stack.
func (a *App) Status(ctx context.Context, t Target) (StackView, error) {
	r, err := a.resolve(t, false)
	if err != nil {
		return StackView{}, err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return StackView{}, err
	}
	return a.view(ctx, stack)
}

// List reports every stack on record, across projects.
func (a *App) List(ctx context.Context) ([]StackView, error) {
	records, err := a.Store.List()
	if err != nil {
		return nil, err
	}
	views := make([]StackView, 0, len(records))
	for _, rec := range records {
		v, err := a.view(ctx, rec)
		if err != nil {
			return nil, err
		}
		views = append(views, v)
	}
	return views, nil
}

func (a *App) view(ctx context.Context, stack domain.Stack) (StackView, error) {
	ws, alive, err := a.stackWindows(ctx, &stack)
	if err != nil {
		return StackView{}, err
	}
	windows := map[string]Window{}
	for _, w := range ws {
		windows[w.Name] = w
	}

	view := StackView{Stack: stack, Services: make([]ServiceView, 0, len(stack.Services))}
	for _, svc := range stack.Services {
		sv := ServiceView{Service: svc, State: StateStopped, ExitCode: 0}
		w, ok := windows[svc.Name]
		switch {
		case !alive:
		case ok && w.Dead:
			sv.State, sv.ExitCode = StateExited, w.ExitCode
		case ok:
			sv.State = StateRunning
		case !svc.Autostart:
			sv.State = StateIdle
		default:
			sv.State = StateMissing
		}
		view.Services = append(view.Services, sv)
	}
	return view, nil
}

// StaleStacks marks down every record whose session is gone and returns
// them.
func (a *App) StaleStacks(ctx context.Context) ([]domain.Identity, error) {
	records, err := a.Store.List()
	if err != nil {
		return nil, err
	}
	var stale []domain.Identity
	for i := range records {
		if records[i].Status != domain.StackUp {
			continue
		}
		alive, err := a.stackAlive(ctx, &records[i])
		if err != nil {
			return nil, err
		}
		if !alive {
			stale = append(stale, records[i].ID)
		}
	}
	return stale, nil
}
