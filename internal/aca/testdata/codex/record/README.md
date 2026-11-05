# Recording Codex

These throwaway scripts made the recordings in `../0.163.0/` (the wdx docs ACA_READY and CODEX_ACTIVITY). They drive the real `codex` with tiny prompts, so they use a little of your quota. No test runs them.

`rec.py` is the driver: a private tmux server, `codex` under `script`, in a new git directory under `/tmp/codexrec` (Codex shows its directory, so the path is short and says nothing private), started as wdx starts it: no update offer, the directory trusted for that session only, which leaves `~/.codex/config.toml` as it was. A scenario writes to `out/<name>/`, or `$CODEX_REC_OUT/<name>/`, which is not to be committed.

- `ready.py <name> <cols> <rows>` records what the parser reads: Codex loading, its box empty, a draft, a multi-line draft, shell mode, the `/` menu, a turn, typing in it, and after it. Its steps are 1.5 s apart, as a mark can be a little off script's clock.
- `turns.py <name> <cols> <rows> [full|ask]` records what the activity is read from. In `full` mode: a tool turn, a background terminal in it, a streamed answer, a message queued during a turn, a subagent, and an interrupt that leaves a background terminal running. In `ask` mode: an approval and its denial. It pauses 1.5 s after each mark, as the next key would otherwise come before the screen the mark is for. Its `frames.log`, not committed, keeps every bottom of the screen that changed, to check the marks against.
- `copy.py <name>` copies a recording in, as the tests read it: without script's header and Codex's goodbye, which holds the session id, and with the username and UUIDs masked. Check its result for anything private before committing it.

Each run leaves a session in `~/.codex/sessions`.

    python3 ready.py ready-120x40 120 40 && python3 copy.py ready-120x40
