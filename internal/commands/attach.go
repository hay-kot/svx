package commands

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
)

// AttachCmd implements the attach command.
type AttachCmd struct {
	flags *Flags
	deps  *Deps
	stack stackFlags
}

// NewAttachCmd creates a new attach command.
func NewAttachCmd(flags *Flags, deps *Deps) *AttachCmd {
	return &AttachCmd{flags: flags, deps: deps}
}

// Register adds the attach command to the application.
func (cmd *AttachCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "attach",
		Usage:     "attach to the stack's session, optionally selecting a service's window",
		ArgsUsage: "[service]",
		Flags:     cmd.stack.flags(),
		Action:    cmd.run,
	})
	return app
}

func (cmd *AttachCmd) run(ctx context.Context, c *cli.Command) error {
	return cmd.deps.App.Attach(ctx, cmd.stack.target(), c.Args().First())
}

// RestartCmd implements the restart command.
type RestartCmd struct {
	flags  *Flags
	deps   *Deps
	stack  stackFlags
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
		ArgsUsage: "<service...>",
		Flags: append(cmd.stack.flags(),
			&cli.BoolFlag{Name: "no-wait", Usage: "do not wait for readiness", Destination: &cmd.noWait},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *RestartCmd) run(ctx context.Context, c *cli.Command) error {
	a := withProgress(cmd.deps.App, os.Stderr, newStyle(os.Stderr, cmd.flags.NoColor))
	_, err := a.Restart(ctx, app.RestartRequest{Target: cmd.stack.target(), Services: c.Args().Slice(), NoWait: cmd.noWait})
	return err
}
