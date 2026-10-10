// Package menubar is svx's macOS menu bar app. It shows running stacks
// with their links and lets you restart, attach to or stop one.
//
// The Cocoa side follows Wails v3's system tray
// (v3/pkg/application/systemtray_darwin.m): an NSStatusItem with an NSMenu
// from a cgo Objective-C file, an accessory activation policy so no Dock
// icon appears, and UI work dispatched to the main queue. Builds without
// cgo, and any OS other than macOS, get a stub that reports the menu bar as
// unsupported.
package menubar

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// EntryKind is how an entry is drawn.
type EntryKind int

const (
	EntryHeader EntryKind = iota
	EntryAction
	// EntryNote is greyed-out text, such as a dead service.
	EntryNote
	EntrySeparator
	// EntrySubmenu opens a submenu titled Title. Entries up to the
	// matching EntrySubmenuEnd go inside it.
	EntrySubmenu
	EntrySubmenuEnd
)

// ActionKind is what clicking an entry does.
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionOpenURL
	ActionAttach
	ActionStop
	// ActionRestart restarts Service, or every started service when
	// Service is empty.
	ActionRestart
)

// Action is an entry's click handler, as data so menus compare by value.
type Action struct {
	Kind    ActionKind
	URL     string
	Stack   domain.Identity
	Service string
}

// Tone colours a row.
type Tone int

const (
	ToneNormal Tone = iota
	// ToneOK and ToneBad put a green or red health dot before a stack row.
	ToneOK
	ToneBad
	// ToneDim greys a row out, for quiet information.
	ToneDim
)

// Entry is one row of the menu.
type Entry struct {
	Kind  EntryKind
	Title string
	// Detail is right-aligned secondary text, such as a port or a summary.
	Detail string
	Tone   Tone
	// Bold marks a stack row, the strongest row in the menu.
	Bold bool
	// Indent nests a row under its stack, or a link under its service.
	Indent int
	// Alternate makes the row replace the one above it while ⌥ is held.
	Alternate bool
	Action    Action
}

// Menu is everything the status item shows.
type Menu struct {
	// Running is the number of stacks shown, drawn next to the icon.
	Running int
	Entries []Entry
}

// Equal reports whether two menus draw the same, so the UI only rebuilds
// on change.
func (m Menu) Equal(o Menu) bool {
	return m.Running == o.Running && slices.Equal(m.Entries, o.Entries)
}

// Build lays out the menu for the given stacks. Stacks that are down, or
// whose project turned the menu bar off, are left out.
//
// Each stack is a bold row with a health dot and a summary; hovering it
// opens the stack's actions. Under it, every running service with a URL is
// one click from opening, and holding ⌥ turns that click into a restart.
// Failed services show in red. Running services with nothing to open fold
// into one quiet line, and finished oneshots and idle services stay out of
// the list.
func Build(views []app.StackView) Menu {
	var m Menu
	for _, v := range views {
		s := v.Stack
		if s.Status != domain.StackUp || !s.Settings.MenuBar {
			continue
		}
		m.Running++
		if len(m.Entries) > 0 {
			m.Entries = append(m.Entries, Entry{Kind: EntrySeparator})
		}
		m.Entries = append(m.Entries, stackMenu(v)...)
		var quiet []string
		for _, sv := range v.Services {
			rows, isQuiet := serviceRows(s.ID, sv)
			m.Entries = append(m.Entries, rows...)
			if isQuiet {
				quiet = append(quiet, sv.Service.Name)
			}
		}
		if len(quiet) > 0 {
			m.Entries = append(m.Entries, Entry{Kind: EntryNote, Title: "also running: " + strings.Join(quiet, ", "), Tone: ToneDim, Indent: 1})
		}
	}
	if m.Running == 0 {
		m.Entries = append(m.Entries, Entry{Kind: EntryNote, Title: "No stacks running", Tone: ToneDim})
	}
	return m
}

// stackMenu is a stack's row and the submenu of its actions.
func stackMenu(v app.StackView) []Entry {
	id := v.Stack.ID
	running, failed := 0, 0
	for _, sv := range v.Services {
		switch {
		case sv.State == app.StateRunning:
			running++
		case failedState(sv):
			failed++
		}
	}
	head := Entry{Kind: EntrySubmenu, Title: id.Project + " · " + id.Stack, Bold: true, Tone: ToneOK, Detail: fmt.Sprintf("%d running", running)}
	if failed > 0 {
		head.Tone, head.Detail = ToneBad, fmt.Sprintf("%d failed", failed)
	}

	entries := []Entry{
		head,
		{Kind: EntryAction, Title: "Restart All Services", Action: Action{Kind: ActionRestart, Stack: id}},
		{Kind: EntryAction, Title: "Attach in Terminal", Action: Action{Kind: ActionAttach, Stack: id}},
		{Kind: EntryAction, Title: "Stop Stack", Action: Action{Kind: ActionStop, Stack: id}},
		{Kind: EntrySeparator},
		{Kind: EntryHeader, Title: "Restart service"},
	}
	for _, sv := range v.Services {
		verb := "Restart "
		if sv.State == app.StateIdle || sv.State == app.StateStopped || sv.State == app.StateMissing {
			verb = "Start "
		}
		entries = append(entries, Entry{
			Kind:   EntryAction,
			Title:  verb + sv.Service.Name,
			Action: Action{Kind: ActionRestart, Stack: id, Service: sv.Service.Name},
		})
	}
	return append(entries, Entry{Kind: EntrySubmenuEnd})
}

// failedState is a service that should be running and is not. A oneshot
// that exited 0 finished its job.
func failedState(sv app.ServiceView) bool {
	switch sv.State {
	case app.StateExited:
		return sv.ExitCode != 0 || !sv.Service.Oneshot
	case app.StateMissing:
		return true
	case app.StateRunning, app.StateIdle, app.StateStopped:
	}
	return false
}

// serviceRows lists one service under its stack. It reports a running
// service with nothing to open as quiet instead of giving it a row.
func serviceRows(id domain.Identity, sv app.ServiceView) ([]Entry, bool) {
	svc := sv.Service
	restart := Entry{
		Kind: EntryAction, Title: "Restart " + svc.Name, Indent: 1, Alternate: true,
		Action: Action{Kind: ActionRestart, Stack: id, Service: svc.Name},
	}
	switch {
	case sv.State == app.StateMissing:
		return []Entry{{
			Kind: EntryAction, Title: svc.Name, Detail: "window closed", Tone: ToneBad, Indent: 1,
			Action: Action{Kind: ActionRestart, Stack: id, Service: svc.Name},
		}}, false
	case failedState(sv):
		return []Entry{{
			Kind: EntryAction, Title: svc.Name, Detail: fmt.Sprintf("exited (%d)", sv.ExitCode), Tone: ToneBad, Indent: 1,
			Action: Action{Kind: ActionAttach, Stack: id, Service: svc.Name},
		}, restart}, false
	case sv.State != app.StateRunning:
		return nil, false
	}

	links := svc.Links
	if len(links) == 0 && svc.URL != "" {
		links = []domain.Link{{Label: "", URL: svc.URL}}
	}
	if len(links) == 0 {
		return nil, true
	}
	rows := []Entry{
		{Kind: EntryAction, Title: svc.Name, Detail: formatPorts(svc.Ports), Indent: 1, Action: Action{Kind: ActionOpenURL, URL: links[0].URL}},
		restart,
	}
	for _, l := range links[1:] {
		rows = append(rows, Entry{Kind: EntryAction, Title: l.Label, Indent: 2, Action: Action{Kind: ActionOpenURL, URL: l.URL}})
	}
	return rows, false
}

// formatPorts lists ports main first, as ":5432 :5433".
func formatPorts(ports map[string]int) string {
	var out []string
	if p, ok := ports[domain.MainPort]; ok {
		out = append(out, ":"+strconv.Itoa(p))
	}
	for _, name := range slices.Sorted(maps.Keys(ports)) {
		if name != domain.MainPort {
			out = append(out, ":"+strconv.Itoa(ports[name]))
		}
	}
	return strings.Join(out, " ")
}

// RestartTargets picks what "Restart All Services" restarts: every service
// that was started. Idle services were never asked for and stay idle.
func RestartTargets(v app.StackView) []string {
	var names []string
	for _, svc := range v.Services {
		if svc.State != app.StateIdle {
			names = append(names, svc.Service.Name)
		}
	}
	return names
}

// ErrorMenu shows a failure to read state in place of the stacks.
func ErrorMenu(err error) Menu {
	return Menu{Entries: []Entry{{Kind: EntryNote, Title: "svx: " + err.Error()}}}
}
