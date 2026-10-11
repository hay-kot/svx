package yamlproject

import (
	"errors"
	"os"
	"os/exec"
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
	if def.Project != "shop" || def.Settings.PortStride != 20 || def.Settings.MaxSlots != domain.DefaultMaxSlots || def.Settings.Logs {
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
	changed, err := Repository{}.Init(dir, app.InitOptions{})
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

	if _, err := (Repository{}).Init(dir, app.InitOptions{}); err == nil {
		t.Fatal("second init should refuse to overwrite")
	}
}

func TestInitStealth(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:gosec // a test temp dir
		t.Skipf("git init: %v: %s", err, out)
	}
	sub := filepath.Join(dir, "app")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}

	changed, err := Repository{}.Init(sub, app.InitOptions{Stealth: true})
	if err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(dir, ".git", "info", "exclude")
	if len(changed) != 2 || !sameFile(changed[1], exclude) {
		t.Fatalf("changed = %v", changed)
	}
	data, _ := os.ReadFile(exclude) //nolint:gosec // test temp file
	for _, want := range []string{"svx.yaml\n", ".svx/\n", "svx.local.yaml\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("exclude missing %q:\n%s", want, data)
		}
	}
	if _, err := os.Stat(filepath.Join(sub, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("stealth init should not touch .gitignore, stat err = %v", err)
	}

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "status", "--porcelain", "--untracked-files=all").Output() //nolint:gosec // a test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("git status should be clean:\n%s", out)
	}
}

func TestInitStealthOutsideGit(t *testing.T) {
	dir := t.TempDir()
	_, err := Repository{}.Init(dir, app.InitOptions{Stealth: true})
	if app.ExitCode(err) != app.ExitUsage {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "svx.yaml")); !os.IsNotExist(err) {
		t.Errorf("failed stealth init should not leave svx.yaml, stat err = %v", err)
	}
}

func TestLoadFromProjectsConfig(t *testing.T) {
	dotfiles := t.TempDir()
	shop := filepath.Join(dotfiles, "shop.yaml")
	write(t, shop, shopYAML)
	catchAll := filepath.Join(dotfiles, "any.yaml")
	write(t, catchAll, "services:\n  web:\n    run: x\n")

	base := t.TempDir()
	checkout := filepath.Join(base, "shop-feature")
	sub := filepath.Join(checkout, "web", "src")
	if err := os.MkdirAll(filepath.Join(checkout, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checkout, "svx.local.yaml"), "settings:\n  port_stride: 5\n")

	repo := Repository{Projects: map[string]string{"shop-*": shop, "*": catchAll}}
	proj, err := repo.Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Root != checkout {
		t.Errorf("root = %q, want %q", proj.Root, checkout)
	}
	if proj.Def.Project != "shop" || proj.Def.Settings.PortStride != 5 {
		t.Errorf("def = %+v", proj.Def)
	}

	write(t, filepath.Join(checkout, "svx.yaml"), "project: inrepo\nservices:\n  web:\n    run: x\n")
	proj, err = repo.Load(sub)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Def.Project != "inrepo" {
		t.Errorf("an svx.yaml in the repo should win, got project %q", proj.Def.Project)
	}
}

func TestLoadFromProjectsConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	repo := Repository{Projects: map[string]string{filepath.Base(dir): filepath.Join(dir, "nope.yaml")}}
	if _, err := repo.Load(dir); err == nil || !strings.Contains(err.Error(), "nope.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestMatchProject(t *testing.T) {
	projects := map[string]string{
		"/code/exact": "full",
		"exact":       "base",
		"shop-*":      "glob",
		"shop-api-*":  "longer",
		"/work/*/svc": "pathglob",
		"*":           "all",
	}
	tests := map[string]string{
		"/code/exact":     "full",
		"/other/exact":    "base",
		"/x/shop-web":     "glob",
		"/x/shop-api-v2":  "longer",
		"/work/team/svc":  "pathglob",
		"/somewhere/else": "all",
	}
	for dir, want := range tests {
		if got := matchProject(projects, dir); got != want {
			t.Errorf("matchProject(%q) = %q, want %q", dir, got, want)
		}
	}
	if got := matchProject(map[string]string{"shop-*": "x"}, "/x/web"); got != "" {
		t.Errorf("no match should be empty, got %q", got)
	}
}

func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && os.SameFile(ai, bi)
}

func TestRetriesDefaultFromSettings(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "svx.yaml"), `
settings:
  retries: 2
services:
  web:
    run: x
  api:
    run: x
    retries: 0
`)
	proj, err := Repository{}.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	web, _ := proj.Def.Service("web")
	api, _ := proj.Def.Service("api")
	if web.Retries != 2 || api.Retries != 0 {
		t.Fatalf("retries: web = %d, api = %d, want 2 and 0", web.Retries, api.Retries)
	}
}
