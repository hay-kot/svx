//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const commandTimeout = 30 * time.Second

// Harness runs the built CLI binary with isolated per-test state.
type Harness struct {
	t         *testing.T
	homeDir   string
	dataDir   string
	cacheDir  string
	configDir string
}

// NewHarness creates an integration test harness with isolated XDG directories.
func NewHarness(t *testing.T) *Harness {
	t.Helper()

	return &Harness{
		t:         t,
		homeDir:   t.TempDir(),
		dataDir:   t.TempDir(),
		cacheDir:  t.TempDir(),
		configDir: t.TempDir(),
	}
}

// Run executes the CLI and returns combined stdout/stderr output.
func (h *Harness) Run(args ...string) (string, error) {
	h.t.Helper()
	cmd := h.command(args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunInDir executes the CLI from dir and returns combined stdout/stderr output.
func (h *Harness) RunInDir(dir string, args ...string) (string, error) {
	h.t.Helper()
	cmd := h.command(args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunStdout executes the CLI and returns stdout only.
func (h *Harness) RunStdout(args ...string) (string, error) {
	h.t.Helper()
	cmd := h.command(args...)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			h.t.Logf("stderr from svx %v: %s", args, exitErr.Stderr)
		}
	}
	return string(out), err
}

// RunWithStdin executes the CLI with stdin and returns stdout only.
func (h *Harness) RunWithStdin(input string, args ...string) (string, error) {
	h.t.Helper()
	cmd := h.command(args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			h.t.Logf("stderr from svx %v: %s", args, exitErr.Stderr)
		}
	}
	return string(out), err
}

func (h *Harness) command(args ...string) *exec.Cmd {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	h.t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, cliBin, args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"TERM=" + os.Getenv("TERM"),
		"HOME=" + h.homeDir,
		"XDG_DATA_HOME=" + h.dataDir,
		"XDG_CACHE_HOME=" + h.cacheDir,
		"XDG_CONFIG_HOME=" + h.configDir,
		"LOG_LEVEL=debug",
		"LOG_FILE=" + filepath.Join(h.dataDir, "svx.log"),
		"NO_COLOR=1",
	}
	return cmd
}
