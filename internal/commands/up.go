package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
)

// UpCmd implements the up command.
type UpCmd struct {
	flags  *Flags
	deps   *Deps
	stack  stackFlags
	attach bool
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
			&cli.BoolFlag{Name: "attach", Usage: "attach to the session afterwards", Destination: &cmd.attach},
			&cli.BoolFlag{Name: "no-wait", Usage: "start windows in dependency order without waiting for readiness", Destination: &cmd.noWait},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *UpCmd) run(ctx context.Context, c *cli.Command) error {
	st := newStyle(os.Stdout, cmd.flags.NoColor)
	a := withProgress(cmd.deps.App, os.Stderr, newStyle(os.Stderr, cmd.flags.NoColor))

	result, err := a.Up(ctx, app.UpRequest{Target: cmd.stack.target(), Services: c.Args().Slice(), NoWait: cmd.noWait})
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
	if err != nil {
		return err
	}
	if cmd.attach {
		return cmd.deps.App.Attach(ctx, cmd.stack.target(), "")
	}
	return nil
}

// withProgress returns a copy of a that prints progress lines to w.
func withProgress(a *app.App, w io.Writer, st style) *app.App {
	cp := *a
	var mu sync.Mutex
	cp.Events = func(e app.Event) {
		mu.Lock()
		defer mu.Unlock()
		elapsed := ""
		if e.Elapsed >= 100*time.Millisecond {
			elapsed = st.dim(" (" + e.Elapsed.Round(100*time.Millisecond).String() + ")")
		}
		switch e.Kind {
		case app.EventStarting:
			_, _ = fmt.Fprintf(w, "%s %s starting\n", st.dim("-"), e.Service)
		case app.EventRunning:
			_, _ = fmt.Fprintf(w, "%s %s %s\n", st.green("✓"), e.Service, e.Detail)
		case app.EventReady:
			msg := "ready"
			if e.Detail != "" {
				msg = e.Detail
			}
			_, _ = fmt.Fprintf(w, "%s %s %s%s\n", st.green("✓"), e.Service, msg, elapsed)
		case app.EventFailed:
			_, _ = fmt.Fprintf(w, "%s %s %s%s\n", st.red("✗"), e.Service, e.Detail, elapsed)
		case app.EventSkipped:
			_, _ = fmt.Fprintf(w, "%s %s not started: %s\n", st.yellow("!"), e.Service, e.Detail)
		case app.EventStopping:
			_, _ = fmt.Fprintf(w, "%s %s stopping\n", st.dim("-"), e.Service)
		case app.EventStopped:
			if e.Detail != "" {
				_, _ = fmt.Fprintf(w, "%s %s %s\n", st.yellow("!"), e.Service, e.Detail)
			} else {
				_, _ = fmt.Fprintf(w, "%s %s stopped%s\n", st.green("✓"), e.Service, elapsed)
			}
		case app.EventWarning:
			_, _ = fmt.Fprintf(w, "%s %s\n", st.yellow("warning:"), e.Detail)
		}
	}
	return &cp
}
