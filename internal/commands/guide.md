# Writing svx.yaml

This guide is for people and coding agents writing or editing an svx.yaml.
It covers the patterns that keep a config short and correct. The full key
reference is at the end.

## Workflow

1. `svx init` writes a starter svx.yaml at the repo root. Skip it if one exists.
2. Edit the file. Start from how the repo runs its dev services today: its
   Procfile, mise or make tasks, docker compose files and README.
3. Run `svx config` after every edit. It renders the whole file for this
   checkout's stack, with real ports, without starting anything. Template
   errors name the key they came from.
4. Run `svx up`, then `svx status`. Outside tmux, `svx up` needs
   `--session <name>`, an existing tmux session to add the windows to. When a service fails, `svx logs <service>`
   shows why.
5. `svx down` stops the stack.

## Keep it short

- Leave out anything that matches the default. Every `settings` key has one:
  `port_stride: 10`, `max_slots: 50`, `logs: false`, `menubar: true`,
  `retries: 0`, and `window_name` is the service name. So does `autostart: true`, `stop_signal: SIGTERM` and
  `stop_timeout: 10s`. A config with no `settings` block is normal.
- `project` defaults to the main checkout's directory name. Set it only when
  that name is wrong.
- Write a comment only for something the file cannot say, such as why a
  service needs a custom ready check.

## Commands in `run`

`run` goes through `/bin/sh -c`, and svx puts the stack's ports in the
environment first. Use those variables instead of templates, which also
saves quoting `{{` for YAML:

```yaml
run: npm run dev -- --port $PORT --strictPort       # not '{{ .port }}'
run: redis-server --port $PORT --dir $SVX_STACK_DIR # not '{{ .stack.dir }}'
run: ./worker --api $SVX_URL_API                    # not '{{ url "api" }}'
```

`ready.cmd` runs the same way with the service's environment, so it can use
these variables too. Templates are still needed in `env`, `vars`, `links`,
`dir` and the other `ready` checks, because no shell reads those values.

## Ports

- Give each port the number the repo already uses without svx. Slot 0 then
  matches the plain dev setup, and each later slot adds `port_stride`.
- Use `port: auto` only when nothing outside svx needs a stable number.
- `port` is the service's main port. It drives `$PORT`, `url "svc"`, links
  that start with `/`, and `ready.http` paths. Extra ports go under `ports`
  by name.
- A service that starts several things, such as `docker compose up`, can have
  only named `ports` and no main `port`.

## Pointing other tools at svx's ports

Do not copy a port into a new variable just so another tool can read it.
svx already exports every port under a fixed name:

| Variable                     | Where it is set                      |
| ---------------------------- | ------------------------------------ |
| `SVX_PORT_<SVC>`             | Every service and `svx exec`         |
| `SVX_PORT_<SVC>_<NAME>`      | Every service and `svx exec`         |
| `SVX_URL_<SVC>`              | Every service and `svx exec`         |
| `PORT`, `PORT_<NAME>`        | The service's own window only        |

`<SVC>` and `<NAME>` are upper-cased with `-` turned into `_`.

A docker compose file can read them with its old port as the fallback, so it
still works without svx:

```yaml
ports:
  - "127.0.0.1:${SVX_PORT_INFRA_POSTGRES:-5432}:5432"
```

Prefer the `SVX_PORT_*` names over `PORT_*` for anything that may also run
under `svx exec`, which sees only the stack-wide set.

## Variables the app reads

When the app reads its own variable names, map them in `env`:

```yaml
env:
  DATABASE_PORT: '{{ port "infra" "postgres" }}'
  SEARCH_URL: '{{ url "infra" "search" }}'
  API_URL: '{{ url "api" }}'
```

- `url "svc"` and `url "svc" "name"` give `http://127.0.0.1:<port>`. Build the
  string by hand only when the app wants a different shape, such as no scheme
  or `localhost`.
- Top-level `env` reaches every service and `svx exec`. Put a variable under a
  service's `env` when only that service needs it.
- `vars` hold values that several templates share, such as a database name
  built from `.stack.name`.
- Never put secrets in the file. Use `{{ secret "env:NAME" }}` or
  `{{ secret "file:/path" }}`, or `{{ env "NAME" }}` for the shell's value.

## Order and readiness

- `depends` lists services that must be ready before this one starts.
- A setup step such as a migration is `oneshot: true`. It must exit 0 before
  its dependents start.
- Without `ready`, a service with a main port is ready when the port takes a
  TCP connection, and one without is ready after a second.
- Add `ready` when that is not enough:
  - `http: /healthz` for a server that listens before it can answer.
  - `cmd: pg_isready -h 127.0.0.1 -p $SVX_PORT_INFRA_POSTGRES` for a database
    or a container.
  - `output: 'ready to accept'` to wait for a log line (a regex).
  - `delay: 2s` as a last resort.
- Raise `ready.timeout` (default 60s) for services that compile before they
  listen.
- A service that is useful only sometimes gets `autostart: false`.
  `svx up <service>` starts it.

## Links

`links` show up in `svx status` and the menu bar. A target that starts with
`/` is relative to the service's main port. A service without a main port
needs a full URL: `Mailpit: 'http://127.0.0.1:{{ .ports.mailpit }}'`.

## Personal overrides

`svx.local.yaml` beside svx.yaml is deep-merged on top and should be
gitignored. Use it for one person's ports, extra env or `autostart`
choices, not for anything the team needs.

## Reference

Top-level keys: `project`, `settings`, `vars`, `env`, `services`.

Service keys: `run` (required), `dir`, `port` (number or `auto`), `ports`,
`env`, `depends`, `ready` (one of `http`, `cmd`, `output`, `delay`, plus
`timeout`), `oneshot`, `links`, `autostart`, `retries`, `stop_signal`,
`stop_timeout`. Unknown keys are errors.

Template values:

| Expression                                       | Value                               |
| ------------------------------------------------ | ----------------------------------- |
| `.project.name`, `.project.root`                 | Project name and checkout root      |
| `.stack.name`, `.stack.slot`, `.stack.dir`       | Stack identity and data directory   |
| `.stack.session`                                 | The tmux session the stack runs in  |
| `.service.name`                                  | The service being rendered          |
| `.port`, `.ports.<name>`                         | This service's ports                |
| `.vars.<name>`                                   | A value from `vars`                 |
| `port "svc"`, `port "svc" "name"`                | Another service's port              |
| `url "svc"`, `url "svc" "name"`                  | `http://127.0.0.1:<port>`           |
| `env "NAME"`                                     | A variable from the shell           |
| `secret "env:NAME"`, `secret "file:/path"`       | A secret                            |
| `snake`, `slug`, `upper`, `lower`                | String helpers                      |

`settings.window_name` is a template too, rendered once per service.
`.service.name`, `.port` and `.ports` do not exist in `vars` or top-level
`env`. Templates fail on a missing key instead of rendering empty.
