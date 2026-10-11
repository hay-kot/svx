package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/adapters/readiness"
	"github.com/hay-kot/svx/internal/adapters/statefs"
	"github.com/hay-kot/svx/internal/adapters/system"
	"github.com/hay-kot/svx/internal/adapters/tmux"
	"github.com/hay-kot/svx/internal/adapters/yamlproject"
	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/commands"
	"github.com/hay-kot/svx/internal/config"
	"github.com/hay-kot/svx/internal/paths"
)

var (
	// Build information. Populated at build-time via -ldflags flag.
	version = "dev"
	commit  = "HEAD"
	date    = "now"
)

func build() string {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok {
			version = info.Main.Version
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					commit = s.Value
				case "vcs.time":
					date = s.Value
				}
			}
		}
	}

	short := commit
	if len(commit) > 7 {
		short = commit[:7]
	}

	return fmt.Sprintf("%s (%s) %s", version, short, date)
}

func setupLogger(level, logFile string, noColor bool) error {
	parsedLevel, err := zerolog.ParseLevel(level)
	if err != nil {
		return fmt.Errorf("failed to parse log level: %w", err)
	}

	var output io.Writer = zerolog.ConsoleWriter{Out: os.Stderr, NoColor: noColor}

	if logFile != "" {
		logDir := filepath.Dir(logFile)
		if err := os.MkdirAll(logDir, 0o750); err != nil {
			return fmt.Errorf("failed to create log directory: %w", err)
		}

		// The log path comes from the application default, config, or an explicit user flag.
		file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}

		output = io.MultiWriter(
			zerolog.ConsoleWriter{Out: os.Stderr, NoColor: noColor},
			file,
		)
	}

	log.Logger = log.Output(output).Level(parsedLevel)

	return nil
}

func main() {
	os.Exit(run())
}

func run() int {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	flags := &commands.Flags{}
	deps := &commands.Deps{}
	status := commands.NewStatusCmd(flags, deps)

	root := &cli.Command{
		Name:                  "svx",
		Usage:                 "run a repo's dev services as windows in a tmux session, one session per stack",
		Version:               build(),
		EnableShellCompletion: true,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "log-level",
				Usage:       "log level (debug, info, warn, error, fatal, panic)",
				Sources:     cli.EnvVars("LOG_LEVEL"),
				Value:       "info",
				Destination: &flags.LogLevel,
			},
			&cli.BoolFlag{
				Name:        "no-color",
				Usage:       "disable colored output",
				Sources:     cli.EnvVars("NO_COLOR"),
				Destination: &flags.NoColor,
			},
			&cli.StringFlag{
				Name:        "log-file",
				Usage:       "path to log file (optional)",
				Sources:     cli.EnvVars("LOG_FILE"),
				Destination: &flags.LogFile,
			},
			&cli.StringFlag{
				Name:        "config",
				Usage:       "path to config file",
				Sources:     cli.EnvVars("CONFIG_FILE"),
				Destination: &flags.ConfigFile,
			},
			&cli.BoolFlag{
				Name:        "json",
				Usage:       "output in JSON format",
				Destination: &flags.JSON,
			},
		},
		Before: func(ctx context.Context, c *cli.Command) (context.Context, error) {
			cfg, err := func() (config.Config, error) {
				if flags.ConfigFile != "" {
					return config.ReadFrom(flags.ConfigFile)
				}
				return config.Read()
			}()
			if err != nil {
				return ctx, fmt.Errorf("loading config: %w", err)
			}

			if !c.IsSet("log-level") && cfg.LogLevel != "" {
				flags.LogLevel = cfg.LogLevel
			}
			if !c.IsSet("log-file") && cfg.LogFile != "" {
				flags.LogFile = cfg.LogFile
			}
			logFile := flags.LogFile
			if logFile == "" {
				dataDir, err := paths.DataDir()
				if err != nil {
					return ctx, fmt.Errorf("resolve data directory: %w", err)
				}
				logFile = filepath.Join(dataDir, "svx.log")
			}

			if err := setupLogger(flags.LogLevel, logFile, flags.NoColor); err != nil {
				return ctx, err
			}

			return ctx, wire(deps, cfg)
		},
		Action: status.Run,
	}
	root = commands.NewUpCmd(flags, deps).Register(root)
	root = commands.NewDownCmd(flags, deps).Register(root)
	root = status.Register(root)
	root = commands.NewLsCmd(flags, deps).Register(root)
	root = commands.NewRestartCmd(flags, deps).Register(root)
	root = commands.NewLogsCmd(flags, deps).Register(root)
	root = commands.NewEnvCmd(flags, deps).Register(root)
	root = commands.NewExecCmd(flags, deps).Register(root)
	root = commands.NewConfigCmd(flags, deps).Register(root)
	root = commands.NewBarCmd(flags, deps).Register(root)
	root = commands.NewInitCmd(flags, deps).Register(root)
	root = commands.NewDoctorCmd(flags, deps).Register(root)
	root = commands.NewGuideCmd().Register(root)
	root = commands.NewRunCmd(flags, deps).Register(root)
	// +scaffold:command:register

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := root.Run(ctx, os.Args); err != nil {
		if _, ok := errors.AsType[*app.CommandExitError](err); ok {
			return app.ExitCode(err)
		}
		colorRed := "\033[38;2;215;95;107m"
		colorGray := "\033[38;2;163;163;163m"
		colorReset := "\033[0m"
		if flags.NoColor {
			colorRed = ""
			colorGray = ""
			colorReset = ""
		}
		fmt.Fprintf(os.Stderr, "\n%s╭ Error%s\n%s│%s %s%s%s\n%s╵%s\n",
			colorRed, colorReset,
			colorRed, colorReset, colorGray, err.Error(), colorReset,
			colorRed, colorReset,
		)
		return app.ExitCode(err)
	}

	return 0
}

// wire builds the adapters and the use cases on top of them.
func wire(deps *commands.Deps, cfg config.Config) error {
	stateDir, err := paths.StateDir()
	if err != nil {
		return fmt.Errorf("resolve state directory: %w", err)
	}
	store, err := statefs.New(stateDir)
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding svx's own path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	mux := tmux.FromEnv()
	deps.Config = cfg
	deps.App = &app.App{
		Projects:   yamlproject.Repository{Projects: cfg.Projects},
		Store:      store,
		Mux:        mux,
		Procs:      system.Processes{},
		Ports:      system.Ports{},
		Ready:      readiness.New(),
		Files:      system.Files{},
		Browser:    system.Browser{},
		Secrets:    system.ResolveSecret,
		Environ:    environ,
		Now:        time.Now,
		Executable: exe,
		Poll:       0,
		Events:     nil,
	}
	return nil
}

func environ() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}
