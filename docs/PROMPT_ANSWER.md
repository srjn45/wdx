# Answering an agent's prompt

Status: implemented (the plan prompt-tab, 2026-11-13). Designed in the wdx doc PROMPT_TAB, whose As built section lists every choice the build made.

A Claude Code agent that waits on a prompt shows it in `Agent.prompt` (docs/AGENT_STATE.md, Why it is waiting). This is how a person answers it without attaching: tide's prompts tab (docs/TIDE_PROMPTS.md) and `wdx prompt` send the answer to the daemon, which hands it to the agent's holder, which presses the keys. Claude Code only: another ACA's agent is refused with `aca_unsupported`, as its prompts are not read.

```
tide / wdx prompt ─► POST /v1/agents/{agent}/prompt/answer ─► agent.Manager.Answer
  ─► terminal.Supervisor.AnswerAgent ─► holder.Conn ─► frameAnswer ─► holder.answerPrompt
  ◄─ frameAnswered (AnswerReport) ◄─ keys, each checked on the screen
```

## The request

`POST /v1/agents/{agent}/prompt/answer` (`answerAgentPrompt`), with `{question, options, choose, tick, text}` for one question, or `{answers: [...]}`, one such object per question of a decision that asks several, in order. Giving both, or neither, is `usage` (400).

| Field | What it is |
|---|---|
| `question`, `options` | the prompt as the person saw it, copied from `Agent.prompt` |
| `choose` | the index of the option Enter goes on: a yes/no answer, a radio option, a text option, `chat`, or a checkbox list's `Submit` row |
| `tick` | a checkbox list's answer: the indexes of the boxes to be ticked; Enter then goes on `Submit` |
| `text` | what a decision's `text` option takes, one line, at most 4096 bytes; in a checkbox list that option is ticked too |

It returns `{"result": "answered", "agent": {...}}` once the holder saw the prompt close, or its next question or Claude's review show. It fails with:

| Code | When |
|---|---|
| `prompt_changed` (409) | the screen does not show the prompt given, or showed a change the holder did not make; `details.agent`, `details.question` (the index of the answer it stopped at), `details.entered` |
| `invalid_argument` (422) | an answer the prompt cannot take, as `text` on a `choice` or a tick on `chat`; `details.argument` is `answer` and the holder's reason |
| `answer_failed` (500) | a holder too old to answer, one that lost its shell, or a key whose effect did not show in its time; `details.result`, and `details.entered` true when Enter was pressed, so the prompt may have taken the answer all the same |
| `aca_unsupported`, `agent_not_running`, `agent_removed`, `not_found` | as for the other agent routes |

The operation is served through a session between nodes, as `sendAgent` is (`remoteOperations`), so `wdx prompt --node` and a remote tide answer on the host.

## The holder

Only the holder sees the screen and owns the keys, so it answers, as it delivers messages (docs/SEND.md).

- **The frames.** `frameAnswer`, 15, from the daemon, carries `{id, answers}`; `frameAnswered`, 16, the `AnswerReport` of that id: `result` (`answered`, `prompt_changed`, `invalid`, `not_confirmed`, `busy`, `ended`, `unread`), a `reason` in the holder's own words, `question` and `entered`. Neither takes a protocol version: a holder that answers says `answers` in its hello, the daemon gives an older one no frame (`ErrAnswerUnsupported`, so `answer_failed`), and an older daemon sends none. A result the daemon does not know, from a newer holder, is a failure.
- **Never a prompt the person did not see.** It answers only while its last read prompt has the question and options sent, and checks the screen still shows `expect`, the prompt as its own last key left it, before each key. Any other prompt read on the way, a person's key, a redraw it did not cause, is `prompt_changed`, and nothing more is pressed. A decision of several questions is answered in order, each checked to be the one sent; the questions answered before a stop stay answered on Claude's screen, as nothing reaches the model before Claude's own review.
- **One key at a time, each seen.** Up or Down (`ESC [ A`, `ESC [ B`), then it reads the screen until `Selected` is the next index. In a checkbox list it sets the boxes top to bottom, pressing Space only where a box must change and checking `Ticked`; a `text` option is typed into, which ticks it, and Space is pressed only if it did not. Text goes through the ACA's `Paste`, and is checked by the option's label showing it, whole or cut with `…`; a label that already shows it is not typed again. Each key or paste has `SendTimes.AnswerKey`, 1 s, to show.
- **Enter once.** Only then Enter (`CR`), on the option or the `Submit` row. What it should show is worked out from the prompt: the next question while more than one is unanswered, the review after the last of several or a checkbox list's `Submit`, else the prompt closing (yes/no, a single radio question, `chat`, the review). It waits `SendTimes.AnswerClose`, 2 s; a nil prompt while the next question or review is awaited is a redraw and waited through, and counts as closed once the wait is over. The same question with its cursor or boxes moved is `prompt_changed`. Enter is never pressed twice: a review is passed with one Enter on its first option, `Submit answers`, and one that shows again is `prompt_changed`.
- **One answer at a time.** A second while one is under way is `busy`. The inbox holds its messages meanwhile and is woken once the answer ends, so a message is never typed into a half-answered prompt. The screen watch tells any number of waiters of a change (`changes`), as the answer waits beside the inbox reader.

## Logs

No log line holds a question, detail, label or the text a person typed: only the kind, indexes, counts, keys and milliseconds.

| Where | Message | Keys |
|---|---|---|
| holder | `prompt question answering` | `question` (index), `option`, `option_kind`, `ticked` (count), `keys` |
| holder | `prompt review passed` | `keys` |
| holder | `prompt answered`, `prompt not answered` | `answer_id`, `answers`, `keys`, `entered`, `duration_ms`; `result`, `reason`, `question` when not |
| daemon | `agent prompt answer` | `agent_id`, `answers`, `result`, `question`, `entered` |
| tide | `prompt answer sent`, `prompt answered`, `prompt not answered`, `prompt kept for review` | `agent_id`, `kind`, `option`, `ticked`, whether text was typed, `questions`, `code` |
| cli | `question answered` | `which prompt to answer`, `which option to choose`, with option numbers, tick counts, the text's length, yes or no |

The daemon's `request received` logs each answer as `options` (a count), `choose`, `ticked` (a count) and `text_bytes` (`answersLogged`); `request returned` logs the agent of a success or of a problem's details through `api.AgentResponseLogged`, a prompt's kind and options' count only. The agents' status `UPDATE` logs its prompt argument as a `sqlite.Secret`, its length only.

## Tests

- `internal/holder/prompttest`, a program the test binaries run, draws Claude Code 2.1.323's prompts as its recordings show them (a permission, a radio decision, a checkbox list with `Submit`, several questions and the review) and answers keys as Claude does; its own test checks Claude Code's parser reads every screen it draws.
- The holder's tests answer each kind, a prompt changed before and during, a second question not the one sent, a person's key in between, Enter not confirmed and never pressed twice, invalid answers, a shell and an older holder, and check no log line holds a label, question or text.
- The daemon's tests take the route end to end, two questions in one request, `prompt_changed`, `invalid_argument`, `usage`, an agent not running or not found, another ACA's agent, and the logs.
- Checked by hand on 2026-11-13 on Claude Code 2.1.323, `--model haiku`, 120 columns: a trust prompt, a Bash permission answered yes and no, a radio decision with typed text, a checkbox list with a box ticked and text typed, two questions through the review and back, `chat`, and `prompt_changed` when the prompt was answered elsewhere first, from tide and from `wdx prompt`; no label or text in the logs.

## Limits

- **The labels are Claude Code's:** a release that changes them loses the kinds, and the prompt reads `waiting` with no options, which nothing answers.
- **A narrow pane** cuts labels; the answer goes by index and is checked against the labels as read, so a cut label is still answered right.
- **A yes/no option that asks for words** (`No, and tell Claude what to do differently`) is chosen by Enter alone: typing on it was not checked on Claude Code.
