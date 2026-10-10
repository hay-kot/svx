// Package system holds the small OS-facing adapters: process signals, port
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
	"syscall"
	"time"

	"github.com/hay-kot/appkit/secret"

	"github.com/hay-kot/svx/internal/app"
)

// Signals implements app.Signaler with kill(2).
type Signals struct{}

var _ app.Signaler = Signals{}

// SignalGroup signals every process in pid's group. A group that no longer
// exists is not an error.
func (Signals) SignalGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signalling process group %d: %w", pid, err)
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

// Size returns a file's size, or 0 when it does not exist.
func (Files) Size(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

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
