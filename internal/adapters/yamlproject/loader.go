// Package yamlproject loads svx.yaml, merges svx.local.yaml over it, and maps
// the result onto the domain definition.
package yamlproject

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/hay-kot/svx/internal/app"
	"github.com/hay-kot/svx/internal/domain"
)

// File names searched for, in order of preference.
var (
	mainNames  = []string{"svx.yaml", "svx.yml"}
	localNames = []string{"svx.local.yaml", "svx.local.yml"}
)

// Repository implements app.ProjectRepository on the filesystem.
type Repository struct {
	// Projects maps checkouts to config files kept outside the repo, from
	// the user config. Load consults it only when no svx.yaml is found.
	Projects map[string]string
}

var _ app.ProjectRepository = Repository{}

// Load walks up from dir to the nearest svx.yaml. When there is none, it
// looks up the checkout root (the nearest directory holding .git, else dir
// itself) in r.Projects and uses that root with the mapped config file.
func (r Repository) Load(dir string) (app.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return app.Project{}, err
	}
	root, path, err := r.find(abs)
	if err != nil {
		return app.Project{}, err
	}

	def, err := loadFiles(path, findIn(root, localNames))
	if err != nil {
		return app.Project{}, err
	}
	return app.Project{Def: def, Root: root, CheckoutName: checkoutName(root)}, nil
}

func (r Repository) find(abs string) (root, path string, err error) {
	if p := walkUp(abs, func(cur string) string { return findIn(cur, mainNames) }); p != "" {
		return filepath.Dir(p), p, nil
	}
	if len(r.Projects) > 0 {
		// Only the checkout root is matched: matching every parent would let
		// a broad glob claim a subdirectory before a specific one reaches
		// the root.
		root := walkUp(abs, func(cur string) string {
			if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
				return cur
			}
			return ""
		})
		if root == "" {
			root = abs
		}
		if file := matchProject(r.Projects, root); file != "" {
			if _, err := os.Stat(file); err != nil {
				return "", "", fmt.Errorf("config for %s: %w", root, err)
			}
			return root, file, nil
		}
	}
	return "", "", &app.UsageError{Err: fmt.Errorf("%w in %s or any parent directory, create one with `svx init`", app.ErrNoProject, abs)}
}

// walkUp calls fn on dir and each parent in turn and returns the first
// non-empty result.
func walkUp(dir string, fn func(string) string) string {
	for cur := dir; ; cur = filepath.Dir(cur) {
		if v := fn(cur); v != "" {
			return v
		}
		if filepath.Dir(cur) == cur {
			return ""
		}
	}
}

// matchProject returns the config file projects maps dir to, trying in
// order: an exact full path, an exact directory name, then globs. A glob
// with a path separator matches the full path, others match the directory
// name. When several globs match, the longest wins, so a specific pattern
// beats a catch-all; equal lengths fall back to sort order so the pick is
// stable.
func matchProject(projects map[string]string, dir string) string {
	if file, ok := projects[dir]; ok {
		return file
	}
	base := filepath.Base(dir)
	if file, ok := projects[base]; ok {
		return file
	}

	var best string
	for pattern := range projects {
		target := base
		if strings.ContainsRune(pattern, filepath.Separator) {
			target = dir
		}
		ok, err := filepath.Match(pattern, target)
		if err != nil || !ok {
			continue
		}
		if len(pattern) > len(best) || (len(pattern) == len(best) && pattern < best) {
			best = pattern
		}
	}
	if best == "" {
		return ""
	}
	return projects[best]
}

func findIn(dir string, names []string) string {
	for _, n := range names {
		p := filepath.Join(dir, n)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func loadFiles(mainPath, localPath string) (domain.Definition, error) {
	merged, err := parseStrict(mainPath)
	if err != nil {
		return domain.Definition{}, err
	}
	source := mainPath
	if localPath != "" {
		local, err := parseStrict(localPath)
		if err != nil {
			return domain.Definition{}, err
		}
		mergeNodes(merged, local)
		source = mainPath + " (with " + filepath.Base(localPath) + ")"
	}

	var fc fileConfig
	if err := merged.Decode(&fc); err != nil {
		return domain.Definition{}, &domain.ConfigError{File: source, Key: "", Msg: cleanYAMLError(err)}
	}
	return toDefinition(source, fc, merged)
}

// parseStrict reads one file into a node and checks it for unknown keys on
// its own, so the error names the file the key is in. The merged node is
// decoded afterwards without the check.
func parseStrict(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the config search
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, &domain.ConfigError{File: path, Key: "", Msg: cleanYAMLError(err)}
	}
	if len(doc.Content) == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, &domain.ConfigError{File: path, Key: "", Msg: "the top level must be a mapping"}
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var fc fileConfig
	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return nil, &domain.ConfigError{File: path, Key: "", Msg: cleanYAMLError(err)}
	}
	return root, nil
}

var (
	unknownFieldRe = regexp.MustCompile(`field (\S+) not found in type \S+`)
	wrongTypeRe    = regexp.MustCompile(`cannot unmarshal !!(\w+)(?: .*?)? into \S+`)
)

// cleanYAMLError rewords the decoder's messages for people editing the file
// rather than reading Go types.
func cleanYAMLError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")
	msg = strings.Replace(msg, "unmarshal errors:\n  ", "", 1)
	msg = strings.ReplaceAll(msg, "\n  ", "; ")
	msg = unknownFieldRe.ReplaceAllString(msg, `unknown key "$1"`)
	return wrongTypeRe.ReplaceAllString(msg, "wrong type ($1)")
}

// mergeNodes deep-merges src mappings into dst. Scalars and sequences in src
// replace dst's, and new keys keep src's order after dst's.
func mergeNodes(dst, src *yaml.Node) {
	if dst.Kind != yaml.MappingNode || src.Kind != yaml.MappingNode {
		*dst = *src
		return
	}
	for i := 0; i+1 < len(src.Content); i += 2 {
		key, val := src.Content[i], src.Content[i+1]
		if existing := lookup(dst, key.Value); existing != nil {
			if existing.Kind == yaml.MappingNode && val.Kind == yaml.MappingNode {
				mergeNodes(existing, val)
			} else {
				*existing = *val
			}
			continue
		}
		dst.Content = append(dst.Content, key, val)
	}
}

func lookup(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// keyOrder returns mapping keys in document order. Go maps lose it, and it
// decides window order and menu order.
func keyOrder(m *yaml.Node) []string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	keys := make([]string, 0, len(m.Content)/2)
	for i := 0; i+1 < len(m.Content); i += 2 {
		keys = append(keys, m.Content[i].Value)
	}
	return keys
}

func toDefinition(source string, fc fileConfig, root *yaml.Node) (domain.Definition, error) {
	def := domain.Definition{
		Source:  source,
		Project: fc.Project,
		Settings: domain.Settings{
			PortStride: valueOr(fc.Settings.PortStride, domain.DefaultPortStride),
			MaxSlots:   valueOr(fc.Settings.MaxSlots, domain.DefaultMaxSlots),
			Logs:       valueOr(fc.Settings.Logs, false),
			MenuBar:    valueOr(fc.Settings.MenuBar, true),
		},
		Vars:     orEmpty(fc.Vars),
		Env:      orEmpty(fc.Env),
		Services: nil,
	}

	var errs []error
	fail := func(key, format string, args ...any) {
		errs = append(errs, &domain.ConfigError{File: source, Key: key, Msg: fmt.Sprintf(format, args...)})
	}

	servicesNode := lookup(root, "services")
	for _, name := range keyOrder(servicesNode) {
		fs := fc.Services[name]
		key := "services." + name
		svc := domain.ServiceDef{
			Name:      name,
			Run:       fs.Run,
			Dir:       fs.Dir,
			Env:       orEmpty(fs.Env),
			Depends:   fs.Depends,
			Oneshot:   fs.Oneshot,
			Autostart: valueOr(fs.Autostart, true),
			Ports:     map[string]domain.PortSpec{},
			Ready:     domain.ReadySpec{Kind: domain.ReadyDefault, Value: "", Timeout: domain.DefaultReadyTimeout},
			Stop:      domain.StopSpec{Signal: domain.DefaultStopSignal, Timeout: domain.DefaultStopTimeout},
		}
		if fs.Port != nil {
			svc.Port = toPortSpec(*fs.Port)
		}
		for pname, pv := range fs.Ports {
			svc.Ports[pname] = toPortSpec(pv)
		}

		linksNode := lookup(lookup(servicesNode, name), "links")
		for _, label := range keyOrder(linksNode) {
			svc.Links = append(svc.Links, domain.LinkDef{Label: label, Target: fs.Links[label]})
		}

		if fs.Ready != nil {
			ready, err := toReady(*fs.Ready)
			if err != nil {
				fail(key+".ready", "%v", err)
			}
			svc.Ready = ready
		}
		if fs.StopSignal != "" {
			svc.Stop.Signal = fs.StopSignal
		}
		if fs.StopTimeout != "" {
			d, err := time.ParseDuration(fs.StopTimeout)
			if err != nil {
				fail(key+".stop_timeout", "%q is not a duration such as 10s", fs.StopTimeout)
			}
			svc.Stop.Timeout = d
		}
		def.Services = append(def.Services, svc)
	}

	if len(errs) > 0 {
		return def, errors.Join(errs...)
	}
	return def, nil
}

func toPortSpec(p portValue) domain.PortSpec {
	if p.Auto {
		return domain.AutoPort()
	}
	return domain.FixedPort(p.Base)
}

func toReady(fr fileReady) (domain.ReadySpec, error) {
	spec := domain.ReadySpec{Kind: domain.ReadyDefault, Value: "", Timeout: domain.DefaultReadyTimeout}
	if fr.Timeout != "" {
		d, err := time.ParseDuration(fr.Timeout)
		if err != nil {
			return spec, fmt.Errorf("timeout %q is not a duration such as 30s", fr.Timeout)
		}
		spec.Timeout = d
	}

	set := map[domain.ReadyKind]string{}
	if fr.HTTP != "" {
		set[domain.ReadyHTTP] = fr.HTTP
	}
	if fr.Cmd != "" {
		set[domain.ReadyCmd] = fr.Cmd
	}
	if fr.Output != "" {
		set[domain.ReadyOutput] = fr.Output
	}
	if fr.Delay != "" {
		if _, err := time.ParseDuration(fr.Delay); err != nil && !strings.Contains(fr.Delay, "{{") {
			return spec, fmt.Errorf("delay %q is not a duration such as 2s", fr.Delay)
		}
		set[domain.ReadyDelay] = fr.Delay
	}
	switch len(set) {
	case 0:
		return spec, nil
	case 1:
		for k, v := range set {
			spec.Kind, spec.Value = k, v
		}
		return spec, nil
	default:
		kinds := make([]string, 0, len(set))
		for _, k := range slices.Sorted(maps.Keys(set)) {
			kinds = append(kinds, string(k))
		}
		return spec, fmt.Errorf("set exactly one of http, cmd, output or delay, found %s", strings.Join(kinds, ", "))
	}
}

func valueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// checkoutName is the directory name of the main git checkout containing
// root, so every worktree of a repo shares a project name. Outside git it is
// root's own name.
func checkoutName(root string) string {
	for cur := root; ; cur = filepath.Dir(cur) {
		gitPath := filepath.Join(cur, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return filepath.Base(cur)
			}
			if main := worktreeMain(gitPath); main != "" {
				return filepath.Base(main)
			}
			return filepath.Base(cur)
		}
		if filepath.Dir(cur) == cur {
			return filepath.Base(root)
		}
	}
}

// worktreeMain follows a worktree's .git file to the main checkout: the file
// points at <main>/.git/worktrees/<name>, whose commondir file points back at
// <main>/.git.
func worktreeMain(gitFile string) string {
	data, err := os.ReadFile(gitFile) //nolint:gosec // a .git file inside the project
	if err != nil {
		return ""
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return ""
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(gitFile), gitdir)
	}

	common := gitdir
	if data, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil { //nolint:gosec // inside the git dir
		common = strings.TrimSpace(string(data))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitdir, common)
		}
	}
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}
