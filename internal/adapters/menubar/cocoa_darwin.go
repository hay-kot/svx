//go:build darwin && cgo

package menubar

/*
#cgo CFLAGS: -x objective-c -mmacosx-version-min=11.0
#cgo LDFLAGS: -framework Cocoa
#include <stdlib.h>
#include "cocoa_darwin.h"
*/
import "C"

import (
	"context"
	"runtime"
	"sync"
	"unsafe"

	"github.com/rs/zerolog/log"
)

// Supported reports whether this build has the Cocoa UI.
const Supported = true

// AppKit only works on the process's main thread. init runs on it, and
// locking here keeps the main goroutine there for when Run starts the
// event loop.
func init() { runtime.LockOSThread() } //nolint:gochecknoinits // only an init runs early enough to stay on the main thread

var ui struct {
	mu      sync.Mutex
	actions map[int]Action
	click   func(Action)
	opened  func()
	calls   map[uintptr]func()
	next    uintptr
}

func runUI(ctx context.Context, b *Bar) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	C.svxInit()

	refresh := make(chan struct{}, 1)
	poke := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	ui.mu.Lock()
	ui.opened = poke
	ui.click = func(a Action) {
		go func() {
			if err := b.perform(ctx, a); err != nil {
				log.Error().Err(err).Msg("menu bar action failed")
			}
			poke()
		}()
	}
	ui.mu.Unlock()

	go b.watch(ctx, refresh, func(m Menu) { onMain(func() { draw(m) }) })
	go func() {
		<-ctx.Done()
		C.svxStop()
	}()

	C.svxRun()
	return nil
}

// draw rebuilds the menu in place. Editing the open NSMenu, rather than
// swapping in a new one, updates it live while it is showing.
func draw(m Menu) {
	actions := map[int]Action{}
	C.svxMenuClear()
	for i, e := range m.Entries {
		title, detail := C.CString(e.Title), C.CString(e.Detail)
		tone, indent := C.int(e.Tone), C.int(e.Indent)
		switch e.Kind {
		case EntryHeader:
			C.svxMenuAddHeader(title)
		case EntryAction:
			tag := i + 1
			actions[tag] = e.Action
			C.svxMenuAddItem(title, detail, C.int(tag), true, indent, tone, C.bool(e.Alternate))
		case EntryNote:
			C.svxMenuAddItem(title, detail, 0, false, indent, tone, false)
		case EntrySeparator:
			C.svxMenuAddSeparator()
		case EntrySubmenu:
			C.svxMenuBeginSubmenu(title, detail, tone, C.bool(e.Bold))
		case EntrySubmenuEnd:
			C.svxMenuEndSubmenu()
		}
		C.free(unsafe.Pointer(title))
		C.free(unsafe.Pointer(detail))
	}
	C.svxMenuAddSeparator()
	C.svxMenuAddQuit()
	C.svxMenuFinish()
	C.svxSetStatus(C.int(m.Running))

	ui.mu.Lock()
	ui.actions = actions
	ui.mu.Unlock()
}

// onMain runs fn on the main thread, the way Wails' dispatchOnMainThread
// does: C cannot hold a Go func, so it gets an id to call back with.
func onMain(fn func()) {
	ui.mu.Lock()
	if ui.calls == nil {
		ui.calls = map[uintptr]func(){}
	}
	ui.next++
	id := ui.next
	ui.calls[id] = fn
	ui.mu.Unlock()
	C.svxDispatchMain(C.uintptr_t(id))
}

//export svxMainCallback
func svxMainCallback(id C.uintptr_t) {
	ui.mu.Lock()
	fn := ui.calls[uintptr(id)]
	delete(ui.calls, uintptr(id))
	ui.mu.Unlock()
	if fn != nil {
		fn()
	}
}

//export svxItemClicked
func svxItemClicked(tag C.int) {
	ui.mu.Lock()
	a, ok := ui.actions[int(tag)]
	click := ui.click
	ui.mu.Unlock()
	if ok && click != nil {
		click(a)
	}
}

//export svxMenuWillOpen
func svxMenuWillOpen() {
	ui.mu.Lock()
	opened := ui.opened
	ui.mu.Unlock()
	if opened != nil {
		opened()
	}
}
