package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// StatusCmd implements the status command. Bare `svx` runs it too.
type StatusCmd struct {
	flags *Flags
	deps  *Deps
	stack stackFlags
}

// NewStatusCmd creates a new status command.
func NewStatusCmd(flags *Flags, deps *Deps) *StatusCmd {
	return &StatusCmd{flags: flags, deps: deps}
}

// Register adds the status command to the application.
func (cmd *StatusCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:   "status",
		Usage:  "show services, state, ports and links for this stack",
		Flags:  cmd.stack.flags(),
		Action: cmd.Run,
	})
	return app
}

// Run prints the stack's status.
func (cmd *StatusCmd) Run(ctx context.Context, _ *cli.Command) error {
	if cmd.stack.name == "" {
		// Bare `svx` reaches here without the status flags being parsed.
		cmd.stack.name = os.Getenv("SVX_STACK")
	}
	view, err := cmd.deps.App.Status(ctx, cmd.stack.target())
	if err != nil {
		return err
	}
	if cmd.flags.JSON {
		return writeJSON(os.Stdout, toJSONStack(view))
	}
	printStack(os.Stdout, newStyle(os.Stdout, cmd.flags.NoColor), view)
	return nil
}

// LsCmd implements the ls command.
type LsCmd struct {
	flags *Flags
	deps  *Deps
}

// NewLsCmd creates a new ls command.
func NewLsCmd(flags *Flags, deps *Deps) *LsCmd {
	return &LsCmd{flags: flags, deps: deps}
}

// Register adds the ls command to the application.
func (cmd *LsCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:    "ls",
		Aliases: []string{"list"},
		Usage:   "list every stack on this machine",
		Action:  cmd.run,
	})
	return app
}

func (cmd *LsCmd) run(ctx context.Context, _ *cli.Command) error {
	views, err := cmd.deps.App.List(ctx)
	if err != nil {
		return err
	}
	if cmd.flags.JSON {
		stacks := make([]jsonStack, 0, len(views))
		for _, v := range views {
			stacks = append(stacks, toJSONStack(v))
		}
		return writeJSON(os.Stdout, map[string]any{"version": 1, "stacks": stacks})
	}

	if len(views) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no stacks on record, start one with `svx up`")
		return nil
	}
	st := newStyle(os.Stdout, cmd.flags.NoColor)
	tw := table(os.Stdout)
	_, _ = fmt.Fprintln(tw, st.dim("PROJECT\tSTACK\tSTATUS\tSLOT\tSERVICES\tROOT"))
	for _, v := range views {
		s := v.Stack
		status := st.dim(string(s.Status))
		services := "-"
		if s.Status == "up" {
			status = st.green(string(s.Status))
			services = fmt.Sprintf("%d/%d running", v.Running(), len(v.Services))
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", s.ID.Project, s.ID.Stack, status, s.Slot, services, s.Root)
	}
	return tw.Flush()
}
