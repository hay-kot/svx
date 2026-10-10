package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/adapters/menubar"
)

// BarCmd implements the bar command.
type BarCmd struct {
	flags     *Flags
	deps      *Deps
	install   bool
	uninstall bool
}

// NewBarCmd creates a new bar command.
func NewBarCmd(flags *Flags, deps *Deps) *BarCmd {
	return &BarCmd{flags: flags, deps: deps}
}

// Register adds the bar command to the application.
func (cmd *BarCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:  "bar",
		Usage: "run the macOS menu bar app, or install it to start at login",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "install", Usage: "start the menu bar now and at every login", Destination: &cmd.install},
			&cli.BoolFlag{Name: "uninstall", Usage: "stop the menu bar and remove the login item", Destination: &cmd.uninstall},
		},
		Action: cmd.run,
	})
	return app
}

func (cmd *BarCmd) run(ctx context.Context, _ *cli.Command) error {
	if cmd.uninstall {
		path, err := menubar.UninstallAgent(ctx)
		if err != nil {
			return err
		}
		if path == "" {
			_, _ = fmt.Fprintln(os.Stdout, "the menu bar login item was not installed")
			return nil
		}
		_, _ = fmt.Fprintf(os.Stdout, "removed %s\n", path)
		return nil
	}

	if !cmd.deps.Config.MenuBar.Enabled {
		if cmd.install {
			return errors.New("the menu bar is turned off; set menubar.enabled: true in ~/.config/svx/config.yaml first")
		}
		_, _ = fmt.Fprintln(os.Stderr, "the menu bar is turned off in ~/.config/svx/config.yaml")
		return nil
	}
	if !menubar.Supported {
		return menubar.ErrUnsupported
	}

	// Checked before --install too, or the login item would fail at every
	// launch.
	var attachURI *template.Template
	if text := cmd.deps.Config.MenuBar.AttachURI; text != "" {
		t, err := menubar.ParseAttachURI(text)
		if err != nil {
			return err
		}
		attachURI = t
	}

	if cmd.install {
		logPath := filepath.Join(cmd.deps.App.Store.Dir(), "menubar.log")
		plist := menubar.Plist(cmd.deps.App.Executable, logPath, menubar.AgentEnv(os.Getenv))
		path, err := menubar.InstallAgent(ctx, plist)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(os.Stdout, "installed %s, the menu bar is running\n", path)
		return nil
	}

	bar := &menubar.Bar{
		App:        cmd.deps.App,
		Executable: cmd.deps.App.Executable,
		Terminal:   cmd.deps.Config.MenuBar.Terminal,
		AttachURI:  attachURI,
	}
	return bar.Run(ctx)
}
