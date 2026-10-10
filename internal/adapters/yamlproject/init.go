package yamlproject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const starter = `# svx.yaml -- services svx runs as windows in a tmux session.
# Reference: run ` + "`svx config`" + ` to see this file rendered for your stack.

# project: myapp            # defaults to the main checkout's directory name

settings:
  port_stride: 10           # every port is base + slot * port_stride
  # max_slots: 50
  # logs: true              # pipe each window to .svx/<stack>/logs/<service>.log
  # menubar: true           # list this project's stacks in the menu bar

# vars:
#   db_name: 'myapp_{{ snake .stack.name }}'

# env:                      # set for every service, values are templates
#   DATABASE_NAME: '{{ .vars.db_name }}'

services:
  web:
    run: 'python3 -m http.server {{ .port }}'
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
// local overrides to .gitignore.
func (Repository) Init(dir string) ([]string, error) {
	if existing := findIn(dir, mainNames); existing != "" {
		return nil, fmt.Errorf("%s already exists", existing)
	}

	var changed []string
	path := filepath.Join(dir, mainNames[0])
	if err := os.WriteFile(path, []byte(starter), 0o644); err != nil { //nolint:gosec // a config file meant to be committed
		return nil, err
	}
	changed = append(changed, path)

	ignore := filepath.Join(dir, ".gitignore")
	added, err := ensureLines(ignore, ".svx/", "svx.local.yaml")
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
