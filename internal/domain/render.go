package domain

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"
	"unicode/utf8"
)

// SecretResolver turns a reference such as "env:TOKEN" or "file:/path" into
// its value. It is injected so rendering stays free of I/O.
type SecretResolver func(ref string) (string, error)

// RenderInput is everything Render needs. Ports must already hold every
// declared port, auto ones included: templates may reference any service's
// port, including one that starts later.
type RenderInput struct {
	Def  Definition
	ID   Identity
	Root string
	Slot int
	// Session is the tmux session the stack runs in.
	Session string
	Ports   PortTable
	Environ map[string]string
	Secrets SecretResolver
	Now     time.Time
}

// Render produces a Stack from a definition and its allocated ports.
func Render(in RenderInput) (Stack, error) {
	r := renderer{in: in, dir: StackDir(in.Root, in.ID.Stack)}
	return r.render()
}

type renderer struct {
	in   RenderInput
	dir  string
	errs []error
}

func (r *renderer) fail(key string, err error) {
	r.errs = append(r.errs, &ConfigError{File: r.in.Def.Source, Key: key, Msg: err.Error()})
}

func (r *renderer) render() (Stack, error) {
	def := r.in.Def
	funcs := r.funcs()

	base := map[string]any{
		"project": map[string]any{"name": r.in.ID.Project, "root": r.in.Root},
		"stack": map[string]any{
			"name":    r.in.ID.Stack,
			"slot":    r.in.Slot,
			"dir":     r.dir,
			"session": r.in.Session,
		},
	}

	// Vars render without `.vars` in scope, which is what stops one var from
	// referencing another: missingkey=error turns the attempt into an error.
	vars := map[string]string{}
	for name, text := range def.Vars {
		vars[name] = r.exec("vars."+name, text, base, funcs)
	}
	base["vars"] = vars

	stackEnv := r.injectedStack()
	for name, text := range def.Env {
		stackEnv[name] = r.exec("env."+name, text, base, funcs)
	}

	stack := Stack{
		ID:        r.in.ID,
		Root:      r.in.Root,
		Dir:       r.dir,
		Slot:      r.in.Slot,
		Session:   r.in.Session,
		Status:    StackUp,
		StartedAt: r.in.Now,
		Settings:  def.Settings,
		Env:       stackEnv,
		Snapshot:  r.in.Environ,
	}

	for _, sd := range def.Services {
		stack.Services = append(stack.Services, r.service(sd, base, funcs, stackEnv))
	}
	r.checkWindowsDistinct(stack.Services)

	if len(r.errs) > 0 {
		return Stack{}, errors.Join(r.errs...)
	}
	return stack, nil
}

func (r *renderer) service(sd ServiceDef, base map[string]any, funcs template.FuncMap, stackEnv map[string]string) Service {
	key := "services." + sd.Name
	ports := maps.Clone(r.in.Ports[sd.Name])
	if ports == nil {
		ports = map[string]int{}
	}

	data := maps.Clone(base)
	data["service"] = map[string]any{"name": sd.Name}
	named := map[string]int{}
	for name, p := range ports {
		if name != MainPort {
			named[name] = p
		}
	}
	data["ports"] = named
	if p, ok := ports[MainPort]; ok {
		data["port"] = p
	}

	svc := Service{
		Name:      sd.Name,
		Ports:     ports,
		Depends:   sd.Depends,
		Oneshot:   sd.Oneshot,
		Autostart: sd.Autostart,
		Retries:   sd.Retries,
		Stop:      sd.Stop,
		Run:       r.exec(key+".run", sd.Run, data, funcs),
		Window:    r.windowName(sd.Name, data, funcs),
	}
	if p, ok := ports[MainPort]; ok {
		svc.URL = BaseURL(p)
	}

	dir := r.exec(key+".dir", sd.Dir, data, funcs)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(r.in.Root, dir)
	}
	svc.Dir = filepath.Clean(dir)

	if r.in.Def.Settings.Logs {
		svc.LogFile = LogFile(r.dir, sd.Name)
	}

	env := maps.Clone(stackEnv)
	maps.Copy(env, r.injectedService(sd.Name, ports))
	// Top-level env wins over injected variables, and must keep winning over
	// the per-service injected ones too.
	for name := range r.in.Def.Env {
		env[name] = stackEnv[name]
	}
	for name, text := range sd.Env {
		env[name] = r.exec(key+".env."+name, text, data, funcs)
	}
	svc.Env = env

	for _, l := range sd.Links {
		target := r.exec(key+".links."+l.Label, l.Target, data, funcs)
		if strings.HasPrefix(target, "/") {
			target = svc.URL + target
		}
		svc.Links = append(svc.Links, Link{Label: l.Label, URL: target})
	}

	svc.Ready = sd.Ready
	if sd.Ready.Kind != ReadyDefault {
		svc.Ready.Value = r.exec(key+".ready."+string(sd.Ready.Kind), sd.Ready.Value, data, funcs)
		if sd.Ready.Kind == ReadyHTTP && strings.HasPrefix(svc.Ready.Value, "/") {
			svc.Ready.Value = svc.URL + svc.Ready.Value
		}
	}
	return svc
}

func (r *renderer) exec(key, text string, data map[string]any, funcs template.FuncMap) string {
	out, err := execTemplate(key, text, data, funcs)
	if err != nil {
		r.fail(key, err)
	}
	return out
}

func execTemplate(key, text string, data map[string]any, funcs template.FuncMap) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := template.New(key).Option("missingkey=error").Funcs(funcs).Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

const windowNameKey = "settings.window_name"

// windowName renders the window name template for one service. The
// template is shared, so errors name the service it failed for.
func (r *renderer) windowName(service string, data map[string]any, funcs template.FuncMap) string {
	text := r.in.Def.Settings.WindowName
	if text == "" {
		return service
	}
	name, err := execTemplate(windowNameKey, text, data, funcs)
	if err == nil {
		err = checkWindowName(name)
	}
	if err != nil {
		r.fail(windowNameKey, fmt.Errorf("service %s: %w", service, err))
	}
	return name
}

// checkWindowName rejects names tmux refuses ("invalid window name") or
// that would leave a window unlabelled. "." and ":" are fine: they only
// matter in targets, and svx targets windows by id.
func checkWindowName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("renders an empty window name")
	}
	if !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("window name %q has control characters or invalid UTF-8, which tmux rejects", name)
	}
	return nil
}

// checkWindowsDistinct rejects a template that gives two services the same
// window name, such as one that leaves out .service.name.
func (r *renderer) checkWindowsDistinct(services []Service) {
	seen := map[string]string{}
	for _, svc := range services {
		if svc.Window == "" {
			continue
		}
		if other, ok := seen[svc.Window]; ok {
			r.fail(windowNameKey, fmt.Errorf("services %s and %s both render window name %q", other, svc.Name, svc.Window))
			continue
		}
		seen[svc.Window] = svc.Name
	}
}

func (r *renderer) injectedStack() map[string]string {
	env := map[string]string{
		"SVX_PROJECT":   r.in.ID.Project,
		"SVX_STACK":     r.in.ID.Stack,
		"SVX_SLOT":      strconv.Itoa(r.in.Slot),
		"SVX_STACK_DIR": r.dir,
	}
	for svc, ports := range r.in.Ports {
		for name, p := range ports {
			if name == MainPort {
				env["SVX_PORT_"+EnvName(svc)] = strconv.Itoa(p)
				env["SVX_URL_"+EnvName(svc)] = BaseURL(p)
				continue
			}
			env["SVX_PORT_"+EnvName(svc)+"_"+EnvName(name)] = strconv.Itoa(p)
		}
	}
	return env
}

func (r *renderer) injectedService(name string, ports map[string]int) map[string]string {
	env := map[string]string{"SVX_SERVICE": name}
	for pname, p := range ports {
		if pname == MainPort {
			env["PORT"] = strconv.Itoa(p)
			continue
		}
		env["PORT_"+EnvName(pname)] = strconv.Itoa(p)
	}
	return env
}

// lookupPort resolves a service's main port, or the named port when name
// holds one. fn names the template function for error messages.
func (r *renderer) lookupPort(fn, svc string, name []string) (int, error) {
	if len(name) > 1 {
		return 0, fmt.Errorf("%s %q: takes a service and at most one port name", fn, svc)
	}
	pname := MainPort
	if len(name) == 1 {
		pname = name[0]
	}
	if _, ok := r.in.Def.Service(svc); !ok {
		return 0, fmt.Errorf("%s %q: no such service", fn, svc)
	}
	p, ok := r.in.Ports.Get(svc, pname)
	if !ok {
		if pname == MainPort {
			return 0, fmt.Errorf("%s %q: service has no `port`", fn, svc)
		}
		return 0, fmt.Errorf("%s %q %q: service has no port named %q", fn, svc, pname, pname)
	}
	return p, nil
}

func (r *renderer) funcs() template.FuncMap {
	environ := r.in.Environ
	secrets := r.in.Secrets
	return template.FuncMap{
		"port": func(svc string, name ...string) (int, error) {
			return r.lookupPort("port", svc, name)
		},
		"url": func(svc string, name ...string) (string, error) {
			p, err := r.lookupPort("url", svc, name)
			if err != nil {
				return "", err
			}
			return BaseURL(p), nil
		},
		"env": func(name string) (string, error) {
			v, ok := environ[name]
			if !ok {
				return "", fmt.Errorf("env %q: not set in the environment svx ran in", name)
			}
			return v, nil
		},
		"secret": func(ref string) (string, error) {
			if secrets == nil {
				return "", fmt.Errorf("secret %q: no secret resolver configured", ref)
			}
			v, err := secrets(ref)
			if err != nil {
				return "", fmt.Errorf("secret %q: %w", ref, err)
			}
			return v, nil
		},
		"snake": Snake,
		"slug":  Slug,
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
	}
}

// templateFuncStubs lets validation parse templates before any stack exists.
// Parsing only needs the names to be known.
var templateFuncStubs = (&renderer{}).funcs()
