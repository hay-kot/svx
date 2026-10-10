package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// InitCmd implements the init command.
type InitCmd struct {
	flags *Flags
	deps  *Deps
}

// NewInitCmd creates a new init command.
func NewInitCmd(flags *Flags, deps *Deps) *InitCmd {
	return &InitCmd{flags: flags, deps: deps}
}

// Register adds the init command to the application.
func (cmd *InitCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:   "init",
		Usage:  "write a starter svx.yaml and ignore .svx/ in .gitignore",
		Action: cmd.run,
	})
	return app
}

func (cmd *InitCmd) run(_ context.Context, _ *cli.Command) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	changed, err := cmd.deps.App.Init(dir)
	for _, p := range changed {
		_, _ = fmt.Fprintf(os.Stdout, "wrote %s\n", p)
	}
	return err
}
