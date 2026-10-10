package commands

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

// OpenCmd implements the open command.
type OpenCmd struct {
	flags *Flags
	deps  *Deps
	stack stackFlags
}

// NewOpenCmd creates a new open command.
func NewOpenCmd(flags *Flags, deps *Deps) *OpenCmd {
	return &OpenCmd{flags: flags, deps: deps}
}

// Register adds the open command to the application.
func (cmd *OpenCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "open",
		Usage:     "open a link in the browser, or list links",
		ArgsUsage: "[link]",
		Flags:     cmd.stack.flags(),
		Action:    cmd.run,
	})
	return app
}

func (cmd *OpenCmd) run(ctx context.Context, c *cli.Command) error {
	label := c.Args().First()
	if label != "" {
		link, err := cmd.deps.App.Open(ctx, cmd.stack.target(), label)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(os.Stdout, "opened %s\n", link.URL)
		return nil
	}

	links, err := cmd.deps.App.Links(ctx, cmd.stack.target())
	if err != nil {
		return err
	}
	if cmd.flags.JSON {
		out := make([]jsonLink, 0, len(links))
		for _, l := range links {
			out = append(out, jsonLink(l))
		}
		return writeJSON(os.Stdout, out)
	}
	if len(links) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no links, add `links` to a service in svx.yaml")
		return nil
	}
	tw := table(os.Stdout)
	for _, l := range links {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", l.Label, l.URL)
	}
	return tw.Flush()
}
