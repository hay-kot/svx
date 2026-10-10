package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hay-kot/svx/internal/domain"
)

// UpRequest asks for a stack, or some of its services, to be running.
type UpRequest struct {
	Target   Target
	Services []string
	// NoWait starts windows in dependency order without waiting for
	// readiness.
	NoWait bool
}

// UpResult describes what `up` did.
type UpResult struct {
	Stack    domain.Stack
	Outcomes map[string]Outcome
	// Moved lists ports that changed because the previous slot was taken.
	Moved []string
	// Reused is true when the session already existed.
	Reused bool
}

// OutcomeState is what happened to one service during up or restart.
type OutcomeState string

const (
	OutcomeReady   OutcomeState = "ready"
	OutcomeRunning OutcomeState = "running"
	OutcomeStarted OutcomeState = "started"
	OutcomeFailed  OutcomeState = "failed"
	OutcomeSkipped OutcomeState = "skipped"
)

// Outcome is one service's result.
type Outcome struct {
	State   OutcomeState
	Detail  string
	Elapsed time.Duration
}

// Up brings a stack up. It is safe to repeat: running windows are left alone
// and only missing or dead ones start.
func (a *App) Up(ctx context.Context, req UpRequest) (UpResult, error) {
	r, err := a.loadValidated(req.Target)
	if err != nil {
		return UpResult{}, err
	}
	def := r.project.Def

	targets, err := domain.Targets(def.Services, req.Services)
	if err != nil {
		return UpResult{}, usage(err)
	}

	stack, p, err := a.prepare(ctx, r)
	if err != nil {
		return UpResult{}, err
	}

	if err := a.Files.MkdirAll(stack.Dir); err != nil {
		return UpResult{}, fmt.Errorf("creating stack directory: %w", err)
	}
	if def.Settings.Logs {
		if err := a.Files.MkdirAll(domain.LogDir(stack.Dir)); err != nil {
			return UpResult{}, fmt.Errorf("creating log directory: %w", err)
		}
	}

	run := &runner{app: a, stack: stack, sessionExists: p.sessionExists, noWait: req.NoWait}
	outcomes := run.start(ctx, targets, p.windows)

	result := UpResult{Stack: stack, Outcomes: outcomes, Moved: p.moved, Reused: p.reused}
	if failed := failedServices(stack, outcomes); len(failed) > 0 {
		return result, &ServiceError{Services: failed}
	}
	return result, nil
}

// prepared is what Up needs to know about where a stack runs.
type prepared struct {
	// reused is true when the stack was already running.
	reused        bool
	sessionExists bool
	windows       []Window
	moved         []string
}

// prepare settles the stack's slot and session and renders it, under the
// state lock. A running stack keeps its session; a stopped one starts in
// the session svx runs in, or its own outside tmux.
func (a *App) prepare(ctx context.Context, r resolved) (domain.Stack, prepared, error) {
	var p prepared
	unlock, err := a.Store.Lock(ctx)
	if err != nil {
		return domain.Stack{}, p, err
	}
	defer unlock()

	prev, found, err := a.Store.Load(r.id)
	if err != nil {
		return domain.Stack{}, p, err
	}
	var prevPtr *domain.Stack
	if found {
		prevPtr = &prev
		p.windows, p.reused, err = a.stackWindows(ctx, &prev)
		if err != nil {
			return domain.Stack{}, p, err
		}
	}

	var (
		slot    int
		ports   domain.PortTable
		session string
	)
	if p.reused {
		slot, session, p.sessionExists = prev.Slot, prev.Session, true
		ports, err = a.slotPorts(r.project.Def, slot, prev.Ports())
		if err != nil {
			return domain.Stack{}, p, err
		}
	} else {
		session, p.sessionExists, err = a.pickSession(ctx, r.id)
		if err != nil {
			return domain.Stack{}, p, err
		}
		alloc, err := a.allocate(ctx, r.project.Def, r.id, prevPtr)
		if err != nil {
			return domain.Stack{}, p, err
		}
		slot, ports = alloc.slot, alloc.ports
		if found && prev.Slot != slot {
			p.moved = portMoves(prev.Ports(), ports)
		}
	}

	stack, err := a.render(r, slot, session, ports, a.Environ())
	if err != nil {
		return domain.Stack{}, p, err
	}
	if p.reused && !prev.StartedAt.IsZero() {
		stack.StartedAt = prev.StartedAt
	}
	if err := a.Store.Save(stack); err != nil {
		return domain.Stack{}, p, err
	}
	return stack, p, nil
}

func failedServices(stack domain.Stack, outcomes map[string]Outcome) []string {
	var failed []string
	for _, name := range stack.ServiceNames() {
		if o, ok := outcomes[name]; ok && o.State == OutcomeFailed {
			failed = append(failed, name)
		}
	}
	return failed
}

// runner starts services in dependency order. Every target gets a goroutine
// that waits on its dependencies, so independent services start together.
type runner struct {
	app           *App
	stack         domain.Stack
	noWait        bool
	sessionMu     sync.Mutex
	sessionExists bool
}

type doneSignal struct {
	ch chan struct{}
	ok bool
}

func (r *runner) start(ctx context.Context, targets map[string]bool, existing []Window) map[string]Outcome {
	windows := map[string]Window{}
	for _, w := range existing {
		windows[w.Name] = w
	}

	done := map[string]*doneSignal{}
	var plan []string
	for _, svc := range r.stack.Services {
		if targets[svc.Name] {
			done[svc.Name] = &doneSignal{ch: make(chan struct{}), ok: false}
			plan = append(plan, svc.Name)
		}
	}
	r.app.emit(Event{Kind: EventPlan, Service: "", Detail: strings.Join(plan, ","), Elapsed: 0})

	var (
		mu       sync.Mutex
		outcomes = map[string]Outcome{}
		wg       sync.WaitGroup
	)
	for _, svc := range r.stack.Services {
		if !targets[svc.Name] {
			continue
		}
		wg.Go(func() {
			sig := done[svc.Name]
			o := r.startOne(ctx, svc, done, windows)
			mu.Lock()
			outcomes[svc.Name] = o
			mu.Unlock()
			sig.ok = o.State != OutcomeFailed && o.State != OutcomeSkipped
			close(sig.ch)
		})
	}
	wg.Wait()
	return outcomes
}

func (r *runner) startOne(ctx context.Context, svc domain.Service, done map[string]*doneSignal, windows map[string]Window) Outcome {
	if pending := pendingDeps(svc, done); len(pending) > 0 {
		r.app.emit(Event{Kind: EventWaiting, Service: svc.Name, Detail: strings.Join(pending, ", "), Elapsed: 0})
	}
	for _, dep := range svc.Depends {
		d := done[dep]
		select {
		case <-ctx.Done():
			return r.skip(svc, "interrupted")
		case <-d.ch:
		}
		if !d.ok {
			return r.skip(svc, "dependency "+dep+" failed")
		}
	}

	if w, ok := windows[svc.Name]; ok {
		switch {
		case !w.Dead:
			r.app.emit(Event{Kind: EventRunning, Service: svc.Name, Detail: "already running", Elapsed: 0})
			return Outcome{State: OutcomeRunning, Detail: "already running", Elapsed: 0}
		case svc.Oneshot && w.ExitCode == 0:
			r.app.emit(Event{Kind: EventRunning, Service: svc.Name, Detail: "already finished", Elapsed: 0})
			return Outcome{State: OutcomeRunning, Detail: "already finished", Elapsed: 0}
		}
	}

	return r.app.launch(ctx, r.stack, svc, windows[svc.Name].ID, r.noWait, r.ensureSession)
}

func pendingDeps(svc domain.Service, done map[string]*doneSignal) []string {
	var pending []string
	for _, dep := range svc.Depends {
		select {
		case <-done[dep].ch:
		default:
			pending = append(pending, dep)
		}
	}
	return pending
}

// ensureSession serialises the first window so exactly one goroutine creates
// the session. It reports whether the caller must create it.
func (r *runner) ensureSession(create func(bool) error) error {
	r.sessionMu.Lock()
	defer r.sessionMu.Unlock()
	if err := create(!r.sessionExists); err != nil {
		return err
	}
	r.sessionExists = true
	return nil
}

func (r *runner) skip(svc domain.Service, why string) Outcome {
	r.app.emit(Event{Kind: EventSkipped, Service: svc.Name, Detail: why, Elapsed: 0})
	return Outcome{State: OutcomeSkipped, Detail: why, Elapsed: 0}
}

// launch starts one service's window, or respawns window when it names one,
// and unless noWait, waits for it to be ready.
func (a *App) launch(
	ctx context.Context,
	stack domain.Stack,
	svc domain.Service,
	window string,
	noWait bool,
	withSession func(func(create bool) error) error,
) Outcome {
	a.emit(Event{Kind: EventStarting, Service: svc.Name, Detail: "", Elapsed: 0})

	var offset int64
	if svc.LogFile != "" {
		offset = a.Files.Size(svc.LogFile)
	}

	spec := a.windowSpec(stack, svc)
	err := withSession(func(create bool) error {
		if window != "" {
			spec.Window = window
			return a.Mux.RespawnWindow(ctx, spec)
		}
		spec.CreateSession = create
		_, err := a.Mux.StartWindow(ctx, spec)
		return err
	})
	if err != nil {
		a.emit(Event{Kind: EventFailed, Service: svc.Name, Detail: err.Error(), Elapsed: 0})
		return Outcome{State: OutcomeFailed, Detail: err.Error(), Elapsed: 0}
	}

	if noWait {
		return Outcome{State: OutcomeStarted, Detail: "", Elapsed: 0}
	}
	return a.waitReady(ctx, stack, svc, offset)
}

func (a *App) windowSpec(stack domain.Stack, svc domain.Service) WindowSpec {
	return WindowSpec{
		Session: stack.Session,
		Stack:   stack.ID.Key(),
		Service: svc.Name,
		Window:  "",
		Dir:     svc.Dir,
		Command: []string{a.Executable, "_run", stack.ID.Key(), svc.Name},
		// The shim finds the state directory through this, since the tmux
		// server's environment may predate any override.
		Env:           map[string]string{"SVX_STATE_DIR": a.Store.Dir()},
		LogFile:       svc.LogFile,
		CreateSession: false,
	}
}

// waitReady polls a service until its readiness check passes, it dies, or the
// timeout runs out. The window's own state is checked every round, so a crash
// fails fast instead of waiting out the timeout.
func (a *App) waitReady(ctx context.Context, stack domain.Stack, svc domain.Service, logOffset int64) Outcome {
	start := a.Now()
	timeout := svc.Ready.Timeout
	if timeout <= 0 {
		timeout = domain.DefaultReadyTimeout
	}

	probe, needsProbe := a.probeFor(stack, svc, logOffset)

	ticker := time.NewTicker(a.poll())
	defer ticker.Stop()

	var lastErr error
	for {
		elapsed := a.Now().Sub(start)

		w, found, err := a.window(ctx, stack, svc.Name)
		switch {
		case err != nil:
			lastErr = err
		case !found:
			return a.fail(ctx, stack, svc, "its window was closed", elapsed)
		case w.Dead && svc.Oneshot && w.ExitCode == 0:
			return a.ready(svc, "finished", elapsed)
		case w.Dead:
			return a.fail(ctx, stack, svc, fmt.Sprintf("exited with code %d", w.ExitCode), elapsed)
		case svc.Oneshot:
		case svc.Ready.Kind == domain.ReadyDelay:
			d, perr := time.ParseDuration(svc.Ready.Value)
			if perr != nil {
				return a.fail(ctx, stack, svc, fmt.Sprintf("ready.delay %q: %v", svc.Ready.Value, perr), elapsed)
			}
			if elapsed >= d {
				return a.ready(svc, "", elapsed)
			}
		case !needsProbe:
			if elapsed >= time.Second {
				return a.ready(svc, "", elapsed)
			}
		default:
			if lastErr = a.Ready.Probe(ctx, probe); lastErr == nil {
				return a.ready(svc, "", elapsed)
			}
		}

		if elapsed >= timeout {
			why := fmt.Sprintf("not ready after %s", timeout)
			if lastErr != nil {
				why += ": " + lastErr.Error()
			}
			return a.fail(ctx, stack, svc, why, elapsed)
		}

		select {
		case <-ctx.Done():
			return a.fail(ctx, stack, svc, "interrupted", elapsed)
		case <-ticker.C:
		}
	}
}

func (a *App) probeFor(stack domain.Stack, svc domain.Service, logOffset int64) (Probe, bool) {
	p := Probe{
		Kind:      svc.Ready.Kind,
		Value:     svc.Ready.Value,
		Dir:       svc.Dir,
		Env:       domain.ProcessEnv(stack.Snapshot, svc.Env, nil),
		Port:      0,
		LogFile:   svc.LogFile,
		LogOffset: logOffset,
	}
	switch svc.Ready.Kind {
	case domain.ReadyHTTP, domain.ReadyCmd, domain.ReadyOutput:
		return p, true
	case domain.ReadyDelay:
		return p, false
	case domain.ReadyDefault:
		port, ok := svc.MainPort()
		p.Port = port
		return p, ok
	}
	return p, false
}

func (a *App) ready(svc domain.Service, detail string, elapsed time.Duration) Outcome {
	a.emit(Event{Kind: EventReady, Service: svc.Name, Detail: detail, Elapsed: elapsed})
	return Outcome{State: OutcomeReady, Detail: detail, Elapsed: elapsed}
}

func (a *App) fail(ctx context.Context, stack domain.Stack, svc domain.Service, why string, elapsed time.Duration) Outcome {
	detail := why
	if w, found, err := a.window(ctx, stack, svc.Name); err == nil && found {
		if tail, err := a.Mux.Capture(ctx, w.ID, 15); err == nil {
			if tail = strings.TrimRight(withoutBanner(tail), "\n "); tail != "" {
				detail += "\n" + indent(tail, "    ")
			}
		}
	}
	a.emit(Event{Kind: EventFailed, Service: svc.Name, Detail: detail, Elapsed: elapsed})
	return Outcome{State: OutcomeFailed, Detail: detail, Elapsed: elapsed}
}

func (a *App) window(ctx context.Context, stack domain.Stack, name string) (Window, bool, error) {
	windows, err := a.Mux.Windows(ctx, stack.Session, stack.ID.Key())
	if err != nil {
		return Window{}, false, err
	}
	i := slices.IndexFunc(windows, func(w Window) bool { return w.Name == name })
	if i < 0 {
		return Window{}, false, nil
	}
	return windows[i], true, nil
}

// withoutBanner drops the shim's banner, which only repeats the command.
func withoutBanner(s string) string {
	lines := strings.Split(s, "\n")
	return strings.Join(slices.DeleteFunc(lines, func(l string) bool { return strings.HasPrefix(l, domain.BannerPrefix) }), "\n")
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
