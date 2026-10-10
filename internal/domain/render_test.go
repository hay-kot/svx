package domain

import (
	"strings"
	"testing"
	"time"
)

const redisSvc = "redis"

func sampleDef() Definition {
	return Definition{
		Source:   "svx.yaml",
		Settings: Settings{PortStride: 10, MaxSlots: 50, Logs: true, MenuBar: true},
		Vars:     map[string]string{"db_name": "shop_{{ snake .stack.name }}"},
		Env: map[string]string{
			"REDIS_URL":     `redis://127.0.0.1:{{ port "redis" }}`,
			"DATABASE_NAME": "{{ .vars.db_name }}",
			"PORT":          "override-attempt",
		},
		Services: []ServiceDef{
			{
				Name: redisSvc, Run: "redis-server --port {{ .port }} --dir {{ .stack.dir }}", Port: FixedPort(6379),
				Ready: ReadySpec{Timeout: DefaultReadyTimeout}, Autostart: true, Stop: StopSpec{Signal: "SIGTERM", Timeout: time.Second},
			},
			{
				Name: "web", Run: "npm run dev -- --port {{ .port }} --hmr {{ .ports.hmr }}", Dir: "web",
				Port: FixedPort(5173), Ports: map[string]PortSpec{"hmr": FixedPort(24678)},
				Env:   map[string]string{"VITE_API_URL": `{{ url "redis" }}`, "SVC": "{{ .service.name }}"},
				Links: []LinkDef{{Label: "App", Target: "/"}, {Label: "Ext", Target: "https://example.com"}},
				Ready: ReadySpec{Kind: ReadyHTTP, Value: "/healthz", Timeout: time.Second}, Autostart: true,
				Stop: StopSpec{Signal: "SIGTERM", Timeout: time.Second},
			},
		},
	}
}

func renderSample(t *testing.T, def Definition, slot int) (Stack, error) {
	t.Helper()
	ports, autos, err := SlotPorts(def, slot)
	if err != nil {
		t.Fatal(err)
	}
	for i, ref := range autos {
		ports.Set(ref.Service, ref.Name, 40000+i)
	}
	return Render(RenderInput{
		Def:     def,
		ID:      Identity{Project: "shop", Stack: "amber-finch"},
		Root:    "/src/shop",
		Slot:    slot,
		Session: "work",
		Ports:   ports,
		Environ: map[string]string{"HOME": "/home/me"},
		Secrets: func(ref string) (string, error) { return "s3cret:" + ref, nil },
		Now:     time.Unix(0, 0),
	})
}

func TestRender(t *testing.T) {
	stack, err := renderSample(t, sampleDef(), 1)
	if err != nil {
		t.Fatal(err)
	}

	redis, _ := stack.Service(redisSvc)
	if redis.Run != "redis-server --port 6389 --dir /src/shop/.svx/amber-finch" {
		t.Errorf("redis run = %q", redis.Run)
	}

	web, _ := stack.Service("web")
	checks := map[string][2]string{
		"run":          {web.Run, "npm run dev -- --port 5183 --hmr 24688"},
		"dir":          {web.Dir, "/src/shop/web"},
		"url":          {web.URL, "http://127.0.0.1:5183"},
		"ready":        {web.Ready.Value, "http://127.0.0.1:5183/healthz"},
		"link":         {web.Links[0].URL, "http://127.0.0.1:5183/"},
		"abs link":     {web.Links[1].URL, "https://example.com"},
		"vite":         {web.Env["VITE_API_URL"], "http://127.0.0.1:6389"},
		"service name": {web.Env["SVC"], "web"},
		"db":           {web.Env["DATABASE_NAME"], "shop_amber_finch"},
		"redis url":    {stack.Env["REDIS_URL"], "redis://127.0.0.1:6389"},
		"PORT_HMR":     {web.Env["PORT_HMR"], "24688"},
		"SVX_PORT_WEB": {web.Env["SVX_PORT_WEB_HMR"], "24688"},
		"SVX_URL":      {stack.Env["SVX_URL_REDIS"], "http://127.0.0.1:6389"},
		"SVX_SLOT":     {stack.Env["SVX_SLOT"], "1"},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", name, c[0], c[1])
		}
	}

	// Top-level env outranks injected variables, even the per-service PORT.
	if web.Env["PORT"] != "override-attempt" {
		t.Errorf("PORT = %q, top-level env should win over injected", web.Env["PORT"])
	}
	if _, ok := stack.Env["SVX_SERVICE"]; ok {
		t.Error("stack-wide env should not carry SVX_SERVICE")
	}
}

func TestRenderErrors(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Definition)
		want string
	}{
		{"missing key", func(d *Definition) { d.Services[0].Run = "{{ .nope }}" }, "services.redis.run"},
		{"var references var", func(d *Definition) { d.Vars["b"] = "{{ .vars.db_name }}" }, "vars.b"},
		{"service name at top level", func(d *Definition) { d.Env["X"] = "{{ .service.name }}" }, "env.X"},
		{"unknown port", func(d *Definition) { d.Services[0].Run = `{{ port "ghost" }}` }, `no such service`},
		{"unknown named port", func(d *Definition) { d.Services[0].Run = `{{ port "web" "nope" }}` }, `no port named "nope"`},
		{"missing env", func(d *Definition) { d.Services[0].Run = `{{ env "NOPE" }}` }, `not set in the environment`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := sampleDef()
			tt.edit(&def)
			_, err := renderSample(t, def, 0)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want mention of %q", err, tt.want)
			}
		})
	}
}

func TestRenderHelpers(t *testing.T) {
	def := sampleDef()
	def.Services[0].Run = `{{ env "HOME" }} {{ secret "env:TOKEN" }} {{ upper "x" }} {{ slug "A B" }}`
	stack, err := renderSample(t, def, 0)
	if err != nil {
		t.Fatal(err)
	}
	redis, _ := stack.Service(redisSvc)
	if redis.Run != "/home/me s3cret:env:TOKEN X a-b" {
		t.Fatalf("run = %q", redis.Run)
	}
}

func TestProcessEnvLayering(t *testing.T) {
	env := ProcessEnv(
		map[string]string{"A": "snap", "TMUX": "outer", "TERM": "xterm-ghostty"},
		map[string]string{"A": "overlay", "B": "b"},
		map[string]string{"TMUX": "inner", "TERM": "tmux-256color"},
		"TMUX", "TERM", "TMUX_PANE",
	)
	want := []string{"A=overlay", "B=b", "TERM=tmux-256color", "TMUX=inner"}
	if strings.Join(env, ",") != strings.Join(want, ",") {
		t.Fatalf("env = %v, want %v", env, want)
	}
}

func TestRenderStackSession(t *testing.T) {
	def := sampleDef()
	def.Env["TMUX_SESSION"] = "{{ .stack.session }}"
	stack, err := renderSample(t, def, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stack.Session != "work" || stack.Env["TMUX_SESSION"] != "work" {
		t.Fatalf("session = %q, env = %q, want work", stack.Session, stack.Env["TMUX_SESSION"])
	}
}
