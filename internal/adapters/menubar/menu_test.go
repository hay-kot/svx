package menubar

import (
	"testing"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

func view(stack string, status domain.StackStatus, menubar bool, services ...app.ServiceView) app.StackView {
	s := domain.Stack{
		ID:       domain.Identity{Project: "shop", Stack: stack},
		Status:   status,
		Settings: domain.Settings{MenuBar: menubar},
	}
	for _, svc := range services {
		s.Services = append(s.Services, svc.Service)
	}
	return app.StackView{Stack: s, Services: services}
}

func svc(name string, state app.ServiceState, code int, links ...domain.Link) app.ServiceView {
	return app.ServiceView{Service: domain.Service{Name: name, Links: links}, State: state, ExitCode: code}
}

func TestBuildEmpty(t *testing.T) {
	m := Build(nil)
	if m.Running != 0 || len(m.Entries) != 1 || m.Entries[0].Title != "No stacks running" {
		t.Fatalf("Build(nil) = %+v", m)
	}
}

func TestBuildSkipsDownAndHiddenStacks(t *testing.T) {
	m := Build([]app.StackView{
		view("down", domain.StackDown, true),
		view("hidden", domain.StackUp, false),
	})
	if m.Running != 0 {
		t.Fatalf("Running = %d, want 0", m.Running)
	}
}

func TestBuildStack(t *testing.T) {
	migrate := svc("migrate", app.StateExited, 0)
	migrate.Service.Oneshot = true
	m := Build([]app.StackView{
		view("blue", domain.StackUp, true,
			migrate,
			svc("web", app.StateRunning, 0, domain.Link{Label: "App", URL: "http://127.0.0.1:3000/"}),
			withPorts(svc("db", app.StateRunning, 0), map[string]int{"main": 5432, "admin": 5433}),
			svc("api", app.StateExited, 3),
			svc("worker", app.StateMissing, 0),
		),
		view("green", domain.StackUp, true, svc("web", app.StateRunning, 0)),
	})

	blue := domain.Identity{Project: "shop", Stack: "blue"}
	green := domain.Identity{Project: "shop", Stack: "green"}
	want := []Entry{
		{Kind: EntryHeader, Title: "shop · blue"},
		{Kind: EntryAction, Title: "App  :3000", Action: Action{Kind: ActionOpenURL, URL: "http://127.0.0.1:3000/"}},
		{Kind: EntryNote, Title: "db  :5433 :5432"},
		{Kind: EntryNote, Title: "api exited (3)"},
		{Kind: EntryNote, Title: "worker window closed"},
		{Kind: EntryAction, Title: "Attach in Terminal", Action: Action{Kind: ActionAttach, Stack: blue}},
		{Kind: EntryAction, Title: "Stop Stack", Action: Action{Kind: ActionStop, Stack: blue}},
		{Kind: EntrySeparator},
		{Kind: EntryHeader, Title: "shop · green"},
		{Kind: EntryAction, Title: "Attach in Terminal", Action: Action{Kind: ActionAttach, Stack: green}},
		{Kind: EntryAction, Title: "Stop Stack", Action: Action{Kind: ActionStop, Stack: green}},
	}
	if m.Running != 2 {
		t.Fatalf("Running = %d, want 2", m.Running)
	}
	if !m.Equal(Menu{Running: 2, Entries: want}) {
		t.Fatalf("Entries =\n%+v\nwant\n%+v", m.Entries, want)
	}
}

func withPorts(sv app.ServiceView, ports map[string]int) app.ServiceView {
	sv.Service.Ports = ports
	return sv
}
