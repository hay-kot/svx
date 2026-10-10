package commands

import (
	"context"
	"fmt"
	"maps"
	"os"
	"time"

	"github.com/urfave/cli/v3"
	"go.yaml.in/yaml/v3"

	"github.com/hay-kot/svx/internal/domain"
)

// ConfigCmd implements the config command.
type ConfigCmd struct {
	flags *Flags
	deps  *Deps
	stack stackFlags
}

// NewConfigCmd creates a new config command.
func NewConfigCmd(flags *Flags, deps *Deps) *ConfigCmd {
	return &ConfigCmd{flags: flags, deps: deps}
}

// Register adds the config command to the application.
func (cmd *ConfigCmd) Register(app *cli.Command) *cli.Command {
	app.Commands = append(app.Commands, &cli.Command{
		Name:   "config",
		Usage:  "print the fully rendered config for this stack without starting anything",
		Flags:  cmd.stack.flags(),
		Action: cmd.run,
	})
	return app
}

type configService struct {
	Run     string            `json:"run"               yaml:"run"`
	Dir     string            `json:"dir"               yaml:"dir"`
	Ports   map[string]int    `json:"ports,omitempty"   yaml:"ports,omitempty"`
	URL     string            `json:"url,omitempty"     yaml:"url,omitempty"`
	Depends []string          `json:"depends,omitempty" yaml:"depends,omitempty"`
	Oneshot bool              `json:"oneshot,omitempty" yaml:"oneshot,omitempty"`
	Ready   string            `json:"ready"             yaml:"ready"`
	Links   map[string]string `json:"links,omitempty"   yaml:"links,omitempty"`
	Env     map[string]string `json:"env,omitempty"     yaml:"env,omitempty"`
	Log     string            `json:"log,omitempty"     yaml:"log,omitempty"`
}

func (cmd *ConfigCmd) run(ctx context.Context, _ *cli.Command) error {
	stack, warnings, err := cmd.deps.App.Preview(ctx, cmd.stack.target())
	if err != nil {
		return err
	}
	st := newStyle(os.Stderr, cmd.flags.NoColor)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(os.Stderr, "%s %s\n", st.yellow("warning:"), w)
	}
	if stack.Status != domain.StackUp {
		_, _ = fmt.Fprintf(os.Stderr, "%s\n", st.dim(fmt.Sprintf("# not running: slot %d is what `svx up` would pick now, nothing is reserved", stack.Slot)))
	}

	services := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	jsonServices := map[string]configService{}
	for _, svc := range stack.Services {
		cs := configService{
			Run:     svc.Run,
			Dir:     svc.Dir,
			Ports:   svc.Ports,
			URL:     svc.URL,
			Depends: svc.Depends,
			Oneshot: svc.Oneshot,
			Ready:   describeReady(svc),
			Links:   map[string]string{},
			Env:     serviceOnlyEnv(stack.Env, svc.Env),
			Log:     svc.LogFile,
		}
		for _, l := range svc.Links {
			cs.Links[l.Label] = l.URL
		}
		jsonServices[svc.Name] = cs

		var val yaml.Node
		if err := val.Encode(cs); err != nil {
			return err
		}
		services.Content = append(services.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: svc.Name}, &val)
	}

	view := map[string]any{
		"project": stack.ID.Project,
		"stack":   stack.ID.Stack,
		"session": stack.Session,
		"root":    stack.Root,
		"dir":     stack.Dir,
		"slot":    stack.Slot,
		"status":  string(stack.Status),
		"env":     stack.Env,
	}
	if cmd.flags.JSON {
		view["version"] = domain.RecordVersion
		view["services"] = jsonServices
		return writeJSON(os.Stdout, view)
	}

	doc := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, k := range []string{"project", "stack", "session", "root", "dir", "slot", "status", "env"} {
		var v yaml.Node
		if err := v.Encode(view[k]); err != nil {
			return err
		}
		doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, &v)
	}
	doc.Content = append(doc.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "services"}, services)

	enc := yaml.NewEncoder(os.Stdout)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return enc.Close()
}

func describeReady(svc domain.Service) string {
	timeout := svc.Ready.Timeout.String()
	switch {
	case svc.Oneshot:
		return "exits 0"
	case svc.Ready.Kind == domain.ReadyDefault:
		if p, ok := svc.MainPort(); ok {
			return fmt.Sprintf("tcp %d (timeout %s)", p, timeout)
		}
		return fmt.Sprintf("alive for %s", time.Second)
	default:
		return fmt.Sprintf("%s %s (timeout %s)", svc.Ready.Kind, svc.Ready.Value, timeout)
	}
}

// serviceOnlyEnv drops what the stack-wide env already shows, so each
// service lists only what is its own.
func serviceOnlyEnv(stackEnv, svcEnv map[string]string) map[string]string {
	out := maps.Clone(svcEnv)
	for k, v := range stackEnv {
		if out[k] == v {
			delete(out, k)
		}
	}
	return out
}
