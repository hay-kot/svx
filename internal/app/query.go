package app

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/hay-kot/svx/internal/domain"
)

// Env returns a stack's variables: the stack-wide overlay, or one service's
// view of it.
func (a *App) Env(_ context.Context, t Target, service string) (map[string]string, error) {
	r, err := a.resolve(t, false)
	if err != nil {
		return nil, err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return nil, err
	}
	if service == "" {
		return maps.Clone(stack.Env), nil
	}
	svc, ok := stack.Service(service)
	if !ok {
		return nil, usage(fmt.Errorf("unknown service %q, known services: %s", service, strings.Join(stack.ServiceNames(), ", ")))
	}
	return maps.Clone(svc.Env), nil
}

// Preview renders the stack without starting anything. A running stack
// keeps its recorded slot and session. Otherwise the slot is what `up` would
// pick right now, though nothing is reserved, and the session is the one svx
// runs in, empty outside tmux.
func (a *App) Preview(ctx context.Context, t Target) (domain.Stack, []string, error) {
	r, err := a.resolve(t, true)
	if err != nil {
		return domain.Stack{}, nil, err
	}
	warnings, err := domain.Validate(r.project.Def)
	if err != nil {
		return domain.Stack{}, nil, err
	}

	prev, err := a.loadPrevious(ctx, r.id)
	if err != nil {
		return domain.Stack{}, nil, err
	}
	session, _, err := a.Mux.CurrentSession(ctx)
	if err != nil {
		return domain.Stack{}, nil, err
	}
	pl, err := a.place(ctx, r.project.Def, r.id, prev, session)
	if err != nil {
		return domain.Stack{}, nil, err
	}

	stack, err := a.render(r, pl.slot, pl.session, pl.ports, a.Environ())
	if err != nil {
		return domain.Stack{}, nil, err
	}
	if !prev.alive {
		stack.Status = domain.StackDown
	}
	return stack, warnings, nil
}

// ServiceLog says where to read a service's output: File when logs are on,
// else Scrollback, the window's tmux history.
type ServiceLog struct {
	File       string
	Scrollback string
}

// ServiceLog finds a service's output.
func (a *App) ServiceLog(ctx context.Context, t Target, service string) (ServiceLog, error) {
	r, err := a.resolve(t, false)
	if err != nil {
		return ServiceLog{}, err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return ServiceLog{}, err
	}
	svc, ok := stack.Service(service)
	if !ok {
		return ServiceLog{}, usage(fmt.Errorf("unknown service %q, known services: %s", service, strings.Join(stack.ServiceNames(), ", ")))
	}
	if svc.LogFile != "" {
		return ServiceLog{File: svc.LogFile, Scrollback: ""}, nil
	}
	w, found, err := a.window(ctx, stack, svc.Name)
	if err != nil {
		return ServiceLog{}, err
	}
	if !found {
		return ServiceLog{}, usage(fmt.Errorf("%s has no window, and logs are off so there is no log file; set settings.logs: true to keep one", service))
	}
	history, err := a.Mux.Capture(ctx, w.ID, 0)
	if err != nil {
		return ServiceLog{}, err
	}
	return ServiceLog{File: "", Scrollback: history}, nil
}
