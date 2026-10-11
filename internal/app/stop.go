package app

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/hay-kot/svx/internal/domain"
)

// stopOne stops a service and waits until nothing it started is left.
//
// The pane dying is not the service stopping. A wrapper such as `go run` or
// mise can exit while the process it started is still in a graceful
// teardown, and closing the window then sends that process SIGHUP and
// takes its stdout. A process the service moved into a group of its own
// never sees the stop signal and survives the window. So the pane's tree
// is read before the signal, and the wait, bounded by stop_timeout, lasts
// until every process in it has exited.
func (a *App) stopOne(ctx context.Context, stack domain.Stack, svc domain.Service, w Window) {
	a.emit(Event{Kind: EventStopping, Service: svc.Name, Detail: "", Elapsed: 0})
	sig, err := domain.ParseSignal(svc.Stop.Signal)
	if err != nil {
		sig = syscall.SIGINT
	}
	timeout := svc.Stop.Timeout
	if timeout <= 0 {
		timeout = domain.DefaultStopTimeout
	}

	start := a.Now()
	tree := a.paneTree(ctx, svc.Name, w.PanePID)
	_ = a.Procs.SignalGroup(w.PanePID, sig)
	if a.waitStopped(ctx, stack, svc.Name, tree, timeout) {
		a.emit(Event{Kind: EventStopped, Service: svc.Name, Detail: "", Elapsed: a.Now().Sub(start)})
		return
	}

	a.forceStop(ctx, stack, svc.Name, tree)
	a.emit(Event{
		Kind:    EventStopped,
		Service: svc.Name,
		Detail:  fmt.Sprintf("did not stop within %s of %s, killed", timeout, svc.Stop.Signal),
		Elapsed: a.Now().Sub(start),
	})
}

// forceStop signals the pane's group and each process left outside it,
// SIGTERM and then SIGKILL after a grace period. Closing the window would
// not reach a process outside the pane's group.
func (a *App) forceStop(ctx context.Context, stack domain.Stack, name string, tree paneTree) {
	signalEach := func(sig syscall.Signal) {
		_ = a.Procs.SignalGroup(tree.pane.PID, sig)
		for _, p := range a.leftovers(ctx, tree) {
			if p.PGID != tree.pane.PID {
				_ = a.Procs.Signal(p.PID, sig)
			}
		}
	}
	signalEach(syscall.SIGTERM)
	if a.waitStopped(ctx, stack, name, tree, a.killGrace()) {
		return
	}
	signalEach(syscall.SIGKILL)
	a.waitStopped(ctx, stack, name, tree, a.killGrace())
}

// waitStopped polls until the service's pane is dead and its tree empty,
// and reports false when timeout runs out first.
func (a *App) waitStopped(ctx context.Context, stack domain.Stack, name string, tree paneTree, timeout time.Duration) bool {
	deadline := a.Now().Add(timeout)
	ticker := time.NewTicker(a.poll())
	defer ticker.Stop()
	for {
		w, found, err := a.window(ctx, stack, name)
		if err == nil && (!found || w.Dead) && len(a.leftovers(ctx, tree)) == 0 {
			return true
		}
		if a.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

// paneTree records a pane's process and everything under it. When the
// process table cannot be read, the stop falls back to waiting on the pane
// alone.
func (a *App) paneTree(ctx context.Context, service string, pane int) paneTree {
	table, err := a.Procs.Snapshot(ctx)
	if err != nil {
		a.emit(Event{Kind: EventWarning, Service: service, Detail: fmt.Sprintf("cannot read the process table, waiting on the pane only: %v", err), Elapsed: 0})
		return paneTree{pane: Process{PID: pane}}
	}
	return newPaneTree(table, pane)
}

// leftovers lists what the tree still has running. A failed read counts
// as nothing left, so a broken ps cannot hold a stop until its timeout.
func (a *App) leftovers(ctx context.Context, tree paneTree) []Process {
	if tree.pane.Command == "" {
		return nil
	}
	table, err := a.Procs.Snapshot(ctx)
	if err != nil {
		return nil
	}
	return tree.running(table)
}

// paneTree is what a window is responsible for: its pane's process and
// every process under it when the stop began.
type paneTree struct {
	pane    Process
	members []Process
}

func newPaneTree(table []Process, pane int) paneTree {
	self, ok := findProcess(table, pane)
	if !ok {
		self = Process{PID: pane}
	}
	return paneTree{pane: self, members: descendants(table, pane)}
}

// running lists the tree's processes still in table: the pane while it is
// the same process, every member still alive, everything under either,
// and anything left in the pane's process group.
//
// Members are found by pid and command together, because a member that
// outlives its parent is reparented away from the pane, and its pid can
// be recycled once it exits. The group is safe to match by id alone: the
// kernel does not reuse a pid while a group with that id exists, so a
// group whose leader pid is free belongs to the pane.
func (t paneTree) running(table []Process) []Process {
	var roots []Process
	paneLive := false
	if now, ok := findProcess(table, t.pane.PID); ok && now.Command == t.pane.Command {
		roots = append(roots, now)
		paneLive = true
	}
	for _, was := range t.members {
		if now, ok := findProcess(table, was.PID); ok && now.Command == was.Command {
			roots = append(roots, now)
		}
	}

	var out []Process
	seen := map[int]bool{}
	add := func(p Process) {
		if !seen[p.PID] {
			seen[p.PID] = true
			out = append(out, p)
		}
	}
	for _, r := range roots {
		add(r)
		for _, d := range descendants(table, r.PID) {
			add(d)
		}
	}

	_, pidTaken := findProcess(table, t.pane.PID)
	if paneLive || !pidTaken {
		for _, p := range table {
			if p.PGID == t.pane.PID {
				add(p)
			}
		}
	}
	return out
}

// descendants returns every process below root.
func descendants(table []Process, root int) []Process {
	children := map[int][]Process{}
	for _, p := range table {
		if p.PID != p.PPID {
			children[p.PPID] = append(children[p.PPID], p)
		}
	}
	var found []Process
	seen := map[int]bool{root: true}
	for queue := []int{root}; len(queue) > 0; queue = queue[1:] {
		for _, c := range children[queue[0]] {
			if seen[c.PID] {
				continue
			}
			seen[c.PID] = true
			found = append(found, c)
			queue = append(queue, c.PID)
		}
	}
	return found
}

func findProcess(table []Process, pid int) (Process, bool) {
	for _, p := range table {
		if p.PID == pid {
			return p, true
		}
	}
	return Process{}, false
}
