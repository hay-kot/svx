package domain

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"time"
)

// RecordVersion is the version of the public state record and of --json
// output. Bump it only for breaking changes to their shape.
const RecordVersion = 1

// StackStatus is the recorded state of a stack.
type StackStatus string

const (
	StackUp   StackStatus = "up"
	StackDown StackStatus = "down"
)

// Stack is one copy of a project with its slot chosen and every template
// rendered. It is what the state directory stores and what the shim reads.
type Stack struct {
	ID   Identity
	Root string
	Dir  string
	Slot int
	// Session is the tmux session holding the stack's windows: the one
	// `svx up` ran in when inside tmux, else the stack's own
	// (Identity.Session).
	Session   string
	Status    StackStatus
	StartedAt time.Time
	Settings  Settings
	Services  []Service
	// Env is the stack-wide overlay: injected identity and port variables
	// plus the rendered top-level env. It is what `svx env` and `svx exec`
	// use when no service is named.
	Env map[string]string
	// Snapshot is the environment `svx up` ran in. Services see it beneath
	// their own overlay. It can hold secrets and is stored privately.
	Snapshot map[string]string
}

// Service is one rendered service.
type Service struct {
	Name      string
	Ports     map[string]int
	URL       string
	Links     []Link
	Run       string
	Dir       string
	Env       map[string]string
	Depends   []string
	Ready     ReadySpec
	Oneshot   bool
	Autostart bool
	Retries   int
	Stop      StopSpec
	LogFile   string
}

// Link is a rendered, absolute link.
type Link struct {
	Label string
	URL   string
}

// MainPort returns the primary port, if any.
func (s Service) MainPort() (int, bool) {
	p, ok := s.Ports[MainPort]
	return p, ok
}

// Service looks up a service by name.
func (s Stack) Service(name string) (Service, bool) {
	for _, svc := range s.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return Service{}, false
}

// ServiceNames lists services in declaration order.
func (s Stack) ServiceNames() []string {
	names := make([]string, len(s.Services))
	for i, svc := range s.Services {
		names[i] = svc.Name
	}
	return names
}

// Ports rebuilds the port table from the services.
func (s Stack) Ports() PortTable {
	t := PortTable{}
	for _, svc := range s.Services {
		for name, p := range svc.Ports {
			t.Set(svc.Name, name, p)
		}
	}
	return t
}

// Links lists every link in service order.
func (s Stack) Links() []Link {
	var out []Link
	for _, svc := range s.Services {
		out = append(out, svc.Links...)
	}
	return out
}

// ServiceDefs adapts the rendered services back to definitions, for the
// dependency helpers that work on either.
func (s Stack) ServiceDefs() []ServiceDef {
	defs := make([]ServiceDef, len(s.Services))
	for i, svc := range s.Services {
		defs[i] = ServiceDef{Name: svc.Name, Depends: svc.Depends, Autostart: svc.Autostart}
	}
	return defs
}

// ProcessEnv builds the final environment for a process: the snapshot, then
// the overlay. Keys in keep are taken from current instead, for variables the
// process's own surroundings must decide, such as TMUX inside a pane.
func ProcessEnv(snapshot, overlay, current map[string]string, keep ...string) []string {
	merged := maps.Clone(snapshot)
	if merged == nil {
		merged = map[string]string{}
	}
	maps.Copy(merged, overlay)
	for _, k := range keep {
		if v, ok := current[k]; ok {
			merged[k] = v
		} else {
			delete(merged, k)
		}
	}
	out := make([]string, 0, len(merged))
	for _, k := range slices.Sorted(maps.Keys(merged)) {
		out = append(out, k+"="+merged[k])
	}
	return out
}

// StackDir is where a stack keeps its data: <root>/.svx/<stack>.
func StackDir(root, stack string) string { return filepath.Join(root, ".svx", stack) }

// LogFile is where a service's output is piped when logs are on.
func LogFile(stackDir, service string) string {
	return filepath.Join(LogDir(stackDir), service+".log")
}

// BaseURL is a service's URL on loopback.
func BaseURL(port int) string { return "http://127.0.0.1:" + strconv.Itoa(port) }

// LogDir is the directory holding a stack's service logs.
func LogDir(stackDir string) string { return filepath.Join(stackDir, "logs") }
