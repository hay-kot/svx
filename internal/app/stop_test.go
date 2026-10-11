package app

import (
	"slices"
	"syscall"
	"testing"
	"time"
)

func pids(ps []Process) []int {
	out := make([]int, len(ps))
	for i, p := range ps {
		out[i] = p.PID
	}
	slices.Sort(out)
	return out
}

// A wrapper that exits early leaves its child reparented to pid 1, no
// longer under the pane. The child still counts until it exits.
func TestPaneTreeFollowsReparentedMember(t *testing.T) {
	before := []Process{
		{PID: 100, PPID: 1, PGID: 100, Command: "sh -c mise run dev"},
		{PID: 101, PPID: 100, PGID: 100, Command: "go run ./devtools"},
		{PID: 102, PPID: 101, PGID: 100, Command: "devtools run"},
		{PID: 103, PPID: 102, PGID: 103, Command: "wails3 dev"},
		{PID: 200, PPID: 1, PGID: 200, Command: "another pane"},
	}
	tree := newPaneTree(before, 100)

	if got := pids(tree.running(before)); !slices.Equal(got, []int{100, 101, 102, 103}) {
		t.Fatalf("running before stop = %v", got)
	}

	after := []Process{
		{PID: 102, PPID: 1, PGID: 100, Command: "devtools run"},
		{PID: 103, PPID: 102, PGID: 103, Command: "wails3 dev"},
		{PID: 104, PPID: 102, PGID: 102, Command: "cleanup"},
		{PID: 200, PPID: 1, PGID: 200, Command: "another pane"},
	}
	if got := pids(tree.running(after)); !slices.Equal(got, []int{102, 103, 104}) {
		t.Fatalf("running after the pane died = %v, want the reparented member and what it runs", got)
	}
}

// A member's pid can be reused once it exits. A process with that pid
// and another command belongs to someone else.
func TestPaneTreeIgnoresRecycledPID(t *testing.T) {
	before := []Process{
		{PID: 100, PPID: 1, PGID: 100, Command: "wrapper"},
		{PID: 101, PPID: 100, PGID: 101, Command: "supervisor"},
	}
	tree := newPaneTree(before, 100)

	after := []Process{
		{PID: 101, PPID: 1, PGID: 101, Command: "unrelated"},
		{PID: 102, PPID: 101, PGID: 101, Command: "unrelated child"},
	}
	if got := tree.running(after); len(got) != 0 {
		t.Fatalf("running = %v, want none", got)
	}
}

// Processes left in the pane's group count while the group's id cannot
// belong to anyone else: the pane is alive, or its pid is free.
func TestPaneTreeCountsPaneGroup(t *testing.T) {
	tree := newPaneTree([]Process{{PID: 100, PPID: 1, PGID: 100, Command: "wrapper"}}, 100)

	orphan := Process{PID: 150, PPID: 1, PGID: 100, Command: "started after the snapshot"}
	if got := pids(tree.running([]Process{orphan})); !slices.Equal(got, []int{150}) {
		t.Fatalf("running with the pane's pid free = %v, want [150]", got)
	}

	recycled := []Process{
		{PID: 100, PPID: 1, PGID: 100, Command: "unrelated"},
		{PID: 150, PPID: 100, PGID: 100, Command: "unrelated child"},
	}
	if got := tree.running(recycled); len(got) != 0 {
		t.Fatalf("running with the pane's pid recycled = %v, want none", got)
	}
}

func TestDescendantsSurvivesCycle(t *testing.T) {
	table := []Process{
		{PID: 0, PPID: 0, Command: "kernel_task"},
		{PID: 1, PPID: 0, Command: "launchd"},
		{PID: 10, PPID: 1, Command: "a"},
	}
	if got := pids(descendants(table, 0)); !slices.Equal(got, []int{1, 10}) {
		t.Fatalf("descendants = %v", got)
	}
}

// The pane dies at once, but a child in its own group is still tearing
// down. The window closes only after the child exits, and nothing is
// killed.
func TestDownWaitsForChildThatOutlivesPane(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	up := mustUp(t, a, UpRequest{Target: target("one")})
	f.procs.addChild("api", 9001, 3, false)
	ws, _ := f.mux.Windows(t.Context(), up.Stack.Session, ident("one").Key())

	if _, err := a.Down(t.Context(), DownRequest{Target: target("one")}); err != nil {
		t.Fatal(err)
	}
	if exit, closed := f.log.index("exit:9001"), f.log.index("close:"+ws[0].ID); exit < 0 || closed < exit {
		t.Errorf("events = %v, want the child to exit before the window closes", f.log.all())
	}
	for _, c := range f.procs.all() {
		if c.Name != "api" || c.Sig != syscall.SIGTERM {
			t.Errorf("unexpected signal %+v", c)
		}
	}
}

// A child outside the pane's group that ignores everything short of
// SIGKILL is killed by pid once stop_timeout runs out, before the window
// closes.
func TestDownKillsLeftoverOutsidePaneGroup(t *testing.T) {
	def := svc("api", 3000)
	def.Stop.Timeout = 30 * time.Millisecond
	a, f := newTestApp(t, testDef(def))
	up := mustUp(t, a, UpRequest{Target: target("one")})
	f.procs.addChild("api", 9001, 1_000_000, true)
	ws, _ := f.mux.Windows(t.Context(), up.Stack.Session, ident("one").Key())

	if _, err := a.Down(t.Context(), DownRequest{Target: target("one")}); err != nil {
		t.Fatal(err)
	}
	if f.procs.alive(9001) {
		t.Fatal("child survived down")
	}
	calls := f.procs.all()
	for _, want := range []signalCall{{Name: "pid:9001", Sig: syscall.SIGTERM}, {Name: "pid:9001", Sig: syscall.SIGKILL}} {
		if !slices.Contains(calls, want) {
			t.Errorf("signals = %v, want %+v", calls, want)
		}
	}
	if killed, closed := f.log.index("signal:pid:9001"), f.log.index("close:"+ws[0].ID); killed < 0 || closed < killed {
		t.Errorf("events = %v, want the child signalled before the window closes", f.log.all())
	}
}

// Restart waits for the old tree to empty before it respawns the pane.
func TestRestartWaitsForChildThatOutlivesPane(t *testing.T) {
	a, f := newTestApp(t, testDef(svc("api", 3000)))
	mustUp(t, a, UpRequest{Target: target("one")})
	f.procs.addChild("api", 9001, 3, false)

	if _, err := a.Restart(t.Context(), RestartRequest{Target: target("one"), Services: []string{"api"}}); err != nil {
		t.Fatal(err)
	}
	if exit, respawn := f.log.index("exit:9001"), f.log.index("respawn:api"); exit < 0 || respawn < exit {
		t.Errorf("events = %v, want the child to exit before the respawn", f.log.all())
	}
}
