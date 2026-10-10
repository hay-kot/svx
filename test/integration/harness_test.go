//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const commandTimeout = 90 * time.Second

// Harness runs the built CLI with isolated state: its own XDG directories,
// its own state directory, and a private tmux server that cleanup kills. It
// never touches the developer's tmux server or real state.
type Harness struct {
	t         *testing.T
	homeDir   string
	dataDir   string
	cacheDir  string
	configDir string
	stateDir  string
	socket    string
}

// NewHarness creates an integration test harness.
func NewHarness(t *testing.T) *Harness {
	t.Helper()
	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	h := &Harness{
		t:         t,
		homeDir:   t.TempDir(),
		dataDir:   t.TempDir(),
		cacheDir:  t.TempDir(),
		configDir: t.TempDir(),
		stateDir:  t.TempDir(),
		socket:    "svx-test-" + hex.EncodeToString(buf),
	}
	t.Cleanup(func() {
		// tmux leaves its socket file behind when the server exits.
		socket, _ := exec.Command("tmux", "-L", h.socket, "display-message", "-p", "#{socket_path}").Output() //nolint:gosec,noctx // cleanup runs after the test context ends
		_ = exec.Command("tmux", "-L", h.socket, "kill-server").Run()                                         //nolint:gosec,noctx // cleanup runs after the test context ends
		if path := strings.TrimSpace(string(socket)); path != "" {
			_ = os.Remove(path)
		}
	})
	return h
}

// Run executes the CLI from dir and returns combined stdout/stderr output.
func (h *Harness) Run(dir string, args ...string) (string, error) {
	h.t.Helper()
	cmd := h.command(args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Stdout executes the CLI from dir and returns stdout only.
func (h *Harness) Stdout(dir string, args ...string) (string, error) {
	h.t.Helper()
	cmd := h.command(args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// Tmux runs tmux against the harness's private server.
func (h *Harness) Tmux(args ...string) (string, error) {
	out, err := exec.CommandContext(h.t.Context(), "tmux", append([]string{"-L", h.socket}, args...)...).CombinedOutput() //nolint:gosec // the harness private socket
	return string(out), err
}

// CopySample copies testdata/sample into a new directory.
func (h *Harness) CopySample(name string) string {
	h.t.Helper()
	dst := filepath.Join(h.t.TempDir(), name)
	if err := os.CopyFS(dst, os.DirFS("../../testdata/sample")); err != nil {
		h.t.Fatal(err)
	}
	return dst
}

func (h *Harness) command(args ...string) *exec.Cmd {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	h.t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, cliBin, args...) //nolint:gosec // the binary under test
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"TERM=" + os.Getenv("TERM"),
		"HOME=" + h.homeDir,
		"XDG_DATA_HOME=" + h.dataDir,
		"XDG_CACHE_HOME=" + h.cacheDir,
		"XDG_CONFIG_HOME=" + h.configDir,
		"SVX_STATE_DIR=" + h.stateDir,
		"SVX_TMUX_SOCKET=" + h.socket,
		"SVX_TMUX_CONFIG=/dev/null",
		"LOG_LEVEL=info",
		"LOG_FILE=" + filepath.Join(h.dataDir, "svx.log"),
		"NO_COLOR=1",
	}
	return cmd
}
