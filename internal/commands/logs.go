package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/domain"
)

// LogsCmd implements the logs command.
type LogsCmd struct {
	flags  *Flags
	deps   *Deps
	stack  stackFlags
	follow bool
	raw    bool
	lines  int
}

// NewLogsCmd creates a new logs command.
func NewLogsCmd(flags *Flags, deps *Deps) *LogsCmd {
	return &LogsCmd{flags: flags, deps: deps}
}

// Register adds the logs command to the application.
func (cmd *LogsCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:      "logs",
		Usage:     "print a service's log",
		ArgsUsage: "<service>",
		Flags: append(cmd.stack.flags(),
			&cli.BoolFlag{Name: "follow", Aliases: []string{"f"}, Usage: "keep printing as the log grows", Destination: &cmd.follow},
			&cli.BoolFlag{Name: "raw", Usage: "keep colour codes", Destination: &cmd.raw},
			&cli.IntFlag{Name: "lines", Aliases: []string{"n"}, Usage: "print only the last n lines, 0 for all", Value: 0, Destination: &cmd.lines},
		),
		Action: cmd.run,
	})
	return app
}

func (cmd *LogsCmd) run(ctx context.Context, c *cli.Command) error {
	service := c.Args().First()
	if service == "" {
		return errors.New("name a service: svx logs <service>")
	}
	path, err := cmd.deps.App.LogFile(ctx, cmd.stack.target(), service)
	if err != nil {
		return err
	}

	f, err := os.Open(path) //nolint:gosec // path comes from the stack record
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no log yet at %s; has %s started?", path, service)
		}
		return err
	}
	defer func() { _ = f.Close() }()

	out := bufio.NewWriter(os.Stdout)
	if err := cmd.copyTail(f, out); err != nil {
		return err
	}
	if err := out.Flush(); err != nil || !cmd.follow {
		return err
	}

	reader := bufio.NewReader(f)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			_, _ = io.WriteString(os.Stdout, cmd.clean(line))
		}
		if errors.Is(err, io.EOF) {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(250 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return err
		}
	}
}

func (cmd *LogsCmd) copyTail(f *os.File, w io.Writer) error {
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	text := string(data)
	if cmd.lines > 0 {
		lines := strings.SplitAfter(text, "\n")
		if lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) > cmd.lines {
			lines = lines[len(lines)-cmd.lines:]
		}
		text = strings.Join(lines, "")
	}
	_, err = io.WriteString(w, cmd.clean(text))
	return err
}

// clean strips colour codes and the carriage returns that pipe-pane keeps
// from the terminal.
func (cmd *LogsCmd) clean(s string) string {
	if cmd.raw {
		return s
	}
	return strings.ReplaceAll(domain.StripANSI(s), "\r", "")
}
