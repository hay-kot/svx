package commands

import (
	"os"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/config"
)

// Flags holds global flags shared across all commands.
type Flags struct {
	LogLevel string
	NoColor  bool

	LogFile string

	ConfigFile string

	JSON bool
}

// Deps is filled in by main's Before hook once the user config is loaded.
// Commands are registered before that, so they hold a pointer.
type Deps struct {
	App    *app.App
	Config config.Config
}

// stackFlags select a stack other than the current checkout's default.
type stackFlags struct {
	name    string
	project string
}

func (s *stackFlags) flags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:        "name",
			Usage:       "stack name, defaults to one derived from the checkout path",
			Sources:     cli.EnvVars("SVX_STACK"),
			Destination: &s.name,
		},
		&cli.StringFlag{
			Name:        "project",
			Usage:       "project name; with --name, acts on a stack without reading any config",
			Destination: &s.project,
		},
	}
}

func (s *stackFlags) target() app.Target {
	dir, _ := os.Getwd()
	return app.Target{Dir: dir, Project: s.project, Stack: s.name}
}
