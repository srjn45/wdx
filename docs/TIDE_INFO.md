# tide: the agent info tab

Status: implemented. Designed with the human on 2026-11-01. The settings' tab is in `docs/TIDE_SETTINGS.md`.

A tab of the right pane showing what is known about an agent, as `key: value` rows. The settings use the same pane. Tabs themselves are in `docs/TIDE_TABS.md`.

## The pane

```
┬─[● swift-falcon(-)]─[● info: swift-falcon(-)]──────────[⤢]─┐

 ▸ name:               swift-falcon
   id:                 agent-3f9a1c
   aca:                claude_code
   model:              Haiku 5.5

   project_name:       wdx
   dir:                /home/srjn45/dev/wdx/.worktrees/auth-fix
   worktree:           auth-fix
   parent_agent_name:  planner
   children:           tests
                       docs

   state:              running
   exit_code:          —
   error:              —
   activity:           working
   draft:              no
   queued:             2

 Enter rename
```

- The fields for now are the ones above. More come later, as needed.
- Keys are the API's names, so the pane reads as `--json` does. `children` lists the names of the direct children, one per line.
- Every row always shows, with `—` when empty, so that rows do not jump when the state changes.
- Long values wrap. A pane too short for the rows scrolls.
- The values come from the project's stream. Nothing is attached.
- The cursor stops only on editable rows.

## Opening it

- `C-q i` with the right pane focused on an agent's tab opens that agent's info.
- `C-q i` or plain `i` with the left pane focused opens the info of the agent selected in the list.
- The menu offers `i`, and plain `i` works, only in those two cases: not from the terminal pane, not on an info tab, and not with no tabs or no agent selected.
- The focus moves to the info tab. If it is open already, the key goes to it.

## Kinds of row

| Kind | For | Shows | Keys |
|---|---|---|---|
| read-only | most agent fields, `home` | the value | the cursor skips it |
| open | text and numbers | the value | `Enter` edits in place, `Enter` saves, `Esc` cancels |
| choice | a fixed set of values; a boolean is `true` and `false` | `← info →` | `←` and `→` step through the values, going round at the ends, and each step saves |

- Text and numbers behave the same in the pane. The API says whether a value can be used, so no rule lives only in tide.
- `Alt+←` and `Alt+→` still move between tabs, so the plain arrows are free for a choice.
- A click on an open row edits it; a click on a choice's arrow steps it.
- Leaving the tab cancels an edit. An unchanged value closes the edit without sending anything.
- A choice that fails to save goes back to its value.
- An error shows under the row, and an open row stays in editing:

```
 ▸ name:               auth-fix_
                       ✗ auth-fix is already taken in this project
 Enter save  Esc cancel
```

Agent info has no choice row yet; the settings do.

## Validation is in the API

Every client gets the same answer, tide included. tide sends what was typed and shows what comes back.

### Agent names

- `PATCH /v1/agents/{agent}` with `{name}` returns `renamed`, or fails with `name_taken`, `invalid_name`, `agent_removed` or `not_found`. `wdx agent rename` calls it.
- A name is trimmed, then must be one or more of `A-Z`, `a-z`, `0-9` and `-`. Anything else is `invalid_name`. Leaving the name out on create still gives a generated one.
- The rule applies when a name is written. Names already stored that do not fit keep working, and can be renamed to one that does.
- The rule lives where the terminal row is written, so it covers creating agents and shells too. That makes creating stricter, so the commit is marked as breaking.
- Agents and shells share one list of names per project, ignoring case, and tombstones count. An agent may change only the case of its own name.
- A tombstone's name is read-only.
- Tabs, streams, the holder and the process go by id, so they do not notice a rename. A script using the old name stops finding the agent.
- A rename is not a change of state, so `updated_at` stays.
- `parent_agent_name` is added to the `Agent` schema.

### Settings

See `docs/TIDE_SETTINGS.md`.

## Tabs

- A tab gains a type: an agent or its info. Only agent tabs attach.
- The Agents list marks the agent of the current tab, whichever type it is.
- Removing an agent closes its info tab too. `C-q r` keeps info tabs.
