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

// Preview renders the stack without starting anything. A stack with a live
// session keeps its recorded slot. Otherwise the slot is what `up` would pick
// right now, though nothing is reserved.
func (a *App) Preview(ctx context.Context, t Target) (domain.Stack, []string, error) {
	r, err := a.resolve(t, true)
	if err != nil {
		return domain.Stack{}, nil, err
	}
	warnings, err := domain.Validate(r.project.Def)
	if err != nil {
		return domain.Stack{}, nil, err
	}

	prev, found, err := a.Store.Load(r.id)
	if err != nil {
		return domain.Stack{}, nil, err
	}
	alive := false
	if found {
		if alive, err = a.stackAlive(ctx, &prev); err != nil {
			return domain.Stack{}, nil, err
		}
	}

	var (
		slot    int
		ports   domain.PortTable
		session string
	)
	if alive {
		slot, session = prev.Slot, prev.Session
		ports, err = a.slotPorts(r.project.Def, slot, prev.Ports())
	} else {
		if session, _, err = a.pickSession(ctx, r.id); err != nil {
			return domain.Stack{}, nil, err
		}
		var prevPtr *domain.Stack
		if found {
			prevPtr = &prev
		}
		var alloc allocation
		alloc, err = a.allocate(ctx, r.project.Def, r.id, prevPtr)
		slot, ports = alloc.slot, alloc.ports
	}
	if err != nil {
		return domain.Stack{}, nil, err
	}

	stack, err := a.render(r, slot, session, ports, a.Environ())
	if err != nil {
		return domain.Stack{}, nil, err
	}
	if !alive {
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

// LogFile returns a service's log path, failing when logs are off.
func (a *App) LogFile(_ context.Context, t Target, service string) (string, error) {
	r, err := a.resolve(t, false)
	if err != nil {
		return "", err
	}
	stack, err := a.loadStack(r.id)
	if err != nil {
		return "", err
	}
	svc, ok := stack.Service(service)
	if !ok {
		return "", usage(fmt.Errorf("unknown service %q, known services: %s", service, strings.Join(stack.ServiceNames(), ", ")))
	}
	if svc.LogFile == "" {
		return "", usage(fmt.Errorf("logs are off for this project, set settings.logs: true or use `svx attach %s`", service))
	}
	return svc.LogFile, nil
}

// RunPlan is what the shim needs to exec a service.
type RunPlan struct {
	Dir     string
	Argv    []string
	Env     []string
	Banner  string
	LogFile string
}

// PaneOwned lists variables a pane sets for itself, which must win over the
// snapshot. The snapshot comes from wherever `svx up` ran, which may be
// another terminal or another tmux session.
var PaneOwned = []string{"TMUX", "TMUX_PANE", "TERM"}

// PlanRun reads the recorded stack and prepares one service's exec.
func (a *App) PlanRun(key, service string, current map[string]string) (RunPlan, error) {
	id, err := domain.ParseKey(key)
	if err != nil {
		return RunPlan{}, usage(err)
	}
	stack, err := a.loadStack(id)
	if err != nil {
		return RunPlan{}, err
	}
	svc, ok := stack.Service(service)
	if !ok {
		return RunPlan{}, usage(fmt.Errorf("stack %s has no service %q", id, service))
	}

	banner := fmt.Sprintf("svx: %s %s", id, svc.Name)
	if p, ok := svc.MainPort(); ok {
		banner += fmt.Sprintf(" on %d", p)
	}
	banner += " -- " + svc.Run

	return RunPlan{
		Dir:     svc.Dir,
		Argv:    []string{"/bin/sh", "-c", svc.Run},
		Env:     domain.ProcessEnv(stack.Snapshot, svc.Env, current, PaneOwned...),
		Banner:  banner,
		LogFile: svc.LogFile,
	}, nil
}

// Init writes a starter config.
func (a *App) Init(dir string) ([]string, error) {
	return a.Projects.Init(dir)
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
