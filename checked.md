# agent-limited: checked by hand, 2026-11-09

For the finish task, to go into the wdx doc AGENT_LIMITED's "Checked by hand".

## How

- `go build ./cmd/wdx` of this worktree at 1fd3bd0, its daemon on a scratch home (`otel.endpoint` empty), a scratch git repository opened as the project `repo`.
- The test ACA (acatest) is only in test binaries; the daemon cannot run it, so a real ACA was used: Antigravity CLI 1.5.3, with `--permissions ask`, no prompt sent. Its agents told real usage at start: `3p`, `gemini` and `gemini-argon-wtu`, all at 0 %.
- Four agents: `on-claude` (`--model claude-sonnet-5-5-low`), `on-gemini` (`--model gemini-3.8-flash-low`), `on-default` (no `--model`; Antigravity showed Claude Sonnet 5.5, its own default here), and later `child-claude` (`--model claude-opus-5-5-low`), a child of `on-gemini`.
- With sqlite3, the row `antigravity / 5h / 3p` of `aca_usage` was set to 100 %, `limited = 1`, `resets_at` four minutes away, and `observed_at` an hour ahead, so that the real agents' next tell would not replace it. Nothing was restarted from then until the end.

## What was seen

While `3p` was limited (Claude and GPT models):

| case | `wdx agent list` ACTIVITY | `wdx agent get` | `--json` `limited` | tide row | right? |
|---|---|---|---|---|---|
| `on-claude`, Claude by id | `limited` | `activity limited`, `limited Claude and GPT models, resets in 3 min` | `{"bucket": "3p", "resets_at": ...}` | `limited`, bold red | yes |
| `on-default`, Claude by shown name only | `limited` | same line | same | `limited`, bold red | yes |
| `child-claude`, Claude by id, a child | `limited` | — | — | `limited`, bold red | yes |
| `on-gemini`, Gemini by id | `idle` | `activity idle`, no limited line | `null` | no word | yes |

- tide's info tab of `on-claude`: `activity: idle` (the screen's) and under it `limited: Claude and GPT models, resets in 3 min` in the failure style (bold red).
- tide, `on-gemini` folded: its row read `1 waiting`, counting its limited child.
- The Agents section's heading, folded, shows only the count of agents (`4`): it has never shown waiting, so nothing to check there.

When the reset passed (23:42:41 local), with nothing restarted or touched:

- the daemon logged `a limited usage bucket reset` at the reset's millisecond; its timer had been set (`agents' limits told again at the next reset`) about a minute after the hand edit, on the next status read that changed something;
- `wdx agent list` read `idle` for all four, `wdx agent get` had no limited line;
- tide's rows lost `limited` on their own, and the folded `on-gemini` read `1 below`, as before;
- the row in `aca_usage` still says `limited = 1` with its reset passed: the limit ended by being derived, not by a change to the row.

Every case passed.

## Notes

- A row edited by hand tells neither the streams nor the reset timer: tide showed the limit at the next change of the project (here a rename), and the timer was set at the next status read. Usage the daemon stores itself does both, so this is the hand check's doing, not a fault.
- Not checked by hand: a model wdx cannot map (both Antigravity agents without a known id showed a known name). The unit tests cover it.
- A stray `z` typed while finding tide's keys left a draft in `on-claude`; it changed nothing of the above.

## The real daemon of this machine

- `wdx usage`: every bucket available: claude_code 9 % session, 80 % week; codex 3 % week; Antigravity's three groups 0 %; cursor, cursor API and cursor Auto 0 %.
- The real daemon runs 0.5.0-dev.135 (a2da34d), from before this plan: its agents have no `limited` field at all.
- Running agents: three on claude_code (two on Opus 5.5, one on Fable 5.1 asked as `fable`). None reads limited, and none should: claude_code's one bucket `""` is available. The Fable agent maps only to `""` today; the Fable weekly is not read yet (USAGE step 5, the doc's Open).
