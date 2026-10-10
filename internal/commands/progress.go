package commands

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"golang.org/x/sys/unix"

	"github.com/hay-kot/svx/internal/app"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// progress shows what a use case is doing, one row per service. On a
// terminal the rows redraw in place with a spinner; elsewhere each event
// prints its own line, so logs and CI output stay readable.
type progress struct {
	mu     sync.Mutex
	w      io.Writer
	fd     int
	st     style
	live   bool
	rows   []*progressRow
	byName map[string]*progressRow
	notes  []string
	drawn  int
	frame  int
	stop   chan struct{}
	done   sync.WaitGroup
	once   sync.Once
}

type progressRow struct {
	name    string
	state   rowState
	text    string
	detail  []string
	started time.Time
	elapsed time.Duration
}

type rowState int

const (
	rowPending rowState = iota
	rowActive
	rowOK
	rowFailed
	rowSkipped
)

// withProgress returns a copy of a that reports progress to f, and a func
// that finishes the report. Call it before printing anything else to f's
// terminal.
func withProgress(a *app.App, f *os.File, noColor bool) (*app.App, func()) {
	p := &progress{
		w:      f,
		fd:     int(f.Fd()),
		st:     newStyle(f, noColor),
		live:   isatty.IsTerminal(f.Fd()) && os.Getenv("TERM") != "dumb",
		byName: map[string]*progressRow{},
		stop:   make(chan struct{}),
	}
	cp := *a
	cp.Events = p.event
	if p.live {
		_, _ = io.WriteString(p.w, "\033[?25l")
		p.done.Go(p.animate)
	}
	return &cp, p.finish
}

func (p *progress) animate() {
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-tick.C:
			p.mu.Lock()
			p.frame++
			p.redraw()
			p.mu.Unlock()
		}
	}
}

func (p *progress) finish() {
	p.once.Do(func() {
		if !p.live {
			return
		}
		close(p.stop)
		p.done.Wait()
		p.mu.Lock()
		defer p.mu.Unlock()
		p.clear()
		var b strings.Builder
		for _, r := range p.rows {
			b.WriteString(p.line(r, time.Now()))
			b.WriteByte('\n')
			for _, d := range r.detail {
				b.WriteString(d + "\n")
			}
		}
		for _, n := range p.notes {
			b.WriteString(n + "\n")
		}
		b.WriteString("\033[?25h")
		_, _ = io.WriteString(p.w, b.String())
	})
}

func (p *progress) row(name string) *progressRow {
	r, ok := p.byName[name]
	if !ok {
		r = &progressRow{name: name, state: rowPending, text: "", detail: nil, started: time.Now(), elapsed: 0}
		p.byName[name] = r
		p.rows = append(p.rows, r)
	}
	return r
}

func (p *progress) event(e app.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e.Kind == app.EventPlan {
		for name := range strings.SplitSeq(e.Detail, ",") {
			if name != "" {
				p.row(name).text = "queued"
			}
		}
		return
	}
	if e.Kind == app.EventWarning {
		note := p.st.yellow("warning:") + " " + e.Detail
		if p.live {
			p.notes = append(p.notes, note)
		} else {
			_, _ = fmt.Fprintln(p.w, note)
		}
		return
	}

	r := p.row(e.Service)
	switch e.Kind {
	case app.EventWaiting:
		r.state, r.text = rowPending, "waiting for "+e.Detail
	case app.EventStarting:
		r.state, r.text, r.started = rowActive, "starting", time.Now()
	case app.EventStopping:
		r.state, r.text, r.started = rowActive, "stopping", time.Now()
	case app.EventRunning:
		r.state, r.text = rowOK, e.Detail
	case app.EventReady:
		r.state, r.text, r.elapsed = rowOK, "ready", e.Elapsed
		if e.Detail != "" {
			r.text = e.Detail
		}
	case app.EventStopped:
		r.state, r.text, r.elapsed = rowOK, "stopped", e.Elapsed
		if e.Detail != "" {
			r.state, r.text = rowSkipped, e.Detail
		}
	case app.EventFailed:
		first, rest, _ := strings.Cut(e.Detail, "\n")
		r.state, r.text, r.elapsed = rowFailed, first, e.Elapsed
		if rest != "" {
			r.detail = strings.Split(rest, "\n")
		}
	case app.EventSkipped:
		r.state, r.text = rowSkipped, "not started: "+e.Detail
	case app.EventPlan, app.EventWarning:
	}

	if p.live {
		p.redraw()
		return
	}
	if e.Kind == app.EventWaiting {
		return
	}
	_, _ = fmt.Fprintln(p.w, p.line(r, time.Now()))
	for _, d := range r.detail {
		_, _ = fmt.Fprintln(p.w, d)
	}
}

// line renders one row. On a terminal, names pad to the longest so the
// states line up; printed line by line, later rows could not match earlier
// ones, so nothing pads.
func (p *progress) line(r *progressRow, now time.Time) string {
	name := r.name
	if p.live {
		width := 0
		for _, o := range p.rows {
			width = max(width, len(o.name))
		}
		name += strings.Repeat(" ", width-len(r.name))
	}

	var mark, text string
	elapsed := r.elapsed
	switch r.state {
	case rowPending:
		mark, text = p.st.dim("·"), p.st.dim(r.text)
	case rowActive:
		mark, text = p.st.dim("-"), r.text
		if p.live {
			mark, elapsed = p.st.cyan(spinnerFrames[p.frame%len(spinnerFrames)]), now.Sub(r.started)
		}
	case rowOK:
		mark, text = p.st.green("✓"), r.text
	case rowFailed:
		mark, text = p.st.red("✗"), p.st.red(r.text)
	case rowSkipped:
		mark, text = p.st.yellow("!"), r.text
	}
	sep := " "
	if p.live {
		sep = "  "
	}
	out := mark + " " + name + sep + text
	if elapsed >= 100*time.Millisecond {
		out += " " + p.st.dim(formatElapsed(elapsed))
	}
	return out
}

func formatElapsed(d time.Duration) string {
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// redraw repaints the live rows. Each is cut to the terminal width: a
// wrapped line would throw off the cursor movement that clears the last
// frame.
func (p *progress) redraw() {
	p.clear()
	cols := 80
	if ws, err := unix.IoctlGetWinsize(p.fd, unix.TIOCGWINSZ); err == nil && ws.Col > 0 {
		cols = int(ws.Col)
	}
	var b strings.Builder
	now := time.Now()
	for _, r := range p.rows {
		line := p.line(r, now)
		if visibleWidth(line) >= cols {
			line = truncate(line, cols-1)
		}
		b.WriteString(line + "\n")
	}
	p.drawn = len(p.rows)
	_, _ = io.WriteString(p.w, b.String())
}

func (p *progress) clear() {
	if p.drawn > 0 {
		_, _ = fmt.Fprintf(p.w, "\033[%dF\033[J", p.drawn)
	}
	p.drawn = 0
}

// truncate cuts s to n visible runes, dropping colour so a cut escape
// sequence cannot leak.
func truncate(s string, n int) string {
	runes := []rune(stripStyle(s))
	if len(runes) <= n {
		return string(runes)
	}
	return string(runes[:max(n-1, 0)]) + "…"
}
