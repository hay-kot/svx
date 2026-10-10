// Package app holds svx's use cases. It drives tmux, state and the network
// only through the ports in ports.go, so its logic runs against fakes in
// tests.
package app

import (
	"context"
	"fmt"
	"time"

	"github.com/hay-kot/svx/internal/domain"
)

// App wires the ports to the use cases.
type App struct {
	Projects ProjectRepository
	Store    StateStore
	Mux      Multiplexer
	Signals  Signaler
	Ports    PortProber
	Ready    ReadyProber
	Files    Files
	Browser  Browser
	Secrets  domain.SecretResolver

	// Environ returns the environment svx runs in, as a map.
	Environ func() map[string]string
	Now     func() time.Time
	// Executable is the svx binary windows run as their shim.
	Executable string
	// Poll is how often readiness and stop waits re-check.
	Poll time.Duration
	// Events receives progress while use cases run. It may be nil.
	Events func(Event)
}

// EventKind classifies progress events.
type EventKind int

const (
	// EventPlan lists, in Detail, the services Up is about to start, in
	// stack order and comma-separated.
	EventPlan EventKind = iota
	// EventWaiting names, in Detail, the dependencies a service waits on.
	EventWaiting
	EventStarting
	EventRunning
	EventReady
	EventFailed
	EventSkipped
	EventStopping
	EventStopped
	EventWarning
)

// Event is one progress update.
type Event struct {
	Kind    EventKind
	Service string
	Detail  string
	Elapsed time.Duration
}

func (a *App) emit(e Event) {
	if a.Events != nil {
		a.Events(e)
	}
}

func (a *App) poll() time.Duration {
	if a.Poll > 0 {
		return a.Poll
	}
	return 200 * time.Millisecond
}

// Target says which stack a command acts on. With Project and Stack both set,
// no config is read. Otherwise the project comes from the config found from
// Dir, and Stack, if empty, from a hash of the checkout path.
type Target struct {
	Dir     string
	Project string
	Stack   string
}

type resolved struct {
	id      domain.Identity
	project *Project
}

func (a *App) resolve(t Target, needProject bool) (resolved, error) {
	if t.Project != "" && t.Stack != "" && !needProject {
		id, err := domain.NewIdentity(t.Project, t.Stack)
		if err != nil {
			return resolved{}, usage(err)
		}
		return resolved{id: id, project: nil}, nil
	}

	proj, err := a.Projects.Load(t.Dir)
	if err != nil {
		return resolved{}, err
	}

	name := proj.Def.Project
	if name == "" {
		name = proj.CheckoutName
	}
	if t.Project != "" {
		name = t.Project
	}
	stack := t.Stack
	if stack == "" {
		stack = domain.DefaultStackName(proj.Root)
	}
	id, err := domain.NewIdentity(name, stack)
	if err != nil {
		return resolved{}, usage(err)
	}
	return resolved{id: id, project: &proj}, nil
}

// loadValidated loads and validates the project, emitting warnings.
func (a *App) loadValidated(t Target) (resolved, error) {
	r, err := a.resolve(t, true)
	if err != nil {
		return r, err
	}
	warnings, err := domain.Validate(r.project.Def)
	if err != nil {
		return r, err
	}
	for _, w := range warnings {
		a.emit(Event{Kind: EventWarning, Service: "", Detail: w, Elapsed: 0})
	}
	return r, nil
}

// loadStack reads a stack's record, failing with a hint when there is none.
func (a *App) loadStack(id domain.Identity) (domain.Stack, error) {
	stack, found, err := a.Store.Load(id)
	if err != nil {
		return domain.Stack{}, err
	}
	if !found {
		return domain.Stack{}, usage(fmt.Errorf("no stack %s on record, start it with `svx up`", id))
	}
	return stack, nil
}

// startGrace covers the gap between `up` saving a record and tmux having the
// session: the record is written under the lock, the session only once the
// first window starts. A record inside the grace still holds its slot, and
// is not marked down on disk, so a concurrent `up` cannot take the same slot.
const startGrace = 30 * time.Second

func (a *App) starting(stack domain.Stack) bool {
	return stack.Status == domain.StackUp && a.Now().Sub(stack.StartedAt) < startGrace
}

// stackWindows lists a stack's windows. The stack is alive while its session
// holds at least one. When a record says up but tmux disagrees, the stack
// reads as down, and outside the start grace the record is marked down too.
// tmux is the source of truth.
func (a *App) stackWindows(ctx context.Context, stack *domain.Stack) ([]Window, bool, error) {
	var windows []Window
	if stack.Session != "" {
		ws, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
		if err != nil {
			return nil, false, err
		}
		windows = ws
	}
	alive := len(windows) > 0
	if !alive && stack.Status == domain.StackUp {
		if !a.starting(*stack) {
			if err := a.Store.SetStatus(stack.ID, domain.StackDown); err != nil {
				return nil, false, err
			}
		}
		stack.Status = domain.StackDown
	}
	return windows, alive, nil
}

func (a *App) stackAlive(ctx context.Context, stack *domain.Stack) (bool, error) {
	_, alive, err := a.stackWindows(ctx, stack)
	return alive, err
}

// pickSession chooses where a stack that is not running starts: the session
// svx runs in, so its windows show up beside the user's own, or outside
// tmux the stack's own session.
func (a *App) pickSession(ctx context.Context, id domain.Identity) (string, bool, error) {
	cur, inside, err := a.Mux.CurrentSession(ctx)
	if err != nil {
		return "", false, err
	}
	if inside {
		return cur, true, nil
	}
	exists, err := a.Mux.HasSession(ctx, id.Session())
	return id.Session(), exists, err
}

func (a *App) render(r resolved, slot int, session string, ports domain.PortTable, environ map[string]string) (domain.Stack, error) {
	return domain.Render(domain.RenderInput{
		Def:     r.project.Def,
		ID:      r.id,
		Root:    r.project.Root,
		Slot:    slot,
		Session: session,
		Ports:   ports,
		Environ: environ,
		Secrets: a.Secrets,
		Now:     a.Now().UTC().Truncate(time.Second),
	})
}

// slotPorts recomputes a stack's ports for a known slot, keeping auto ports
// that were already chosen.
func (a *App) slotPorts(def domain.Definition, slot int, prev domain.PortTable) (domain.PortTable, error) {
	table, autos, err := domain.SlotPorts(def, slot)
	if err != nil {
		return nil, err
	}
	for _, ref := range autos {
		if p, ok := prev.Get(ref.Service, ref.Name); ok {
			table.Set(ref.Service, ref.Name, p)
			continue
		}
		p, err := a.Ports.Ephemeral()
		if err != nil {
			return nil, fmt.Errorf("picking a port for %s: %w", ref, err)
		}
		table.Set(ref.Service, ref.Name, p)
	}
	return table, nil
}
