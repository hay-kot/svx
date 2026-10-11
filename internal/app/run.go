package app

import (
	"fmt"
	"syscall"

	"github.com/hay-kot/svx/internal/domain"
)

// RunPlan is what the shim needs to exec a service.
type RunPlan struct {
	Dir    string
	Argv   []string
	Env    []string
	Banner string
	// Retries is how many times to rerun the command after a non-zero
	// exit. With none, the shim execs the command in its own place.
	Retries int
	// StopSignal is the signal svx stops the service with. The shim must
	// not retry after it.
	StopSignal syscall.Signal
}

// PaneOwned lists variables a pane sets for itself, which must win over the
// snapshot. The snapshot comes from wherever `svx up` ran, which may be
// another terminal or another tmux session.
var PaneOwned = []string{"TMUX", "TMUX_PANE", "TERM"}

// PlanRun reads the recorded stack and prepares one service's exec. pid is
// the shim's own, which the service keeps since the shim execs it.
func (a *App) PlanRun(key, service string, pid int, current map[string]string) (RunPlan, error) {
	id, err := domain.ParseKey(key)
	if err != nil {
		return RunPlan{}, usage(err)
	}
	stack, err := a.loadStack(id)
	if err != nil {
		return RunPlan{}, err
	}
	svc, ok := stack.Service(service)
	if !ok {
		return RunPlan{}, usage(fmt.Errorf("stack %s has no service %q", id, service))
	}

	banner := fmt.Sprintf("%s%s %s", domain.BannerPrefix, id, svc.Name)
	if p, ok := svc.MainPort(); ok {
		banner += fmt.Sprintf(" on %d", p)
	}
	banner += " " + domain.RunMarker(pid) + " -- " + svc.Run

	stop, err := domain.ParseSignal(svc.Stop.Signal)
	if err != nil {
		return RunPlan{}, err
	}

	return RunPlan{
		Dir:        svc.Dir,
		Argv:       []string{"/bin/sh", "-c", svc.Run},
		Env:        domain.ProcessEnv(stack.Snapshot, svc.Env, current, PaneOwned...),
		Banner:     banner,
		Retries:    svc.Retries,
		StopSignal: stop,
	}, nil
}
