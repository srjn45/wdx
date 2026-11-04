# Kitty keys in wdx terminals

Status: approved by the human on 2026-11-04.

In a terminal or agent wdx runs, `Shift+Enter` does not arrive at all, even when the terminal on the screen (iTerm2, kitty, WezTerm, Ghostty, foot) can tell them apart. So Claude cannot take `Shift+Enter` as a new line, and the same goes for every key that only the kitty keyboard protocol tells apart (`Ctrl+I` from `Tab`, `Ctrl+Shift+A` from `Ctrl+A`, `Esc` from the start of a sequence). This design makes the terminals wdx runs speak levels 1 and 8 of that protocol, agreed with the human on 2026-11-04. The other levels can follow when something needs them.

## Why keys lose their modifiers today

A key goes from the terminal on the screen to termview (the attach view, in `wdx terminal attach`, `wdx agent attach` and tide's panes), then to the pane's vt, which turns it into the bytes the program reads:

1. **The screen's terminal to termview already works.** Bubble Tea asks the terminal for kitty level 1 when it starts (`cursed_renderer.go`), so termview gets `Shift+Enter` as Enter with the Shift modifier, as tide's own `Shift+Enter` does (docs/TIDE_ENTER.md).
2. **The vt drops the key.** The fork's `SendKey` (`vt/key.go`) knows Enter only without a modifier: `Shift+Enter` matches none of its keys and is sent as nothing (found while building step 2; the design first said it was sent as `\r`). The vt says so itself: `TODO: Support Kitty, CSI u, and XTerm modifyOtherKeys`. `screen.modifiedKey` works around it only for arrows, Home, End and F1 to F4.
3. **Programs never learn they could ask.** Claude asks for the kitty flags at startup (`\x1b[?u`, docs/AGENT_STATE.md). The vt has no handler for the question and does not answer, so Claude takes the terminal for one without the protocol and asks for nothing more.

## The protocol, in short

The [kitty keyboard protocol](https://sw.kovidgoyal.net/kitty/keyboard-protocol/) lets a program ask the terminal for one or more levels, as bits of a number:

| Level | Gives the program | Here |
|---|---|---|
| 1, tell keys apart | keys that sent the same bytes now differ: `Shift+Enter`, `Ctrl+I`, `Esc` | **yes** |
| 2, key events | repeats and releases | no |
| 4, alternate keys | the shifted key and the key's place on a US layout | no |
| 8, every key as a code | text keys too arrive as codes | **yes** |
| 16, text with keys | the text a key types, along with level 8's code | no |

The program asks with sequences the terminal keeps: it pushes flags onto a stack when it starts and pops them when it ends, so a program that crashed or ran another leaves the right flags behind. It can ask which flags are on, and the terminal answers with the ones it has. A level wdx does not have is never on, and the answer says so: the program then goes without it, which is the protocol working as meant.

## The rule

### What the vt keeps

- **The flags are a stack per screen**: the main and the alternate screen each have one, as the protocol says, and the flags in force are the top of the current screen's stack, 0 when it is empty.
- **`CSI > flags u` pushes** the flags. **`CSI < n u` pops** n entries, 1 when n is left out; popping more than the stack holds empties it. **`CSI = flags ; mode u` changes the top**: mode 1 (the default) sets it to the flags, 2 adds them, 3 removes them. On an empty stack it changes the flags in force as if it were the top.
- **Only levels 1 and 8 are kept**: any other bit a program asks for is dropped when the flags are stored, so `CSI > 31 u` pushes 9.
- **The stack holds at most 16 entries**: a push onto a full stack drops the oldest, as kitty does, so a program pushing in a loop cannot grow it without end.
- **`CSI ? u` is answered** with `CSI ? flags u`, the flags in force.
- **A full reset (`ESC c`) empties both stacks.** A soft reset (`CSI ! p`) leaves them, as it leaves the screens. (To check against kitty while building step 1; if kitty differs, we follow kitty.)
- **The fork gains `KittyFlags()`**, the stacks of both screens, for the snapshot. The fork's `Modes()` and `CharSets()` are the same kind of getter (docs/VT.md #9, #10).

### How keys are sent

With the flags in force at 0, nothing changes: keys are sent as today, with `screen.modifiedKey`'s fix.

With **level 1** (tell keys apart), as the protocol's spec says:

- **Keys that type text are sent as text**, Shift included: `a`, `A`, `!`.
- **`Esc`** is `CSI 27 u`. **`Ctrl`, `Alt` or both with a text key** is `CSI code ; mods u`, the code being the key's unshifted character: `Ctrl+C` is `CSI 99;5u`, `Ctrl+Shift+A` is `CSI 97;6u`, `Alt+x` is `CSI 120;3u`. `Ctrl+C` then reaches the program as a key, not as a signal: the program asked for that.
- **`Enter`, `Tab` and `Backspace` without a modifier stay `\r`, `\t` and `\x7f`**, so a shell left in this level by a program that crashed still takes `reset` and Enter. With a modifier they are `CSI 13;mods u`, `CSI 9;mods u` and `CSI 127;mods u`: **`Shift+Enter` is `CSI 13;2u`**.
- **Arrows, Home, End and F1 to F4** are `CSI 1;mods A` and the rest, as `screen.modifiedKey` sends them today, F3 being `CSI 13;mods ~` as in kitty. **Insert, Delete, Page Up, Page Down and F5 to F12** are `CSI number;mods ~`, their legacy number. Without a modifier these keys are sent as today.
- **The keypad's keys that type a character** (the digits, `.`, `/`, `*`, `-`, `+`, `=`, `,`) type it alone or with Shift, as in kitty, and are `CSI number;mods u` with the protocol's own numbers (57399 and on) with Ctrl, Alt or Super. **The keypad's other keys** (its Enter, arrows, Home and the rest) are always `CSI number;mods u`, as are F13 to F35, Print Screen, Pause, Menu and the media keys, which have no legacy form. (The design first said every keypad key was a code at level 1; kitty keeps the digits text, and so does this, changed while building.)
- **The lock keys and the modifiers pressed alone** (`Shift`, `Caps Lock`) are sent as nothing at level 1, as the protocol reports them only at level 8. Caps Lock and Num Lock are not modifiers at level 1 either. (Added while building: the vt sent such a key as bytes that meant nothing.)
- `mods` is 1 plus the modifiers' bits: Shift 1, Alt 2, Ctrl 4, Super 8. Bubble Tea's `ModMeta` and `ModSuper` are each other's bits in kitty's numbering (`uv/key.go` says so), so the encoder swaps them.

With **level 8** (every key as a code), every key is `CSI code ; mods u` or one of the forms above, even plain `a` (`CSI 97u`), `Enter` (`CSI 13u`), `Tab` (`CSI 9u`), `Backspace` (`CSI 127u`), a keypad digit and a modifier pressed alone; Shift is a modifier then, not part of the text (`A` is `CSI 97;2u`), and Caps Lock (64) and Num Lock (128) are modifiers too. No text is sent with it, since that is level 16. Text no single key names, as from an input method, is sent as text at every level.

### What level 8 cannot give here

Level 8 also reports the modifier keys on their own (`Shift` pressed alone) and Caps Lock and Num Lock as modifiers. termview hears neither: it asks the screen's terminal for level 1 only, as Bubble Tea does by default. So a program at level 8 gets every key but those. Asking the screen's terminal for more, following what the program in the focused pane asked for, is a change to termview and tide of its own, left for when a program needs it.

### Attaching

- **The snapshot carries the stacks.** When a client attaches, its vt starts from the holder's snapshot (`screen.Snapshot`), and while attached it is that vt that answers the program's questions and turns keys into bytes (docs/ATTACH.md). So the snapshot pushes the main screen's stack before entering the alternate screen, and the alternate screen's after. The whole stack goes, not just its top: a pop while attached must leave the client's vt where it leaves the holder's.
- **The holder's protocol does not change**: the snapshot is bytes a vt reads, and these are more of them. A client too old for the sequences ignores them and sends keys as today. A holder too old to keep the flags sends a snapshot without them; its client keeps the flags the program sets from then on.
- **A program started before the update** asked an old vt and was told nothing, so it will not use the protocol until it is started again (an agent by `wdx agent restart`).

### Nothing else changes

- tide's own keys, `C-q` and `C-\` among them, are read before a key goes to a pane, as today.
- Pasting is bracketed paste, untouched by the flags.
- `wdx terminal send` and `wdx agent send` write their bytes as they are, not as keys, so a program at level 1 or 8 gets a legacy `\r` from them. Every program that speaks the protocol still reads legacy bytes (the spec keeps them for that), and Claude is checked for it in the tests below.
- A terminal on the screen without the protocol sends `Shift+Enter` as `\r`, and nothing in wdx can tell it apart; the program gets `\r`, as today. Claude's other new-line keys (`\` then `Enter`, `Option+Enter`) still work there.

## Build

Each step is designed, built and understood before the next. Steps 1 and 2 are commits in the fork's branch `wdx`, each with its tests (docs/VT.md). They ship together in step 3: a vt that answered `CSI ? u` but did not encode keys would tell programs a lie.

1. **Fork: keep the flags.** The stacks per screen, the push, pop, set and reset rules above, the mask to levels 1 and 8, the limit of 16, `KittyFlags()`. No answer to `CSI ? u` yet.
2. **Fork: send keys by the flags, and answer.** `SendKey` encodes by the flags in force (level 1, then level 8), and `CSI ? u` is answered. The upstream TODO in `key.go` now names only what is still missing: modifyOtherKeys and levels 2, 4 and 16.
3. **wdx: use it.** Move both `replace`s to the fork's tip and run `make notices`. `screen.Snapshot` writes the stacks. `screen.SendKey` uses `modifiedKey` only while the flags in force are 0. The docs: a row in docs/VT.md's table for the fork's change, docs/ATTACH_VIEW.md (keys), docs/TIDE.md's "Possible later: richer keys" line, docs/TIDE_ENTER.md's note on keys typed into panes, docs/AGENT_STATE.md's note that the vt answered `\x1b[?u` (it did not), and a row in docs/CLI_TIDE.md: the same in an attach and in tide's panes, both being termview. Released in a dev build for the human to try on iTerm2.

## Tests

In the fork (step 1 and 2):

- Each of push, pop (with and without n, and past the bottom), set in modes 1, 2 and 3, on an empty and a full stack; the mask (`CSI > 31 u` keeps 9); the limit (the 17th push drops the oldest).
- The two screens' stacks are apart: a push on the alternate screen is gone on the main one, and back when the alternate one is entered again. A full reset empties both; a soft reset leaves them.
- `CSI ? u` is answered with the flags in force, 0 before any push.
- A table of keys and the bytes they make at flags 1 and 9: every row of the rules above, `Shift+Enter`, `Ctrl+C`, `Ctrl+Shift+A`, `Esc`, plain `Enter`, `Tab` and `Backspace`, `Alt+x`, an arrow with and without Shift, F3, the keypad's digits and its other keys, a modifier and Caps Lock alone, `A` at level 8, Super and Meta swapped; and keys sent through `SendKey` at flags 0, 1 and 8, after a pop and on the other screen.

In wdx (step 3):

- `screen`: a snapshot of a vt with stacks on both screens, read by a new vt, gives the same `KittyFlags()`, on the main screen and on the alternate one.
- `termview`: a program that has pushed level 1 gets `CSI 13;2u` for `Shift+Enter` and `\r` for `Enter`; one that has pushed nothing gets `\r` for both. Then the same with the push made before the attach, so that only the snapshot can have carried it.
- `holder`: with no client attached, a program that pushes level 1 and asks `CSI ? u` reads `CSI ? 1 u`.
- By hand, before the release: Claude in an agent attached from iTerm2 and from kitty, where `Shift+Enter` makes a new line and `Enter` sends; `wdx agent send` still submits a prompt; bash, vim and less in a terminal behave as before.
