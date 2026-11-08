# tide: the stats tab

Status: implemented. Designed with the human on 2026-11-07 (the wdx doc USAGE).

A tab of the right pane showing machine-wide facts about the AI coding agents, in the value pane agent info uses (`docs/TIDE_INFO.md`). Usage is its first section: how much of each ACA's account's limits is used, as its agents last told.

```
┬─[● swift-falcon(-)]─[stats(-)]─────────────────────────[⤢]─┐

   claude_code:       available
     Session (5 h):     54% ▰▰▰▰▰▱▱▱▱▱  resets in 2 h 10 min
     Week:              51% ▰▰▰▰▰▱▱▱▱▱  resets Nov 12 10:00
     as of:             16:11

   codex:             nothing observed yet
```

## Decisions

- `C-q S`, or `S` with the left pane focused, opens it or goes to it, and focuses it. The menu offers it while a project is shown, except with the focus already on it. `s` is the settings tab, so its capital is the stats.
- The tab is named for what it will hold, not for usage alone: other facts a person wants at a glance join it over time, each designed when wanted. The CLI keeps `wdx usage`, a command naming one fact for a script (docs/CLI_TIDE.md).
- Each project keeps its own tab, as with every other tab, though the stats are one for all; it is kept across restarts as the settings tab is (`stats` in `TabKind` and in the kind check of `tide_tabs`, migration 0029, docs/TIDE_STATE.md).
- The tab has no state mark, and is of no agent: no agent is marked in the list while it is current, and `C-q x`, `C-q c` and `C-q i` are not offered. `C-q -`, `Alt+←/→` and `C-q r` work as on other tabs.
- The rows are read-only. Per bucket of models (`api.UsageBuckets`): a heading with the ACA's id, and the bucket's name when it has one, whose value is `available` in the success style, or `limited, resets …` in the failure style; a row per window with the label of the windows wdx knows (`api.UsageLabel`), the percentage, a bar of 10 cells and when it resets (`api.ResetText`: the time left under a day, the date and time beyond); then `as of` the newest observation. The ACA's line and the windows' words are the same as `wdx usage`'s, derived in `internal/api` from the same rows.
- A window's line is in the failure style once limited, else coloured by fixed percentages, 75 and 90, with the context size's styles. The context thresholds are token counts and cannot colour a percentage; a setting is not wanted until someone asks.
- An ACA that can be used but has told nothing has one row, `nothing observed yet`: tide cannot tell an agent that has not answered from a plan without limits. With no ACA at all the pane says so.

## When it reads

- The usage and the ACAs are read when the tab comes to show (`valuesSync`), and again on each event of the project shown while it shows (`projectEvent`): the daemon notifies the streams when a row changes, so the tab follows at the cost of one small read per event. Usage is not in the project's envelope; moving it there is a step of its own if the reads prove too many.
- A minute timer runs while the tab shows (`statsMinute`), so that `resets in 2 h 10 min` counts down; it stops when the tab is left.
- An error reading says `stats could not be read: …` until the next read.

## Limits

- The Fable weekly of Claude Code's `/usage` screen is not in the status line JSON, so it is not shown yet (the wdx doc USAGE, step 5).
- Nothing is automatic: the tab shows the fact, and the person acts on it.
