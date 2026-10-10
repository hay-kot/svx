package app

import (
	"context"
	"syscall"

	"github.com/hay-kot/svx/internal/domain"
)

// The interfaces in this file are the ports the use cases drive. Adapters in
// internal/adapters implement them; tests substitute fakes.

// Project is a loaded config plus where it was found.
type Project struct {
	Def  domain.Definition
	Root string
	// CheckoutName is the directory name of the main checkout, so git
	// worktrees of one repo share a project name.
	CheckoutName string
}

// ProjectRepository finds and loads project config.
type ProjectRepository interface {
	// Load walks up from dir to the nearest project config.
	Load(dir string) (Project, error)
	// Init writes a starter config in dir and ignores the data directory.
	// It returns the files it wrote or changed.
	Init(dir string) ([]string, error)
}

// StateStore persists stacks. Save writes both the public record and the
// private runtime file.
type StateStore interface {
	// Lock takes the machine-wide lock used for slot allocation and state
	// writes. Call the returned func to release it.
	Lock(ctx context.Context) (func(), error)
	// Load reads a stack with its runtime fields.
	Load(id domain.Identity) (domain.Stack, bool, error)
	Save(stack domain.Stack) error
	// List reads every public record. Runtime fields (Run, Env, Snapshot)
	// are empty: use Load for those.
	List() ([]domain.Stack, error)
	SetStatus(id domain.Identity, status domain.StackStatus) error
	Dir() string
}

// Window is one of a stack's tmux windows.
type Window struct {
	ID string
	// Name is the service the window runs, from its @svx_service tag, not
	// the tmux window name.
	Name     string
	PanePID  int
	Dead     bool
	ExitCode int
}

// WindowSpec describes a window to create or respawn.
type WindowSpec struct {
	Session string
	// Stack and Service tag the window, so svx finds its windows by tag in
	// a session that may hold the user's own windows too.
	Stack   string
	Service string
	// Window is the id of the window to respawn.
	Window string
	Dir    string
	// Command is run directly, not through a shell.
	Command []string
	// Env is set on the window. It must carry nothing secret: tmux shows
	// it to anyone who can reach the server.
	Env map[string]string
	// LogFile, when set, receives the pane's output.
	LogFile string
}

// Multiplexer is svx's view of tmux.
type Multiplexer interface {
	Version(ctx context.Context) (string, error)
	// CurrentSession returns the session svx was run from, when it runs
	// in a pane on the same tmux server.
	CurrentSession(ctx context.Context) (string, bool, error)
	// StartWindow creates a tagged window, detached, and returns its id.
	StartWindow(ctx context.Context, spec WindowSpec) (string, error)
	RespawnWindow(ctx context.Context, spec WindowSpec) error
	// Windows lists the windows in session tagged with stack.
	Windows(ctx context.Context, session, stack string) ([]Window, error)
	KillWindow(ctx context.Context, window string) error
	Capture(ctx context.Context, window string, lines int) (string, error)
	// Attach hands the terminal to tmux, switching client when already
	// inside tmux. window is a window id and may be empty.
	Attach(ctx context.Context, session, window string) error
	// PaneReady reports whether svx has finished setting up a pane's
	// window: remain-on-exit, tags and the log pipe.
	PaneReady(ctx context.Context, pane string) (bool, error)
}

// Signaler signals process groups.
type Signaler interface {
	SignalGroup(pid int, sig syscall.Signal) error
}

// PortProber tests ports on loopback.
type PortProber interface {
	// Free reports whether nothing is listening on the port and it can be
	// bound.
	Free(port int) bool
	// Ephemeral returns a port the OS considers free right now.
	Ephemeral() (int, error)
}

// Probe is one readiness attempt.
type Probe struct {
	Kind domain.ReadyKind
	// Value is the check's argument: URL, command or regex.
	Value string
	Dir   string
	Env   []string
	// Port is the primary port, for the default TCP check.
	Port int
	// LogFile and LogOffset scope an output check to what the current run
	// wrote.
	LogFile   string
	LogOffset int64
}

// ReadyProber runs one readiness attempt. A nil error means ready.
type ReadyProber interface {
	Probe(ctx context.Context, p Probe) error
}

// Files is the filesystem the use cases touch beyond state.
type Files interface {
	MkdirAll(path string) error
	RemoveAll(path string) error
	Size(path string) int64
}

// Browser opens URLs.
type Browser interface {
	Open(ctx context.Context, url string) error
}
