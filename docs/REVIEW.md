# Code review, October 2026

A read-only review of the whole codebase (structure, readability, consistency, duplication, completeness, edge cases, tests), before going further. The fixes go in the steps at the end, one at a time; each was designed with the human before its code.

## How it was done

Four reviewers each read one area against AGENTS.md and the docs, changing nothing in the repo: holder, pty and termview; cli and cmd; daemon, terminal, project, db, client, api, git and identity; config, logging, telemetry, update, observe, service, style, the Makefile, scripts and CI. Each ran its packages' tests repeatedly to find flakes. The holder reviewer proved H1 to H4 with throwaway tests in a copy of the repo. The most serious findings (H1, H2, C1, C2, D1, U1, U2) were then read again in the code before planning.

What holds throughout: the layout and dependency directions follow AGENTS.md; every test calls `t.Parallel()`; no test uses `t.Setenv`, `os.Chdir` or shared globals; waits poll against deadlines; tests use real PTYs, git, SQLite and an httptest collector. Logging, telemetry, style, config rewriting, release.sh and publish.sh had no findings.

### Test runs

| Packages | Runs | Result |
|---|---|---|
| holder, pty, termview | `-race -shuffle=on -count=5`, then `-count=20` | pass (holder 36 s and 125 s, termview 14 s) |
| holder, pty, termview | `-cpu 1,4 -count=5` while other test runs loaded the machine | **fail**: `TestOutputDrained` `read unix ...term-00000005.sock: i/o timeout` after 5.07 s (H3); passes alone 10× at `-cpu 1` and `-cpu 4` |
| cli, cmd | `-race -shuffle=on -count=3`, twice | pass (50 to 57 s); the old update flake did not show |
| daemon, terminal, project, db, client, api, git | `-race -shuffle=on -count=3`; terminal and daemon also `-cpu 1,2 -count=10`; the Reconcile, Stop/Start, Lost and Remove tests `-count=15` | pass |
| config, logging, telemetry, update, service, style | `-race -shuffle=on -count=5` | pass (logging's `TestConcurrentWriters` ~1.9 s a run, slow but steady) |

### Reproductions

| Finding | How | Result |
|---|---|---|
| H1 | attach, dial as the daemon, type `exit 4`, `Release` on `Exited` | the client missed `exited` 1 in 30 runs; with `sleep 30 & exit 4`, 4 in 15 |
| H2 | a fetch that always errors, the view scrolled up 500 lines | 248,845 fetches in 300 ms |
| H3 | dial the holder during `yes \| head -c 40000000` | slowest hello 294 ms, 3.07 s with `-race` on an idle machine; past 5 s under load (the failure above) |
| H4 | a shell running `stty raw -echo; while :; do printf '\033[6n'; done` | the daemon's dial fails after 5.0 s and stays wedged; a bounded burst of 2,000 queries does not wedge it |
| H5 (b) | `TestPaneReset` | passes only with `sleep 0.2` between the bad sequence and the next output, which otherwise lands in the dropped rest of the chunk |
| C1 | read: `main.go:36` passes `context.Background()`, `attach_view.go:52` uses `tea.WithoutSignalHandler()`, no `signal.Notify` outside `daemon start --foreground` | confirmed in code |
| C2 | ultraviolet `decoder.go:1139-1148`: 0x09 is `KeyTab`, 0x0D `KeyEnter` unless the legacy flags are set; 0x08 is `ctrl+h` | confirmed in code |
| D1 | read: `supervisor.go:179` `s.store.running(ctx, …)` on the request context after `holder.Start`, which ignores it | confirmed in code |
| U1, U2 | read: `undo` (update.go:381-428) never calls `Daemon.Stop`; `keep` (update.go:225-242) removes `previous/` before the rename | confirmed in code |

## Bugs

### Terminal runtime (Step 1)

| # | Bug | Where |
|---|---|---|
| H1 | Typing `exit` in an attached shell sometimes reports `connection_lost`, not `exited`: the holder sends `exited` to the daemon at once, the daemon releases it and the holder closes its clients, while their `exited` still waits up to 1 s for the last output (`exitClients`). Reproduced 1 in 30, 4 in 15 with `sleep 30 & exit 4`. `attach_test.go:200-204` is latently flaky for it | `holder/holder.go` `serve`, `attach.go` `exitClients` |
| H2 | A failed history fetch is retried in a tight loop: ~250k fetches in 300 ms (daemon down, terminal removed) | `termview/termview.go` `pageMsg`, `scroll.go` `fetched` |
| H3 | `hello` takes `screenMu`, which `feed` holds while the vt parses a 32 KiB chunk: under a flood the daemon's redial times out (5 s) and marks a running terminal `lost`. Also the flaky `TestOutputDrained` under load | `holder/holder.go` `hello` |
| H4 | A program that floods queries and never reads its input wedges the holder: `answer` writes the PTY blocking, the vt's `Write` waits for it, `feed` holds `screenMu`; attach, resize and stop hang | `holder/holder.go` `answer`, `screen.go` |
| H5 | The pane's history line numbers drift from the holder's: (a) the pane does not count the `history interrupted` line the holder writes on a vt reset; (b) the pane recovers once per 32 KiB chunk and drops its rest, the holder per 1 KiB piece; (c) the pane's `SetSize` recover does not reset its scrollback; (d) clients are never told a size another client set, so their vt draws and scrolls at the wrong size | `termview/termview.go`, `scroll.go` |
| H7 | A history frame can exceed the 1 MiB `maxPayload`: `historyFrame` counts raw bytes, JSON escapes `<>&` and ESC to 6 bytes. Only hits clients that ask for history on attach (tide); deferred until then | `holder/protocol.go`, `attach.go` `sendHistory` |
| C1 | A signal (SIGTERM, SIGHUP: `kill`, the window closed) during attach leaves the calling terminal raw, on the alternate screen, mouse on: `main` passes `context.Background()`, attach uses `tea.WithoutSignalHandler()`, nothing catches them | `cmd/wdx/main.go`, `cli/attach_view.go` |
| C2 | `--prefix-key ctrl-i` and `ctrl-m` are accepted and never fire: the decoder gives Tab and Enter (`ctrl-h` and the rest are fine) | `cli/attach.go` `parsePrefixKey` |
| D1 | Ctrl-C on `terminal create` or `project open` while a holder starts cancels the request context: the holder runs on, the row stays `starting` unwatched, and a later remove sweeps its directory under the live shell | `terminal/supervisor.go` `launch`, `daemon/http.go` `oneChangeAtATime` |

Smaller ones in the same code: `writeClient` can slice with a negative `n` if the spool dropped past `start.live` (attach.go); the pane's `scrollback.add` copies all 10,000 kept lines per harvest once full; `terminal_id` is logged twice on holder lines (cli/terminal.go and holder.go).

### Daemon lifecycle (Step 2)

- D2: a clone killed midway (client gone, shutdown timeout) leaves a partial checkout that a later `project open` reuses as valid; `git.go`'s "On failure git removes dest" is false then. Clone to a temp sibling and rename, or remove `dest` on failure.
- D3: a shutdown past its 30 s closes the DB and the supervisor under running handlers; a late `launch` then `follow`s into a leaked watcher. Wait for handlers in flight before `d.close()`; `follow` refuses once closed.
- D4: `Supervisor.Close` can hang forever when a watcher redials at that moment. Check `ctx.Err()` after a redial and close `next`.
- D5: `terminal remove --stop-running` returns the terminal as before the stop; re-read as `StopProject` does.
- D6: a project's terminals stop one after another (up to 10 s each) under the global change lock; stop them concurrently.
- D13: `Sweep` never cleans `run/*.sock` of killed holders.
- `stopOne` with no watch but a `HolderPID` should dial or kill the holder.

### `wdx update` (Step 3)

- U1: `undo` restores the binary and DB without stopping the new daemon (one that missed `startTimeout`, or answered with the wrong version, or that systemd restarted), then accepts any daemon that answers. Stop first; check the answering version is `o.Current`; kill the child `startDaemon` started when it times out.
- U2: `keep` removes `previous/` before renaming the new one in: a failure there, or in `db.ReadSchema`, reports a working update as failed and loses both rollback points.
- U3: Ctrl-C during the switch strands `update-*` / `replaced-*` dirs, never cleaned. Defer signals during the switch, undo on `context.WithoutCancel`, clean stale dirs under the lock.
- U4: rollback does not check the installed binary is the one the last update installed (install.sh in between): record it in `previous.json`, or install.sh removes `previous/`.
- U5: three hand-rolled temp-and-rename writes (config, update, db), none fsyncs; two `copyFile`s with opposite argument order. One shared atomic-write helper.
- `update --rollback --json` lacks `jsonRequires` for its restore prompt, and a flag for "no".

## Structure and duplication (Step 4)

- termview copies holder's vt wrapper (construction, mode tracking, ED 3 harvest, write pieces, recovery, the pipe close workaround, constants); the copies drifted (H5). Fixed in Step 1 by `internal/screen`.
- project and terminal stores repeat id generation, SQL exec with debug logging, the `:` name check, the `?1 = '' OR col = ?1` filter; a helper in `internal/db`.
- git test helpers (`gitCmd`, `gitRepo`, `bareRemote`) are copied in project, daemon and cli tests; `internal/gittest` exists for them.
- holder `Attach` and `Dial` repeat the greeting: one `greet`.
- cli: color mode lookup twice (`viewStyle`, `colorOn`); a `field` closure six times; the unavailable-error check twice; `detailProject` re-implements `decodeDetail`; deadline polling hand-rolled four times in tests.
- Placement: `project.go` is 887 lines (split errors, paths, store); `terminal history` lives in cli `attach.go`; the hidden `holder` command in `terminal.go`; result types half in `output.go`, half beside their commands; error-to-problem mappings half inline, half in `xxxProblem`; `ptr`, `optional`, `absolute` in `project.go`; the "project in use" rules in daemon handlers (`terminals.go`); `cli_test.go` mixes shared helpers with every project test.
- AGENTS.md's layout misses `identity`, `gittest`, `holdertest`; depguard enforces only cli-through-api (no rule keeps domain packages off daemon, client, cli); `cli -> telemetry` is not listed.
- scripts: `fetch-git-cliff.sh` and `fetch-shellcheck.sh` near identical, the sha256 fallback four times; the release layout is written in three places (dist.sh, `update.archive`, the test `publish`) and dist.sh never runs in CI.
- config: the find-setting-by-key search three times.

## Readability and consistency (Step 4)

- `"err", err` vs `"err", err.Error()`; the frame type logged as `type` and `frame`; `transition` logs a state change when nothing changed and does not log its SQL.
- Stale comments: "registered with warden" (project.go), "On failure git removes dest" (git.go), "Only screen uses the vt" (screen.go, ATTACH.md Dependencies), the `identity.Name` "set at build time" reason (service.go), `Env.TTY` "nil when there is none".
- `failurePrefix` on some commands only; not-found hints on terminal, not project; raw result strings in daemon, update, settings.
- Dead field `attachView.detached`.
- Names: `Env.Terminal` / `TerminalEnv()` (what shells inherit) vs `Env.TermEnv` / `TermEnv()` (the calling terminal): to rename, with the human.
- The environment read outside `main`: `os.Environ()` in `startDaemon`, `os.CreateTemp("")` (TMPDIR), `os.UserHomeDir()` in config's `defaultHome`; forbidigo catches only Getenv/LookupEnv.
- Relative paths: `absolute` uses the process cwd, terminal commands `Env.WorkDir`.
- Docs vs code: ATTACH_VIEW.md promises logs of scroll mode and fetched pages (only `vt_resets` is logged), says termview handles window size messages (the CLI calls `SetSize`), shows `history 1,234 of 56,789` (printed without separators); attach's output note "Once you detach or the shell exits, the terminal." and its `--json` line miss `exited`; openapi does not list `invalid_path` for a relative `dir`; `count` without `from` is ignored silently; "no such endpoint" is 404 with code `usage` (400); `Terminal` has no `updated_at`.
- `update.go:273` `from.Version > p.Schema &&` is redundant.

## Tests (Step 5)

- No test answers a prompt: `Stdin` is never a terminal, so every prompt branch is untested; give it a PTY.
- Missing: H1, H2, two clients at different sizes, numbering after a reset, queries suppressed while a client is attached, client cancellation midway, the kill after `stopWait`, `launch`'s holder-does-not-answer, `Supervisor.Close` during a redial, `project_mismatch`, `attach_unsupported` in the cli, `exited` text output, the title given back after attach, migration rules (numbering, STRICT, append-only), update's undo after back-up or install fails, a failing `keep`, `checksum`, `extract`, the GitHub API branch of `latest()` (make its URL a field).
- Timing: `TestPaneReset`'s `sleep 0.2`; 20 ms after each key in attach tests; `TestAttachConsistent` sleeps 20 ms; `terminal_test.go:426` checks the PID before the watcher's `ended` write (wait on stopped and PID 0); observe's `freePort`.
- `TestTerminalAttachConnectionLost` kills its leftover shell at the end, not in `t.Cleanup`.
- TestTerminalAttach and TestTerminalAttachRefused chain several outcomes in one test.
- CI runs tests on Linux only; macOS (launchd, flock, socket path length, PTYs) is a release target. A `macos-latest` job, at least on main and tags.
- ci.yml's commit range breaks on a force push (`before` unreachable).

## Steps

1. Terminal runtime bugs and the shared vt wrapper: H1–H5, C1, C2, D1. Done; below.
2. Daemon lifecycle: D2–D6, D13.
3. `wdx update` failure handling: U1–U5.
4. Tidying: duplication, placement, logging, stale comments, names. Done; below.
5. Test gaps, macOS in CI. Done; below.

Then the vt fork (docs/VT.md), which after Step 1 is one swap in `internal/screen`.

## Step 1 design

Approved by the human. **Done** 2026-10-29: `8ecb74d` to `60f4808`, one commit each. One commit per fix, the refactor first; `make check` and `make test` before each; commit only when asked.

1. **`refactor:` share the vt wrapper.** New package `internal/screen` (the name is approved): the holder's `screen.go` moved and exported, used by holder and termview, the only importer of `x/vt` (a depguard rule; AGENTS.md layout and dependency directions: holder -> screen, termview -> screen; docs/VT.md and ATTACH.md updated). It feeds output in 1 KiB pieces, harvests, recovers per piece and reports resets, tracks modes, title, cursor style and margins, and gives the input/answers stream through a callback. termview keeps keys, mouse, paste, rendering and its scrollback on top. Fixes H5 (b) and (c); the holder's screen tests move with it. **Done**: a piece the vt fails on is fed again a byte at a time after the reset, so what follows the bad sequence is drawn; the cli's attach test drives its calling terminal through `screen` too.
2. **`fix:` numbering after a reset (H5 a).** On a reset the pane adds the same `history interrupted` line the holder writes, so both number every line alike. The holder's "fell too far behind" reset (over 16 MiB behind) still drifts; noted in ATTACH_VIEW.md, logged. **Done**: lines kept across a reset rather than dropped; a failure whose output reaches holder and pane in different pieces can still drift, noted too.
3. **`feat:` the pane follows the terminal's size.** A new holder-to-client frame `resized` `{cols, rows}`, queued in output order whenever the size changes. The pane resizes its vt to it and draws it cropped or padded in its area. A key pressed in a pane whose area differs from the terminal's size sends its size first (tmux's latest). Protocol stays version 2: old clients ignore unknown frames (`Session.Read`), old holders never send it. ATTACH.md's protocol table. **Done**: a pane that sent a size ignores the sizes told before its own comes back, and the holder answers every `resize`, even one that changes nothing, so that always happens.
4. **`fix:` hello never waits on the vt (H3).** The next history line number in an atomic, set by `writeHistory`; `hello` reads it without `screenMu`. **Done**: the slowest of 10 hellos during a 40 MB flood went from 3.1 s (race) / 385 ms to about 1 ms.
5. **`fix:` answers never block (H4).** `answer` queues to a bounded channel written to the PTY by its own goroutine; full drops, logged once. **Done**.
6. **`fix:` attached clients get `exited` (H1).** On release, `serve` waits until every attached client was written `exited` and left, at most `exitDrain`, before `closeClients`. **Done**, at most twice `exitDrain`: the clients' `exited` itself waits up to `exitDrain` for the last output.
7. **`fix:` a failed fetch is not retried at once (H2).** A `pageMsg` with an error returns no command; the next scroll tries again; the status line shows it meanwhile. **Done**: one scroll fetched 23,264 times in 200 ms before, once after.
8. **`fix:` attach gives the window back on a signal (C1).** Attach runs under `signal.NotifyContext(SIGINT, SIGTERM, SIGHUP)`; Bubble Tea is killed through its context, restores the terminal, and the result is `detached`. Only attach: catching signals in every command is a separate decision. **Done**: tested with the built binary in a PTY of its own; `wantLogged` takes the pid of the process that ran the command.
9. **`fix:` refuse `--prefix-key ctrl-i` and `ctrl-m` (C2)** with a usage error saying they are Tab and Enter. **Done**.
10. **`fix:` changes finish when the client leaves (D1).** `oneChangeAtATime` runs requests that change something on `context.WithoutCancel`: a Ctrl-C'd create still completes and shows in the list; a clone is no longer killed by the client leaving. **Done**: a create cancelled once its holder starts stayed `starting` 10 of 10 times before, reaches `running` 10 of 10 after.

Tests, each failing before its fix: H1 attach, dial as daemon, `exit`, release, repeated; H2 a fetch that always fails is called once per scroll; H3 hello stays fast during `yes | head -c 40000000`; H4 a never-reading query loop still lets the daemon dial and stop; H5 numbering equal to the holder's after a reset, a second client's size reaching the first and taken back by a key, `TestPaneReset` without its sleep; C1 the built `wdx` in a PTY given SIGTERM, the window given back; C2 table cases; D1 a create cancelled midway reaches `running`.

## Step 2 design: daemon lifecycle

Approved by the human. **Done** 2026-10-29: `3ab5eb2` to `b8efba2`. Each change finishes what it started, or cleans up what it leaves, however the client or the daemon ends. Since Step 1's D1 fix a client leaving no longer cancels a change, so what is left is the daemon giving up on a change at shutdown, or dying. One commit per fix, each with a test failing before it.

1. **`fix:` clone into a temporary sibling (D2).** `cloneAndOpen` clones into `.<name>.clone-<random>` beside `dest` and renames it into place only on success, so `dest` only ever exists complete. A clone that dies leaves only the hidden directory; the next clone into the same parent removes such leftovers (changes run one at a time, so none is in progress). `git.go`'s "on failure git removes dest" corrected. Test: a clone killed midway leaves no `dest`, and the next open clones afresh. **Done**: git's progress now names the hidden directory (`Cloning into '.app.clone-…'`).
2. **`fix:` shutdown cancels changes that outlive it, then waits for them (D3).** The daemon gets a context cancelled only when `shutdownTimeout` runs out; changing requests run on the client's context without its cancellation (as now) and with this one, so a change still running then is cancelled: a clone is killed (fix 1 cleans up), a launch fails fast. The daemon then takes `d.changes` itself, which every change holds, waiting at most `stopWait` (stopping terminals waits on timers, not the context), and only then closes the DB and the supervisor. A closed supervisor's `follow` refuses and closes the connection it is given. Decided: cancel rather than wait without limit (a stalled clone would hang `wdx daemon stop` until systemd kills the daemon). Test: a 100 ms shutdown limit during a slow project close; the daemon stops after the change, with its rows written. **Done**: a cancelled close stops its terminals (recorded by the supervisor) but leaves the project open, its last write cancelled; `wdx daemon stop` now waits 50 s, not 40.
3. **`fix:` close during a redial (D4).** After a successful redial, a watcher whose supervisor is closing closes the new connection and returns; a redial failing because of the shutdown no longer marks the terminal `lost`. Test: `Close` while a holder is being redialled returns. **Done**: the test holds the redial's handshake until the close began; it hung 3 of 3 times before.
4. **`fix:` remove reports the stopped terminal (D5).** `Remove` reads the row again after `stopOne`, before deleting it, as `StopProject` does. Test: `TestRemoveTerminal` checks `state` and `pid`. **Done**.
5. **`fix:` stop a project's terminals together (D6).** `stopOne` splits in two: mark stopped and send Stop; then wait for the holder to report the shell gone, killing it otherwise. `StopProject` does the first for every terminal, then the second for every one against one deadline: a project closes in about one `stopWait`, however many terminals it has. Test: three terminals that ignore SIGHUP close in about one grace. **Done**: 3.0 s before, 1.1 s after, with a 1 s grace.
6. **`fix:` stop holders nobody watches.** `stopOne` finding no watch on a live row dials the holder first: one that answers is followed and stopped as usual; one that does not leaves the row marked ended, as now. Decided: never kill the recorded pids, which may have been reused; a holder that does not answer is gone, as `Reconcile` already assumes. Test: a row left `starting` with a live holder is stopped by remove. **Done**.
7. **`fix:` sweep stale sockets (D13).** `Sweep` also removes `run/term-*.sock` whose terminal has no row, or a row neither live nor watched. Test: a dead holder's socket is gone after a sweep. **Done**.

Settings for tests (decided): `daemon.Options` gets `ShutdownTimeout` and `StopGrace` (passed on to the supervisor's `Options`), defaulting to today's 30 s and 5 s; `stopWait` stays grace plus 5 s. Not user settings.

Impact: closing a project with several terminals blocks other changes about 10 s at most, not 10 s per terminal; `wdx daemon stop` takes at most 30 s plus 10 s when a change is in flight at the limit, rather than closing the DB under it; nothing is left half done on disk. No API or CLI change, except `terminal remove --stop-running` reporting `stopped`.

## Step 3 design: `wdx update` failure handling

Approved by the human. **Done** 2026-10-29: `f5669eb` to the release tests, with `d4b7996` (a held signal always said) and the streaming of release archives and test binaries (`ae486e3`, `3aa4a3e`) found on the way. An update either completes, or leaves the previous version running on its own database; a rollback point is never lost. One commit per fix, each with a test failing before it.

1. **`fix:` undo stops what it started (U1).** When a step after "stop" fails, `undo` stops the daemon first, then puts back the binary, database and config file, then starts it; the version answering must be `o.Current`, else `UndoErr` names the one that answered. `startDaemon` kills the process it started itself when it gives up waiting (which also fixes `wdx daemon start` leaving a daemon it reported as failed); a service's daemon is stopped through the service. Test: a release whose daemon answers too late is undone, and the old version answers. **Done**: tested with a release whose daemon answers as another version (a late one would take the 10 s start timeout); `wdx daemon start` killing a daemon that never answers has its own test.
2. **`fix:` keep never loses a rollback point (U2).** `keep` renames the old `previous/` aside, renames the replaced files in (renaming the old one back if that fails), then deletes the old one. What fails after the switch succeeded (`keep`, reading the schema) is a warning on a successful update, never `update_failed`. Decided: the update JSON gets a `warnings` key (a list of strings, empty when none); text mode prints `warning: …` on stderr. Test: a `previous/` that cannot be replaced gives a successful update with a warning, and the old `previous/` still rolls back. **Done**, tested with an old `previous/` that cannot be deleted: before, the working update failed with exit 3; now it succeeds with a warning, the replaced version kept and rolled back to. A `keep` that fails outright leaves the replaced files in `replaced-*` and says so.
3. **`fix:` interrupts (U3).** SIGINT, SIGTERM and SIGHUP are held while the switch runs (stop, back up, install, start, undo); progress says so, and the command ends once the switch has (updated, or `update_failed` if undone). Undo runs on `context.WithoutCancel`. `Apply` and `Rollback` remove stale `update-*` directories (downloads only) under the update lock. Decided: stale `replaced-*` directories, left only by a `wdx` killed mid-switch and possibly the only copy of the version it replaced, are kept; the next update or rollback logs them and prints a warning naming them. Test: a SIGTERM during the switch leaves the new version running; a stale `update-*` is removed and a stale `replaced-*` warned about. **Done**: the warning goes in `warnings`, so text prints it once, with the result.
4. **`fix:` rollback checks what is installed (U4).** `previous.json` also records the version the update installed; a rollback when another one is installed fails with `no_previous_version` and a hint that another install replaced it. A `previous.json` without the field, from before, is trusted as now. Test: a rollback after the installed version changed is refused. **Done**: the update tests now roll back with the installed binary, as a user does; run in the test process (version `dev`), a rollback after an update to 0.2.0 is such a changed install.
5. **`refactor:` one atomic write (U5).** New package `internal/atomicfile` (name approved): `atomicfile.Write(path, data, mode)` and `atomicfile.Copy(dest, src, mode)` write a temporary file beside the target, fsync it, rename it into place and fsync the directory. config, update and db use it; their own copies go. AGENTS.md's layout lists it. **Done**: `Copy` streams, so a large database is not read into memory.
6. **`fix!:` `update --rollback --json` answers its prompt up front.** A `--cancel` flag answers no ("if the previous version needs the backup restored, change nothing"), returning `restore_required` with its hint as a declined prompt does now; rollback's `jsonRequires` is `--discard-changes` or `--cancel`, as other prompting commands. `BREAKING CHANGE:` `update --rollback --json` without either is a usage error. Decided after weighing: the outcome of no flag stays the same, but `--json` never chooses an answer for the script. **Done**.
7. **`fix:` launchd waits for the daemon to stop.** The plist sets `ExitTimeOut` to 60 s (launchd's default 20 s kills a daemon still finishing a change at its 30 s limit); a daemon that stops sooner is not waited for. Test: the generated plist carries it. **Done**: the test runs install.sh as on macOS (stubbed `uname` and `launchctl`, a release in a directory); before, nothing tested the service files.
8. **`test:` the release code.** Unit tests for `checksum`, `extract` and `comparePre`; the GitHub API URL becomes a field of `releases` so a test serves it. **Done**.

Impact: an update failing at any step ends with the old version answering, or an error naming what does; a rollback point is never deleted before its replacement is in place, nor is a working update reported failed; Ctrl-C during the few seconds of the switch waits for it. Breaking only for `update --rollback --json` scripts (fix 6). The update JSON gains `warnings`.

## Step 4 design: tidying

Agreed with the human on 2026-10-29. **Done** (a088332..e4d95e7), with these differences: result types went beside their commands, not into `output.go`; config's default home stays the one marked read of the environment outside `main` (it is the settings table's default, which help and `Default` show), and install.sh and dist.sh keep their own sha256 (install.sh runs alone); item 6 is a `feat:` (the failure prefixes and the project hint show), and item 8 a `chore:`; `TestLost` flaked once (it killed the shell before the holder) and was fixed in b6d8c47. One `refactor:` commit per item and no behaviour change, except the three marked fixes and the one feature at the end. The items are the lists under "Structure and duplication" and "Readability and consistency" above. In order:

1. Shared helpers: `internal/db` id and exec helpers for both stores; the git test helpers (`gitCmd`, `gitRepo`, `bareRemote`) into `gittest`; holder `greet`; the cli's color mode, `field`, unavailable check and `decodeDetail`; one `eventually` test helper for the four polling loops.
2. Placement: split `project.go` (errors, paths, store); `terminal history` and the hidden `holder` command into their own cli files; result types beside their commands (as daemon, update and observe had them), and error mappings as one `xxxProblem` per command group that `problem` chains; the daemon's project handlers and "in use" rules in `projects.go`; the cli project tests in `project_test.go`, leaving `cli_test.go` the shared helpers.
3. Logging: `"err", err` everywhere (slog prints the message); `frame` the one key for the frame type; `transition` logs only real changes, and its SQL at debug.
4. Comments and docs: the stale ones listed; ATTACH_VIEW.md's logs and size handling; attach's output note and `--json` line with `exited`; openapi's `invalid_path` for a relative `dir`.
5. Rules: AGENTS.md's layout gets `identity`, `gittest`, `holdertest`, `screen` and `cli -> telemetry`; depguard keeps domain packages off daemon, client and cli; forbidigo adds `os.Environ` and `os.UserHomeDir`, and `main` passes what the cli and update need (`startDaemon`, update's daemon start, config's `defaultHome`); `absolute` uses `Env.WorkDir`.
6. Consistency: `failurePrefix` on every changing command; not-found hints on project too; result constants in daemon, update and settings; the dead `attachView.detached` removed.
7. Names: `Env.Terminal` becomes `Env.ShellEnv` (what shells inherit from the daemon), `Env.TermEnv` becomes `Env.CallerTerm` (the terminal `wdx` runs in); the daemon's `store` field becomes `projects`.
8. Scripts: one `fetch-release.sh` and one sha256 function.

Fixes and the feature, each its own commit:

- `fix!:` history's paging. `from` and `count` are one mode, `tail` the other: `count` alone starts at line 0 (the oldest kept), `from` alone takes 1000 lines, `tail` with either is a usage error (`usage` from the API), neither gives the default tail. Breaking: `count` alone and `count` with `tail` were ignored before. History lists once; following new lines (`--follow`) is a feature for later, if wanted.
- `fix!:` an unknown endpoint answers 400 `usage`, not 404 with that code.
- `feat:` `Terminal` gets `updated_at`, the time of its last state change; the column exists, only the API leaves it out.

## Step 5 design: tests and CI

Approved on 2026-10-30. **Done** 2026-10-30: `239a73f` to the commit marking it done; `make check` and `make test` before each. The CI changes passed on Linux once pushed; the macOS job did not (item 6). The CI commits are `chore:`, since `commits.sh` has no `ci` type. The test resource items were approved on 2026-10-29, after `make test` got the session's terminal killed for memory (the update tests peaked at 6 GB, fixed by `3aa4a3e` and `ae486e3`): tests are kept light on memory and CPU, not only fast, since wdx is developed on the machine it runs on.

1. **`test:` resources.** `make test` runs at most four packages at once (`-p 4`) and, where `systemd-run` is available, under a 3 GB memory cap without swap; elsewhere (macOS) it runs uncapped. CI's Linux job runs under the same cap, so a regression fails CI instead of killing a machine. Each `TestMain` that builds `wdx-bin*` first removes the ones killed runs left behind, making them writable first. The slow packages (`logging`, `cli`, `holder`, `daemon`) are timed with `-json`, their sleeps and repeated setup cut; timings before and after go here. **Done**: `cli.Env.Timeouts` (start and completion) took `TestDaemonStartGivesUp` from 10 s to 1 s and fixed `TestCompletionLoggedAtDebug` early; the logging test writes 400 lines, not 3,200. cli 18 s to 10 s, logging 4.7 s to 1.3 s; the whole suite peaks at 1.9 GB. The sweep covers the homes under `/tmp` too, killing their daemons. `holder` stays at about 7 s: `TestSessionOutlivesWriteTimeout` idles past the 5 s `writeTimeout`, a constant, using no CPU.
2. **`test:` prompts.** The cli tests give `Stdin` a PTY, and each prompt gets a test per answer: project create o/n/c, open c/r, terminal remove's stop-running y/n, update's rollback y/N. **Done**, plus an answer that is not a choice asked again.
3. **`test:` the missing tests**, one commit per package: holder (H1, H2, two clients at different sizes, numbering after a reset, queries suppressed while a client is attached, a client cancelling midway); terminal and daemon (the kill after `stopWait`, `launch`'s holder that does not answer, `Supervisor.Close` during a redial, `project_mismatch`); cli (`attach_unsupported`, `exited` in text output, the title given back after attach); update (undo after the backup or install fails, a failing `keep`, `checksum`, `extract`, the GitHub API branch of `latest()`, its URL made a field). **Done**, except what Steps 1 to 3 had already tested: H1, H2, two clients at different sizes, numbering after a reset, `Supervisor.Close` during a redial, a failing `keep`, `checksum`, `extract`, `latest()`; `project_mismatch` was tested in the cli and project. "A client cancelling midway" is a client that leaves while attaching; `holder.Attach` itself heeds its context only through the dial deadline, so a cancel during the handshake waits up to `dialTimeout`: not fixed here.
4. **`test:` timing and cleanup.** `completionTimeout` becomes a `cli.Env` field tests raise, fixing `TestCompletionLoggedAtDebug` under load; users see no change. The 20 ms after each key in the attach tests and `TestAttachConsistent`'s sleep become waits on the output; `terminal_test.go:426` waits on stopped and PID 0. `TestTerminalAttachConnectionLost` kills its shell in `t.Cleanup`; `TestTerminalAttach` and `TestTerminalAttachRefused` split into one test per outcome. **Done**: a key waits until attach has read it from its terminal; `TestAttachConsistent` attaches once the shell has printed 1,000 lines. Observe's `freePort` is left as it is.
5. **`test:` migrations.** A test checks the numbering has no gaps, every table is STRICT, and each committed migration matches a checksum recorded in the test (append-only); a new migration adds its checksum there. **Done**.
6. **`ci:` macOS and the commit range.** A `macos-latest` job runs `make test` on every push and PR (the repo is public: its minutes are free). The commit message check, when `github.event.before` is unreachable (a force push, a new branch), checks only the pushed commit. **Done**, then the macOS job removed: its first runs failed, so `v0.2.0-dev.97` was not published. The commit range is kept. What it found, open until a Mac can run the tests:
   - cli and daemon `TestCreate`, cli `TestOpenGit`: `/tmp` is a link to `/private/tmp`, which the daemon resolves in worktree paths; the tests compare the unresolved home.
   - holder `TestStartFails`: macOS has no `/bin/false`.
   - holder `TestOutputDrained` (4 MB) and `TestAttachSlowClientDropped` (30 MB): the shell does not finish within 10 s; both take 0.6 s on Linux.
   - cli `TestInstallLaunchd`: the daemon launchd starts does not answer within 10 s.
   - cli `TestTerminalAttachScroll` timed out in one run of two.
7. **`ci:` update end to end.** CI builds a release for the host with `dist.sh` and runs `wdx update` against its output through `BaseURL`, then the rollback. **Done**: `scripts/test-update.sh` (`make test-update`) in the Linux job, about 9 s; `dist.sh` takes `DIST` and `PLATFORMS`.
8. **`docs:`** this file and AGENTS.md's testing section. **Done**, with docs/RELEASING.md's CI section.

## After the steps: the vt fork

Per docs/VT.md, in the order users notice: #11 (a snapshot under less or vim blanks the shell's screen afterwards), #10 (the pen and character sets lost on attach), #3 (lines scrolled in a region go to history), push #2 (fixed in the fork), then #1 (the alternate screen's scrollback memory) and #7 (speed, an ultraviolet fork). After Step 1 only `internal/screen` imports the vt, so the fork is one `replace` in `go.mod`.
