# svx

Runs a repo's dev services as tmux windows. `svx up` runs inside tmux and adds the windows to the session you're in. Each stack gets its own name, ports and variables, so several copies of one repo can run at once. tmux holds the processes, and a small state directory tells the menu bar what is running.

The design is in [SPEC.md](./SPEC.md). Where this implementation differs, the differences are listed under [Differences from the spec](#differences-from-the-spec).

## Installation

```bash
go install github.com/hay-kot/svx@latest
```

Needs tmux 3.2 or newer. `svx doctor` checks. The macOS menu bar needs a build with cgo, which `go install` on a Mac is by default.

## Quick start

```bash
svx init        # writes svx.yaml and adds .svx/ to .gitignore (--stealth: .git/info/exclude)
svx up          # starts every autostart service in dependency order
svx             # same as svx status
svx logs web    # read a service's output
svx down
```

Try it on the demo project, which shows dependency ordering with a live progress view:

```bash
mise run demo          # db and cache start together, migrate waits for db, api waits for both
cd testdata/demo && svx up flaky   # a service that fails, and what svx shows for it
mise run demo:down
```

`testdata/sample` is a smaller project the end-to-end tests use.

## Configuration

`svx.yaml` lives at the repo root and is found by walking up from the current directory. `svx.local.yaml` beside it is merged on top for personal overrides (deep merge, keep it gitignored). A repo without an `svx.yaml` can use one kept elsewhere, such as your dotfiles, through `projects` in the [user config](#user-config).

To use svx in a repo without committing anything, run `svx init --stealth`. It ignores `svx.yaml`, `svx.local.yaml` and `.svx/` in `.git/info/exclude`, which git never tracks, instead of `.gitignore`.

```yaml
project: shop # optional, defaults to the main checkout's directory name

settings:
  port_stride: 10 # each slot offsets every port by this much
  max_slots: 50
  logs: false # true also writes each window to .svx/<stack>/logs/<service>.log
  menubar: true # false hides this project's stacks from the menu bar
  retries: 0 # default for services: reruns after a non-zero exit
  window_name: "shop.{{ .service.name }}" # tmux window name, defaults to the service name

vars:
  db_name: "shop_{{ snake .stack.name }}"

env:
  REDIS_URL: 'redis://127.0.0.1:{{ port "redis" }}'
  DATABASE_NAME: "{{ .vars.db_name }}"

services:
  redis:
    run: "redis-server --port {{ .port }} --dir {{ .stack.dir }}"
    port: 6379

  migrate:
    run: bin/migrate
    oneshot: true # must exit 0 before dependents start
    depends: [redis]

  api:
    run: go run ./cmd/api
    dir: api
    port: 8080
    depends: [migrate]
    ready: { http: /healthz, timeout: 30s }
    links: { API: /, Docs: /docs }
    stop_signal: SIGINT
    stop_timeout: 5s
    retries: 3 # rerun up to 3 times if it crashes

  web:
    run: "npm run dev -- --port {{ .port }} --strictPort"
    dir: web
    port: 5173
    ports: { hmr: 24678 }
    depends: [api]
    env:
      VITE_API_URL: '{{ url "api" }}'
    links: { App: / }

  worker:
    run: go run ./cmd/worker
    depends: [redis]
    autostart: false # only starts when named: svx up worker
```

Service keys: `run` (required), `dir`, `port` (number or `auto`), `ports`, `env`, `depends`, `ready` (one of `http`, `cmd`, `output`, `delay`, plus `timeout`), `oneshot`, `links`, `autostart`, `retries`, `stop_signal`, `stop_timeout`. Unknown keys are errors.

`retries` reruns a command in its window after a non-zero exit, one second apart, and the window prints each retry. It covers crashes after `svx up` returns too. An exit that follows `svx down` or `svx restart` is never retried. A oneshot retries until it succeeds or runs out.

Without `ready`, a service with a `port` is ready when the port accepts a TCP connection, and one without is ready after staying up for a second.

### Templates

`run`, `dir`, `env` values, `vars`, `links`, `ready` values and `settings.window_name` are Go templates with `missingkey=error`, so a typo fails `svx up` instead of rendering empty.

| Expression                                                   | Value                                                                      |
| ------------------------------------------------------------ | -------------------------------------------------------------------------- |
| `.project.name`, `.project.root`                             | Project name and checkout root                                             |
| `.stack.name`, `.stack.slot`, `.stack.dir`, `.stack.session` | Stack identity and its data directory                                      |
| `.service.name`                                              | The service being rendered (not in `vars` or top-level `env`)              |
| `.port`, `.ports.<name>`                                     | This service's ports                                                       |
| `.vars.<name>`                                               | A value from `vars`                                                        |
| `port "svc"`, `port "svc" "name"`                            | Another service's port                                                     |
| `url "svc"`                                                  | Another service's base URL                                                 |
| `env "NAME"`                                                 | A variable from the shell `svx up` ran in                                  |
| `secret "env:NAME"`, `secret "file:/path"`                   | A secret, resolved with [appkit/secret](https://github.com/hay-kot/appkit) |
| `snake`, `slug`, `upper`, `lower`                            | String helpers                                                             |

Every service also gets `SVX_PROJECT`, `SVX_STACK`, `SVX_SLOT`, `SVX_STACK_DIR`, `SVX_SERVICE`, `PORT`, `PORT_<NAME>`, `SVX_PORT_<SERVICE>[_<NAME>]` and `SVX_URL_<SERVICE>`. Later sources win: your shell, then injected variables, then top-level `env`, then the service's `env`.

Run `svx config` to see everything rendered for your stack without starting anything.

### User config

`~/.config/svx/config.yaml`:

```yaml
log_level: info
menubar:
  enabled: true # false: `svx bar` exits at once, so the menu bar stays off
projects: # svx.yaml files for repos that have none of their own
  shop: ~/dotfiles/svx/shop.yaml # checkout directory name
  ~/code/api: ~/dotfiles/svx/api.yaml # full path
  'shop-*': svx/shop.yaml # glob, relative to this config file
  '~/work/*/web': ~/dotfiles/svx/web.yaml # a glob with a / matches the full path
```

`projects` applies only when no `svx.yaml` is found walking up. svx looks up the checkout root (the nearest directory with `.git`, else the current directory) in this order: full path, directory name, then globs, where the longest matching glob wins. The checkout root becomes the project root, so service `dir`s, `svx.local.yaml` and `.svx/` resolve against the repo as usual. Exclude `.svx/` in `.git/info/exclude` to keep it out of `git status`.

## Commands

| Command                    | What it does                                                                                            |
| -------------------------- | ------------------------------------------------------------------------------------------------------- |
| `svx up [service...]`      | Start the stack or the named services plus dependencies. Safe to repeat. `--name`, `--no-wait`          |
| `svx down`                 | Stop the stack. `--all`, `--purge`                                                                      |
| `svx status`               | Services, state, ports and links. `--json`                                                              |
| `svx ls`                   | Every stack on the machine. `--json`                                                                    |
| `svx restart [service...]` | Restart in place, keeping ports, in dependency order. `--all` restarts every started service            |
| `svx logs <service>`       | Print a log, or the window's scrollback when logs are off. `-f` needs `logs: true`. `--raw`, `-n`       |
| `svx env`                  | Print the stack's variables. `--format shell\|dotenv\|json`, `--service`                                |
| `svx exec -- <cmd>`        | Run a command with the stack's environment                                                              |
| `svx config`               | Print the rendered config                                                                               |
| `svx bar`                  | Run the menu bar. `--install` starts it now and at login, `--uninstall` removes that                    |
| `svx init`                 | Write a starter `svx.yaml`. `--stealth` ignores svx's files in `.git/info/exclude`, `svx.yaml` included |
| `svx doctor`               | Check tmux, state, stale stacks and the menu bar                                                        |

Commands act on the current checkout's stack. `--name` (or `SVX_STACK`) picks another stack name, which is how one checkout runs two stacks. `--project` with `--name` targets a stack without reading any config.

Exit codes: 0 success, 1 a service failed or timed out, 2 a config or usage error.

## Windows and sessions

`svx up` adds the stack's windows to your current session, in the background, so your current window keeps focus. It refuses to run outside tmux: svx never creates sessions. A running stack stays in the session it started in, whichever session a later command runs from. Other commands, such as `down`, `restart` and the menu bar, work from anywhere.

svx finds its windows by the `@svx_stack` and `@svx_service` window options, not by name, so your own windows are never touched.

Each window is named after its service. Set `settings.window_name` to change that: it renders once per service with the same template values as `run`, so `"shop.{{ .service.name }}"` gives windows such as `shop.api`. The name is only for display, so `.` and `:` are fine, and `#` shows as typed rather than as a tmux format. A name that renders empty, has control characters, or repeats another service's name is a config error. `svx config` shows each service's `window`. `svx restart` keeps a window's existing name. `svx down` closes only the stack's windows and never kills a session.

## Menu bar

The menu bar is a native macOS status item, built the same way as the [Wails v3](https://github.com/wailsapp/wails) system tray: Cocoa through cgo, with no Dock icon.

```bash
svx bar             # run it in the foreground
svx bar --install   # run it now and at every login, as a launchd agent
svx bar --uninstall
```

The icon shows how many stacks are running. Each stack is a bold row with a health dot and a summary (`5 running`, `1 failed`); hover it for Restart All Services, Stop Stack and a restart for each service. Under it, each running service with a URL shows its port and opens on click. Hold ⌥ and the same rows read "Restart <service>". Failed services show in red and restart on click. Running services with nothing to open share one "also running" line, and finished oneshots and idle services stay out of the list. Restarts follow dependency order. The menu re-reads state every two seconds and whenever it opens.

To opt out, set `menubar.enabled: false` in the user config, or `settings.menubar: false` in a project to hide just that project. The login item copies your `PATH` at install time, since launchd starts agents without your shell profile; run `svx bar --install` again after moving tmux or svx.

## State

`$XDG_STATE_HOME/svx` (default `~/.local/state/svx`, override with `SVX_STATE_DIR`) holds:

- `stacks/<project>--<stack>.json` -- the public record other tools can read. Its shape is versioned.
- `stacks/<project>--<stack>.env.json` -- rendered commands and the environment snapshot, mode 0600.
- `lock` -- taken while allocating slots.
- `menubar.lock` and `menubar.log` -- the menu bar's single-instance lock and its log when run by launchd.

tmux is the source of truth for what is running. A stack is running while its session holds at least one of its tagged windows. Records that tmux contradicts are marked down whenever svx reads them.

## Layout

The code follows ports and adapters:

| Package                         | Role                                                                                                                           |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `internal/domain`               | The model: definitions, identity and naming, the dependency graph, slot and port math, validation, template rendering. No I/O. |
| `internal/app`                  | Use cases (`Up`, `Down`, `Restart`, `Status`, ...) and the port interfaces they drive. Unit tested against fakes.              |
| `internal/adapters/yamlproject` | Finds, merges and maps `svx.yaml`                                                                                              |
| `internal/adapters/statefs`     | State directory, flock, atomic writes                                                                                          |
| `internal/adapters/tmux`        | The tmux binary                                                                                                                |
| `internal/adapters/readiness`   | HTTP, TCP, command and output readiness probes                                                                                 |
| `internal/adapters/menubar`     | The macOS menu bar app and its launchd login item                                                                              |
| `internal/adapters/system`      | Signals, port probes, files, browser, secrets                                                                                  |
| `internal/commands`             | CLI wiring and output only                                                                                                     |

## Development

```bash
mise run test          # unit tests, including tmux behaviour tests on a private socket
mise run integration   # end-to-end tests against a built binary
mise run lint
```

Tests never touch your tmux server or state: they use `tmux -L svx-test-<random> -f /dev/null`, `SVX_TMUX_SOCKET`, `SVX_TMUX_CONFIG` and `SVX_STATE_DIR`.

## Differences from the spec

- YAML instead of TOML: `svx.yaml` and `svx.local.yaml`.
- The proxy and HTTPS milestone is not built. `url` and links always render as `http://127.0.0.1:<port>`.
- `svx up` must run inside tmux and adds windows to the current session instead of creating one session per stack. Windows are found by tag, and `down` closes windows rather than killing a session.
- The menu bar is a native Cocoa status item run by `svx bar` (a launchd agent once installed), not SwiftBar. It is a long-running process, which the spec listed as a later option.
- The menu bar is opt-out in the user config, and per project with `settings.menubar`.
- Windows are named after their service by default, and `settings.window_name` can rename them. The name is display only.
- The package layout follows ports and adapters rather than the spec's package table. The responsibilities match.
- `svx up --no-wait` still starts windows in dependency order, but does not wait for readiness between them.
- A oneshot that already exited 0 counts as done on a repeat `up` and is not rerun. Use `svx restart <name>` to rerun it.
- `svx restart` re-renders the config against the recorded slot, so config edits apply without moving ports.
- `svx logs` takes `-n` to limit output to the last lines.
- `settings.logs` defaults to false. `ready.output` reads the window's tmux history instead of the log file, so it works either way.
- The public record adds `dir`, `menubar`, `service_order` and per-service `oneshot` fields. `session` is the session `up` ran from.
