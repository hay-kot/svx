# svx: spec and starter prompt

Oct 10, 2026 · @Hayden

svx is a Go CLI that runs a repo's dev services as windows in a tmux session, one session per stack. Each stack gets its own name, ports and variables, so several copies of the same repo can run at once. There is no supervisor daemon: tmux holds the processes, and a small state directory tells the macOS menu bar what is running.

## Goals and non-goals

Goals:

- **Several stacks per repo.** Two checkouts of one repo, or two named stacks in one checkout, run side by side without sharing ports, database names or data directories.
- **tmux is the process manager.** Every service is a tmux window you can attach to, scroll, interrupt and restart by hand. svx starts things, records what it started, and exits.
- **Two commands for the common case.** `svx up` brings a stack up in dependency order and `svx down` stops it cleanly.
- **Discoverable URLs.** Services declare links in config, and a macOS menu bar item lists them for every running stack.

Non-goals for the first version:

- No background supervisor. Nothing restarts a crashed service unless you ask.
- No file watching, cron scheduling or CPU and memory limits. Dev servers already reload themselves.
- No Windows support. Linux works for everything except the menu bar.

The design borrows from [pitchfork](https://pitchfork.jdx.dev/): one TOML file, `depends`, readiness checks, port bumping and local URLs. See its [configuration reference](https://pitchfork.jdx.dev/reference/configuration.html) and [port and proxy guide](https://pitchfork.jdx.dev/guides/port-management.html).

## Concepts

| Term    | Meaning                                                                                                                                   |
| ------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| Project | A directory containing `svx.toml`. Its name defaults to the directory name of the main checkout, so git worktrees share one project name. |
| Service | One command from the config. It runs in one tmux window named after the service.                                                          |
| Stack   | One running copy of a project's services. It owns one tmux session, one slot and one data directory.                                      |
| Slot    | A small integer, unique per machine among running stacks. It offsets every declared port.                                                 |
| Link    | A labelled URL a service publishes for the menu bar and `svx open`.                                                                       |

Stack identity works like this:

- A stack is keyed by project name plus stack name, and the tmux session is `svx-<project>-<stack>`.
- The default stack name is a two-word name such as `amber-finch`, derived from a hash of the checkout path. The same checkout always gets the same name, and different worktrees get different ones.
- `--name` or `SVX_STACK` overrides the name. That is how you run two stacks from one checkout.
- tmux rewrites `.` and `:` in session names to `_`, so svx restricts project and stack names to `[a-z0-9-]` and slugifies anything else.

## Configuration

Config lives in `svx.toml` at the repo root, found by walking up from the current directory. An optional `svx.local.toml` beside it is merged on top for personal overrides and should be gitignored.

```toml
# svx.toml
project = "shop"            # optional, defaults to the main checkout's directory name

[settings]
port_stride = 10            # distance between slots, see Port allocation

[vars]
db_name = 'shop_{{ snake .stack.name }}'

[env]
REDIS_URL = 'redis://127.0.0.1:{{ port "redis" }}'
DATABASE_NAME = '{{ .vars.db_name }}'

[services.redis]
run = 'redis-server --port {{ .port }} --dir {{ .stack.dir }}'
port = 6379

[services.migrate]
run = "bin/migrate"
oneshot = true
depends = ["redis"]

[services.api]
run = "go run ./cmd/api"
dir = "api"
port = 8080
depends = ["migrate"]
ready = { http = "/healthz", timeout = "30s" }
links = { API = "/", Docs = "/docs" }

[services.web]
run = 'npm run dev -- --port {{ .port }} --strictPort'
dir = "web"
port = 5173
ports = { hmr = 24678 }
depends = ["api"]
env = { VITE_API_URL = '{{ url "api" }}' }
links = { App = "/" }

[services.worker]
run = "go run ./cmd/worker"
depends = ["redis"]
autostart = false           # only starts when named: svx up worker
```

Top-level keys:

| Key          | Meaning                                                                                 |
| ------------ | --------------------------------------------------------------------------------------- |
| `project`    | Project name used in session names and hostnames.                                       |
| `[settings]` | `port_stride` (default 10), `max_slots` (default 50), `logs` (default true).            |
| `[vars]`     | Named template values, available as `.vars.<name>`. A var cannot reference another var. |
| `[env]`      | Environment variables for every service. Values are templates.                          |

Service keys:

| Key                           | Meaning                                                                                           |
| ----------------------------- | ------------------------------------------------------------------------------------------------- |
| `run`                         | Required. Shell command, run with `sh -c` in the foreground. A template.                          |
| `dir`                         | Working directory, relative to the checkout root. Defaults to the root.                           |
| `port`                        | Base number of the service's primary port, or "auto" for any free port.                           |
| `ports`                       | Extra named ports, each with a base number.                                                       |
| `env`                         | Extra environment variables for this service. Values are templates.                               |
| `depends`                     | Services that must be ready, or finished if oneshot, before this one starts.                      |
| `ready`                       | Readiness check: one of `http`, `cmd`, `output` (regex) or `delay`, plus `timeout` (default 60s). |
| `oneshot`                     | The command is expected to exit. Exit code 0 counts as ready, anything else fails `up`.           |
| `links`                       | Label to URL. A value starting with `/` is relative to the service's own URL.                     |
| `autostart`                   | Default true. When false, the service starts only when named on the command line.                 |
| `stop_signal`, `stop_timeout` | Defaults `SIGTERM` and `10s`.                                                                     |

With no `ready` key, a service with a `port` is ready when that port accepts a TCP connection. A service with no port is ready once its window has stayed alive for one second.

Validation happens before anything starts. Unknown keys, unknown `depends` names, dependency cycles, duplicate link labels within a stack and template errors all fail with the file and key named.

## Templating and injected variables

Templates use Go's `text/template` with `missingkey=error`, so a typo fails `svx up` instead of rendering an empty string. `run`, `dir`, `env` values, `vars`, `links` and the `ready` fields are templates.

| Expression                        | Value                                                                        |
| --------------------------------- | ---------------------------------------------------------------------------- |
| `.project.name`, `.project.root`  | Project name and the checkout root path.                                     |
| `.stack.name`, `.stack.slot`      | Stack name and slot number.                                                  |
| `.stack.dir`                      | Per-stack data directory, `<root>/.svx/<stack>/`, created by `svx up`.       |
| `.stack.session`                  | The tmux session name.                                                       |
| `.service.name`                   | The service being rendered. Not set in `[vars]` and top-level `[env]`.       |
| `.port`, `.ports.<name>`          | The current service's own resolved ports.                                    |
| `.vars.<name>`                    | A value from `[vars]`.                                                       |
| `port "svc"`, `port "svc" "name"` | Another service's primary or named port.                                     |
| `url "svc"`                       | Another service's base URL, `http://127.0.0.1:<port>` until the proxy is on. |
| `env "NAME"`                      | A variable from the environment `svx up` was run in. Missing is an error.    |
| `snake`, `slug`, `upper`          | String helpers, for example `shop_amber_finch` for a database name.          |

All ports are allocated before any template is rendered. A service can therefore reference the port of a service that starts after it.

Every service also gets these environment variables without asking:

| Variable                                                | Value                                   |
| ------------------------------------------------------- | --------------------------------------- |
| `SVX_PROJECT`, `SVX_STACK`, `SVX_SLOT`, `SVX_STACK_DIR` | Stack identity.                         |
| `SVX_SERVICE`                                           | This service's name.                    |
| `PORT`, `PORT_<NAME>`                                   | This service's primary and named ports. |
| `SVX_PORT_<SERVICE>`, `SVX_PORT_<SERVICE>_<NAME>`       | Every service's ports.                  |
| `SVX_URL_<SERVICE>`                                     | Every service's base URL.               |

Names are upper-cased with `-` turned into `_`. Later sources win in this order: the environment `svx up` ran in, svx's injected variables, top-level `[env]`, then the service's `env`.

svx can only tell a program which port to use. A dev server that hard-codes its port must be changed to read `PORT` or take it as a flag.

## Port allocation

Every port in a stack is its base number plus `slot × port_stride`. With the default stride of 10, slot 0 runs the example's web server on 5173, slot 1 on 5183 and slot 2 on 5193. Offsetting all ports together keeps a stack's ports recognisable and their spacing intact.

Allocation happens under an exclusive file lock so two `svx up` runs cannot pick the same slot:

1. Collect the slots held by other stacks from the state directory, dropping any whose tmux session no longer exists.
2. Start from this stack's last slot if it had one, otherwise slot 0.
3. Compute every port for the candidate slot and try to listen on each on `127.0.0.1`. If the slot is held, a port is leased to another stack, or a port is busy, move to the next slot. This is the rotation.
4. Give up with a clear error after `max_slots` candidates, naming the ports that were busy.
5. Write the slot and resolved ports to the stack's state file, then release the lock.

Rules that follow from this:

- A stack keeps its slot across `down` and `up` when the ports are still free, so URLs stay stable.
- When a stack has to move, `svx up` prints the old and new ports.
- `svx restart <service>` never reallocates. The service gets the port it had.
- Two bases whose difference is a multiple of the stride overlap across slots: with bases 3000 and 3010, slot 1 of the first equals slot 0 of the second. The listen test and the leases in the state directory catch this at allocation. `svx up` also warns about such pairs at validation.
- `port = "auto"` skips the slot scheme for that port and takes any free port the OS offers, for services nobody addresses by number.

There is a short window between the listen test and the service binding the port. A service that loses that race fails visibly in its window, and `svx up` reports it as not ready.

## tmux model

One stack is one detached tmux session, and one service is one window in it. svx drives tmux by running the `tmux` binary and never keeps a process of its own alive.

What `svx up` does:

1. Load and validate the config, then resolve the project and stack names.
2. If the session already exists, reuse its recorded slot and ports. Start only the requested services whose windows are missing or dead, and leave running ones alone.
3. Otherwise allocate the slot, render every template, create `.stack.dir`, and write the resolved stack to the state directory.
4. Start services in dependency order. Services with no unmet dependencies start together, and each waits for its dependencies to be ready or, for oneshots, to exit 0.
5. Wait for readiness, print a table of services, ports and links, and exit. `--no-wait` skips the wait and `--attach` attaches afterwards.
6. If a service fails or times out, exit non-zero, name the service, and leave every window up for inspection.

Each window runs `svx _run <stack> <service>`, a hidden subcommand that reads the resolved stack, sets the working directory and environment, prints a one-line banner, and then `exec`s `sh -c <run>`. This shim exists for three reasons:

- Commands and environment values never pass through tmux's command line, so there is no quoting to get wrong and no secrets in `ps` output.
- tmux windows inherit the tmux server's environment, which is often stale. The shim applies a snapshot of the environment `svx up` ran in, so tools such as mise and direnv behave as they do in your shell.
- It is the one place to add restart policies later without adding a daemon.

What `svx down` does:

1. Walk services in reverse dependency order.
2. Send each window's process group its `stop_signal` and wait up to `stop_timeout` for the pane to die.
3. Kill the session, which removes anything still running.
4. Mark the stack down in the state directory. Its slot is released but remembered as a preference, and `.stack.dir` is kept unless `--purge` is given.

Behaviours I checked on tmux 3.4, which the implementation should rely on:

- `remain-on-exit on` keeps a dead window visible with its exit code, readable as `#{pane_dead}` and `#{pane_dead_status}`. It must be set in the same tmux invocation that creates the window, chained with `;`. Set afterwards, a fast-exiting command's window is already gone. Setting it on the session does not apply to new windows.
- The pane's process is its own process group leader, so signalling the negative of `#{pane_pid}` reaches the command and its children.
- `respawn-pane -k` restarts a service in place, in the same window.
- Session user options such as `@svx_stack` and `@svx_root` are readable through `list-sessions -F`, so a stack can be recognised from tmux alone.
- A target written `=name` matches a session name exactly instead of by prefix.

Other rules:

- Status comes from tmux, not from a recorded guess: alive, exited with a code, or missing because someone closed the window.
- `svx attach` uses `switch-client` when run inside tmux and `attach-session` otherwise, so sessions are never nested.
- With `logs` on, each window is piped with `pipe-pane` to `.svx/<stack>/logs/<service>.log`. That file backs `svx logs` and the `ready.output` check.
- Treat tmux 3.2 as the minimum until older versions are tested, and have `svx doctor` check it.

## CLI

Commands act on the stack for the current checkout unless `--name` or `SVX_STACK` says otherwise. Bare `svx` is `svx status`.

| Command                    | What it does                                                                                                              |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `svx up [service...]`      | Start the stack, or the named services plus their dependencies. Safe to repeat. Flags: `--name`, `--attach`, `--no-wait`. |
| `svx down`                 | Stop the stack. Flags: `--all` for every stack on the machine, `--purge` to delete its data directory.                    |
| `svx status`               | Services, state, ports and links for this stack. `--json` for scripts.                                                    |
| `svx ls`                   | Every stack on the machine, across projects. `--json` for scripts.                                                        |
| `svx attach [service]`     | Attach to the session, optionally selecting a window.                                                                     |
| `svx restart <service...>` | Restart services in place, keeping their ports.                                                                           |
| `svx logs <service>`       | Print a service's log. Flags: `-f` to follow, `--raw` to keep colour codes.                                               |
| `svx env`                  | Print the stack's variables. `--format shell`, `dotenv` or `json`; `--service` for one service's view.                    |
| `svx exec -- <cmd>`        | Run a command with the stack's environment, for tests and one-off scripts.                                                |
| `svx open [link]`          | Open a link in the browser. With no argument, list them.                                                                  |
| `svx config`               | Print the fully rendered config for this stack without starting anything.                                                 |
| `svx bar`                  | Print menu bar output. See Menu bar.                                                                                      |
| `svx init`                 | Write a commented starter `svx.toml` and add `.svx/` to `.gitignore`.                                                     |
| `svx doctor`               | Check the tmux version, state directory, stale stacks and menu bar setup.                                                 |

`svx config` matters more than it looks. It is how you debug a template before a service starts with the wrong port.

Exit codes: 0 for success, 1 for a failed or timed-out service, 2 for a config or usage error.

## State and discovery

svx keeps one small directory of state, `$XDG_STATE_HOME/svx`, defaulting to `~/.local/state/svx` on macOS as well as Linux. It exists for two things tmux cannot do: hold port leases, and tell other programs what is running.

| Path                                 | Contents                                                                                             |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------- |
| `lock`                               | The file lock taken during slot allocation and state writes.                                         |
| `stacks/<project>--<stack>.json`     | The public record: names, root, session, slot, status, services, ports and links.                    |
| `stacks/<project>--<stack>.env.json` | The environment snapshot and rendered commands the shim reads. Mode 0600, since it can hold secrets. |

The public record looks like this, and its shape is a versioned contract:

```json
{
  "version": 1,
  "project": "shop",
  "stack": "amber-finch",
  "root": "/Users/you/src/shop",
  "session": "svx-shop-amber-finch",
  "slot": 1,
  "status": "up",
  "started_at": "2026-10-10T15:04:05Z",
  "services": {
    "web": {
      "ports": { "main": 5183, "hmr": 24688 },
      "url": "http://127.0.0.1:5183",
      "links": [{ "label": "App", "url": "http://127.0.0.1:5183/" }]
    }
  }
}
```

Rules:

- Files are written to a temporary name and renamed, so a reader never sees half a file.
- tmux is the source of truth for whether something is running. Every command that reads state checks each `up` stack's session and marks it `down` if the session is gone.
- The menu bar and the proxy read only the public records. Nothing else needs to know svx's internals.

## Menu bar

The first version uses [SwiftBar](https://github.com/swiftbar/SwiftBar) instead of a native app. SwiftBar runs a script on a timer and renders its text output as a menu, so svx only has to print text and still runs no process of its own.

How it fits together:

- A service registers a link with one line of config, `links = { App = "/" }`. Nothing else is needed for it to appear.
- `svx bar` reads the public state records, reconciles them against tmux, and prints SwiftBar's format.
- `svx bar --install` writes a two-line plugin script named `svx.10s.sh` into SwiftBar's plugin folder. The script calls `svx bar` by absolute path, because SwiftBar does not load your shell profile.
- After `up`, `down` and `restart`, svx opens `swiftbar://refreshplugin?name=svx` in the background, so the menu updates at once instead of on the next 10-second tick. This is skipped when SwiftBar is not installed.

The output is shaped like this. Text before the first `---` is the menu bar title, and `href` makes an item open a URL:

```text
svx 2
---
shop · amber-finch
App | href=http://127.0.0.1:5183/
API | href=http://127.0.0.1:8090/
Docs | href=http://127.0.0.1:8090/docs
Attach in terminal
Stop stack
---
shop · quiet-otter
App | href=http://127.0.0.1:5193/
worker exited (1)
```

Menu rules:

- The title shows the number of running stacks and is dimmed when there are none.
- Each stack lists its links first, then `Attach in terminal` and `Stop stack`, which run `svx attach` and `svx down` for that stack through SwiftBar's script parameters.
- A dead service is listed by name with its exit code. The bar does no health probing of its own, so it stays fast.
- Take the exact parameter syntax for running commands from the SwiftBar README. I confirmed the title separator, `href`, the file naming and the refresh URL there, and left the rest for the implementer to read.

A native menu bar app that watches the state directory is a later option. It would give instant updates and notifications, at the cost of a second binary and a long-running process.

## Proxy and HTTPS

This layer is optional and comes after everything above works. It gives each service a stable hostname, `<service>.<stack>.<project>.localhost`, in place of a port number.

Why it is worth having for multi-stack work:

- URLs survive port rotation.
- Browsers scope cookies and local storage by hostname and ignore the port for cookies. Two stacks on `127.0.0.1` share a login session, and two stacks on different hostnames do not.

Design:

- `svx proxy up` starts the proxy as a window in its own tmux session, `svx-proxy`. It is a long-running process, but it lives in tmux like every other service and `svx proxy down` stops it.
- The proxy listens on one unprivileged port, 8088 for HTTP. It watches the state directory and routes by `Host` header to the port in the matching public record.
- It must pass WebSocket upgrades through, since dev servers use them for hot reload.
- A user-level setting in `~/.config/svx/config.toml` turns it on. When on, `url "svc"`, `SVX_URL_*` and links render as proxy hostnames at `svx up` time.
- HTTPS adds a local certificate authority created on first use, a certificate signed per hostname on demand, and `svx proxy trust` to add the authority to the login keychain. Shelling out to [mkcert](https://github.com/FiloSottile/mkcert) is an acceptable first cut.

Known limits to settle when this milestone starts:

- Chrome and Firefox resolve `*.localhost` to loopback themselves. Safari and command-line tools may not, and I have not tested them. Pitchfork solves this with its own DNS responder and an `/etc/resolver` entry.
- Serving on ports 80 and 443 needs a privileged redirect on macOS. The first cut keeps the port in the URL.

## Implementation notes

One Go module producing one static binary, `svx`, for macOS and Linux.

| Package           | Responsibility                                                                  |
| ----------------- | ------------------------------------------------------------------------------- |
| `cmd/svx`         | Command wiring and flag parsing only.                                           |
| `internal/config` | Load, merge and validate `svx.toml` and `svx.local.toml`.                       |
| `internal/tmpl`   | Template context, helper functions and rendering.                               |
| `internal/ports`  | Slot allocation, listen tests and leases.                                       |
| `internal/state`  | State directory, file lock, atomic writes and reconciliation.                   |
| `internal/tmux`   | A small interface over the `tmux` binary: sessions, windows, panes, options.    |
| `internal/stack`  | The `up`, `down`, `restart` and `status` logic, including the dependency graph. |
| `internal/ready`  | Readiness checks.                                                               |
| `internal/bar`    | SwiftBar output and plugin install.                                             |
| `internal/proxy`  | The later proxy milestone.                                                      |

Conventions:

- Keep dependencies few: a CLI library such as cobra, a TOML parser, and the standard library for the rest.
- `internal/stack` talks to tmux only through the `internal/tmux` interface, so its logic is unit-tested against a fake.
- Integration tests run a real tmux on a private socket with `tmux -L svx-test-<random> -f /dev/null` and kill that server afterwards. They never touch the developer's own tmux server.
- The tmux socket and the state directory are both overridable by environment variable, `SVX_TMUX_SOCKET` and `SVX_STATE_DIR`, which is what makes those tests possible.
- Every error names what failed and what to do next. A busy port names the port and the stack holding it, and a failed readiness check prints the last lines of that service's log.
- Human output goes to stdout as plain aligned text with colour only on a terminal. `--json` output is stable and versioned.

## Milestones

Each milestone ends with something usable and a check that proves it.

| #   | Scope                                                                                                                                         | Done when                                                                                                                     |
| --- | --------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| 1   | Config, templating, slot allocation, state, the shim, and `up`, `down`, `status`, `ls`, `attach`, `config`. No dependencies or readiness yet. | Two checkouts of a sample repo run `svx up` at once and get different ports. `svx down` in one leaves the other running.      |
| 2   | `depends`, readiness checks, `oneshot`, `restart`, logs, `env`, `exec`, `init`, `doctor`.                                                     | A service waits for a failing dependency and `up` exits 1 naming it. `svx exec -- env` shows the stack's ports.               |
| 3   | Links, `open`, `svx bar` and its installer.                                                                                                   | Starting a second stack makes its links appear in the menu bar within a second or two, and clicking one opens the right port. |
| 4   | HTTP proxy with hostnames, then HTTPS.                                                                                                        | `http://web.<stack>.shop.localhost:8088` reaches the right stack, and hot reload works through it.                            |
| 5   | Candidates: restart-on-failure in the shim, a shell hook that runs `up` on entering a directory, a native menu bar app.                       | Decided when 1 to 4 are in daily use.                                                                                         |

Milestone 1 is the one to get right. Everything later reads the state it writes.

## Open decisions

These are defaults I chose so the spec is complete. Each is cheap to change now and expensive later.

| Decision           | Chosen                                                                     | Alternative                                                                                                                            |
| ------------------ | -------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| Where windows go   | A dedicated session per stack, so `down` is one kill and stacks never mix. | New windows in the session you are already in, prefixed with the stack name. Closer to "my existing workflow", harder to stop cleanly. |
| Default stack name | A stable two-word name hashed from the checkout path.                      | The git branch or worktree directory name. More meaningful, but it changes when you switch branch.                                     |
| Port scheme        | Base plus slot times a stride of 10. Predictable and easy to read.         | Random free ports from a range. No collisions to think about, but nothing memorable.                                                   |
| Environment        | A snapshot of the shell that ran `svx up`, stored in a 0600 file.          | Only the config's `[env]`, with nothing from your shell written to disk.                                                               |
| Menu bar           | A SwiftBar plugin. Needs SwiftBar installed.                               | A native app in milestone 5, or sooner if SwiftBar is a non-starter.                                                                   |
| Binary name        | `svx`. No command-line tool by that name turned up in a web search.        | Registry checks on Homebrew, npm and GitHub are still to do before publishing.                                                         |

## Starter prompt

Save this document into the new repo as `SPEC.md`, then give your coding agent the prompt below. It asks for milestone 1 only, so you can review the foundation before the rest is built on it.

```text
You are starting a new Go project called svx in this empty repository.

svx is a command-line tool that runs a repo's development services as
windows in a tmux session, one session per "stack". Each stack gets its own
name, port offsets and variables, so several copies of the same repo can run
at the same time. It deliberately has no background daemon: tmux holds the
processes, and svx starts things, records what it started, and exits. I use
tmux all day, and I want to attach to a service's window and see it running
like anything else I start by hand.

SPEC.md in this repo is the full design. Read all of it before writing code.
It covers the config format, templating, port allocation, how svx drives
tmux, the CLI, the state files, and the build order. Where the spec gives a
specific rule, follow it. Where it is silent, pick the simplest behaviour
that fits the goals at the top of the spec and tell me what you chose.

Build milestone 1 from the Milestones table and stop there: config loading
and validation, templating, slot and port allocation, the state directory,
the hidden `svx _run` shim, and the commands up, down, status, ls, attach
and config. Do not start on dependencies, readiness checks, the menu bar or
the proxy. Later milestones read the state milestone 1 writes, so I want to
review it first. Do lay out the packages as the spec describes, so the later
work has a place to go.

Things that matter to me:

- All tmux access goes through one small interface, with a fake for unit
  tests. Integration tests use a real tmux on a private socket
  (tmux -L svx-test-<random> -f /dev/null) and must never touch my own tmux
  server or my real state directory.
- The section "tmux model" lists tmux behaviours that were checked on tmux
  3.4, including one about remain-on-exit that is easy to get wrong. Write
  an integration test for each of them before relying on it.
- Error messages say what failed and what to do next.
- Keep third-party dependencies few, and tell me which you added and why.

You are done when the milestone 1 check in the spec passes for real: create
a sample project with two services under testdata, copy it to two
directories, run `svx up` in both, and show me that the two stacks got
different ports, that `svx ls` lists both, and that `svx down` in one leaves
the other running. Run go vet and the full test suite, and show me the
output.

When you finish, summarise what you built, every place you departed from or
added to the spec, and anything in the spec that turned out to be wrong or
unclear. If something in the spec does not work on the tmux version
installed here, stop and tell me instead of working around it silently.
```

For later milestones, reuse the prompt and rewrite two paragraphs: the one that names milestone 1 and its scope, and the one that describes its check.
