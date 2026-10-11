package app

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hay-kot/svx/internal/domain"
)

func mustUp(t *testing.T, a *App, req UpRequest) UpResult {
	t.Helper()
	res, err := a.Up(t.Context(), req)
	if err != nil {
		t.Fatalf("Up(%+v): %v", req.Target, err)
	}
	return res
}

func portOf(t *testing.T, st domain.Stack, service string) int {
	t.Helper()
	s, ok := st.Service(service)
	if !ok {
		t.Fatalf("no service %s", service)
	}
	p, ok := s.MainPort()
	if !ok {
		t.Fatalf("service %s has no port", service)
	}
	return p
}

func TestUpAllocatesSlotsPerLiveStack(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000), svc("web", 5000, "api")))

	one := mustUp(t, a, UpRequest{Target: target("one")})
	two := mustUp(t, a, UpRequest{Target: target("two")})

	tests := []struct {
		res       UpResult
		slot      int
		api, web  int
		stackName string
	}{
		{one, 0, 3000, 5000, "one"},
		{two, 1, 3010, 5010, "two"},
	}
	for _, tt := range tests {
		if tt.res.Stack.Slot != tt.slot {
			t.Errorf("%s slot = %d, want %d", tt.stackName, tt.res.Stack.Slot, tt.slot)
		}
		if got := portOf(t, tt.res.Stack, "api"); got != tt.api {
			t.Errorf("%s api port = %d, want %d", tt.stackName, got, tt.api)
		}
		if got := portOf(t, tt.res.Stack, "web"); got != tt.web {
			t.Errorf("%s web port = %d, want %d", tt.stackName, got, tt.web)
		}
		rec, ok, _ := f.store.Load(ident(tt.stackName))
		if !ok || rec.Status != domain.StackUp || rec.Slot != tt.slot {
			t.Errorf("%s record = %+v (found %v), want up in slot %d", tt.stackName, rec.Status, ok, tt.slot)
		}
	}
}

func TestUpReusesSessionAndStartsOnlyMissingOrDead(t *testing.T) {
	a, f := newTestApp(t, testDef(oneshot("migrate"), svc("db", 4000), svc("api", 3000, "db", "migrate"), svc("web", 5000, "api")))
	f.mux.dieOnStart["migrate"] = 0

	first := mustUp(t, a, UpRequest{Target: target("one")})
	session := first.Stack.Session

	f.mux.markDead(session, "api", 1)
	f.mux.removeWindow(session, "web")
	f.mux.resetCalls()

	second := mustUp(t, a, UpRequest{Target: target("one")})
	if second.Stack.Slot != first.Stack.Slot {
		t.Errorf("slot = %d, want recorded %d", second.Stack.Slot, first.Stack.Slot)
	}
	if got := f.mux.startedNames(); !slices.Equal(got, []string{"web"}) {
		t.Errorf("started = %v, want [web]", got)
	}
	if got := f.mux.respawnedNames(); !slices.Equal(got, []string{"api"}) {
		t.Errorf("respawned = %v, want [api]", got)
	}
	for name, want := range map[string]OutcomeState{"migrate": OutcomeRunning, "db": OutcomeRunning, "api": OutcomeReady, "web": OutcomeReady} {
		if got := second.Outcomes[name].State; got != want {
			t.Errorf("%s outcome = %s, want %s", name, got, want)
		}
	}
}

func TestUpRotatesPastBusyPort(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000), svc("web", 5000)))

	mustUp(t, a, UpRequest{Target: target("one")})
	if _, err := a.Down(t.Context(), DownRequest{Target: target("one")}); err != nil {
		t.Fatal(err)
	}

	f.ports.busy[3000] = true
	res := mustUp(t, a, UpRequest{Target: target("one")})
	if res.Stack.Slot != 1 {
		t.Fatalf("slot = %d, want 1", res.Stack.Slot)
	}
	if !slices.Contains(res.Moved, "api 3000 -> 3010") || !slices.Contains(res.Moved, "web 5000 -> 5010") {
		t.Errorf("Moved = %v", res.Moved)
	}
}

func TestUpFailsWhenEverySlotIsBusy(t *testing.T) {
	def := testDef(svc("api", 3000))
	def.Settings.MaxSlots = 2
	a, f := newTestApp(t, def)
	f.ports.busy[3000], f.ports.busy[3010] = true, true

	_, err := a.Up(t.Context(), UpRequest{Target: target("one")})
	if err == nil {
		t.Fatal("Up succeeded with every slot busy")
	}
	for _, want := range []string{"max_slots", "3000", "3010"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestUpStartsAfterDependencyIsReady(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("db", 4000), svc("api", 3000, "db")))
	f.ready.failures[4000] = 3

	mustUp(t, a, UpRequest{Target: target("one")})

	ready, start := f.log.index("ready:4000"), f.log.index("start:api")
	if ready < 0 || start < 0 || start < ready {
		t.Fatalf("events = %v, want db ready before api starts", f.log.all())
	}
}

func TestUpFailingDependencySkipsDependents(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("db", 4000), svc("api", 3000, "db"), svc("web", 5000, "api"), svc("cache", 6000)))
	f.mux.dieOnStart["db"] = 1

	res, err := a.Up(t.Context(), UpRequest{Target: target("one")})
	se, ok := errors.AsType[*ServiceError](err)
	if !ok {
		t.Fatalf("err = %v, want *ServiceError", err)
	}
	if !slices.Equal(se.Services, []string{"db"}) {
		t.Errorf("failed services = %v, want [db]", se.Services)
	}
	if ExitCode(err) != ExitFailure {
		t.Errorf("ExitCode = %d, want %d", ExitCode(err), ExitFailure)
	}
	for name, want := range map[string]OutcomeState{"db": OutcomeFailed, "api": OutcomeSkipped, "web": OutcomeSkipped, "cache": OutcomeReady} {
		if got := res.Outcomes[name].State; got != want {
			t.Errorf("%s outcome = %s, want %s", name, got, want)
		}
	}
	if slices.Contains(f.mux.startedNames(), "api") {
		t.Error("api started despite failed dependency")
	}
}

func TestOneshotExitCode(t *testing.T) {
	tests := []struct {
		name string
		code int
		want OutcomeState
	}{
		{"exit 0 is ready", 0, OutcomeReady},
		{"exit 1 fails", 1, OutcomeFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, f := newTestApp(t, testDef(oneshot("migrate")))
			f.mux.dieOnStart["migrate"] = tt.code

			res, err := a.Up(t.Context(), UpRequest{Target: target("one")})
			if got := res.Outcomes["migrate"].State; got != tt.want {
				t.Errorf("outcome = %s, want %s", got, tt.want)
			}
			if (err != nil) != (tt.want == OutcomeFailed) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestStaleRecordReleasesSlot(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	stale := domain.Stack{
		ID:     ident("ghost"),
		Slot:   0,
		Status: domain.StackUp,
		Services: []domain.Service{
			{Name: "api", Ports: map[string]int{domain.MainPort: 3000}},
		},
	}
	_ = f.store.Save(stale)

	res := mustUp(t, a, UpRequest{Target: target("one")})
	if res.Stack.Slot != 0 {
		t.Errorf("slot = %d, want 0 since ghost's session is gone", res.Stack.Slot)
	}
	rec, _, _ := f.store.Load(ident("ghost"))
	if rec.Status != domain.StackDown {
		t.Errorf("ghost status = %s, want down", rec.Status)
	}
}

// A record saved moments ago may not have its windows yet. It must still
// hold its slot, or two concurrent `up` runs could share one.
func TestFreshRecordHoldsSlotBeforeWindowsExist(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	starting := domain.Stack{
		ID:        ident("starting"),
		Slot:      0,
		Status:    domain.StackUp,
		StartedAt: time.Now(),
		Services: []domain.Service{
			{Name: "api", Ports: map[string]int{domain.MainPort: 3000}},
		},
	}
	_ = f.store.Save(starting)

	res := mustUp(t, a, UpRequest{Target: target("one")})
	if res.Stack.Slot != 1 {
		t.Errorf("slot = %d, want 1 since starting still holds slot 0", res.Stack.Slot)
	}
	rec, _, _ := f.store.Load(ident("starting"))
	if rec.Status != domain.StackUp {
		t.Errorf("starting status = %s, want up", rec.Status)
	}
}

func TestDownStopsInReverseDependencyOrder(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("db", 4000), svc("api", 3000, "db"), svc("web", 5000, "api")))
	up := mustUp(t, a, UpRequest{Target: target("one")})

	if _, err := a.Down(t.Context(), DownRequest{Target: target("one")}); err != nil {
		t.Fatal(err)
	}
	if got := f.signals.names(); !slices.Equal(got, []string{"web", "api", "db"}) {
		t.Errorf("signal order = %v, want [web api db]", got)
	}
	if ws, _ := f.mux.Windows(t.Context(), up.Stack.Session, ident("one").Key()); len(ws) != 0 {
		t.Errorf("windows after down = %v", ws)
	}
	rec, _, _ := f.store.Load(ident("one"))
	if rec.Status != domain.StackDown || rec.Slot != up.Stack.Slot {
		t.Errorf("record = %s slot %d, want down slot %d", rec.Status, rec.Slot, up.Stack.Slot)
	}
}

func TestDownKillsServiceThatIgnoresStopSignal(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	mustUp(t, a, UpRequest{Target: target("one")})
	f.signals.ignore = map[string]bool{"api": true}

	if _, err := a.Down(t.Context(), DownRequest{Target: target("one")}); err != nil {
		t.Fatal(err)
	}
	if got := f.signals.sigs(); !slices.Equal(got, []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL}) {
		t.Errorf("signals = %v, want [SIGTERM SIGKILL]", got)
	}
	if ws, _ := f.mux.Windows(t.Context(), "work", ident("one").Key()); len(ws) != 0 {
		t.Errorf("windows after down = %v", ws)
	}
}

func TestDownAllStopsEveryUpStack(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	mustUp(t, a, UpRequest{Target: target("one")})
	mustUp(t, a, UpRequest{Target: target("two")})

	res, err := a.Down(t.Context(), DownRequest{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Stopped) != 2 {
		t.Fatalf("stopped = %v, want 2 stacks", res.Stopped)
	}
	for _, name := range []string{"one", "two"} {
		rec, _, _ := f.store.Load(ident(name))
		if rec.Status != domain.StackDown {
			t.Errorf("%s status = %s, want down", name, rec.Status)
		}
		if ws, _ := f.mux.Windows(t.Context(), "work", ident(name).Key()); len(ws) != 0 {
			t.Errorf("%s windows after down = %v", name, ws)
		}
	}
}

// The window name template reaches tmux as the window name, while the
// service tag stays the bare service name.
func TestUpNamesWindowsFromTemplate(t *testing.T) {
	def := testDef(svc("api", 3000), svc("ui", 5000, "api"))
	def.Settings.WindowName = "rcp.{{ .service.name }}"
	a, f := newTestApp(t, def)
	mustUp(t, a, UpRequest{Target: target("one")})

	got := map[string]string{}
	for _, spec := range f.mux.started {
		got[spec.Service] = spec.Name
	}
	if want := map[string]string{"api": "rcp.api", "ui": "rcp.ui"}; !maps.Equal(got, want) {
		t.Fatalf("window names by service = %v, want %v", got, want)
	}
}

func TestRestartKeepsPortsAndRespawns(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000), svc("web", 5000, "api")))
	mustUp(t, a, UpRequest{Target: target("one")})
	mustUp(t, a, UpRequest{Target: target("two")})
	f.mux.resetCalls()

	res, err := a.Restart(t.Context(), RestartRequest{Target: target("two"), Services: []string{"api"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := portOf(t, res.Stack, "api"); got != 3010 {
		t.Errorf("api port = %d, want 3010", got)
	}
	if got := f.mux.respawnedNames(); !slices.Equal(got, []string{"api"}) {
		t.Errorf("respawned = %v, want [api]", got)
	}
	if got := f.mux.startedNames(); len(got) != 0 {
		t.Errorf("started = %v, want none", got)
	}
	if got := f.signals.names(); !slices.Equal(got, []string{"api"}) {
		t.Errorf("signalled = %v, want [api]", got)
	}
}

// Restarting several services brings them back in dependency order, the
// same as up: a dependency is ready before its dependents respawn.
func TestRestartFollowsDependencyOrder(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("db", 5432), oneshot("migrate", "db"), svc("api", 3000, "migrate")))
	f.mux.dieOnStart["migrate"] = 0
	mustUp(t, a, UpRequest{Target: target("one")})
	f.mux.resetCalls()

	if _, err := a.Restart(t.Context(), RestartRequest{Target: target("one"), Services: []string{"api", "migrate", "db"}}); err != nil {
		t.Fatal(err)
	}
	if got := f.mux.respawnedNames(); !slices.Equal(got, []string{"db", "migrate", "api"}) {
		t.Errorf("respawned = %v, want [db migrate api]", got)
	}
}

func TestRestartSkipsDependentOfFailedDependency(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("db", 5432), svc("api", 3000, "db")))
	mustUp(t, a, UpRequest{Target: target("one")})
	f.mux.resetCalls()
	f.mux.dieOnStart["db"] = 1

	res, err := a.Restart(t.Context(), RestartRequest{Target: target("one"), Services: []string{"db", "api"}})
	if _, ok := errors.AsType[*ServiceError](err); !ok {
		t.Fatalf("err = %v, want *ServiceError", err)
	}
	if got := res.Outcomes["api"].State; got != OutcomeSkipped {
		t.Errorf("api outcome = %s, want skipped", got)
	}
	if got := f.mux.respawnedNames(); !slices.Equal(got, []string{"db"}) {
		t.Errorf("respawned = %v, want [db]", got)
	}
}

// --all restarts what up started and leaves a never-started idle service
// alone.
func TestRestartAllSkipsIdle(t *testing.T) {
	worker := svc("worker", 0)
	worker.Autostart = false
	a, f := newTestApp(t, testDef(svc("db", 5432), svc("api", 3000, "db"), worker))
	mustUp(t, a, UpRequest{Target: target("one")})
	f.mux.resetCalls()

	if _, err := a.Restart(t.Context(), RestartRequest{Target: target("one"), All: true}); err != nil {
		t.Fatal(err)
	}
	if got := f.mux.respawnedNames(); !slices.Equal(got, []string{"db", "api"}) {
		t.Errorf("respawned = %v, want [db api]", got)
	}
	if got := f.mux.startedNames(); len(got) != 0 {
		t.Errorf("started = %v, want none", got)
	}

	_, err := a.Restart(t.Context(), RestartRequest{Target: target("one"), Services: []string{"api"}, All: true})
	if ExitCode(err) != ExitUsage {
		t.Errorf("services with --all: err = %v, want a usage error", err)
	}
}

func TestStatusStates(t *testing.T) {
	worker := svc("worker", 0)
	worker.Autostart = false
	a, f := newTestApp(t, testDef(svc("api", 3000), svc("web", 5000), svc("crash", 6000), worker))
	up := mustUp(t, a, UpRequest{Target: target("one")})
	session := up.Stack.Session

	f.mux.markDead(session, "crash", 2)
	f.mux.removeWindow(session, "web")

	view, err := a.Status(t.Context(), target("one"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]ServiceState{"api": StateRunning, "web": StateMissing, "crash": StateExited, "worker": StateIdle}
	for _, sv := range view.Services {
		if sv.State != want[sv.Service.Name] {
			t.Errorf("%s state = %s, want %s", sv.Service.Name, sv.State, want[sv.Service.Name])
		}
		if sv.Service.Name == "crash" && sv.ExitCode != 2 {
			t.Errorf("crash exit code = %d, want 2", sv.ExitCode)
		}
	}

	f.mux.dropSession(session)
	view, err = a.Status(t.Context(), target("one"))
	if err != nil {
		t.Fatal(err)
	}
	for _, sv := range view.Services {
		if sv.State != StateStopped {
			t.Errorf("%s state = %s after session gone, want stopped", sv.Service.Name, sv.State)
		}
	}
	if view.Stack.Status != domain.StackDown {
		t.Errorf("stack status = %s, want down", view.Stack.Status)
	}
}

func TestPlanRunEnvLayering(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	_ = f.store.Save(domain.Stack{
		ID:       ident("one"),
		Status:   domain.StackUp,
		Snapshot: map[string]string{"A": "snap", "ONLY": "s", "TMUX": "snap-tmux", "TERM": "xterm-ghostty"},
		Services: []domain.Service{{
			Name:  "api",
			Run:   "go run ./api",
			Dir:   "/src/shop/api",
			Ports: map[string]int{domain.MainPort: 3000},
			Env:   map[string]string{"A": "overlay", "PORT": "3000"},
		}},
	})

	plan, err := a.PlanRun("shop--one", "api", 42, map[string]string{"TMUX": "pane-tmux", "TMUX_PANE": "%3"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A=overlay", "ONLY=s", "PORT=3000", "TMUX=pane-tmux", "TMUX_PANE=%3"}
	if !slices.Equal(plan.Env, want) {
		t.Errorf("env = %v, want %v", plan.Env, want)
	}
	if !slices.Equal(plan.Argv, []string{"/bin/sh", "-c", "go run ./api"}) || plan.Dir != "/src/shop/api" {
		t.Errorf("plan = %+v", plan)
	}
	if !strings.Contains(plan.Banner, "on 3000") {
		t.Errorf("banner = %q", plan.Banner)
	}
}

func TestUpUnknownServiceIsUsageError(t *testing.T) {
	a, _ := newTestApp(t, testDef(svc("api", 3000)))
	_, err := a.Up(t.Context(), UpRequest{Target: target("one"), Services: []string{"ghost"}})
	if _, ok := errors.AsType[*UsageError](err); !ok {
		t.Fatalf("err = %v, want *UsageError", err)
	}
	if ExitCode(err) != ExitUsage {
		t.Errorf("ExitCode = %d, want %d", ExitCode(err), ExitUsage)
	}
}

// A running stack stays in the session it started in, and down leaves that
// session and the user's own windows alone.
func TestUpKeepsRunningStackInItsSession(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 8080), svc("web", 3000, "api")))
	mustUp(t, a, UpRequest{Target: target("one")})

	f.mux.addSession("other")
	f.mux.current = "other"
	again := mustUp(t, a, UpRequest{Target: target("one")})
	if again.Stack.Session != "work" || len(f.mux.startedNames()) != 2 {
		t.Fatalf("repeat up: session=%q started=%v, want the running stack left in work", again.Stack.Session, f.mux.startedNames())
	}

	if _, err := a.Down(t.Context(), DownRequest{Target: target("one")}); err != nil {
		t.Fatal(err)
	}
	if !f.mux.hasSession("work") || len(f.mux.sessions["work"]) != 1 || f.mux.sessions["work"][0].ID != "@user" {
		t.Errorf("user's session after down = %v", f.mux.sessions["work"])
	}
}

func TestUpOutsideTmuxIsUsageError(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 8080)))
	f.mux.current = ""
	_, err := a.Up(t.Context(), UpRequest{Target: target("one")})
	if ExitCode(err) != ExitUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if len(f.mux.started) != 0 {
		t.Errorf("started = %v, want none", f.mux.startedNames())
	}
}

func TestUpInNamedSessionFromOutsideTmux(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 8080)))
	f.mux.current = ""
	f.mux.addSession("dev")

	up := mustUp(t, a, UpRequest{Target: target("one"), Session: "dev"})
	if up.Stack.Session != "dev" || !slices.Equal(f.mux.startedNames(), []string{"api"}) {
		t.Fatalf("session=%q started=%v, want api started in dev", up.Stack.Session, f.mux.startedNames())
	}
}

func TestUpInMissingSessionIsUsageError(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 8080)))
	_, err := a.Up(t.Context(), UpRequest{Target: target("one"), Session: "ghost"})
	if ExitCode(err) != ExitUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if len(f.mux.started) != 0 {
		t.Errorf("started = %v, want none", f.mux.startedNames())
	}
}
