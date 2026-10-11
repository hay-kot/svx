package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/hay-kot/svx/internal/domain"
)

// eventLog records the order of interesting things across fakes.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

func (l *eventLog) index(e string) int { return slices.Index(l.all(), e) }

type fakeWindow struct {
	Window

	stack string
}

// fakeMux models tmux the way svx uses it: windows are tagged with their
// stack and service, svx runs from a pane in current, and a session ends
// when its last window closes.
type fakeMux struct {
	mu         sync.Mutex
	log        *eventLog
	sessions   map[string][]*fakeWindow
	nextPID    int
	started    []WindowSpec
	respawned  []WindowSpec
	dieOnStart map[string]int
	// current is the session svx runs in, "" for outside tmux.
	current string
}

func newFakeMux(log *eventLog) *fakeMux {
	return &fakeMux{log: log, sessions: map[string][]*fakeWindow{}, nextPID: 1000, dieOnStart: map[string]int{}}
}

func (m *fakeMux) Version(context.Context) (string, error) { return "tmux 3.4", nil }

func (m *fakeMux) CurrentSession(context.Context) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current, m.current != "", nil
}

func (m *fakeMux) HasSession(_ context.Context, name string) (bool, error) {
	return m.hasSession(name), nil
}

// addSession makes a session holding one untagged window, like a user's.
func (m *fakeMux) addSession(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[name] = append(m.sessions[name], &fakeWindow{ID: "@user", Name: "zsh", PanePID: 1})
}

func (m *fakeMux) hasSession(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[name]
	return ok
}

func (m *fakeMux) dropSession(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, name)
}

func (m *fakeMux) newPID() int {
	m.nextPID++
	return m.nextPID
}

func (m *fakeMux) applyDie(w *fakeWindow) {
	if code, ok := m.dieOnStart[w.Name]; ok {
		w.Dead, w.ExitCode = true, code
	}
}

func (m *fakeMux) StartWindow(_ context.Context, spec WindowSpec) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[spec.Session]; !exists {
		return "", fmt.Errorf("can't find session %s", spec.Session)
	}
	pid := m.newPID()
	w := &fakeWindow{ID: fmt.Sprintf("@%d", pid), Name: spec.Service, PanePID: pid, stack: spec.Stack}
	m.applyDie(w)
	m.sessions[spec.Session] = append(m.sessions[spec.Session], w)
	m.started = append(m.started, spec)
	m.log.add("start:" + spec.Service)
	return w.ID, nil
}

func (m *fakeMux) RespawnWindow(_ context.Context, spec WindowSpec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.sessions[spec.Session] {
		if w.ID == spec.Window {
			w.PanePID, w.Dead, w.ExitCode = m.newPID(), false, 0
			m.applyDie(w)
			m.respawned = append(m.respawned, spec)
			m.log.add("respawn:" + spec.Service)
			return nil
		}
	}
	return fmt.Errorf("can't find window %s", spec.Window)
}

func (m *fakeMux) Windows(_ context.Context, s, stack string) ([]Window, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Window
	for _, w := range m.sessions[s] {
		if w.stack == stack {
			out = append(out, w.Window)
		}
	}
	return out, nil
}

func (m *fakeMux) KillWindow(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.log.add("close:" + id)
	for s, ws := range m.sessions {
		m.sessions[s] = slices.DeleteFunc(ws, func(w *fakeWindow) bool { return w.ID == id })
		if len(m.sessions[s]) == 0 {
			delete(m.sessions, s)
		}
	}
	return nil
}

func (m *fakeMux) Capture(context.Context, string, int) (string, error) { return "", nil }
func (m *fakeMux) PaneReady(context.Context, string) (bool, error)      { return true, nil }

func (m *fakeMux) markDead(session, name string, code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, w := range m.sessions[session] {
		if w.Name == name {
			w.Dead, w.ExitCode = true, code
		}
	}
}

func (m *fakeMux) removeWindow(session, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session] = slices.DeleteFunc(m.sessions[session], func(w *fakeWindow) bool { return w.Name == name })
}

func (m *fakeMux) paneByPID(pid int) *fakeWindow {
	for _, ws := range m.sessions {
		for _, w := range ws {
			if w.PanePID == pid {
				return w
			}
		}
	}
	return nil
}

func (m *fakeMux) paneOf(name string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ws := range m.sessions {
		for _, w := range ws {
			if w.Name == name {
				return w.PanePID
			}
		}
	}
	return 0
}

// livePanes maps the pid of each live svx pane to its service.
func (m *fakeMux) livePanes() map[int]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	panes := map[int]string{}
	for _, ws := range m.sessions {
		for _, w := range ws {
			if w.stack != "" && !w.Dead {
				panes[w.PanePID] = w.Name
			}
		}
	}
	return panes
}

func (m *fakeMux) nameByPID(pid int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.paneByPID(pid); w != nil {
		return w.Name
	}
	return ""
}

// markDeadByPID kills the window whose pane leads pid's group.
func (m *fakeMux) markDeadByPID(pid, code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w := m.paneByPID(pid); w != nil {
		w.Dead, w.ExitCode = true, code
	}
}

func (m *fakeMux) startedNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, len(m.started))
	for i, s := range m.started {
		names[i] = s.Service
	}
	return names
}

func (m *fakeMux) respawnedNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, len(m.respawned))
	for i, s := range m.respawned {
		names[i] = s.Service
	}
	return names
}

func (m *fakeMux) resetCalls() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started, m.respawned = nil, nil
}

type signalCall struct {
	// Name is the service for a pane, or "pid:N" for any other process.
	Name  string
	Sig   syscall.Signal
	Group bool
}

// fakeChild is a process a service started beyond its pane's own.
type fakeChild struct {
	Process

	// lingers is how many snapshots it keeps running once its pane is
	// dead, as a supervisor that is still tearing down would.
	lingers int
	// stubborn survives anything short of SIGKILL.
	stubborn bool
}

// fakeProcs models the process table: each live pane leads its own group,
// and children are reparented to pid 1 when their pane dies.
type fakeProcs struct {
	mu       sync.Mutex
	mux      *fakeMux
	log      *eventLog
	calls    []signalCall
	children []*fakeChild
	// ignore names services whose pane survives anything short of SIGKILL.
	ignore map[string]bool
}

// addChild starts a child under service's pane, in a process group of its
// own.
func (s *fakeProcs) addChild(service string, pid, lingers int, stubborn bool) {
	pane := s.mux.paneOf(service)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.children = append(s.children, &fakeChild{
		PID:      pid,
		PPID:     pane,
		PGID:     pid,
		Command:  fmt.Sprintf("child-%d", pid),
		lingers:  lingers,
		stubborn: stubborn,
	})
}

func (s *fakeProcs) Snapshot(context.Context) ([]Process, error) {
	panes := s.mux.livePanes()
	s.mu.Lock()
	defer s.mu.Unlock()
	var table []Process
	for pid, name := range panes {
		table = append(table, Process{PID: pid, PPID: 1, PGID: pid, Command: "svx _run " + name})
	}
	s.children = slices.DeleteFunc(s.children, func(c *fakeChild) bool {
		if _, ok := panes[c.PPID]; ok {
			return false
		}
		c.PPID = 1
		if c.lingers == 0 {
			s.log.add(fmt.Sprintf("exit:%d", c.PID))
			return true
		}
		c.lingers--
		return false
	})
	for _, c := range s.children {
		table = append(table, c.Process)
	}
	return table, nil
}

func (s *fakeProcs) Signal(pid int, sig syscall.Signal) error {
	s.deliver(pid, sig, false)
	return nil
}

func (s *fakeProcs) SignalGroup(pgid int, sig syscall.Signal) error {
	s.deliver(pgid, sig, true)
	return nil
}

func (s *fakeProcs) deliver(id int, sig syscall.Signal, group bool) {
	name := s.mux.nameByPID(id)
	if name != "" && (sig == syscall.SIGKILL || !s.ignore[name]) {
		s.mux.markDeadByPID(id, 128+int(sig))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.children = slices.DeleteFunc(s.children, func(c *fakeChild) bool {
		hit := c.PID == id
		if group {
			hit = c.PGID == id
		}
		return hit && (sig == syscall.SIGKILL || !c.stubborn)
	})
	if name == "" {
		name = fmt.Sprintf("pid:%d", id)
	}
	s.calls = append(s.calls, signalCall{Name: name, Sig: sig, Group: group})
	s.log.add("signal:" + name)
}

func (s *fakeProcs) alive(pid int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.children, func(c *fakeChild) bool { return c.PID == pid })
}

func (s *fakeProcs) all() []signalCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *fakeProcs) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	for i, c := range s.calls {
		out[i] = c.Name
	}
	return out
}

type fakeStore struct {
	mu     sync.Mutex
	lockMu sync.Mutex
	stacks map[domain.Identity]domain.Stack
}

func newFakeStore() *fakeStore { return &fakeStore{stacks: map[domain.Identity]domain.Stack{}} }

func (s *fakeStore) Lock(context.Context) (func(), error) {
	s.lockMu.Lock()
	return s.lockMu.Unlock, nil
}

func (s *fakeStore) Load(id domain.Identity) (domain.Stack, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stacks[id]
	return st, ok, nil
}

func (s *fakeStore) Save(st domain.Stack) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stacks[st.ID] = st
	return nil
}

// List strips runtime fields, as the real store's public records do.
func (s *fakeStore) List() ([]domain.Stack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := slices.Collect(maps.Keys(s.stacks))
	sort.Slice(keys, func(i, j int) bool { return keys[i].Key() < keys[j].Key() })
	out := make([]domain.Stack, 0, len(keys))
	for _, k := range keys {
		st := s.stacks[k]
		st.Env, st.Snapshot = nil, nil
		svcs := make([]domain.Service, len(st.Services))
		for i, svc := range st.Services {
			svcs[i] = domain.Service{Name: svc.Name, Ports: svc.Ports, URL: svc.URL, Links: svc.Links}
		}
		st.Services = svcs
		out = append(out, st)
	}
	return out, nil
}

func (s *fakeStore) SetStatus(id domain.Identity, status domain.StackStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stacks[id]
	if !ok {
		return nil
	}
	st.Status = status
	s.stacks[id] = st
	return nil
}

func (s *fakeStore) Dir() string { return "/state" }

type fakePorts struct {
	mu   sync.Mutex
	busy map[int]bool
	next int
}

func (p *fakePorts) Free(port int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.busy[port]
}

func (p *fakePorts) Ephemeral() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.next++
	return p.next, nil
}

// fakeReady fails a port's probe a set number of times before passing.
type fakeReady struct {
	mu       sync.Mutex
	log      *eventLog
	failures map[int]int
}

func (r *fakeReady) Probe(_ context.Context, p Probe) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures[p.Port] > 0 {
		r.failures[p.Port]--
		return errors.New("not yet")
	}
	r.log.add(fmt.Sprintf("ready:%d", p.Port))
	return nil
}

type fakeFiles struct{}

func (fakeFiles) MkdirAll(string) error  { return nil }
func (fakeFiles) RemoveAll(string) error { return nil }

type fakeProjects struct{ project Project }

func (f *fakeProjects) Load(string) (Project, error)               { return f.project, nil }
func (f *fakeProjects) Init(string, InitOptions) ([]string, error) { return nil, nil }

type fakes struct {
	log      *eventLog
	mux      *fakeMux
	procs    *fakeProcs
	store    *fakeStore
	ports    *fakePorts
	ready    *fakeReady
	projects *fakeProjects
}

func newTestApp(t *testing.T, def domain.Definition) (*App, *fakes) {
	t.Helper()
	log := &eventLog{}
	mux := newFakeMux(log)
	mux.addSession("work")
	mux.current = "work"
	f := &fakes{
		log:      log,
		mux:      mux,
		procs:    &fakeProcs{mux: mux, log: log},
		store:    newFakeStore(),
		ports:    &fakePorts{busy: map[int]bool{}, next: 40000},
		ready:    &fakeReady{log: log, failures: map[int]int{}},
		projects: &fakeProjects{project: Project{Def: def, Root: testRoot, CheckoutName: "shop"}},
	}
	a := &App{
		Projects:   f.projects,
		Store:      f.store,
		Mux:        f.mux,
		Procs:      f.procs,
		Ports:      f.ports,
		Ready:      f.ready,
		Files:      fakeFiles{},
		Environ:    func() map[string]string { return map[string]string{"HOME": "/home/me"} },
		Now:        time.Now,
		Executable: "svx",
		Poll:       2 * time.Millisecond,
		KillGrace:  20 * time.Millisecond,
	}
	return a, f
}

// svc builds a service definition with the defaults the loader fills in.
func svc(name string, port int, deps ...string) domain.ServiceDef {
	s := domain.ServiceDef{
		Name:      name,
		Run:       "run-" + name,
		Depends:   deps,
		Autostart: true,
		Ports:     map[string]domain.PortSpec{},
		Ready:     domain.ReadySpec{Kind: domain.ReadyDefault, Timeout: 2 * time.Second},
		Stop:      domain.StopSpec{Signal: "SIGTERM", Timeout: time.Second},
	}
	if port > 0 {
		s.Port = domain.FixedPort(port)
	} else {
		s.Ready = domain.ReadySpec{Kind: domain.ReadyDelay, Value: "1ms", Timeout: 2 * time.Second}
	}
	return s
}

func oneshot(name string, deps ...string) domain.ServiceDef {
	s := svc(name, 0, deps...)
	s.Oneshot = true
	s.Ready = domain.ReadySpec{Kind: domain.ReadyDefault, Timeout: 2 * time.Second}
	return s
}

func testDef(services ...domain.ServiceDef) domain.Definition {
	return domain.Definition{
		Source:   "svx.yaml",
		Project:  "shop",
		Settings: domain.Settings{PortStride: 10, MaxSlots: 50, Logs: false, MenuBar: true},
		Vars:     map[string]string{},
		Env:      map[string]string{},
		Services: services,
	}
}

const testRoot = "/src/shop"

func target(stack string) Target { return Target{Dir: testRoot, Stack: stack} }

// checkoutTarget is a command run from the checkout without --name.
func checkoutTarget() Target { return Target{Dir: testRoot} }

func ident(stack string) domain.Identity { return domain.Identity{Project: "shop", Stack: stack} }
