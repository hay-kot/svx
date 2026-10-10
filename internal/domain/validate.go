package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ConfigError is a config problem tied to a file and key.
type ConfigError struct {
	File string
	Key  string
	Msg  string
}

func (e *ConfigError) Error() string {
	switch {
	case e.File != "" && e.Key != "":
		return fmt.Sprintf("%s: %s: %s", e.File, e.Key, e.Msg)
	case e.File != "":
		return fmt.Sprintf("%s: %s", e.File, e.Msg)
	case e.Key != "":
		return fmt.Sprintf("%s: %s", e.Key, e.Msg)
	default:
		return e.Msg
	}
}

// Validate checks a definition before anything starts. It returns warnings
// that should be shown but do not stop `up`, and an error joining every
// problem found.
func Validate(def Definition) ([]string, error) {
	var errs []error
	fail := func(key, format string, args ...any) {
		errs = append(errs, &ConfigError{File: def.Source, Key: key, Msg: fmt.Sprintf(format, args...)})
	}

	if def.Settings.PortStride < 1 {
		fail("settings.port_stride", "must be at least 1, got %d", def.Settings.PortStride)
	}
	if def.Settings.MaxSlots < 1 {
		fail("settings.max_slots", "must be at least 1, got %d", def.Settings.MaxSlots)
	}
	if len(def.Services) == 0 {
		fail("services", "no services declared, add at least one under `services`")
	}

	for name, v := range def.Vars {
		checkTemplate(fail, "vars."+name, v)
	}
	for name, v := range def.Env {
		checkTemplate(fail, "env."+name, v)
	}

	seen := declared{labels: map[string]string{}, bases: map[int]string{}}
	for _, s := range def.Services {
		validateService(s, &seen, fail)
	}

	if _, err := Levels(def.Services); err != nil {
		errs = append(errs, &ConfigError{File: def.Source, Key: "", Msg: err.Error()})
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return StrideOverlaps(def), nil
}

// declared tracks link labels and port bases across services, since both
// must be unique within a stack.
type declared struct {
	labels map[string]string
	bases  map[int]string
}

func validateService(s ServiceDef, seen *declared, fail func(key, format string, args ...any)) {
	key := "services." + s.Name
	if !nameRe.MatchString(s.Name) {
		fail(key, "service names may only use a-z, 0-9 and -, and must start with a letter or digit")
	}
	if strings.TrimSpace(s.Run) == "" {
		fail(key+".run", "is required")
	}
	checkTemplate(fail, key+".run", s.Run)
	checkTemplate(fail, key+".dir", s.Dir)
	for k, v := range s.Env {
		checkTemplate(fail, key+".env."+k, v)
	}

	checkPort := func(pkey string, p PortSpec) {
		if p.Auto {
			return
		}
		if p.Base < 1 || p.Base > MaxPort {
			fail(pkey, "port %d is outside 1-%d", p.Base, MaxPort)
			return
		}
		if other, ok := seen.bases[p.Base]; ok {
			fail(pkey, "port %d is already declared by %s", p.Base, other)
			return
		}
		seen.bases[p.Base] = pkey
	}
	if s.Port.Set {
		checkPort(key+".port", s.Port)
	}
	for name, p := range s.Ports {
		pkey := key + ".ports." + name
		if name == MainPort {
			fail(pkey, "%q is reserved for the primary port, use `port`", MainPort)
			continue
		}
		if !nameRe.MatchString(name) {
			fail(pkey, "port names may only use a-z, 0-9 and -")
		}
		checkPort(pkey, p)
	}

	for _, l := range s.Links {
		lkey := key + ".links." + l.Label
		if other, ok := seen.labels[l.Label]; ok {
			fail(lkey, "link label %q is already used by service %s, labels must be unique within a stack", l.Label, other)
		}
		seen.labels[l.Label] = s.Name
		if strings.HasPrefix(l.Target, "/") && !s.Port.Set {
			fail(lkey, "relative link %q needs a `port` on the service", l.Target)
		}
		checkTemplate(fail, lkey, l.Target)
	}

	validateReady(s, fail)

	if _, err := ParseSignal(s.Stop.Signal); err != nil {
		fail(key+".stop_signal", "%v", err)
	}
	if s.Stop.Timeout <= 0 {
		fail(key+".stop_timeout", "must be positive")
	}
}

func validateReady(s ServiceDef, fail func(key, format string, args ...any)) {
	key := "services." + s.Name
	switch s.Ready.Kind {
	case ReadyDefault:
	case ReadyHTTP:
		if !s.Port.Set && strings.HasPrefix(s.Ready.Value, "/") {
			fail(key+".ready.http", "a relative path needs a `port` on the service")
		}
	case ReadyOutput:
		if _, err := regexp.Compile(s.Ready.Value); err != nil && !strings.Contains(s.Ready.Value, "{{") {
			fail(key+".ready.output", "invalid regex: %v", err)
		}
	case ReadyCmd, ReadyDelay:
	}
	if s.Ready.Kind != ReadyDefault {
		checkTemplate(fail, key+".ready."+string(s.Ready.Kind), s.Ready.Value)
		if s.Oneshot {
			fail(key+".ready", "a oneshot service is ready when it exits 0, remove `ready`")
		}
	}
	if s.Ready.Timeout <= 0 {
		fail(key+".ready.timeout", "must be positive")
	}
}

func checkTemplate(fail func(key, format string, args ...any), key, text string) {
	if _, err := template.New(key).Funcs(templateFuncStubs).Parse(text); err != nil {
		fail(key, "template: %v", err)
	}
}
