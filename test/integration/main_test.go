//go:build integration

package integration

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

var cliBin string

func TestMain(m *testing.M) {
	if os.Getenv("CLI_INTEGRATION") != "1" {
		fmt.Fprintln(os.Stderr, "skipping integration tests: CLI_INTEGRATION=1 not set (run via mise run integration)")
		os.Exit(0)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		panic("tmux not found in PATH; the integration tests drive a real tmux")
	}

	cliBin = os.Getenv("SVX_BIN")
	if cliBin == "" {
		path, err := exec.LookPath("svx")
		if err != nil {
			panic("svx binary not found; set SVX_BIN or put it on PATH")
		}
		cliBin = path
	}

	os.Exit(m.Run())
}
