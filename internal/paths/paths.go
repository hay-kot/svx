package paths

import (
	"fmt"
	"os"
	"path/filepath"
)

const appName = "svx"

// ConfigDir returns the XDG config directory for the application.
func ConfigDir() (string, error) {
	return appDir("XDG_CONFIG_HOME", ".config")
}

// DataDir returns the XDG data directory for the application.
func DataDir() (string, error) {
	return appDir("XDG_DATA_HOME", ".local", "share")
}

func appDir(envName string, fallbackParts ...string) (string, error) {
	if dir := os.Getenv(envName); dir != "" {
		return filepath.Join(dir, appName), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	parts := append([]string{home}, fallbackParts...)
	return filepath.Join(append(parts, appName)...), nil
}

// StateDir returns svx's state directory: SVX_STATE_DIR when set, else
// $XDG_STATE_HOME/svx, else ~/.local/state/svx. macOS uses the same default
// as Linux, so other tools find the records in one place.
func StateDir() (string, error) {
	if dir := os.Getenv("SVX_STATE_DIR"); dir != "" {
		return dir, nil
	}
	return appDir("XDG_STATE_HOME", ".local", "state")
}
