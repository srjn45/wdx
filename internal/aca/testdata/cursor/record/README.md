# Recording Cursor

These throwaway scripts made the recordings in `../2026.11.05/` (the wdx doc CURSOR_PARITY). They drive the real `cursor-agent` with tiny prompts on a cheap model (`gemini-3.8-flash-low`), so they use a little of your quota. No test runs them.

`rec.py` is the driver: a private tmux server, `cursor-agent` under `script`, in a new git directory. Cursor runs with `HOME` set to `$CURSOR_REC_HOME`, a scratch home, so nothing touches your `~/.cursor`: `--model` would rewrite your default model, `--trust` saves trust, and every chat is kept. Make the home first, with your login and settings copied in:

    mkdir -p $H/.cursor $H/.config/cursor
    cp ~/.config/cursor/auth.json $H/.config/cursor/
    cp ~/.cursor/cli-config.json $H/.cursor/
    export CURSOR_REC_HOME=$H

The work directories go under that home, so Cursor shows them as `~/w/<random>`: short, and naming nobody. `$CURSOR_REC_ENV` is added to Cursor's environment, as `CURSOR_CONFIG_DIR=<dir>` for a config of its own. A scenario writes to `out/<name>/`, or `$CURSOR_REC_OUT/<name>/`, which is not to be committed.

- `ready.py <name> <cols> <rows>` records what a ready check and the draft read: the blank screen while Cursor loads, the empty box, a draft, a multi-line draft, a long paste, shell mode (`!` typed), `&` typed (cloud mode), the `/` menu, a turn, typing during it, after it, and Ctrl+C arming the exit. Its steps are 1.5 s apart. It clears the box with Ctrl+C, then waits 3.6 s for the exit to disarm.
- `turns.py <name> <cols> <rows> [full|ask]` records what the activity is read from. In `full` mode: a tool turn, a follow-up queued during it, a steer into a running tool (paste, Enter, Enter), the agent idle while the steered command runs on as a task, the new turn Cursor starts when it finishes, a streamed answer, a steer pending while it streams, Ctrl+C putting the message back in the box with the steer left stuck, and a subagent. In `ask` mode (give it a config whose `approvalMode` is not `unrestricted`, through `CURSOR_REC_ENV=CURSOR_CONFIG_DIR=<dir>`): an approval, Esc to its "tell the agent" box, and the turn after. `frames.log` keeps every bottom of the screen that changed, to check the marks against.
- `frames.py` is `turns.py`'s helper that writes `frames.log`; it is not run alone.
- `undim.py <screen.ansi>...` prints a saved screen as the parser gets it: each line plain, and under it (`U`) with its dim cells blank, when that differs.
- `copy.py <name>` copies a recording in, as tests would read it: without script's header, which names the program's path, and Cursor's goodbye, which holds the chat id, and with the username and UUIDs masked. Check its result for anything private before committing it.
- `copysession.py <config dir> <data dir> <chat id> [hook input]` copies one chat's files to `../<version>/session/`: the agent transcript and its subagents' as Cursor keeps them, the chat's `meta.json`, the messages its store's latest root names in order (`store-messages.jsonl`, the system prompt and `user_info` left out, reasoning signatures too) and the root's fields (`store-root.txt`), and a `sessionStart` hook's input without `user_email`. Home and work directories are masked. Check its result for anything private before committing it.

The session sample in `../2026.11.05/session/` is one chat of a `turns.py` run in `full` mode. Its `transcript.md` is the golden file the tests render from it (`go test ./internal/aca -run TestCursorTranscript -update` writes it anew).

The compaction sample in `../2026.11.05/compaction/` is a chat driven by hand: one turn, `/summarize`, one more turn. Its `store-messages-before.jsonl` is from a snapshot of the store taken before the `/summarize` (`STORE=<snapshot> SAMPLE=compaction copysession.py ...`), its `store-messages.jsonl` from the store after it. For the hook input, put a `sessionStart` hook in the scratch home's `.cursor/hooks.json` whose command saves its stdin to a file, before the run. Then `copysession.py` takes the config folder (`$CURSOR_REC_HOME/.cursor`, or the `CURSOR_CONFIG_DIR` given), the data folder (`$CURSOR_REC_HOME/.cursor`, which holds `projects/`), the chat id (the folder under `chats/<md5>/`) and that file.

Each run leaves its chats and project folders in the scratch home, and Cursor may put a folder per work directory in `/tmp/.cursor` (its worker's socket, when the path is long). A command Cursor runs mid-turn outlives `kill`: look for it (`pgrep -af sleep`) and kill its process group.

    python3 ready.py ready-120x40 120 40 && python3 copy.py ready-120x40
    python3 turns.py turns-120x40 120 40 && python3 copy.py turns-120x40
    CURSOR_REC_ENV=CURSOR_CONFIG_DIR=<dir> python3 turns.py approval-120x40 120 40 ask && python3 copy.py approval-120x40
    python3 copysession.py $CURSOR_REC_HOME/.cursor $CURSOR_REC_HOME/.cursor <chat id> <hook input file>
