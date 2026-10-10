package commands

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mattn/go-isatty"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// style colours text only when writing to a terminal.
type style struct{ on bool }

func newStyle(f *os.File, noColor bool) style {
	return style{on: !noColor && (isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()))}
}

func (s style) wrap(code, text string) string {
	if !s.on {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}

func (s style) green(t string) string  { return s.wrap("32", t) }
func (s style) red(t string) string    { return s.wrap("31", t) }
func (s style) yellow(t string) string { return s.wrap("33", t) }
func (s style) dim(t string) string    { return s.wrap("2", t) }
func (s style) bold(t string) string   { return s.wrap("1", t) }

func table(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// formatPorts renders a service's ports as "5183" or "5183 hmr=24688".
func formatPorts(ports map[string]int) string {
	var parts []string
	if p, ok := ports[domain.MainPort]; ok {
		parts = append(parts, strconv.Itoa(p))
	}
	for _, name := range slices.Sorted(maps.Keys(ports)) {
		if name != domain.MainPort {
			parts = append(parts, name+"="+strconv.Itoa(ports[name]))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func formatLinks(links []domain.Link) string {
	if len(links) == 0 {
		return ""
	}
	parts := make([]string, len(links))
	for i, l := range links {
		parts[i] = l.Label + " " + l.URL
	}
	return strings.Join(parts, ", ")
}

func (s style) state(sv app.ServiceView) string {
	switch sv.State {
	case app.StateRunning:
		return s.green("running")
	case app.StateExited:
		if sv.ExitCode == 0 && sv.Service.Oneshot {
			return s.dim("done")
		}
		if sv.ExitCode == 0 {
			return s.dim("exited (0)")
		}
		return s.red(fmt.Sprintf("exited (%d)", sv.ExitCode))
	case app.StateMissing:
		return s.red("missing")
	case app.StateIdle:
		return s.dim("idle")
	case app.StateStopped:
		return s.dim("stopped")
	}
	return string(sv.State)
}

func printStack(w io.Writer, st style, v app.StackView) {
	s := v.Stack
	status := st.green(string(s.Status))
	if s.Status != domain.StackUp {
		status = st.dim(string(s.Status))
	}
	_, _ = fmt.Fprintf(w, "%s  %s  slot %d  %s\n", st.bold(s.ID.String()), status, s.Slot, st.dim(s.Session))

	tw := table(w)
	_, _ = fmt.Fprintln(tw, st.dim("SERVICE\tSTATE\tPORTS\tLINKS"))
	for _, sv := range v.Services {
		links := formatLinks(sv.Service.Links)
		if links == "" {
			links = sv.Service.URL
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sv.Service.Name, st.state(sv), formatPorts(sv.Service.Ports), links)
	}
	_ = tw.Flush()
}

// JSON views. Their shape is versioned with domain.RecordVersion.

type jsonLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type jsonService struct {
	Name     string         `json:"name"`
	State    string         `json:"state"`
	ExitCode *int           `json:"exit_code,omitempty"`
	Ports    map[string]int `json:"ports"`
	URL      string         `json:"url,omitempty"`
	Links    []jsonLink     `json:"links"`
}

type jsonStack struct {
	Version  int           `json:"version"`
	Project  string        `json:"project"`
	Stack    string        `json:"stack"`
	Root     string        `json:"root"`
	Session  string        `json:"session"`
	Slot     int           `json:"slot"`
	Status   string        `json:"status"`
	Services []jsonService `json:"services"`
}

func toJSONStack(v app.StackView) jsonStack {
	s := v.Stack
	out := jsonStack{
		Version:  domain.RecordVersion,
		Project:  s.ID.Project,
		Stack:    s.ID.Stack,
		Root:     s.Root,
		Session:  s.Session,
		Slot:     s.Slot,
		Status:   string(s.Status),
		Services: make([]jsonService, 0, len(v.Services)),
	}
	for _, sv := range v.Services {
		js := jsonService{
			Name:     sv.Service.Name,
			State:    string(sv.State),
			ExitCode: nil,
			Ports:    sv.Service.Ports,
			URL:      sv.Service.URL,
			Links:    make([]jsonLink, 0, len(sv.Service.Links)),
		}
		if sv.State == app.StateExited {
			code := sv.ExitCode
			js.ExitCode = &code
		}
		for _, l := range sv.Service.Links {
			js.Links = append(js.Links, jsonLink(l))
		}
		out.Services = append(out.Services, js)
	}
	return out
}
