// Package menubar is svx's macOS menu bar app. It shows running stacks
// with their links and lets you attach to or stop one.
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
	"net/url"
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
)

// ActionKind is what clicking an entry does.
type ActionKind int

const (
	ActionNone ActionKind = iota
	ActionOpenURL
	ActionAttach
	ActionStop
)

// Action is an entry's click handler, as data so menus compare by value.
type Action struct {
	Kind  ActionKind
	URL   string
	Stack domain.Identity
}

// Entry is one row of the menu.
type Entry struct {
	Kind   EntryKind
	Title  string
	Action Action
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
		m.Entries = append(m.Entries, Entry{Kind: EntryHeader, Title: s.ID.Project + " · " + s.ID.Stack})
		for _, svc := range v.Services {
			m.Entries = append(m.Entries, serviceEntries(svc.Service)...)
		}
		for _, svc := range v.Services {
			if note, ok := serviceNote(svc); ok {
				m.Entries = append(m.Entries, Entry{Kind: EntryNote, Title: note})
			}
		}
		m.Entries = append(m.Entries,
			Entry{Kind: EntryAction, Title: "Attach in Terminal", Action: Action{Kind: ActionAttach, Stack: s.ID}},
			Entry{Kind: EntryAction, Title: "Stop Stack", Action: Action{Kind: ActionStop, Stack: s.ID}},
		)
	}
	if m.Running == 0 {
		m.Entries = append(m.Entries, Entry{Kind: EntryNote, Title: "No stacks running"})
	}
	return m
}

// serviceEntries lists a service's links with the port each one points at.
// A service with ports but no links still gets a row, so every port the
// stack holds is visible.
func serviceEntries(svc domain.Service) []Entry {
	var entries []Entry
	for _, l := range svc.Links {
		title := l.Label
		if u, err := url.Parse(l.URL); err == nil && u.Port() != "" {
			title += "  :" + u.Port()
		}
		entries = append(entries, Entry{Kind: EntryAction, Title: title, Action: Action{Kind: ActionOpenURL, URL: l.URL}})
	}
	if len(svc.Links) == 0 && len(svc.Ports) > 0 {
		ports := make([]string, 0, len(svc.Ports))
		for _, name := range slices.Sorted(maps.Keys(svc.Ports)) {
			ports = append(ports, ":"+strconv.Itoa(svc.Ports[name]))
		}
		entries = append(entries, Entry{Kind: EntryNote, Title: svc.Name + "  " + strings.Join(ports, " ")})
	}
	return entries
}

// serviceNote describes a service that needs attention. Running services
// and oneshots that finished cleanly need none.
func serviceNote(svc app.ServiceView) (string, bool) {
	switch svc.State {
	case app.StateExited:
		if svc.Service.Oneshot && svc.ExitCode == 0 {
			return "", false
		}
		return fmt.Sprintf("%s exited (%d)", svc.Service.Name, svc.ExitCode), true
	case app.StateMissing:
		return svc.Service.Name + " window closed", true
	case app.StateRunning, app.StateIdle, app.StateStopped:
	}
	return "", false
}

// ErrorMenu shows a failure to read state in place of the stacks.
func ErrorMenu(err error) Menu {
	return Menu{Entries: []Entry{{Kind: EntryNote, Title: "svx: " + err.Error()}}}
}
