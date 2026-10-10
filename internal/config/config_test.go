package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFromMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := ReadFrom(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}

	want := Default()
	if cfg != want {
		t.Fatalf("ReadFrom() = %#v, want %#v", cfg, want)
	}
}

func TestReadFromIgnoresUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte("log_level: debug\nplugin_setting: enabled\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ReadFrom(path)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("ReadFrom().LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}
