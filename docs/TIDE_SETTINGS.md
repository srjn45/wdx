# tide: the settings tab

Status: implemented. Designed with the human on 2026-11-01.

A tab of the right pane showing every setting, in the value pane agent info uses (`docs/TIDE_INFO.md`).

```
┬─[● swift-falcon(-)]─[settings(-)]──────────────────────[⤢]─┐

   home:                    /home/srjn45/.wdx
 ▸ no_input:                ← false →
   color:                   ← auto →
   log.level:               ← info →
   log.retention_days:      7
   otel.endpoint:           http://localhost:4318
   terminal.shell:          —

 never prompt, as --no-input does: true or false
 ←→ change
```

## Decisions

- `C-q s`, or `s` with the left pane focused, opens it or goes to it, and focuses it. The menu offers it while a project is shown, except with the focus already on it.
- Each project keeps its own tab, as with every other tab, though the settings are one for all. So a tab cannot open before a project is shown.
- The tab has no state mark, and is of no agent: no agent is marked in the list while it is current, and `C-q x`, `C-q c` and `C-q i` are not offered. `C-q -`, `Alt+←/→` and `C-q r` work as on other tabs.
- The rows are in the order of the table in `internal/config`. Only `home` is read-only. The description of the row under the cursor shows above the keys.

## Kinds of value

- A setting with `choices` shows `← info →`. `←` and `→`, or a click on an arrow, send the next value, going round at the ends; `no_input`, `color`, `log.level` and `update.channel` have them. The row moves when the answer comes; keys pressed meanwhile are dropped.
- Any other value is edited in place as in `docs/TIDE_INFO.md`. An emptied value is sent as empty, which some settings take.
- A refusal shows under the row. A choice keeps its value, and the reason goes with the next key.

## The API

- `GET /v1/settings` returns each setting's `key`, `value`, `default`, `description`, `from`, `choices` and `editable`. `wdx setting list` and `get` return the same.
- `PATCH /v1/settings/{setting}` with `{value}` returns `updated` or `unchanged`. It fails with `invalid_setting` for a value the setting cannot take or for `home`, `not_found`, or `reload_failed` when the value was saved but the daemon cannot apply the file.
- The table's `set` is what validates. `choices` are what is offered, and do not narrow what `set` takes: `no_input` also accepts `1` and `t`, and a stricter check could stop a config file that loads today from loading. A test checks every choice is accepted.
- A `PATCH` does not wait for other changes in flight, as a reload does not.

## Limits

- Settings are not in the project's stream. The pane reads them when the tab comes to show, and takes what each save answers, so a `wdx setting set` from elsewhere shows when the tab is next shown.
- A save does not say when it takes effect: `color` applies to a running tide once it is reopened, and `terminal.shell` and `terminal.history_lines` to new terminals (`docs/SETTINGS.md`).
- Saving rewrites the file, so comments of its own are lost, as with `wdx setting set`.
- There is no reset to the default.
