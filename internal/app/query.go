package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
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

// Links lists a stack's links in service order.
func (a *App) Links(_ context.Context, t Target) ([]domain.Link, error) {
	r, err := a.resolve(t, false)
	if err != nil {
		return nil, err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return nil, err
	}
	return stack.Links(), nil
}

// Open opens a link by label, matched without case.
func (a *App) Open(ctx context.Context, t Target, label string) (domain.Link, error) {
	links, err := a.Links(ctx, t)
	if err != nil {
		return domain.Link{}, err
	}
	labels := make([]string, 0, len(links))
	for _, l := range links {
		if strings.EqualFold(l.Label, label) {
			return l, a.Browser.Open(ctx, l.URL)
		}
		labels = append(labels, l.Label)
	}
	if len(labels) == 0 {
		return domain.Link{}, usage(errors.New("this stack has no links, add `links` to a service"))
	}
	return domain.Link{}, usage(fmt.Errorf("no link %q, links: %s", label, strings.Join(labels, ", ")))
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

// Attach attaches to a stack's session.
func (a *App) Attach(ctx context.Context, t Target, service string) error {
	r, err := a.resolve(t, false)
	if err != nil {
		return err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return err
	}
	windows, alive, err := a.stackWindows(ctx, &stack)
	if err != nil {
		return err
	}
	if !alive {
		return usage(fmt.Errorf("stack %s is not running, start it with `svx up`", stack.ID))
	}
	window := ""
	if service != "" {
		if _, ok := stack.Service(service); !ok {
			return usage(fmt.Errorf("unknown service %q, known services: %s", service, strings.Join(stack.ServiceNames(), ", ")))
		}
		i := slices.IndexFunc(windows, func(w Window) bool { return w.Name == service })
		if i < 0 {
			return usage(fmt.Errorf("%s has no window, start it with `svx up %s`", service, service))
		}
		window = windows[i].ID
	}
	return a.Mux.Attach(ctx, stack.Session, window)
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
