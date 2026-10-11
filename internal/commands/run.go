package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// retryDelay is the pause before a failed command reruns.
const retryDelay = time.Second

// RunCmd implements the hidden `_run` shim each window runs. It reads the
// recorded stack, so commands and environment values never pass through
// tmux's command line, then execs the service's command.
type RunCmd struct {
	flags *Flags
	deps  *Deps
}

// NewRunCmd creates the shim command.
func NewRunCmd(flags *Flags, deps *Deps) *RunCmd {
	return &RunCmd{flags: flags, deps: deps}
}

// Register adds the shim command to the application.
func (cmd *RunCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "_run",
		Usage:     "run one service; used inside tmux windows",
		ArgsUsage: "<project--stack> <service>",
		Hidden:    true,
		Action:    cmd.run,
	})
	return app
}

func (cmd *RunCmd) run(ctx context.Context, c *cli.Command) error {
	if c.Args().Len() != 2 {
		return errors.New("usage: svx _run <project--stack> <service>")
	}
	plan, err := cmd.deps.App.PlanRun(c.Args().Get(0), c.Args().Get(1), os.Getpid(), cmd.deps.App.Environ())
	if err != nil {
		return err
	}

	cmd.waitForSetup(ctx)

	st := newStyle(os.Stdout, cmd.flags.NoColor)
	_, _ = fmt.Fprintln(os.Stdout, st.dim(plan.Banner))

	if err := os.Chdir(plan.Dir); err != nil {
		return fmt.Errorf("service directory %s: %w", plan.Dir, err)
	}
	if plan.Retries == 0 {
		return syscall.Exec(plan.Argv[0], plan.Argv, plan.Env) //nolint:gosec // running the configured command is the point
	}

	// svx stops a service by signalling the pane's process group, which
	// holds the command too. The shim only has to outlive that signal and
	// not count the exit it causes as a crash.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT, plan.StopSignal)
	defer signal.Stop(stop)

	code := runWithRetries(ctx, os.Stdout, plan, stop, retryDelay)
	if code != 0 {
		return &app.CommandExitError{Code: code}
	}
	return nil
}

// runWithRetries runs plan's command and reruns it after a non-zero exit,
// up to plan.Retries times. It stops early once a signal arrives on stop.
// It returns the last exit code, 128 plus the signal for a killed command.
func runWithRetries(ctx context.Context, out io.Writer, plan app.RunPlan, stop <-chan os.Signal, delay time.Duration) int {
	stopped := false
	for attempt := 0; ; attempt++ {
		code, err := runOnce(plan, stop, &stopped)
		if err != nil {
			_, _ = fmt.Fprintf(out, "%s%v\n", domain.BannerPrefix, err)
			return 1
		}
		if code == 0 || stopped || attempt == plan.Retries {
			return code
		}

		_, _ = fmt.Fprintf(out, "%sexited with code %d, retry %d of %d in %s\n", domain.BannerPrefix, code, attempt+1, plan.Retries, delay)
		select {
		case <-stop:
			return code
		case <-ctx.Done():
			return code
		case <-time.After(delay):
		}
	}
}

func runOnce(plan app.RunPlan, stop <-chan os.Signal, stopped *bool) (int, error) {
	cmd := exec.Command(plan.Argv[0], plan.Argv[1:]...) //nolint:gosec,noctx // the configured command; it ends by signal, not by context
	cmd.Env = plan.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var err error
	for waiting := true; waiting; {
		select {
		case <-stop:
			*stopped = true
		case err = <-done:
			waiting = false
		}
	}

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, err
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	return exitErr.ExitCode(), nil
}

// waitForSetup holds the command back until svx has finished setting up
// the window: with remain-on-exit on and the log pipe open, a command that
// exits at once still leaves its status and first lines behind.
func (cmd *RunCmd) waitForSetup(ctx context.Context) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok, err := cmd.deps.App.Mux.PaneReady(ctx, pane); err != nil || ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
