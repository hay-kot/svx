package yamlproject

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hay-kot/svx/internal/app"
)

const starter = `# svx.yaml -- services svx runs as windows in a tmux session.
# Run ` + "`svx guide`" + ` for how to write this file, and ` + "`svx config`" + ` to see it
# rendered for your stack.

# project: myapp            # defaults to the main checkout's directory name

# settings:                 # every value below is the default
  # port_stride: 10         # every port is base + slot * port_stride
  # max_slots: 50
  # logs: false             # true also writes each window to .svx/<stack>/logs/<service>.log
  # menubar: true           # list this project's stacks in the menu bar
  # retries: 0              # rerun a service this many times after a crash
  # window_name: "{{ .service.name }}"  # tmux window name, a template

# vars:
#   db_name: 'myapp_{{ snake .stack.name }}'

# env:                      # set for every service, values are templates
#   DATABASE_NAME: '{{ .vars.db_name }}'

services:
  web:
    run: python3 -m http.server $PORT
    port: 8000
    links:
      App: /
  # api:
  #   run: go run ./cmd/api
  #   dir: api
  #   port: 8080
  #   depends: [db]
  #   ready: { http: /healthz, timeout: 30s }
  # migrate:
  #   run: bin/migrate
  #   oneshot: true
  # worker:
  #   run: go run ./cmd/worker
  #   autostart: false      # only starts when named: svx up worker
`

// Init writes a starter svx.yaml in dir and adds svx's data directory and
// local overrides to .gitignore. With opts.Stealth it ignores svx.yaml too,
// in the repo's info/exclude file, which git never tracks.
func (Repository) Init(dir string, opts app.InitOptions) ([]string, error) {
	if existing := findIn(dir, mainNames); existing != "" {
		return nil, fmt.Errorf("%s already exists", existing)
	}

	ignore := filepath.Join(dir, ".gitignore")
	lines := []string{".svx/", "svx.local.yaml"}
	if opts.Stealth {
		// Look up the exclude file before writing anything, so outside a
		// repo init fails without leaving a stray svx.yaml.
		exclude, err := excludeFile(dir)
		if err != nil {
			return nil, err
		}
		ignore = exclude
		lines = append([]string{mainNames[0]}, lines...)
	}

	var changed []string
	path := filepath.Join(dir, mainNames[0])
	if err := os.WriteFile(path, []byte(starter), 0o644); err != nil { //nolint:gosec // a config file meant to be committed
		return nil, err
	}
	changed = append(changed, path)

	if err := os.MkdirAll(filepath.Dir(ignore), 0o750); err != nil {
		return changed, err
	}
	added, err := ensureLines(ignore, lines...)
	if err != nil {
		return changed, err
	}
	if added {
		changed = append(changed, ignore)
	}
	return changed, nil
}

func ensureLines(path string, lines ...string) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the project's .gitignore
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	have := map[string]bool{}
	for l := range strings.SplitSeq(string(data), "\n") {
		have[strings.TrimSpace(l)] = true
	}

	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteString("\n")
	}
	added := false
	for _, l := range lines {
		if have[l] || have["/"+l] {
			continue
		}
		b.WriteString(l + "\n")
		added = true
	}
	if !added {
		return false, nil
	}
	return true, os.WriteFile(path, []byte(b.String()), 0o644) //nolint:gosec // .gitignore is meant to be committed
}

// excludeFile returns the info/exclude path of the repo containing dir. Git
// resolves it, since a worktree shares the main checkout's exclude file and
// its .git is a file rather than a directory.
func excludeFile(dir string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", &app.UsageError{Err: fmt.Errorf("--stealth needs a git repository: %s is not inside one", dir)}
	}
	return strings.TrimSpace(string(out)), nil
}
