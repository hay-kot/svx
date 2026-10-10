package commands

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/domain"
)

// EnvCmd implements the env command.
type EnvCmd struct {
	flags   *Flags
	deps    *Deps
	stack   stackFlags
	format  string
	service string
}

// NewEnvCmd creates a new env command.
func NewEnvCmd(flags *Flags, deps *Deps) *EnvCmd {
	return &EnvCmd{flags: flags, deps: deps}
}

// Register adds the env command to the application.
func (cmd *EnvCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "env",
		Usage: "print the stack's variables",
		Flags: append(cmd.stack.flags(),
			&cli.StringFlag{Name: "format", Usage: "shell, dotenv or json", Value: "shell", Destination: &cmd.format},
			&cli.StringFlag{Name: "service", Usage: "print one service's view, with PORT and its own env", Destination: &cmd.service},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *EnvCmd) run(ctx context.Context, _ *cli.Command) error {
	env, err := cmd.deps.App.Env(ctx, cmd.stack.target(), cmd.service)
	if err != nil {
		return err
	}
	format := cmd.format
	if cmd.flags.JSON {
		format = "json"
	}
	switch format {
	case "json":
		return writeJSON(os.Stdout, env)
	case "shell":
		for _, k := range slices.Sorted(maps.Keys(env)) {
			_, _ = fmt.Fprintf(os.Stdout, "export %s=%s\n", k, shellQuote(env[k]))
		}
	case "dotenv":
		for _, k := range slices.Sorted(maps.Keys(env)) {
			_, _ = fmt.Fprintf(os.Stdout, "%s=%s\n", k, dotenvQuote(env[k]))
		}
	default:
		return fmt.Errorf("unknown --format %q, use shell, dotenv or json", cmd.format)
	}
	return nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func dotenvQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "$", `\$`)
	return `"` + r.Replace(s) + `"`
}

// ExecCmd implements the exec command.
type ExecCmd struct {
	flags   *Flags
	deps    *Deps
	stack   stackFlags
	service string
}

// NewExecCmd creates a new exec command.
func NewExecCmd(flags *Flags, deps *Deps) *ExecCmd {
	return &ExecCmd{flags: flags, deps: deps}
}

// Register adds the exec command to the application.
func (cmd *ExecCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "exec",
		Usage:     "run a command with the stack's environment",
		ArgsUsage: "-- <cmd> [args...]",
		Flags: append(cmd.stack.flags(),
			&cli.StringFlag{Name: "service", Usage: "use one service's environment", Destination: &cmd.service},
		),
		Action: cmd.run,
	})
	return app
}

// run replaces svx with the command, so its exit code and signals pass
// straight through.
func (cmd *ExecCmd) run(ctx context.Context, c *cli.Command) error {
	args := c.Args().Slice()
	if len(args) == 0 {
		return errors.New("name a command: svx exec -- <cmd> [args...]")
	}
	overlay, err := cmd.deps.App.Env(ctx, cmd.stack.target(), cmd.service)
	if err != nil {
		return err
	}
	path, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("%s: %w", args[0], err)
	}
	env := domain.ProcessEnv(cmd.deps.App.Environ(), overlay, nil)
	return syscall.Exec(path, args, env) //nolint:gosec // running the user's command is the point
}
