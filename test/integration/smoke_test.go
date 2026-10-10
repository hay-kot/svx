//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestSmokeHelp(t *testing.T) {
	h := NewHarness(t)
	out, err := h.Run("help")
	if err != nil {
		t.Fatalf("help command failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "svx") {
		t.Fatalf("expected help output to mention svx, got:\n%s", out)
	}
}

func TestSmokeVersion(t *testing.T) {
	h := NewHarness(t)
	out, err := h.Run("--version")
	if err != nil {
		t.Fatalf("version command failed: %v\noutput: %s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected version output, got empty string")
	}
}

func TestSmokeCommand(t *testing.T) {
	h := NewHarness(t)
	out, err := h.Run("up")
	if err != nil {
		t.Fatalf("command failed: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Hello World!") {
		t.Fatalf("expected command output to contain Hello World!, got:\n%s", out)
	}
}
