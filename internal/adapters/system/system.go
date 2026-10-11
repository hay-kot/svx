// Package system holds the small OS-facing adapters: processes, port
// probes, files and the browser.
package system

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hay-kot/appkit/secret"

	"github.com/hay-kot/svx/internal/app"
)

// Processes implements app.Processes with ps(1) and kill(2). ps is the
// portable reader: macOS has no /proc.
type Processes struct{}

var _ app.Processes = Processes{}

// Snapshot reads the whole process table.
func (Processes) Snapshot(ctx context.Context) ([]app.Process, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,pgid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("reading the process table: %w", err)
	}
	return parsePS(string(out)), nil
}

// parsePS reads `ps -o pid=,ppid=,pgid=,command=` output. A row that does
// not parse is skipped, so one odd line cannot hide every other process.
func parsePS(out string) []app.Process {
	var table []app.Process
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		var ids [3]int
		ok := true
		for i := range ids {
			n, err := strconv.Atoi(f[i])
			ids[i], ok = n, ok && err == nil
		}
		if !ok {
			continue
		}
		table = append(table, app.Process{PID: ids[0], PPID: ids[1], PGID: ids[2], Command: strings.Join(f[3:], " ")})
	}
	return table
}

// Signal signals one process. One that no longer exists is not an error.
func (Processes) Signal(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signalling process %d: %w", pid, err)
	}
	return nil
}

// SignalGroup signals every process in pgid's group. A group that no longer
// exists is not an error.
func (Processes) SignalGroup(pgid int, sig syscall.Signal) error {
	if pgid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signalling process group %d: %w", pgid, err)
	}
	return nil
}

// Ports implements app.PortProber on 127.0.0.1.
type Ports struct{}

var _ app.PortProber = Ports{}

// Free dials the port first and then tries to bind it. The dial catches a
// server bound to the wildcard address, which a 127.0.0.1 bind with
// SO_REUSEADDR can slip past on macOS.
func (Ports) Free(port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 100 * time.Millisecond}
	if conn, err := dialer.DialContext(context.Background(), "tcp", addr); err == nil {
		_ = conn.Close()
		return false
	}
	l, err := net.Listen("tcp", addr) //nolint:noctx // a bind test, closed at once
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// Ephemeral asks the OS for a free port.
func (Ports) Ephemeral() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0") //nolint:noctx // a bind test, closed at once
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %v", l.Addr())
	}
	return addr.Port, nil
}

// Files implements app.Files on the local disk.
type Files struct{}

var _ app.Files = Files{}

func (Files) MkdirAll(path string) error { return os.MkdirAll(path, 0o750) }

func (Files) RemoveAll(path string) error { return os.RemoveAll(path) }

// Browser opens URLs with the platform opener.
type Browser struct{}

var _ app.Browser = Browser{}

func (Browser) Open(ctx context.Context, url string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if err := exec.CommandContext(ctx, opener, url).Run(); err != nil { //nolint:gosec // a fixed opener given a link from the stack
		return fmt.Errorf("opening %s with %s: %w", url, opener, err)
	}
	return nil
}

// ResolveSecret resolves "env:NAME", "file:/path" and any other prefix
// registered with appkit's secret package.
func ResolveSecret(ref string) (string, error) {
	s, err := secret.Resolve(ref)
	if err != nil {
		return "", err
	}
	return s.Value(), nil
}
