package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/hay-kot/svx/internal/paths"
)

// Config holds the user-level configuration in ~/.config/svx/config.yaml.
// Project config lives in each repo's svx.yaml instead.
type Config struct {
	LogLevel string        `yaml:"log_level"`
	LogFile  string        `yaml:"log_file"`
	MenuBar  MenuBarConfig `yaml:"menubar"`
}

// MenuBarConfig controls the macOS menu bar app.
type MenuBarConfig struct {
	// Enabled turns the menu bar on or off. Off, `svx bar` exits at once,
	// so an installed login item stays out of the menu bar too.
	Enabled bool `yaml:"enabled"`
	// Terminal is the app "Attach in Terminal" opens, by name.
	Terminal string `yaml:"terminal"`
}

// Default returns a Config with default values.
func Default() Config {
	return Config{
		LogLevel: "info",
		LogFile:  "",
		MenuBar:  MenuBarConfig{Enabled: true, Terminal: "Terminal"},
	}
}

// Read loads config from the default XDG config path.
// Returns default config if the file does not exist.
func Read() (Config, error) {
	configDir, err := paths.ConfigDir()
	if err != nil {
		return Default(), fmt.Errorf("resolve config directory: %w", err)
	}

	return ReadFrom(filepath.Join(configDir, "config.yaml"))
}

// ReadFrom loads config from the given file path.
// Returns default config if the file does not exist.
func ReadFrom(path string) (Config, error) {
	cfg := Default()

	// The config path comes from the application default or an explicit user flag.
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}

		return cfg, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config: %w", err)
	}

	return cfg, nil
}
