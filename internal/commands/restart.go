package commands

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
)

// RestartCmd implements the restart command.
type RestartCmd struct {
	flags  *Flags
	deps   *Deps
	stack  stackFlags
	all    bool
	noWait bool
}

// NewRestartCmd creates a new restart command.
func NewRestartCmd(flags *Flags, deps *Deps) *RestartCmd {
	return &RestartCmd{flags: flags, deps: deps}
}

// Register adds the restart command to the application.
func (cmd *RestartCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "restart",
		Usage:     "restart services in place, keeping their ports",
		ArgsUsage: "[service...]",
		Flags: append(cmd.stack.flags(),
			&cli.BoolFlag{Name: "all", Usage: "restart every started service in dependency order", Destination: &cmd.all},
			&cli.BoolFlag{Name: "no-wait", Usage: "do not wait for readiness", Destination: &cmd.noWait},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *RestartCmd) run(ctx context.Context, c *cli.Command) error {
	a, finish := withProgress(cmd.deps.App, os.Stderr, cmd.flags.NoColor)
	defer finish()
	_, err := a.Restart(ctx, app.RestartRequest{Target: cmd.stack.target(), Services: c.Args().Slice(), All: cmd.all, NoWait: cmd.noWait})
	return err
}
