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
	return app.ServiceView{Service: domain.Service{Name: name, Links: links, Autostart: true}, State: state, ExitCode: code}
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
	web := withPorts(svc("web", app.StateRunning, 0,
		domain.Link{Label: "App", URL: "http://127.0.0.1:3000/"},
		domain.Link{Label: "Admin", URL: "http://127.0.0.1:3000/admin"},
	), map[string]int{"main": 3000})
	db := withPorts(svc("db", app.StateRunning, 0), map[string]int{"main": 5432, "admin": 5433})
	db.Service.URL = "http://127.0.0.1:5432"
	m := Build([]app.StackView{
		view("blue", domain.StackUp, true,
			migrate,
			web,
			db,
			svc("queue", app.StateRunning, 0),
			svc("api", app.StateExited, 3),
			svc("worker", app.StateIdle, 0),
			svc("mail", app.StateRunning, 0),
		),
		view("green", domain.StackUp, true, svc("web", app.StateMissing, 0)),
	})

	blue := domain.Identity{Project: "shop", Stack: "blue"}
	green := domain.Identity{Project: "shop", Stack: "green"}
	restart := func(id domain.Identity, title, service string) Entry {
		return Entry{Kind: EntryAction, Title: title, Action: Action{Kind: ActionRestart, Stack: id, Service: service}}
	}
	alt := func(id domain.Identity, service string) Entry {
		e := restart(id, "Restart "+service, service)
		e.Indent, e.Alternate = 1, true
		return e
	}
	stackActions := func(id domain.Identity, services ...Entry) []Entry {
		out := []Entry{
			restart(id, "Restart All Services", ""),
			{Kind: EntryAction, Title: "Stop Stack", Action: Action{Kind: ActionStop, Stack: id}},
			{Kind: EntrySeparator},
			{Kind: EntryHeader, Title: "Restart service"},
		}
		return append(append(out, services...), Entry{Kind: EntrySubmenuEnd})
	}

	want := []Entry{{Kind: EntrySubmenu, Title: "shop · blue", Detail: "1 failed", Tone: ToneBad, Bold: true}}
	want = append(want, stackActions(blue,
		restart(blue, "Restart migrate", "migrate"),
		restart(blue, "Restart web", "web"),
		restart(blue, "Restart db", "db"),
		restart(blue, "Restart queue", "queue"),
		restart(blue, "Restart api", "api"),
		restart(blue, "Start worker", "worker"),
		restart(blue, "Restart mail", "mail"),
	)...)
	want = append(want,
		Entry{Kind: EntryAction, Title: "web", Detail: ":3000", Indent: 1, Action: Action{Kind: ActionOpenURL, URL: "http://127.0.0.1:3000/"}},
		alt(blue, "web"),
		Entry{Kind: EntryAction, Title: "Admin", Indent: 2, Action: Action{Kind: ActionOpenURL, URL: "http://127.0.0.1:3000/admin"}},
		Entry{Kind: EntryAction, Title: "db", Detail: ":5432 :5433", Indent: 1, Action: Action{Kind: ActionOpenURL, URL: "http://127.0.0.1:5432"}},
		alt(blue, "db"),
		Entry{Kind: EntryAction, Title: "api", Detail: "exited (3)", Tone: ToneBad, Indent: 1, Action: Action{Kind: ActionRestart, Stack: blue, Service: "api"}},
		Entry{Kind: EntryNote, Title: "also running: queue, mail", Tone: ToneDim, Indent: 1},
		Entry{Kind: EntrySeparator},
		Entry{Kind: EntrySubmenu, Title: "shop · green", Detail: "1 failed", Tone: ToneBad, Bold: true},
	)
	want = append(want, stackActions(green, restart(green, "Start web", "web"))...)
	want = append(want,
		Entry{Kind: EntryAction, Title: "web", Detail: "window closed", Tone: ToneBad, Indent: 1, Action: Action{Kind: ActionRestart, Stack: green, Service: "web"}},
	)

	if m.Running != 2 {
		t.Fatalf("Running = %d, want 2", m.Running)
	}
	if !m.Equal(Menu{Running: 2, Entries: want}) {
		for i := range max(len(m.Entries), len(want)) {
			var got, exp Entry
			if i < len(m.Entries) {
				got = m.Entries[i]
			}
			if i < len(want) {
				exp = want[i]
			}
			if got != exp {
				t.Errorf("entry %d:\n got  %+v\n want %+v", i, got, exp)
			}
		}
	}
}

func TestBuildHealthyStackSummary(t *testing.T) {
	m := Build([]app.StackView{view("blue", domain.StackUp, true, svc("api", app.StateRunning, 0), svc("web", app.StateRunning, 0))})
	head := m.Entries[0]
	if head.Title != "shop · blue" || head.Detail != "2 running" || head.Tone != ToneOK || !head.Bold {
		t.Fatalf("stack row = %+v", head)
	}
}

func withPorts(sv app.ServiceView, ports map[string]int) app.ServiceView {
	sv.Service.Ports = ports
	return sv
}
