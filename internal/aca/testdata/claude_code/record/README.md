# Recording Claude Code

These throwaway scripts made the recordings in `../2.1.314/` (docs/AGENT_STATE.md, Recordings). They drive the real `claude` with Haiku and tiny prompts, so they use a little of your quota. No test runs them.

`rec.py` is the driver: a private tmux server, `claude` under `script`, in a new git directory, in `--safe-mode`, which keeps the login and drops CLAUDE.md, settings and plugins. It uses the version named in `CLAUDE` at its top. The others are scenarios, as `python3 narrow.py <name> 60 24`, and write to `out/<name>/`, which is not to be committed.

- `truth.py` reads the true turn times from the sessions' transcripts, kept in `out/`; `motion_test.go` holds the results.
- `ptysmoke.py` runs `claude` on a bare PTY, to see what it sends without tmux.
- `sanitize.py` copies a recording in, masking what is private. Check its docstring before committing.

Each run leaves a trust entry for its directory in `~/.claude.json`.

`holdercheck/` is a Go program, since it uses the holder's client: it runs one `wdx holder` in a home under `$REAL_BASE`, sends keys as an attaching client does, and writes the states the holder logged and what the output asked of the terminal; docs/AGENT_STATE.md, Step 2 findings. `$REAL_BASE` must be a short path, as a holder's socket lives under it; a symlink will do. It removes the session folder of its own directory, and leaves a trust entry too. Its client attaches, types and leaves at once, which can lose the keys, as those findings say.

    CGO_ENABLED=0 go build -o /some/dir/wdx ./cmd/wdx
    REAL_BASE=/tmp/x go run ./internal/aca/testdata/claude_code/record/holdercheck states s316 /some/dir/wdx ~/.local/share/claude/versions/2.1.316 120x40
