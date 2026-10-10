package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
)

// DownCmd implements the down command.
type DownCmd struct {
	flags *Flags
	deps  *Deps
	stack stackFlags
	all   bool
	purge bool
}

// NewDownCmd creates a new down command.
func NewDownCmd(flags *Flags, deps *Deps) *DownCmd {
	return &DownCmd{flags: flags, deps: deps}
}

// Register adds the down command to the application.
func (cmd *DownCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "down",
		Usage: "stop the stack",
		Flags: append(cmd.stack.flags(),
			&cli.BoolFlag{Name: "all", Usage: "stop every stack on this machine", Destination: &cmd.all},
			&cli.BoolFlag{Name: "purge", Usage: "delete the stack's data directory", Destination: &cmd.purge},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *DownCmd) run(ctx context.Context, _ *cli.Command) error {
	st := newStyle(os.Stdout, cmd.flags.NoColor)
	a, finish := withProgress(cmd.deps.App, os.Stderr, cmd.flags.NoColor)
	defer finish()

	result, err := a.Down(ctx, app.DownRequest{Target: cmd.stack.target(), All: cmd.all, Purge: cmd.purge})
	finish()
	for _, id := range result.Stopped {
		_, _ = fmt.Fprintf(os.Stdout, "%s %s down\n", st.green("✓"), id)
	}
	if err != nil {
		return err
	}
	if cmd.all && len(result.Stopped) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no stacks running")
	}
	return nil
}
