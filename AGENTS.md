# Guidance for AI agents

This file applies to every AI working on this repo (Claude, Codex, Cursor, Antigravity, ...).

## Workflow

- Never start coding until the human explicitly says so.
- Brainstorm with the human first: work out the complete solution (structure, tables, commands, names, edge cases) and get it approved before writing any code.
- Work on one feature at a time. Do not add scope the human did not ask for; suggest it instead.
- Commit only when the human asks.

## Code style

- Keep the code clean and readable by a human.
- Prefer self-explanatory code over comments. Comment only what the code cannot say: the why, a non-obvious constraint, a workaround.
- Use good names. When a name for a variable, type, function or package is not obvious, discuss it with the human.

## Project layout

warden manages AI CLI agents in PTYs, plus projects, messaging, a TUI and later pipelines. The CLI is `wdx`.

```
cmd/wdx/        entry point
internal/cli/   cobra commands: parse args and print, no business logic
internal/config/   the settings, loaded once at startup from their sources
internal/db/    opens the single SQLite DB, runs embedded migrations
internal/git/   thin wrapper over the git CLI
internal/logging/  JSON-line logs in daily files; the logger travels in the context
internal/<domain>/  one package per feature (e.g. project): types, Store, tests
```

- Dependencies point one way: cmd -> cli -> domain -> db/git. Every package may use logging.
- Name packages by what they provide, never by layer (no models/, services/, utils/).
- Add interfaces only where a consumer needs them, not up front.
- Tests use real dependencies (a temp SQLite DB, the real git binary), not mocks.

## Database

- One SQLite database (`~/.wdx/wdx.db`) holds every table. Never add another DB file.
- Migrations live in `internal/db/migrations`, are numbered, and are append-only: never edit a committed migration.
- Conventions: STRICT tables, INTEGER unix-millisecond timestamps, NULL for absent values, prefixed random ids (`proj-`, `wt-`), indexed foreign keys with ON DELETE CASCADE, user-facing names `COLLATE NOCASE`, JSON in TEXT columns with a `json_valid` CHECK.

## CLI conventions

Every command serves both humans and scripts or AIs.

- Only commands are positional; every input is a flag. An entity is named by `--id` or `--name`, exactly one of them.
- Every prompt choice that makes sense without a human has a flag. Without that flag and without a terminal (or with `--no-input` / `WDX_NO_INPUT=1`), fail when the question comes up, with a hint naming the flags.
- Every command supports `--json`, which never prompts: the flags that answer a command's prompts are required up front (a `usage` error otherwise). stdout then holds exactly one object and nothing else:
  - success: `{"ok": true, "action": "created", "project": {...}}`, with the key named after the entity and command-specific extras such as `dropped`;
  - failure: `{"ok": false, "error": {"code": "name_taken", "message": "...", "hint": "...", "details": {"project": {...}}}}`.
- `action` names the most significant thing that happened: `created`, `registered`, `cloned`, `opened`, `closed`, `removed`. Read-only commands have no `action`; they return the entity under its own key (`project`, or `projects` for a list).
- JSON keys are snake_case, absent values are `null`, timestamps are RFC 3339. Leave internal fields out: everything in the JSON is a contract.
- Text mode prints results to stdout and errors to stderr; the first line says what happened.
- Exit codes: 0 ok, 1 rejected (conflict, not found, cancelled), 2 usage, 3 system failure.
- The domain returns typed errors and knows nothing about output. `internal/cli/output.go` owns the error codes (each with its exit code and a description), maps errors to them and renders both formats from one response value. Hints are added by the cli, not written into domain messages.
- Every command documents itself with annotations: `output` (what it prints), `actions`, `errors` (codes besides `usage` and `internal`) and `jsonRequires` (the flags that answer its prompts; the check is shared, not written per command). `--help` renders them after cobra's usage text, or as one JSON object with `--json`; subcommands are listed alphabetically. Every usage error hints at the command's `--help`.

## Config

- Every setting is a row in the table in `internal/config`: key, description, default (or required) and parser. The help's Settings section and the logs are built from it.
- `main` is the only place that reads the environment: it loads the `Config` once and passes it to `cli.Run`. Nothing else calls `os.Getenv`.
- An invalid or missing required setting fails every command at startup with a `usage` error listing all of them.
- `cli` holds the `Config`; domain packages get only the values they need as arguments and never import `config`.
- Sources are layered, later winning: defaults, then the environment (`log.level` is `WDX_LOG_LEVEL`). A config file will slot in before the environment.

## Logging

The log must tell the story of any run: what was asked, every step taken and why, and what was returned.

- Get the logger with `logging.FromContext(ctx)`; it already carries the run's `debug_id`. Never use slog's default logger or a global, and pass `ctx` to every goroutine so its lines keep the id.
- `cli.Run` logs `command received` (raw args and settings), `command started` (resolved command and every flag) and `command returned` (exit, outcome and the exact response). Failures are logged there at error level; don't log the same error again on its way up.
- Log each step where it happens, at info: lookups and what they matched, decisions and their reason, every change to the database or disk, git commands, and how each question was answered. At error level, log what a failure knows that its returned error does not (git's stderr, a failed cleanup).
- Debug is for detail: SQL with arguments, git output.
- Messages are short lowercase statements of what happened (`project registered`); keys are snake_case and reused (`project_id`, `path`, `name`, `duration_ms`, `err`).
- Never log the environment beyond wdx's own settings, prompt text or agent output. URL credentials are redacted by the logger.
- Logging never fails a command and never writes to stdout or stderr.

## Testing

- Every `wdx` run in the cli tests is checked to be logged from `command received` to `command returned`, with the logged response equal to what was printed.
- A command fails the tests when it returns an action or error code its help does not list, and `TestCommandDocs` checks every command's help is complete; keep both in sync rather than loosening them.
- Every command has integration tests that call `cli.Run` with `--json` and a temp `WDX_HOME`: one test per outcome, asserting `ok`, `action` or `error.code`, and the exit code. Domain edge cases stay in domain tests; text output gets one smoke test per command.
- Every test calls `t.Parallel()`. Never use `t.Setenv`, `os.Chdir` or shared globals; build settings with `config.Default()` and pass them in through `cli.Env`.
- All state lives in `t.TempDir()`: DB, repos, home. Never touch the network, `~/.wdx` or fixed ports.
- git runs with the user's config ignored (set in each package's `TestMain`).
- Run tests with `make test` (race detector, shuffled order).
