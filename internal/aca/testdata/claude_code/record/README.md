# Recording Claude Code

These throwaway scripts made the recordings in `../2.1.314/` (docs/AGENT_STATE.md, Recordings). They drive the real `claude` with Haiku and tiny prompts, so they use a little of your quota. No test runs them.

`rec.py` is the driver: a private tmux server, `claude` under `script`, in a new git directory, in `--safe-mode`, which keeps the login and drops CLAUDE.md, settings and plugins. It uses the version named in `CLAUDE` at its top. The others are scenarios, as `python3 narrow.py <name> 60 24`, and write to `out/<name>/`, which is not to be committed.

- `truth.py` reads the true turn times from the sessions' transcripts, kept in `out/`; `motion_test.go` holds the results.
- `ptysmoke.py` runs `claude` on a bare PTY, to see what it sends without tmux.
- `sanitize.py` copies a recording in, masking what is private. Check its docstring before committing.

Each run leaves a trust entry for its directory in `~/.claude.json`.

`holdercheck/` is a Go program, since it uses the holder's client: it runs one `wdx holder` in a home under `$REAL_BASE`, sends keys as an attaching client does, and writes the states the holder logged and what the output asked of the terminal; docs/AGENT_STATE.md, Step 2 findings. `$REAL_BASE` must be a short path, as a holder's socket lives under it; a symlink will do. It removes the session folder of its own directory, and leaves a trust entry too. Its client attaches, types and leaves at once, which can lose the keys, as those findings say.

Its `send` scenario gives Claude messages in each state docs/SEND.md says something about; its `Checked on the real thing` came from it.

    CGO_ENABLED=0 go build -o /some/dir/wdx ./cmd/wdx
    REAL_BASE=/tmp/x go run ./internal/aca/testdata/claude_code/record/holdercheck states s316 /some/dir/wdx ~/.local/share/claude/versions/2.1.316 120x40

`model.py`, `model2.py`, `model3.py` and `model4.py` record `/model` and what shows old names, and `holdercheck`'s `model` scenario checks what a real holder tells of them. `/model` saves its choice to Claude's settings, so run them with a config directory of their own, logged in once by hand: `MODEL_CONFIG` for the scripts, `CLAUDE_CONFIG_DIR` for `holdercheck`. The results are in `../2.1.316/`, copied with `VERSION=2.1.316 sanitize.py`.

`prompts.py` and `planprompt.py` record the prompts of 2.1.319 (the wdx doc CLAUDE_PROMPTS, step 1): the trust prompt, a Bash and a Write permission, a radio and a checkbox decision, with the selection moved and a box ticked, and the plan approval of a session in plan mode. Run them from a copy in a short scratch directory (`python3 prompts.py prompts-120x40 120 40`), as the directory's path shows in the prompts, and copy the result in with `VERSION=2.1.319 sanitize.py`; the results are in `../2.1.319/`. Two things they learnt: a key sent right after a snap lands in the marked screen on replay, so every snap settles before the script goes on; and `--safe-mode` silences hooks, those given with `--settings` too, so the `PermissionRequest` and `PreToolUse` hooks they turn on, which append what Claude sends to `hooks/<name>/hooks.jsonl`, wrote nothing on 2.1.319.

`restdraft.py` records a draft at rest and in a turn on 2.1.322 (the wdx doc STATE_LEFTOVERS, section 2): at startup, typed as a turn starts, typed while a tool runs, and each left after its turn. Run it from a copy in a scratch directory (`python3 restdraft.py rest-120x40 120 40`) and copy the result in with `VERSION=2.1.322 sanitize.py`, naming the parts of the scratch path the trust prompt wraps past the masks; the results are in `../2.1.322/`, and their turns in `motion_test.go` are timed from the Enter that sent each, read from `raw.in`. Safe mode's notice sits under the logo, so the logo's line above the box was checked by hand without `--safe-mode`.
