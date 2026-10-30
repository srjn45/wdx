# Guidance for AI agents

This file applies to every AI working on this repo (Claude, Codex, Cursor, Antigravity, ...).

## Workflow

- Never start coding until the human explicitly says so.
- Brainstorm with the human first: work out the complete solution (structure, tables, commands, names, edge cases) and get it approved before writing any code.
- Work on one feature at a time. Do not add scope the human did not ask for; suggest it instead.
- Commit only when the human asks. Before committing, run `make check` and `make test`: CI runs both on every push to `main` and every pull request.

## Releases

- Commit messages follow Conventional Commits (`feat:`, `fix:`, `perf:`, `docs:`, `test:`, `refactor:`, `chore:`): the next version and `CHANGELOG.md` are worked out from them, so the type must say what the change is.
- A change a user or script can notice breaks them: the `--json` output, error codes, exit codes, commands and flags, settings, the API. Mark it with `!` (`feat!:`) and say what breaks in a `BREAKING CHANGE:` footer.
- After adding, removing or upgrading a dependency, run `make notices` and commit `THIRD-PARTY-NOTICES.md` with it; never edit it by hand. A dependency must be under a permissive license (Apache-2.0, MIT, BSD, ISC); ask the human before adding any other.
- Never edit `CHANGELOG.md` or move a tag. Release only when the human asks (`make release-dev`, `make release`); see docs/RELEASING.md.

## Code style

- Keep the code clean and readable by a human.
- `make lint` (in `make check`) enforces part of this file: depguard the dependency directions, forbidigo the environment and test rules, sloglint the logging rules, paralleltest `t.Parallel()`. Fix a finding rather than silencing it; a `//nolint` names its linter and says why. Ask the human before turning a linter off in `.golangci.yml`.
- Prefer self-explanatory code over comments. Comment only what the code cannot say: the why, a non-obvious constraint, a workaround.
- Use good names. When a name for a variable, type, function or package is not obvious, discuss it with the human.

## Project layout

warden manages AI CLI agents in PTYs, plus projects, messaging, a TUI and later pipelines. The CLI is `wdx`.

```
api/openapi.yaml   the API contract; everything in internal/api is generated from it
cmd/wdx/        entry point
internal/api/   generated types, server interface and client, plus the error codes
internal/cli/   cobra commands: parse args, call the daemon, print; no business logic
internal/client/   calls the daemon's API over its socket
internal/daemon/   serves the API over the Unix socket on top of the domain
internal/config/   the settings, loaded once at startup from their sources, and the config file written from them
internal/db/    opens the single SQLite DB, runs embedded migrations
internal/git/   thin wrapper over the git CLI
internal/holder/   the process that owns a terminal's PTY and shell (wdx holder), its screen, spool and history, its protocol and clients; docs/TERMINALS.md, docs/ATTACH.md
internal/holder/holdertest/  lets a test binary be its own holder, for the tests that start terminals
internal/pty/   opens pseudo-terminals and starts commands in them
internal/termview/  the Bubble Tea pane that shows an attached terminal; docs/ATTACH_VIEW.md
internal/screen/  a terminal's screen kept by a headless vt, for the holder and termview; the only user of the vt; docs/VT.md
internal/tide/   the terminal IDE (wdx tide), a client of the daemon as the cli is: its panes' layout, focus and zoom, keys and mouse, the projects; docs/TIDE.md
internal/logging/  JSON-line logs in daily files; the logger travels in the context
internal/service/  finds the daemon installed as a systemd or launchd service
internal/telemetry/  sends the daemon's metrics to an OpenTelemetry collector
internal/style/  colors text output on a terminal with semantic styles
internal/deps/   the programs wdx needs besides itself (git, a clipboard tool, docker or podman), the features each enables, and whether they are ready; docs/DEPS.md
internal/observe/  runs Grafana with the logs and metrics in a container (wdx observe); docs/OBSERVABILITY.md
scripts/install.sh installs the binary and the service; docs/DAEMON.md explains both
scripts/release.sh tags releases, builds them and pushes them; cliff.toml and docs/RELEASING.md explain the versions
scripts/test-update.sh updates a wdx built from the tree to a release dist.sh builds of it and rolls it back (make test-update, in CI)
scripts/vt-sync.sh rebases the vt fork onto upstream and moves go.mod to it (make vt-sync); docs/VT.md
scripts/notices.sh writes THIRD-PARTY-NOTICES.md, which every release archive carries with LICENSE and NOTICE
.github/workflows/ci.yml  runs make check and the tests on every push to main and every PR; docs/RELEASING.md
.github/workflows/release.yml  publishes the GitHub Release of every pushed tag, through scripts/publish.sh
.golangci.yml   the linters make lint runs
internal/update/  replaces the installed wdx with a release and rolls it back (wdx update); docs/RELEASING.md
internal/atomicfile/  writes a file whole or not at all: a synced temporary file renamed into place
internal/identity/  the name, version and paths every package agrees on: the home's files, sockets, environment variables
internal/gittest/  makes git behave the same in every test run, and creates the repositories tests use
internal/waittest/  gives every wait in the tests one deadline, from the test binary's -timeout
internal/<domain>/  one package per feature (e.g. project, terminal): types, Store, tests
```

- Dependencies point one way: cmd -> cli -> client -> api, cli -> service, cli -> observe, cli -> style, cli -> deps, cli -> update -> db, cli -> holder, cli -> termview -> holder/screen, cli -> tide -> client/style, and cli -> daemon -> domain -> db/git/holder -> pty/screen, and cli/daemon -> telemetry; every package may use identity; config, db and update -> atomicfile. The cli runs the daemon but never calls the domain: every command goes through the API, except `daemon`, `observe`, `settings` and `update`, which manage the daemon, the observability stack, the config file and the installed binary themselves, and the hidden `holder`, which the daemon runs for each terminal. Every package may use logging. depguard enforces the directions it can: the cli never reaches the domain, only the cli uses daemon, only the cli and tide use client, only cmd and cli use config, only screen uses the vt.
- Name packages by what they provide, never by layer (no models/, services/, utils/).
- Add interfaces only where a consumer needs them, not up front.
- Tests use real dependencies (a temp SQLite DB, the real git binary), not mocks.

## Database

- One SQLite database (`~/.wdx/wdx.db`) holds every table. Never add another DB file.
- Migrations live in `internal/db/migrations`, are numbered, and are append-only: never edit a committed migration.
- Prefer migrations that only add (a table, an index, a column that is nullable or has a default): the previous version keeps working on the database, so `wdx update --rollback` keeps the data. A migration that changes or removes what older binaries use starts with the line `-- breaks-older: true`; rolling back across it restores the database backup taken at the update.
- Conventions: STRICT tables, INTEGER unix-millisecond timestamps, NULL for absent values, prefixed random ids (`proj-`, `wt-`), indexed foreign keys with ON DELETE CASCADE, user-facing names `COLLATE NOCASE`, JSON in TEXT columns with a `json_valid` CHECK.

## API

- The spec comes first: change `api/openapi.yaml`, then run `make generate`. Never edit `api.gen.go`; a test fails when it is stale.
- Resources are plural nouns under `/v1`, with standard methods and statuses: 201 when something was created, 409 for a conflict, 422 for a value that cannot be used, 400 for a request the spec does not allow.
- A `{project}` in a path is its name, or `id:` and its id; names cannot contain `:`.
- Paths in requests are absolute: the daemon's working directory means nothing to a client.
- Errors are Problem Details (`application/problem+json`) with a stable `code` from the `ErrorCode` enum. `internal/api/codes.go` gives each code its HTTP status, CLI exit code and description; the daemon maps domain errors to codes in one place.
- Requests are validated against the spec before a handler runs. Requests that change anything run one at a time, and to their end even when the client leaves.

## CLI conventions

Every command serves both humans and scripts or AIs.

- Only commands are positional; every input is a flag. An entity is named by `--id` or `--name`, exactly one of them.
- Every prompt choice that makes sense without a human has a flag. Without that flag and without a terminal (or with `--no-input` / `no_input: true` in the config file), fail when the question comes up, with a hint naming the flags.
- Every command supports `--json`, which never prompts, except `tide`, interactive by nature, which fails with a `usage` error: the flags that answer a command's prompts are required up front (a `usage` error otherwise). stdout then holds exactly one object and nothing else, the same the API returns:
  - success: the API's body, such as `{"result": "created", "project": {...}}`, with the key named after the entity and extras such as `dropped`;
  - failure: Problem Details, as the API returns them: `{"type": "urn:wdx:error:name_taken", "title": "...", "status": 409, "detail": "...", "code": "name_taken", "hint": "...", "details": {"project": {...}}, "debug_id": "dbg-..."}`. Errors only the CLI reports (`usage`, `daemon_unavailable`, `already_running`, `internal`) take the same shape.
- `result` names the most significant thing that happened: `created`, `registered`, `cloned`, `opened`, `closed`, `removed`. Read-only commands have no `result`; they return the entity under its own key (`project`, or `projects` for a list).
- A command that cannot reach the daemon fails with `daemon_unavailable` (exit 3) and the hint `run wdx daemon start`; it never starts the daemon itself. A daemon of another version gets a warning on stderr.
- Paths given on the command line are made absolute before they are sent: the daemon does not share the working directory.
- Prompts stay in the CLI: on a conflict it asks, then sends the request again with the answer (`on_conflict`, `on_missing_checkout`).
- JSON keys are snake_case, absent values are `null`, timestamps are RFC 3339. Leave internal fields out: everything in the JSON is a contract.
- Text mode prints results to stdout and errors to stderr; the first line says what happened.
- Exit codes: 0 ok, 1 rejected (conflict, not found, cancelled), 2 usage, 3 system failure.
- The domain returns typed errors and knows nothing about output; the daemon maps them to codes, and `internal/api/codes.go` gives each code its exit code. The cli renders both formats from one response value. Hints are added by the cli, not written into domain messages.
- Every command documents itself with annotations: `output` (what it prints), `results`, `errors` (codes besides `usage` and `internal`) and `jsonRequires` (the flags that answer its prompts; the check is shared, not written per command). `--help` renders them in our own layout (never cobra's template), or as one JSON object with `--json`; subcommands are listed alphabetically. Every usage error hints at the command's `--help`.
- Every leaf command has `Example`s built with `examples(description, command line, ...)`: the common use first, then the flag combinations that answer prompts or that `--json` needs. `TestExamples` runs each through the command's flag parsing and rules, so they cannot go stale; keep them free of shell quoting.
- A flag that takes a value names it in backticks in its description (``"`path` to a local git repository"``); help shows it as `--local <path>`.
- `Long` does not repeat `Short`: help prints the summary on its first line, after the command path.
- Every flag that takes a value registers its completion (`RegisterFlagCompletionFunc`), `cobra.NoFileCompletions` when it has none; `TestCommandDocs` checks it. Completions that ask the daemon never prompt, wait at most `completionTimeout` and offer nothing when it does not answer.
- Reference that is about no single command is a help topic (`wdx help json`, `settings`, `logs`), not a section of the root help.

## Text output style

- Text is styled only through `internal/style`, by what it is (`Success`, `Warning`, `Failure`, `Name`, `Muted`, `Command`, `Flag`, `Hint`...), never with raw escape codes or colors in commands.
- Results take the stdout style in `text(w, st)`; what goes to stderr (errors, warnings, prompts) uses `stderrStyle(cmd)`. Tables use `style.Table`, since `text/tabwriter` counts escape codes as characters.
- Color follows `--color`, then the `color` setting (`auto`, `always`, `never`); `auto` colors a stream only when it is a terminal and `NO_COLOR` / `TERM=dumb` are not set. Never with `--json`, and never in logs: the logged response is the plain value.
- Text errors go to stderr as `wdx: <message>  [<code>]`, then `hint:` and `debug_id:` lines.
- Styling never changes the words: with color off the text reads the same.

## Config

- Every setting is a row in the table in `internal/config`: key, description, default (or required) and parser. The `wdx help settings` topic and the logs are built from it.
- `main` is the only place that reads the environment: it loads the `Config` once and passes it to `cli.Run`. Nothing else calls `os.Getenv` or reads the config file.
- An invalid or missing required setting fails every command at startup with a `usage` error listing all of them.
- `cli` holds the `Config`; domain packages get only the values they need as arguments and never import `config`.
- Only the home comes from the environment (`WDX_HOME`, default `~/.wdx`), since the config file lives in it. One exception is `NO_COLOR` and `TERM=dumb`, conventions of terminals rather than wdx settings: `main` reads them into `Env.NoColor`. The other is the fixed list of variables terminals inherit from the daemon (`PATH`, the locale, the ssh agent, the desktop session; docs/TERMINALS.md): `main` reads them into `Env.ShellEnv`, and they are never logged. Every other setting is read from `<home>/config.yaml`, nested keys making the dotted key (`log: {level: debug}` is `log.level`); a missing file means all defaults.
- The config file is written from the table, never by hand or from a template: `wdx settings write` lists every setting with its description as the comment, keeping the values set, and `wdx update` runs the new release's to write it anew. So a description reads as a sentence once capitalized, and a removed or renamed setting needs nothing more: its key is commented out at the end of the file.
- Sources are layered, later winning: defaults, then the config file, then the environment. Unknown keys, duplicates and values that are not a single value are errors, reported with the file and line.

## Logging

The log must tell the story of any run: what was asked, every step taken and why, and what was returned.

- Get the logger with `logging.FromContext(ctx)`; it already carries the run's `debug_id`. Never use slog's default logger or a global, and pass `ctx` to every goroutine so its lines keep the id.
- `cli.Run` logs `command received` (raw args and settings), `command started` (resolved command and every flag) and `command returned` (exit, outcome, the error `code` when it failed and the exact response). Failures are logged there at error level; don't log the same error again on its way up.
- The daemon logs `request received` and `request returned` (status, the error `code` when it failed and the exact response) for every request, under the debug id the client sent in `Wdx-Debug-Id`, so one id traces a command through both processes.
- Log each step where it happens, at info: lookups and what they matched, decisions and their reason, every change to the database or disk, git commands, and how each question was answered. At error level, log what a failure knows that its returned error does not (git's stderr, a failed cleanup).
- Debug is for detail: SQL with arguments, git output.
- A tab completion (`__complete`) is the one run logged at debug level from received to returned, through `logging.Quiet`; it asks the daemon to do the same for its requests with `Wdx-Log-Level: debug`. Failures stay at error.
- Messages are short lowercase statements of what happened (`project registered`); keys are snake_case and reused (`project_id`, `path`, `name`, `duration_ms`, `err`).
- Never log the environment beyond wdx's own settings, prompt text or agent output. URL credentials are redacted by the logger.
- Logging never fails a command and never writes to stdout or stderr.

## Metrics

- Only the daemon measures: it lives long enough to send metrics, which `wdx` runs do not. It sends them over OTLP/HTTP to `otel.endpoint`, by default `http://localhost:4318` where `wdx observe` listens, and nothing when that is empty.
- Metrics are named `wdx.<noun>...` in OpenTelemetry's dotted style, with the unit in `WithUnit` (seconds for durations), never in the name.
- Labels take values from a fixed set (an operation, a status, an error code), never ids, names, paths or anything a user types.
- A collector that is down or refuses the metrics is logged once and never fails or slows a request.
- Tests receive the metrics with a collector served in the test, not a fake meter.
- Every other test sends no metrics: test homes set `otel.endpoint` to empty, so nothing reaches a collector the developer runs.

## Testing

- Every `wdx` run in the cli tests is checked to be logged from `command received` to `command returned`, with the logged response equal to what was printed, and every request it sent logged by the daemon under the same debug id.
- A command fails the tests when it returns a result or error code its help does not list, and `TestCommandDocs` checks every command's help is complete; keep both in sync rather than loosening them.
- Every command has integration tests that call `cli.Run` with `--json` against a home from `newHome`, which a daemon serves in the test process: one test per outcome, asserting `result` or `code`, and the exit code. Domain edge cases stay in domain tests; text output gets one smoke test per command.
- Starting the daemon in the background runs the `wdx` binary the cli tests build in `TestMain`; tests that do so stop it when they end.
- Every test calls `t.Parallel()`. Never use `t.Setenv`, `os.Chdir` or shared mutable globals; build settings with `config.Default()` and pass them in through `cli.Env`.
- All state lives in `t.TempDir()`: DB, repos, home. Never touch the network, `~/.wdx` or fixed ports. Unix sockets are the one exception: they, and homes that hold one, go in a fresh directory under `/tmp`, since a path under `t.TempDir()` can exceed the 104 bytes macOS allows.
- Daemon tests run an in-process daemon and call it through the generated client, checking every request is logged from received to returned.
- git runs with the user's config ignored (set in each package's `TestMain`).
- Each prompt answer has a test that types it into a PTY given as the command's stdin (`typed`, `answering`): without one, stdin is not a terminal and nothing is asked.
- Tests wait on what they need (a file the shell creates, a state, the bytes read), never a fixed sleep, and until `waittest.Deadline(t)` (what is left of the test binary's `-timeout`), never a fixed limit a busy machine may need more than; a timeout a test must shorten or lengthen is a field of `cli.Env.Timeouts`, not a global.
- Tests are light on memory and CPU, not only fast: wdx is developed on the machine it runs on. Stream large files rather than reading them whole, and keep loops no larger than the check needs.
- Run tests with `make test`: race detector, shuffled order, four packages at a time, and under a 3 GB memory cap where `systemd-run` can set one, as in CI.
