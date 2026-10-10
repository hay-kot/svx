package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadFromMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := ReadFrom(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}

	want := Default()
	if !reflect.DeepEqual(cfg, want) {
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

func TestReadFromMenuBarOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("menubar:\n  enabled: false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ReadFrom(path)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	if cfg.MenuBar.Enabled {
		t.Fatal("ReadFrom().MenuBar.Enabled = true, want false")
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("ReadFrom().LogLevel = %q, want default", cfg.LogLevel)
	}
}

func TestDefaultEnablesMenuBar(t *testing.T) {
	if !Default().MenuBar.Enabled {
		t.Fatal("menu bar should default to on")
	}
}

func TestReadFromResolvesProjectPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte(`projects:
  "shop-*": svx/shop.yaml
  "~/code/api": ~/dotfiles/api.yaml
  "/abs/web": /etc/svx/web.yaml
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := ReadFrom(path)
	if err != nil {
		t.Fatalf("ReadFrom() error = %v", err)
	}
	want := map[string]string{
		"shop-*":                           filepath.Join(dir, "svx", "shop.yaml"),
		filepath.Join(home, "code", "api"): filepath.Join(home, "dotfiles", "api.yaml"),
		"/abs/web":                         "/etc/svx/web.yaml",
	}
	if !reflect.DeepEqual(cfg.Projects, want) {
		t.Fatalf("Projects = %#v, want %#v", cfg.Projects, want)
	}
}
