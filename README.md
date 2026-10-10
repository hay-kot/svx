# svx

Runs a repo's dev services as tmux windows. Run inside tmux, `svx up` adds the windows to the session you're in; outside tmux, each stack gets its own session. Each stack gets its own name, ports and variables, so several copies of one repo can run at once. tmux holds the processes, and a small state directory tells the menu bar what is running.

The design is in [SPEC.md](./SPEC.md). Where this implementation differs, the differences are listed under [Differences from the spec](#differences-from-the-spec).

## Installation

```bash
go install github.com/hay-kot/svx@latest
```

Needs tmux 3.2 or newer. `svx doctor` checks. The macOS menu bar needs a build with cgo, which `go install` on a Mac is by default.

## Quick start

```bash
svx init        # writes svx.yaml and adds .svx/ to .gitignore
svx up          # starts every autostart service in dependency order
svx             # same as svx status
svx attach web  # jump into a service's window
svx down
```

Try it on the sample project:

```bash
cd testdata/sample && svx up
```

## Configuration

`svx.yaml` lives at the repo root and is found by walking up from the current directory. `svx.local.yaml` beside it is merged on top for personal overrides (deep merge, keep it gitignored).

```yaml
project: shop               # optional, defaults to the main checkout's directory name

settings:
  port_stride: 10           # each slot offsets every port by this much
  max_slots: 50
  logs: true                # pipe windows to .svx/<stack>/logs/<service>.log
  menubar: true             # false hides this project's stacks from the menu bar

vars:
  db_name: 'shop_{{ snake .stack.name }}'

env:
  REDIS_URL: 'redis://127.0.0.1:{{ port "redis" }}'
  DATABASE_NAME: '{{ .vars.db_name }}'

services:
  redis:
    run: 'redis-server --port {{ .port }} --dir {{ .stack.dir }}'
    port: 6379

  migrate:
    run: bin/migrate
    oneshot: true            # must exit 0 before dependents start
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

  web:
    run: 'npm run dev -- --port {{ .port }} --strictPort'
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
    autostart: false         # only starts when named: svx up worker
```

Service keys: `run` (required), `dir`, `port` (number or `auto`), `ports`, `env`, `depends`, `ready` (one of `http`, `cmd`, `output`, `delay`, plus `timeout`), `oneshot`, `links`, `autostart`, `stop_signal`, `stop_timeout`. Unknown keys are errors.

Without `ready`, a service with a `port` is ready when the port accepts a TCP connection, and one without is ready after staying up for a second.

### Templates

`run`, `dir`, `env` values, `vars`, `links` and `ready` values are Go templates with `missingkey=error`, so a typo fails `svx up` instead of rendering empty.

| Expression | Value |
| --- | --- |
| `.project.name`, `.project.root` | Project name and checkout root |
| `.stack.name`, `.stack.slot`, `.stack.dir`, `.stack.session` | Stack identity and its data directory |
| `.service.name` | The service being rendered (not in `vars` or top-level `env`) |
| `.port`, `.ports.<name>` | This service's ports |
| `.vars.<name>` | A value from `vars` |
| `port "svc"`, `port "svc" "name"` | Another service's port |
| `url "svc"` | Another service's base URL |
| `env "NAME"` | A variable from the shell `svx up` ran in |
| `secret "env:NAME"`, `secret "file:/path"` | A secret, resolved with [appkit/secret](https://github.com/hay-kot/appkit) |
| `snake`, `slug`, `upper`, `lower` | String helpers |

Every service also gets `SVX_PROJECT`, `SVX_STACK`, `SVX_SLOT`, `SVX_STACK_DIR`, `SVX_SERVICE`, `PORT`, `PORT_<NAME>`, `SVX_PORT_<SERVICE>[_<NAME>]` and `SVX_URL_<SERVICE>`. Later sources win: your shell, then injected variables, then top-level `env`, then the service's `env`.

Run `svx config` to see everything rendered for your stack without starting anything.

### User config

`~/.config/svx/config.yaml`:

```yaml
log_level: info
menubar:
  enabled: true       # false: `svx bar` exits at once, so the menu bar stays off
  terminal: Terminal  # the app "Attach in Terminal" opens, e.g. iTerm
```

## Commands

| Command | What it does |
| --- | --- |
| `svx up [service...]` | Start the stack or the named services plus dependencies. Safe to repeat. `--name`, `--attach`, `--no-wait` |
| `svx down` | Stop the stack. `--all`, `--purge` |
| `svx status` | Services, state, ports and links. `--json` |
| `svx ls` | Every stack on the machine. `--json` |
| `svx attach [service]` | Attach, or switch client when already in tmux |
| `svx restart <service...>` | Restart in place, keeping ports |
| `svx logs <service>` | Print a log. `-f`, `--raw`, `-n` |
| `svx env` | Print the stack's variables. `--format shell\|dotenv\|json`, `--service` |
| `svx exec -- <cmd>` | Run a command with the stack's environment |
| `svx open [link]` | Open a link, or list them |
| `svx config` | Print the rendered config |
| `svx bar` | Run the menu bar. `--install` starts it now and at login, `--uninstall` removes that |
| `svx init` | Write a starter `svx.yaml` |
| `svx doctor` | Check tmux, state, stale stacks and the menu bar |

Commands act on the current checkout's stack. `--name` (or `SVX_STACK`) picks another stack name, which is how one checkout runs two stacks. `--project` with `--name` targets a stack without reading any config.

Exit codes: 0 success, 1 a service failed or timed out, 2 a config or usage error.

## Windows and sessions

Inside tmux, `svx up` adds the stack's windows to your current session, in the background, so your current window keeps focus. Outside tmux, it creates `svx-<project>-<stack>` and leaves it detached; `svx attach` gets you there. A running stack stays in the session it started in, whichever session a later command runs from.

svx finds its windows by the `@svx_stack` and `@svx_service` window options, not by name, so your own windows are never touched. `svx down` closes only the stack's windows and never kills a session. A stack's own session ends with its last window.

## Menu bar

The menu bar is a native macOS status item, built the same way as the [Wails v3](https://github.com/wailsapp/wails) system tray: Cocoa through cgo, with no Dock icon.

```bash
svx bar             # run it in the foreground
svx bar --install   # run it now and at every login, as a launchd agent
svx bar --uninstall
```

The icon shows how many stacks are running. Each stack lists its links with their ports, services that hold a port but have no link, dead services, and "Attach in Terminal" and "Stop Stack" actions. The menu re-reads state every two seconds and whenever it opens.

To opt out, set `menubar.enabled: false` in the user config, or `settings.menubar: false` in a project to hide just that project. The login item copies your `PATH` at install time, since launchd starts agents without your shell profile; run `svx bar --install` again after moving tmux or svx.

## State

`$XDG_STATE_HOME/svx` (default `~/.local/state/svx`, override with `SVX_STATE_DIR`) holds:

- `stacks/<project>--<stack>.json` -- the public record other tools can read. Its shape is versioned.
- `stacks/<project>--<stack>.env.json` -- rendered commands and the environment snapshot, mode 0600.
- `lock` -- taken while allocating slots.
- `menubar.lock`, `menubar.log` and `menubar/` -- the menu bar's single-instance lock, its log when run by launchd, and its attach scripts.

tmux is the source of truth for what is running. A stack is running while its session holds at least one of its tagged windows. Records that tmux contradicts are marked down whenever svx reads them.

## Layout

The code follows ports and adapters:

| Package | Role |
| --- | --- |
| `internal/domain` | The model: definitions, identity and naming, the dependency graph, slot and port math, validation, template rendering. No I/O. |
| `internal/app` | Use cases (`Up`, `Down`, `Restart`, `Status`, ...) and the port interfaces they drive. Unit tested against fakes. |
| `internal/adapters/yamlproject` | Finds, merges and maps `svx.yaml` |
| `internal/adapters/statefs` | State directory, flock, atomic writes |
| `internal/adapters/tmux` | The tmux binary |
| `internal/adapters/readiness` | HTTP, TCP, command and log readiness probes |
| `internal/adapters/menubar` | The macOS menu bar app and its launchd login item |
| `internal/adapters/system` | Signals, port probes, files, browser, secrets |
| `internal/commands` | CLI wiring and output only |

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
- Inside tmux, `svx up` adds windows to the current session instead of creating one session per stack. Windows are found by tag, and `down` closes windows rather than killing a session.
- The menu bar is a native Cocoa status item run by `svx bar` (a launchd agent once installed), not SwiftBar. It is a long-running process, which the spec listed as a later option.
- The menu bar is opt-out in the user config, and per project with `settings.menubar`.
- The package layout follows ports and adapters rather than the spec's package table. The responsibilities match.
- `svx up --no-wait` still starts windows in dependency order, but does not wait for readiness between them.
- A oneshot that already exited 0 counts as done on a repeat `up` and is not rerun. Use `svx restart <name>` to rerun it.
- `svx restart` re-renders the config against the recorded slot, so config edits apply without moving ports.
- `svx logs` takes `-n` to limit output to the last lines.
- The public record adds `dir`, `menubar`, `service_order` and per-service `oneshot` fields. `session` is the session the stack runs in, which is not always `svx-<project>-<stack>`.
