# Recording Antigravity

These throwaway scripts made the recordings in `../1.5.2/` (the wdx doc ANTIGRAVITY_PARITY). They drive the real `agy` with tiny prompts on the cheapest model, so they use a little of your quota. No test runs them.

`rec.py` is the driver: a private tmux server, `agy` under `script`, in a new git directory under `/tmp/agyrec` (or `$AGY_REC_WORK`), started as wdx starts it on the cheapest model (`gemini-3.8-flash-low`, or `$AGY_REC_MODEL`): the directory trusted for that session only, messages queued, and in full mode no prompts and no terminal sandbox. Those settings go in `--settings`, which leaves `~/.gemini` as it was. The recordings in `../1.5.2/` were made with `$AGY_REC_WORK` in a scratch directory under `/tmp/claude-1000`, whose path agy shows and the hooks report, with the username masked. The launch note is a folder given with `--add-dir`, holding `AGENTS.md` and a `.agents/hooks.json` whose hooks append each event's JSON to `hooks.jsonl`. A scenario writes to `out/<name>/`, or `$AGY_REC_OUT/<name>/`, which is not to be committed.

- `turns.py <name> <cols> <rows>` records what a parser reads, in full mode: the box drawn, ready (the model in the footer), a draft, a multi-line draft, the `/` menu, a turn generating and running a command, a message queued during it, one sent at once with alt+m, a background task during and after a turn, and an interrupt with Esc.
- `approval.py <name> <cols> <rows>` records a command approval in ask mode, with the allow list emptied for that session, and its denial with Esc.
- `steer.py <name> <cols> <rows>` records messages sent at once with alt+m while agy generates an answer and while it runs a command.
- `sub.py <name> <cols> <rows>` records a subagent working after the main turn has ended, marking the screen every 2.5 s.
- `status.py <name> <cols> <rows> [user]` records turns.py's steps, less the message sent at once, with the `statusLine` wdx gives in `--settings` (the wdx doc ANTIGRAVITY_CONTEXT): a command that keeps the JSON in a file and prints nothing, with `stack_with_default`, so that the footer the parser reads stays; with `user`, a stand-in for the user's own status line follows it in braces, as wdx chains theirs, and agy draws its line under the footer. Settings holding a `$` go to agy as a file (`settings_file=True`), as the shell tmux runs the command in would expand them inline.
- `transcript.py <name>` records one conversation's files: a command, a file written and edited, a file read, `/compact`, `/exit`, a resume with `--conversation`, then `/clear` and a turn in the new conversation. It keeps `brain/<id>/` (agy's `.system_generated`, without the `chunks/` that only split the same lines) and `hooks.jsonl`.
- `copy.py <name>` copies a recording in: without script's header and what came after the last timed read, with the username, the account's email and plan, and every UUID masked, lengths kept; a plan a narrow screen cuts short is masked too. Check its result for anything private before committing it.

A mark named `timeout` is a wait that gave up: the scenario went on, and the screen it gave up on is kept.

Each run leaves conversations in `~/.gemini/antigravity-cli` (`conversations/`, `brain/`, `conversation_summaries.db`).

    python3 turns.py turns-120x40 120 40 && python3 copy.py turns-120x40
    python3 turns.py turns-40x24 40 24 && python3 copy.py turns-40x24
    python3 approval.py approval-120x40 120 40 && python3 copy.py approval-120x40
    python3 approval.py approval-40x24 40 24 && python3 copy.py approval-40x24
    python3 steer.py steer-120x40 120 40 && python3 copy.py steer-120x40
    python3 sub.py sub-120x40 120 40 && python3 copy.py sub-120x40
    python3 status.py status-120x40 120 40 && python3 copy.py status-120x40
    python3 status.py status-user-120x40 120 40 user && python3 copy.py status-user-120x40
    python3 transcript.py transcript && python3 copy.py transcript
