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
internal/db/    opens the single SQLite DB, runs embedded migrations
internal/git/   thin wrapper over the git CLI
internal/<domain>/  one package per feature (e.g. project): types, Store, tests
```

- Dependencies point one way: cmd -> cli -> domain -> db/git.
- Name packages by what they provide, never by layer (no models/, services/, utils/).
- Add interfaces only where a consumer needs them, not up front.
- Tests use real dependencies (a temp SQLite DB, the real git binary), not mocks.

## Database

- One SQLite database (`~/.wdx/wdx.db`) holds every table. Never add another DB file.
- Migrations live in `internal/db/migrations`, are numbered, and are append-only: never edit a committed migration.
- Conventions: STRICT tables, INTEGER unix-millisecond timestamps, NULL for absent values, prefixed random ids (`proj-`, `wt-`), indexed foreign keys with ON DELETE CASCADE, user-facing names `COLLATE NOCASE`, JSON in TEXT columns with a `json_valid` CHECK.
