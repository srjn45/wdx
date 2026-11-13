# tide: the prompts tab

Status: implemented (the plan prompt-tab, 2026-11-13). Designed in the wdx doc PROMPT_TAB, with the human, on 2026-11-12 and 2026-11-13; its As built section lists every choice the build made. The look was sketched first in https://claude.ai/artifact/D8odmmJGFCpFv9bgt6piGR.

Every prompt the project's Claude Code agents wait on, answered from one place without attaching to each. The answer goes through the daemon to the agent's holder, which presses the keys (docs/PROMPT_ANSWER.md). `wdx prompt` is the same in the CLI (docs/CLI_TIDE.md).

```
┬─[● planner(-)]─[? prompts 2(-)]───────────────────────[⤢]─┐
 ▸ wdx-arch      yes/no    Bash command · go test ./…
   cor-fixes-2   decision  Which store keeps the sessions?
```

Enter on a row opens its prompt on a screen of its own:

```
┬─[● planner(-)]─[? prompts 2(-)]───────────────────────[⤢]─┐
 ‹ prompts  ·  cor-fixes-2  ·  decision  ·  2 of 2
 ──────────────────────────────────────────────────
     Which store keeps the sessions?

   ❯ 1. (•) SQLite
     2. ( ) Files
     3. ( ) Type something.
       ─────
     4. Chat about this
```

## The tab

- **A project tab of no agent,** as the stats tab is (docs/TIDE_STATS.md): `C-q P`, or `P` with the left pane focused, opens it or goes to it, and focuses it. The menu offers it while a project is shown, except with the focus already on it. `C-q -`, `Alt+←/→` and `C-q r` work as on other tabs; `C-q x`, `C-q c` and `C-q i` are not offered.
- **Its title** is `[? prompts N(-)]` with the number of prompts waiting, the `?` the waiting mark, and `[prompts(-)]` with none.
- **Kept across restarts:** `prompts` is a `TabKind` of the API and of `tidestate`. Migration 0035 builds `tide_tabs` again without the `CHECK` on its kinds, which `tidestate` checks in Go, as AGENTS.md asks of a list that grows; the columns' checks stay. An older tide passes over a kind it does not know, so the migration is not breaks-older (docs/TIDE_STATE.md).

## The list

- One row per running Claude Code agent of the project whose `prompt` was read, in the agents' order, live from the project's stream: the name, `yes/no` or `decision`, and a yes/no prompt's title and detail or a decision's question, cut to the width. Another ACA's agents never show, as their prompts are not read.
- `no prompts` when none waits.

| Key | What it does |
|---|---|
| `↑` `↓` | move through the rows |
| `Enter` | open the prompt on its own screen |
| `o` | open and focus the agent's tab, without answering |

## A prompt's screen

A header (`‹ prompts · <agent> · <kind> · n of m`, `decision (1 of 2)` for a question of several, `decision (review)` for the review, `changed` when the prompt was drawn again), the tab line of a decision of several questions as Claude draws it (`☐ Color  ☐ Size  ✔ Submit`, `☒` once answered), a yes/no prompt's title and detail, the question and the options, numbered as Claude numbers them, the `Submit` row without a number. `(•)` and `( )` mark a radio list, `[✔]` and `[ ]` a checkbox list; a `text` option shows what was typed in it, with a cursor while it is under tide's.

- **Tide's own cursor, ticks and text.** They start on Claude's `Selected` and `Ticked`, and Claude's cursor does not move until the answer is sent, in one request.
- **A digit** only moves the cursor to that number, so one key never answers.

| Key | What it does |
|---|---|
| `↑` `↓`, a digit | move through the options |
| `Enter` on a `choice` of a yes/no or radio prompt | answer it |
| `Enter` on a radio list's `text` option | send it with the text typed |
| `Enter` on `chat` | answer it, which declines every question, then open and focus the agent's tab to talk it over |
| `Space` or `Enter` in a checkbox list | tick or untick the option |
| `Enter` on `Submit` | send the boxes ticked; with none it sends nothing and says so |
| typing, on a `text` option | goes into its field, every printable key (`o`, digits and Space included); a paste is typed as one line. In a checkbox list typing ticks it and emptying it unticks it |
| `o` | open and focus the agent's tab, without answering (not while typing in a field) |
| `Esc` | back to the list, the draft kept until the prompt closes |

The status line gives the keys of each state: `Enter choose`, `Space or Enter tick  Submit sends`, `Enter send 3 ticked`, `type your answer  Enter send`, `Enter keep, then review`, `Enter submit all answers`, `answering…` while an answer is on its way.

## Several questions

Claude's screen shows only the current question of a decision that asks several, so tide cannot show the next before the one shown is answered (the human's choice, 2026-11-13):

- Enter on an answer to any question but the last **sends it**: Claude moves on to the next question, and sends nothing to its model until its own review. The next question showing is no change.
- The last answer is **kept in tide**, and tide's review follows: `Review your answers`, each question with `→` and its answer (one answered in the agent's terminal reads `→ answered in the agent's tab`), `Ready to submit your answers?`, then `Submit answers` and `Back to the questions`.
- Only Enter on `Submit answers` sends the last answer, and the holder passes Claude's own review. `Back to the questions` reopens the last question, still open on Claude's screen, its answer kept. `Esc` goes back to the list and keeps it too.
- `chat` on any question is sent at once, as it declines them all.

```
 ‹ prompts  ·  ui-polish  ·  decision (review)  ·  1 of 2
 ──────────────────────────────────────────────────
     ☒ Color  ☒ Size  ✔ Submit

     Review your answers

     ● Which accent color?
       → Green
     ● Which base size?
       → 14 px

     Ready to submit your answers?

   ❯ 1. Submit answers
     2. Back to the questions
```

## When the prompt moves under the person

- **Changed:** a prompt changes when its kind, title, question, detail, options, kinds, multi, review, questions or answered differ; Claude's cursor or ticks moving is not a change. A changed prompt is drawn again from Claude's, tide's draft dropped, with `changed` in the header.
- **Closed:** one that closes leaves its screen, back to the list.
- **While an answer is on its way** nothing moves and Enter is not sent again. After it the tab goes back to the list, whose row stays until the stream says the prompt closed; `chat` goes to the agent's tab once answered.
- **Refused:** a refused answer says why on a line at the top of the tab, until the next key: `not answered: the prompt changed before it was` for `prompt_changed`, `not answered: the agent is not running`, else `not answered:` and the problem's detail. A refused review goes back to the last question.
- **An older host,** whose prompt has no kinds, sends nothing: the tab says `its options are not read: o answers it in the agent's tab`.

## Logs

`prompts opened`, `prompt opened`, `prompt changed`, `prompt closed`, `prompt kept for review`, `prompt answer sent`, `prompt answered` and `prompt not answered`, with the agent's id, the kind, the option's index, how many were ticked, whether text was typed, the number of questions and the error code: never a question, detail, label or text.

## Tests

`internal/tide/prompts_test.go`: the rows and the title with and without prompts, the tab kept and restored, every key in the list, a yes/no, a radio prompt with text, `chat` going to the agent's tab, a checkbox list, two questions with tide's review and back, a refused review, a prompt changing and closing under the person, Enter never sent twice, and no label reaching a log line. `Model.answerPrompt` sends the answer; the tests replace it to see every request. Checked by hand with a real Claude Code (docs/PROMPT_ANSWER.md, Tests).
