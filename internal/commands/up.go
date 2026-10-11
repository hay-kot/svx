package commands

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
)

// UpCmd implements the up command.
type UpCmd struct {
	flags  *Flags
	deps   *Deps
	stack  stackFlags
	noWait bool
}

// NewUpCmd creates a new up command.
func NewUpCmd(flags *Flags, deps *Deps) *UpCmd {
	return &UpCmd{flags: flags, deps: deps}
}

// Register adds the up command to the application.
func (cmd *UpCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "up",
		Usage:     "start the stack, or the named services plus their dependencies",
		ArgsUsage: "[service...]",
		Flags: append(cmd.stack.flags(),
			&cli.BoolFlag{Name: "no-wait", Usage: "start windows in dependency order without waiting for readiness", Destination: &cmd.noWait},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *UpCmd) run(ctx context.Context, c *cli.Command) error {
	st := newStyle(os.Stdout, cmd.flags.NoColor)
	a, finish := withProgress(cmd.deps.App, os.Stderr, cmd.flags.NoColor)
	defer finish()

	result, err := a.Up(ctx, app.UpRequest{Target: cmd.stack.target(), Services: c.Args().Slice(), NoWait: cmd.noWait})
	finish()
	if result.Stack.ID.Project == "" {
		return err
	}

	if len(result.Moved) > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "%s previous slot was taken, moved to slot %d: %s\n",
			st.yellow("!"), result.Stack.Slot, strings.Join(result.Moved, ", "))
	}

	view, verr := cmd.deps.App.Status(ctx, cmd.stack.target())
	if verr == nil {
		if cmd.flags.JSON {
			_ = writeJSON(os.Stdout, toJSONStack(view))
		} else {
			_, _ = fmt.Fprintln(os.Stdout)
			printStack(os.Stdout, st, view)
		}
	}
	return err
}
