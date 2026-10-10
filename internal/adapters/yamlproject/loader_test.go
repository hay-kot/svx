package yamlproject

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

const shopYAML = `
project: shop
settings:
  port_stride: 20
vars:
  db_name: 'shop_{{ snake .stack.name }}'
env:
  REDIS_URL: 'redis://127.0.0.1:{{ port "redis" }}'
services:
  web:
    run: npm run dev -- --port {{ .port }}
    dir: web
    port: 5173
    ports: { hmr: 24678, debug: auto }
    depends: [api]
    links: { App: /, Docs: /docs }
  redis:
    run: redis-server --port {{ .port }}
    port: 6379
  api:
    run: go run ./cmd/api
    port: 8080
    depends: [redis]
    ready: { http: /healthz, timeout: 30s }
    stop_signal: SIGINT
    stop_timeout: 3s
  worker:
    run: go run ./cmd/worker
    autostart: false
`

func TestLoadWalksUpAndKeepsOrder(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "svx.yaml"), shopYAML)
	sub := filepath.Join(root, "web", "src")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	proj, err := Repository{}.Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Root != root {
		t.Fatalf("root = %q, want %q", proj.Root, root)
	}

	def := proj.Def
	if got := strings.Join(def.ServiceNames(), ","); got != "web,redis,api,worker" {
		t.Fatalf("order = %s", got)
	}
	if def.Project != "shop" || def.Settings.PortStride != 20 || def.Settings.MaxSlots != domain.DefaultMaxSlots || !def.Settings.Logs {
		t.Fatalf("settings = %+v", def.Settings)
	}

	web, _ := def.Service("web")
	if web.Port != domain.FixedPort(5173) || web.Ports["hmr"] != domain.FixedPort(24678) || web.Ports["debug"] != domain.AutoPort() {
		t.Fatalf("web ports = %+v %+v", web.Port, web.Ports)
	}
	if len(web.Links) != 2 || web.Links[0].Label != "App" || web.Links[1].Label != "Docs" {
		t.Fatalf("links = %+v", web.Links)
	}

	api, _ := def.Service("api")
	if api.Ready.Kind != domain.ReadyHTTP || api.Ready.Value != "/healthz" || api.Ready.Timeout != 30*time.Second {
		t.Fatalf("ready = %+v", api.Ready)
	}
	if api.Stop.Signal != "SIGINT" || api.Stop.Timeout != 3*time.Second {
		t.Fatalf("stop = %+v", api.Stop)
	}

	worker, _ := def.Service("worker")
	if worker.Autostart {
		t.Fatal("worker autostart should be false")
	}
	if _, err := domain.Validate(def); err != nil {
		t.Fatalf("sample should validate: %v", err)
	}
}

func TestLocalOverrideMerges(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "svx.yaml"), shopYAML)
	write(t, filepath.Join(root, "svx.local.yaml"), `
services:
  api:
    port: 9090
    env: { DEBUG: "1" }
  extra:
    run: echo hi
`)
	proj, err := Repository{}.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	api, _ := proj.Def.Service("api")
	if api.Port.Base != 9090 || api.Run != "go run ./cmd/api" || api.Env["DEBUG"] != "1" {
		t.Fatalf("api = %+v", api)
	}
	if got := strings.Join(proj.Def.ServiceNames(), ","); got != "web,redis,api,worker,extra" {
		t.Fatalf("order = %s", got)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{"unknown key", "services:\n  web:\n    run: x\n    bogus: 1\n", `line 4: unknown key "bogus"`},
		{"bad port", "services:\n  web:\n    run: x\n    port: nine\n", `a port is a number or "auto"`},
		{"two ready kinds", "services:\n  web:\n    run: x\n    ready: { http: /, cmd: true }\n", "set exactly one of"},
		{"bad duration", "services:\n  web:\n    run: x\n    stop_timeout: soon\n", "not a duration"},
		{"not a mapping", "- a\n- b\n", "top level must be a mapping"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "svx.yaml"), tt.yaml)
			_, err := Repository{}.Load(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), "svx.yaml") {
				t.Fatalf("error does not name the file: %v", err)
			}
		})
	}
}

func TestLoadUnknownKeyInLocalNamesLocalFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "svx.yaml"), shopYAML)
	write(t, filepath.Join(root, "svx.local.yaml"), "nope: 1\n")
	_, err := Repository{}.Load(root)
	if err == nil || !strings.Contains(err.Error(), "svx.local.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Repository{}.Load(t.TempDir())
	if !errors.Is(err, app.ErrNoProject) || app.ExitCode(err) != app.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckoutNameFollowsWorktrees(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "shop")
	if err := os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature"), 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(main, ".git", "worktrees", "feature", "commondir"), "../..\n")

	wt := filepath.Join(base, "shop-feature")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+filepath.Join(main, ".git", "worktrees", "feature")+"\n")

	if got := checkoutName(main); got != "shop" {
		t.Errorf("main checkout name = %q", got)
	}
	if got := checkoutName(wt); got != "shop" {
		t.Errorf("worktree checkout name = %q", got)
	}
	if got := checkoutName(t.TempDir()); got == "" {
		t.Error("non-git dir should fall back to its own name")
	}
}

func TestInit(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".gitignore"), "node_modules")
	changed, err := Repository{}.Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 2 {
		t.Fatalf("changed = %v", changed)
	}
	ignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore")) //nolint:gosec // test temp file
	if string(ignore) != "node_modules\n.svx/\nsvx.local.yaml\n" {
		t.Fatalf(".gitignore = %q", ignore)
	}

	proj, err := Repository{}.Load(dir)
	if err != nil {
		t.Fatalf("starter config does not load: %v", err)
	}
	if _, err := domain.Validate(proj.Def); err != nil {
		t.Fatalf("starter config does not validate: %v", err)
	}

	if _, err := (Repository{}).Init(dir); err == nil {
		t.Fatal("second init should refuse to overwrite")
	}
}
