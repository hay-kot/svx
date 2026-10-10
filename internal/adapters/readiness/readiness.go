// Package readiness runs single readiness attempts. Timing, retries and
// timeouts belong to the caller.
package readiness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"github.com/hay-kot/appkit/httpclient"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// attemptTimeout bounds one attempt, so a hung server does not stall the
// caller's loop past its own timeout.
const attemptTimeout = 2 * time.Second

// Prober implements app.ReadyProber.
type Prober struct {
	http *httpclient.Client
}

var _ app.ReadyProber = (*Prober)(nil)

// New returns a prober. Redirects are not followed: a 3xx already means the
// server is answering.
func New() *Prober {
	client := &http.Client{
		Timeout: attemptTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &Prober{http: httpclient.New(client, "")}
}

// Probe runs one attempt. nil means ready.
func (p *Prober) Probe(ctx context.Context, probe app.Probe) error {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()

	switch probe.Kind {
	case domain.ReadyHTTP:
		return p.httpCheck(ctx, probe.Value)
	case domain.ReadyCmd:
		return cmdCheck(ctx, probe)
	case domain.ReadyOutput:
		return outputCheck(probe)
	case domain.ReadyDefault:
		return tcpCheck(ctx, probe.Port)
	case domain.ReadyDelay:
		return nil
	}
	return fmt.Errorf("unknown readiness check %q", probe.Kind)
}

func (p *Prober) httpCheck(ctx context.Context, url string) error {
	resp, err := p.http.Get(ctx, url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}
	return fmt.Errorf("GET %s returned %d", url, resp.StatusCode)
}

func cmdCheck(ctx context.Context, probe app.Probe) error {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", probe.Value) //nolint:gosec // the ready.cmd the user configured
	cmd.Dir = probe.Dir
	cmd.Env = probe.Env
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := bytes.TrimSpace(out)
		if len(msg) > 200 {
			msg = msg[len(msg)-200:]
		}
		return fmt.Errorf("`%s` failed: %w %s", probe.Value, err, msg)
	}
	return nil
}

// outputCheck matches the regex against what the service logged since it
// started, so a match from a previous run does not count.
func outputCheck(probe app.Probe) error {
	re, err := regexp.Compile(probe.Value)
	if err != nil {
		return fmt.Errorf("ready.output %q: %w", probe.Value, err)
	}
	f, err := os.Open(probe.LogFile)
	if err != nil {
		return fmt.Errorf("log not written yet: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(probe.LogOffset, io.SeekStart); err != nil {
		return err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	if re.MatchString(domain.StripANSI(string(data))) {
		return nil
	}
	return fmt.Errorf("no output matching %q yet", probe.Value)
}

func tcpCheck(ctx context.Context, port int) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("port %d not accepting connections", port)
	}
	_ = conn.Close()
	return nil
}
