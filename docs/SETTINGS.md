# wdx setting: read and change settings from the CLI

Status: implemented, uncommitted (see Plan). Worktree `.worktrees/setting-command`, branch `setting-command`.

`wdx setting list` and `wdx setting get` show the settings by their dotted keys; `wdx setting set` changes one, in `config.yaml` and at once in the running daemon.

## Decisions

Agreed with the human:

- **The command is `wdx setting`**, singular like `project`, `terminal` and `agent`. `settings` stays as an alias of it, permanently (see "`setting write` and the alias").
- **`wdx setting list`** shows every setting: dotted key, value, default, description and where the value came from (the default, the file and line, or the variable).
- **`wdx setting get --name <key>`** shows the same for one setting.
- **`wdx setting set --name <key> --value <value>`** writes the file, then tells the daemon.
- **`--name` completes setting names**, from the settings table, so it works without a daemon. `get` offers every setting; `set` leaves out `home`, which cannot be set.
- **`otel.*` applies immediately**, like the daemon's other settings: no restart needed.

Decided on the way:

- **The file is the source of truth.** Each `wdx` command loads it when it starts, so a setting only commands read needs nothing beyond the write.
- **`set` writes first, then asks the daemon to reload.** If it dies between the two, the saved value is correct and the daemon takes it at its next start.
- **The daemon re-reads the file** rather than being sent values, so the two cannot disagree. A reload also picks up hand edits made since the daemon started.
- **`set` always asks for a reload, even when the value is unchanged**, so running it again recovers from a reload that failed.
- **Writes take a lock.** Two `set`s at once, say from two agents, would otherwise lose one update. `setting set` and `setting write` hold a file lock around reading and writing the config file.
- **Running terminals keep what they started with.** `terminal.shell` and `terminal.history_lines` go to a holder when a terminal starts, so they apply to new terminals.
- **A running `tide` keeps its colors** until it is reopened.

## When each setting takes effect

| Setting | Read by | Effect of `set` |
|---|---|---|
| `no_input`, `color`, `update.channel`, `update.base_url`, `terminal.history_tail` | each command | the next command |
| `log.level`, `log.retention_days` | each command and the daemon | immediately |
| `terminal.shell`, `terminal.history_lines` | the daemon, when a terminal starts | new terminals |
| `otel.endpoint`, `otel.interval_seconds` | the daemon's metrics exporter | immediately; the metric's counts restart from zero, a normal counter reset for Grafana |
| `home` | `WDX_HOME` | cannot be set; `list` and `get` show it |

## Results

- `set` returns `result` (`updated` or `unchanged`, whether the file changed), `setting`, `reload` (what the daemon did: `applied`, `not_running`, or `not_applied`) and `dropped_settings` (keys no setting has, moved to the end of the file).
- `set` exits 0 once the file is written. `not_applied` also warns and hints `wdx daemon restart`, since the value is saved but the daemon has not taken it.
- `not_applied` covers a daemon that refused the file and a daemon of an older version without the reload request (404), such as the one still running while `install.sh` runs.
- A value the setting does not accept, a name no setting has, or `home` is a `usage` error, and the file is left alone.

## Limits kept as they are

- An invalid value in the file fails every command at startup, `setting set` included, so that is fixed by hand. Unknown keys do not block `set`: the rewrite moves them to the end of the file, as `setting write` does.
- Comments of the user's own in the file are lost on a rewrite, as with `wdx update`.
- The header the rewrite writes and `wdx help settings` change to say `wdx setting set` applies at once, and that hand edits need `wdx daemon restart`.

## The daemon's reload

`POST /v1/daemon/reload`, with no body, re-reads the settings and applies them. depguard keeps `config` out of the daemon, so `runForeground` in `cli` gives it a reload function in `daemon.Options`, which calls `config.LoadHome`. The home is the only setting from the environment and is already known, so no variable is read after `main`.

On a file that does not load, the daemon keeps its current values and the request fails with `reload_failed`.

A reload does not take the lock that runs changes one at a time: it touches nothing they use, and a clone can hold that lock for minutes. It has a lock of its own, and runs to its end when the client leaves.

- `log.level`: a `slog.LevelVar`.
- `log.retention_days`: changeable on the `DailyFile`.
- `terminal.shell`, `terminal.history_lines`: read by the supervisor at each terminal start.
- `otel.*`, only when they changed: start a new exporter, swap the `wdx.request.duration` instrument atomically, since requests record to it meanwhile, and let the old exporter send its last metrics in the background, so a collector that hangs does not hold up the request.

## `setting write` and the alias

`settings write` is not replaced by `setting set`. It refreshes the whole file for a version of wdx, adding new settings and moving removed ones out of the way, where `set` changes one value and tells the daemon.

- **`write` stays.** `wdx update` runs it, with `--json --to <scratch file>`, to check a release accepts the settings before touching anything. `install.sh` runs it on every install, and the startup error for unknown keys suggests it.
- **The name `settings write` is a permanent contract.** Every installed wdx runs the release it updates to as `settings write`, and that code can never be changed. Removing the alias would make every update from those versions fail in that check.
- **`wdx update` and `install.sh` keep calling `settings write`.** `wdx update --version` and `WDX_VERSION` can install older releases, which have no `setting` command. Moving them would gain nothing.
- **`RewritesSettings` matches the command under both names**, since `main` must let it run despite unknown keys.
- **`--set` is removed from `write`.** Only `install.sh` uses it, and only dev releases have it (v0.2.0-dev.159 on). `install.sh` moves to `setting set`, with its warning for releases that lack the command. The README and docs/RELEASING.md follow. An `install.sh` from before the change keeps working: it warns and names `wdx update --channel dev`.

Help lists `write` as the command that refreshes the file and `set` as the one that changes a value.

## Plan

Every step is done in the worktree; nothing is committed.

1. **API**: `reloadDaemon`, the `reloaded` result and the `reload_failed` code in `api/openapi.yaml` and `internal/api/codes.go`; `make generate`; `client.ReloadDaemon`.
2. **Config**: `config.LoadHome`, `config.Lock` on `identity.ConfigLockPath`, `config.Set`; the file's new header.
3. **Daemon applies settings**: `logging.New` takes a `slog.Leveler`, `DailyFile.SetRetentionDays`, `Supervisor.Reconfigure`, `daemon.Settings`, `Options.Reload`, and an exporter that can be replaced.
4. **Commands**: `setting` with the alias `settings`; `list`, `get`, `set`, `write`; `--name` completion; `RewritesSettings` under the new name and for `set`; `--set` removed; `runForeground` gives the daemon its reload.
5. **Install and docs**: `install.sh` uses `setting set`; README, docs/RELEASING.md, the help topic and AGENTS.md.
6. **Tests**: one per outcome in `internal/cli/settings_test.go`, with a real background daemon taking `log.level` and `terminal.shell` without restarting; `internal/daemon/reload_test.go`, moving metrics between two collectors; `internal/config`, including sets at once; `internal/logging`.

The commit is a `feat!:` with a `BREAKING CHANGE:` footer: `settings write --set` is gone, and a settings command's path is now `wdx setting ...`.

## Suggested, not included

- `wdx setting unset --name`, to return a setting to its default.
- `wdx daemon reload`, to apply hand edits without a restart.
- `--value` completion for settings with fixed choices (`color`, `log.level`, `update.channel`, `no_input`).
