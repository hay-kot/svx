//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestSmokeHelp(t *testing.T) {
	h := NewHarness(t)
	out, err := h.Run(t.TempDir(), "help")
	if err != nil {
		t.Fatalf("help command failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "svx") {
		t.Fatalf("expected help output to mention svx, got:\n%s", out)
	}
}

func TestSmokeVersion(t *testing.T) {
	h := NewHarness(t)
	out, err := h.Run(t.TempDir(), "--version")
	if err != nil {
		t.Fatalf("version command failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected version output, got empty string")
	}
}

func TestNoConfigExitsTwo(t *testing.T) {
	h := NewHarness(t)
	out, err := h.Run(t.TempDir(), "up")
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit code = %d, want 2\n%s", code, out)
	}
	if !strings.Contains(out, "svx init") {
		t.Fatalf("error should point at svx init:\n%s", out)
	}
}
