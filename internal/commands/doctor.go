package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/hay-kot/svx/internal/adapters/menubar"
	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// minTmux is the oldest tmux svx supports. new-window -e and the pane
// options svx relies on need 3.2; older versions are untested.
var minTmux = [2]int{3, 2}

// DoctorCmd implements the doctor command.
type DoctorCmd struct {
	flags *Flags
	deps  *Deps
	stack stackFlags
}

// NewDoctorCmd creates a new doctor command.
func NewDoctorCmd(flags *Flags, deps *Deps) *DoctorCmd {
	return &DoctorCmd{flags: flags, deps: deps}
}

// Register adds the doctor command to the application.
func (cmd *DoctorCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:   "doctor",
		Usage:  "check tmux, the state directory, stale stacks and the menu bar",
		Flags:  cmd.stack.flags(),
		Action: cmd.run,
	})
	return app
}

type checkStatus int

const (
	checkPass checkStatus = iota
	checkWarn
	checkFail
	checkInfo
)

type check struct {
	Status checkStatus `json:"-"`
	Name   string      `json:"name"`
	Detail string      `json:"detail"`
	Result string      `json:"status"`
}

func (cmd *DoctorCmd) run(ctx context.Context, _ *cli.Command) error {
	var checks []check
	add := func(s checkStatus, name, detail string) {
		result := map[checkStatus]string{checkPass: "pass", checkWarn: "warn", checkFail: "fail", checkInfo: "info"}[s]
		checks = append(checks, check{Status: s, Name: name, Detail: detail, Result: result})
	}

	a := cmd.deps.App
	if v, err := a.Mux.Version(ctx); err != nil {
		add(checkFail, "tmux", err.Error()+"; install tmux 3.2 or newer")
	} else if major, minor, ok := parseTmuxVersion(v); !ok {
		add(checkWarn, "tmux", v+" (could not read the version, need 3.2 or newer)")
	} else if major < minTmux[0] || (major == minTmux[0] && minor < minTmux[1]) {
		add(checkFail, "tmux", fmt.Sprintf("%s is older than %d.%d; upgrade tmux", v, minTmux[0], minTmux[1]))
	} else {
		add(checkPass, "tmux", v)
	}

	if err := writable(a.Store.Dir()); err != nil {
		add(checkFail, "state directory", fmt.Sprintf("%s: %v", a.Store.Dir(), err))
	} else {
		add(checkPass, "state directory", a.Store.Dir())
	}

	if stale, err := a.StaleStacks(ctx); err != nil {
		add(checkFail, "stale stacks", err.Error())
	} else if len(stale) > 0 {
		names := make([]string, len(stale))
		for i, id := range stale {
			names[i] = id.String()
		}
		add(checkWarn, "stale stacks", "marked down, their sessions were gone: "+strings.Join(names, ", "))
	} else {
		add(checkPass, "stale stacks", "none")
	}

	if _, warnings, err := a.Preview(ctx, cmd.stack.target()); err != nil {
		if errors.Is(err, app.ErrNoProject) {
			add(checkInfo, "project", "no svx.yaml here")
		} else {
			add(checkFail, "project", err.Error())
		}
	} else if len(warnings) > 0 {
		add(checkWarn, "project", strings.Join(warnings, "; "))
	} else {
		add(checkPass, "project", "config is valid")
	}

	cmd.menuBarChecks(ctx, add)

	if cmd.flags.JSON {
		if err := writeJSON(os.Stdout, map[string]any{"version": domain.RecordVersion, "checks": checks}); err != nil {
			return err
		}
	} else {
		printChecks(os.Stdout, newStyle(os.Stdout, cmd.flags.NoColor), checks)
	}
	for _, c := range checks {
		if c.Status == checkFail {
			return errors.New("doctor found problems")
		}
	}
	return nil
}

func (cmd *DoctorCmd) menuBarChecks(ctx context.Context, add func(checkStatus, string, string)) {
	switch {
	case !cmd.deps.Config.MenuBar.Enabled:
		add(checkInfo, "menu bar", "turned off in config")
		return
	case !menubar.Supported:
		add(checkInfo, "menu bar", menubar.ErrUnsupported.Error())
		return
	}
	path, err := menubar.AgentPath()
	if err != nil {
		add(checkWarn, "menu bar", err.Error())
		return
	}
	data, err := os.ReadFile(path) //nolint:gosec // the login item svx writes
	switch {
	case err != nil:
		add(checkInfo, "menu bar", "not set to start at login, run svx bar --install")
	case !strings.Contains(string(data), cmd.deps.App.Executable):
		add(checkWarn, "menu bar", path+" starts a different svx, run svx bar --install")
	case !menubar.AgentLoaded(ctx):
		add(checkWarn, "menu bar", path+" is not loaded, run svx bar --install")
	default:
		add(checkPass, "menu bar", path)
	}
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

func parseTmuxVersion(v string) (int, int, bool) {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major, minor, true
}

func writable(dir string) error {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return err
	}
	_ = f.Close()
	return os.Remove(f.Name())
}

func printChecks(w io.Writer, st style, checks []check) {
	tw := table(w)
	for _, c := range checks {
		var mark string
		switch c.Status {
		case checkPass:
			mark = st.green("✓")
		case checkWarn:
			mark = st.yellow("~")
		case checkFail:
			mark = st.red("✗")
		case checkInfo:
			mark = st.dim("-")
		}
		_, _ = fmt.Fprintf(tw, "%s %s\t%s\n", mark, c.Name, st.dim(c.Detail))
	}
	_ = tw.Flush()
}
