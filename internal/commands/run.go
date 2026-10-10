package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"
)

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
	plan, err := cmd.deps.App.PlanRun(c.Args().Get(0), c.Args().Get(1), cmd.deps.App.Environ())
	if err != nil {
		return err
	}

	cmd.waitForSetup(ctx)

	st := newStyle(os.Stdout, cmd.flags.NoColor)
	_, _ = fmt.Fprintln(os.Stdout, st.dim(plan.Banner))

	if err := os.Chdir(plan.Dir); err != nil {
		return fmt.Errorf("service directory %s: %w", plan.Dir, err)
	}
	return syscall.Exec(plan.Argv[0], plan.Argv, plan.Env) //nolint:gosec // running the configured command is the point
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
