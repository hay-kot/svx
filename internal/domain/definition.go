// Package domain holds svx's core model: what a project declares, how a stack
// is identified, how its ports are laid out, and how its templates render.
// It does no I/O. Everything that touches tmux, the filesystem or the network
// lives behind the ports in internal/app.
package domain

import (
	"fmt"
	"syscall"
	"time"
)

// Defaults for settings and service keys the config leaves out.
const (
	DefaultPortStride   = 10
	DefaultMaxSlots     = 50
	DefaultReadyTimeout = 60 * time.Second
	DefaultStopTimeout  = 10 * time.Second
	DefaultStopSignal   = "SIGTERM"

	// MainPort is the key the primary port is stored under alongside the
	// named ports. It is reserved, so a service cannot declare a named port
	// called "main".
	MainPort = "main"
)

// Definition is a project's config after loading and merging, before any
// stack-specific value is filled in.
type Definition struct {
	// Source is the path of the main config file, used in error messages.
	Source   string
	Project  string
	Settings Settings
	Vars     map[string]string
	Env      map[string]string
	Services []ServiceDef
}

// Settings are the project-wide knobs under `settings`.
type Settings struct {
	PortStride int
	MaxSlots   int
	Logs       bool
	// MenuBar lets a project hide its stacks from the menu bar.
	MenuBar bool
}

// ServiceDef is one entry under `services`.
type ServiceDef struct {
	Name      string
	Run       string
	Dir       string
	Port      PortSpec
	Ports     map[string]PortSpec
	Env       map[string]string
	Depends   []string
	Ready     ReadySpec
	Oneshot   bool
	Links     []LinkDef
	Autostart bool
	// Retries is how many times the command reruns after a non-zero exit.
	// The project's settings.retries fills it when the service sets none.
	Retries int
	Stop    StopSpec
}

// PortSpec is a declared port: a base number offset by the slot, or "auto"
// for any free port.
type PortSpec struct {
	Set  bool
	Auto bool
	Base int
}

// FixedPort declares a port with a base number.
func FixedPort(base int) PortSpec { return PortSpec{Set: true, Auto: false, Base: base} }

// AutoPort declares a port the OS picks.
func AutoPort() PortSpec { return PortSpec{Set: true, Auto: true, Base: 0} }

// ReadyKind names which readiness check a service uses.
type ReadyKind string

const (
	// ReadyDefault is a TCP connect to the primary port, or one second of
	// staying alive for a service without a port.
	ReadyDefault ReadyKind = ""
	ReadyHTTP    ReadyKind = "http"
	ReadyCmd     ReadyKind = "cmd"
	ReadyOutput  ReadyKind = "output"
	ReadyDelay   ReadyKind = "delay"
)

// ReadySpec is the `ready` block. Value holds the check's argument: a path or
// URL for http, a shell command for cmd, a regex for output, a duration for
// delay. It is a template.
type ReadySpec struct {
	Kind    ReadyKind
	Value   string
	Timeout time.Duration
}

// LinkDef is a labelled URL. A target starting with "/" is relative to the
// service's own URL.
type LinkDef struct {
	Label  string
	Target string
}

// StopSpec says how `down` asks a service to stop.
type StopSpec struct {
	Signal  string
	Timeout time.Duration
}

// ParseSignal turns a name such as "SIGINT" or "INT" into a signal.
func ParseSignal(name string) (syscall.Signal, error) {
	switch name {
	case "SIGTERM", "TERM", "":
		return syscall.SIGTERM, nil
	case "SIGINT", "INT":
		return syscall.SIGINT, nil
	case "SIGQUIT", "QUIT":
		return syscall.SIGQUIT, nil
	case "SIGHUP", "HUP":
		return syscall.SIGHUP, nil
	case "SIGKILL", "KILL":
		return syscall.SIGKILL, nil
	case "SIGUSR1", "USR1":
		return syscall.SIGUSR1, nil
	case "SIGUSR2", "USR2":
		return syscall.SIGUSR2, nil
	default:
		return 0, fmt.Errorf("unknown signal %q, use one of SIGTERM, SIGINT, SIGQUIT, SIGHUP, SIGKILL, SIGUSR1, SIGUSR2", name)
	}
}

// Service returns the named service definition.
func (d Definition) Service(name string) (ServiceDef, bool) {
	for _, s := range d.Services {
		if s.Name == name {
			return s, true
		}
	}
	return ServiceDef{}, false
}

// ServiceNames lists services in declaration order.
func (d Definition) ServiceNames() []string {
	names := make([]string, len(d.Services))
	for i, s := range d.Services {
		names[i] = s.Name
	}
	return names
}
