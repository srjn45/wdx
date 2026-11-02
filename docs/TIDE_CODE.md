# tide: code, the editor and the left pane's filter

Status: designed with the human on 2026-11-01, not built. The human chose the section and its selector, what `Enter` does, hiding gitignored files, the filter's keys and filtering as you type, highlighting, `vi` with save or discard, and the validated order setting. The other details came from the AI and stand until the human changes them.

Reading and editing a project's files without leaving tide: step 4 of the order in `docs/IDE.md`. This is tide's alone but for the editors in `wdx deps`: there is no other CLI command, since a script or an AI reads files itself. `docs/CLI_TIDE.md` gets a row saying so.

## The left pane

```
 Agents                    2
 ...
 Worktrees                 4
 ...
 Code
 < auth-fix >
   + docs
   − internal
   ├ + git
   └ − tide
       ├ code.go
     ▸ ├ tabs.go                ◂
       └ tide.go
     README.md
 / tabs                    3
```

### The section

- It is called `Code`, with the letter `c`.
- `Code` comes last by default; `tide.section_order` changes that.

### The worktree selector

- It is a row of its own, the section's first, since a heading takes no cursor and a worktree's name can be long. `←→` on it moves through the worktrees, going round at the ends. `<` and `>` can be clicked.
- `missing` worktrees are skipped.
- It starts on the main checkout. Each project remembers its choice, through `C-q r` too. When the chosen worktree is removed it goes back to the main checkout.
- Only worktrees: other branches, proposed in `docs/IDE.md`, are not part of this.

### The tree

- It holds what `git ls-files -co --exclude-standard` lists: tracked and untracked files, without gitignored ones (the human's choice, for now). Directories come from the paths, so an empty one does not show.
- A tracked file missing from disk is listed, and says so when opened.
- Directories come before files, and both are sorted by name. Directories start folded. Each worktree keeps its own folds and cursor.
- It looks and moves as the Agents list does, design A of its [design sheet](https://claude.ai/artifact/PNZzmDWdZeiMHY1PPBFLy6), by the human's choice: a green `▸` and bold name for the cursor, hidden while the pane has no focus; a tinted row and `◂` for the file in the current tab; `−` and `+` for folds, `├ └ │` for the tree, and past about level 8 the row shows its level (`⋯9 ├`). Names end in `…`. Files have no state mark, having no state. With colour off the glyphs carry it all.
- `↑↓` or `k j` move through the rows shown, and the heading stays pinned. `→` or `l` unfolds. `←` or `h` folds, or moves to the parent when already folded or on a file. A click on a row moves the cursor and opens a file or folds a directory; a click on `+` or `−` only folds.
- `Enter` on a directory folds or unfolds it, as on a pipeline in that sheet. `Enter` on a file opens its tab, or goes to it, and the focus stays in the list.
- The tree is read again on the events in `docs/TIDE_WORKTREES.md` and on `f`. No file is watched, so an agent's edits show when it stops, as in Worktrees.

## The file tab

- One tab per worktree and file, read-only, in the text view diffs use, with `/` search inside the file and line numbers.
- Syntax highlighting is a must. Nothing is installed per language: Chroma's definitions, about 300 of them, are compiled into wdx. Its colours go through `internal/style`, so `color: never` shows none. A language it does not know shows as plain text.
- Binary files and very large ones say so rather than show. How large is decided when built.
- The file is read again on the same events as the tree, when its tab comes to show, and when its editor exits.

## The editor

### The setting

`editor`, default `vi`: one of the supported editors, shown as `← vi →` in the settings tab. Unlike other settings with `choices`, `set` refuses anything else (`docs/TIDE_SETTINGS.md`), since wdx must know how to start and quit it. It takes no arguments. It is a general setting rather than a `tide.` one because `wdx doc edit`, proposed in `docs/IDE.md`, will want it too. `$EDITOR` is not read.

The first release supports `vi` alone, and the others follow quickly, each being a row of what to run and what to send. A new editor needs no change for a rollback: `wdx update --rollback` puts back the config file as it was (`docs/RELEASING.md`).

Any terminal editor should work in a pane, as a pane is a terminal emulator; a GUI one does not. Not yet tried in a pane. [Editors](#editors) lists them.

### In `wdx deps`

Each supported editor is an optional dependency of its own, enabling `tide edit`, in `wdx deps list` with its status, and installed by `wdx deps install --name <editor>` as any other (`docs/DEPS.md`). So this much of the feature is on the CLI too. Packages:

| Editor | Ubuntu, Debian | macOS (brew names from memory, to be checked when added) |
|---|---|---|
| `vi` | `vim-tiny` | preinstalled |
| `vim` | `vim` | `vim` |
| `nvim` | `neovim` | `neovim` |
| `emacs` | `emacs-nox` | `emacs` |
| `hx` | `hx` | `helix` |
| `nano` | `nano` | `nano` |

These six are supported, by the human's choice. micro and Kakoune are not, since they overwrite another program's write with no question. macOS's own `nano` is pico since 12.3, by the sources found, so its keys need trying there.

### Running it

- `e` on a file tab, or on a file in the tree, turns that tab into the editor. It starts on the file in its worktree.
- It runs under a holder, as an agent does, so it survives `C-q r` and a daemon restart. It starts through the login shell, as an agent does, since the daemon's `PATH` may lack Homebrew or `~/.local/bin`.
- When it exits, the tab is read-only again and reads the file anew.
- An editor that cannot start leaves the tab read-only, saying `vi not found` and pointing to `wdx deps install --name vi`.
- An editor running with no tab, as after quitting tide and starting it anew, gets its tab back when the project first comes, or nothing could reach it.
- Its row goes when its process ends, so it never counts as a terminal to start again. Closing a project or removing a worktree stops the editors there, and their confirmations say so.
- Not a new `kind`: changing that `CHECK` means rebuilding `terminals`, whose `DROP` would cascade into `agents` and `messages` inside the migrator's transaction. `terminals` gains a nullable `editor_path` instead, which also keeps a rollback's data. An older binary sees such a row as a shell.

### Closing a tab with an editor running

This is the one exception to "closing a tab stops nothing" (`docs/TIDE_TABS.md`).

Closing asks **save**, **discard** or **cancel**, the human's choice, always, as wdx cannot see whether anything is unsaved. wdx then sends the editor the keys in [Editors](#editors).

- Only the file the tab is of is saved, by the human's correction. vi starts on it, so it is always buffer 1, which also spares escaping its path.
- The tab closes when the editor exits. Until then it stays on screen with the focus, so that whatever stopped it is there to answer. There is no timeout and nothing to detect.
- Another file edited in the same vi is left alone: vi stays with `E162` on screen.
- An editor with nothing unsaved may exit on the first key, so sending the rest to one already gone is not a failure.

### Why not autosave

vi can autosave, with `-c 'autocmd TextChanged,TextChangedI,InsertLeave <buffer> silent! update'`, and the human chose the popup instead. Half-typed text would reach the disk, where an agent or a build in that worktree can read it, and discarding would be gone. Above all, when another program has written the file, nothing shows until the next key. That key brings up the question, and the keys after it answer it:

| Editor | Next key typed | Result |
|---|---|---|
| `vi`, `vim`, `nvim` | `y` | **the other program's text is overwritten** |
| `vi`, `vim`, `nvim` | `n` | its text kept, the edit stays in the buffer, typing goes on |
| `vi`, `vim`, `nvim` | any other | swallowed, the question stays |
| `emacs` | `y` | a second question, its text kept |
| `emacs` | `r` | **the user's own edits are dropped**, its text kept |

A guard against this needs vimscript, which Ubuntu's `vi` lacks (`-eval`).

### What vi did

Measured on 2026-11-01 in tmux with vim.tiny 9.1, and with vim 9.1 and neovim 0.11 for autosave; not in a tide pane and not on macOS:

| Case | Answer | Result |
|---|---|---|
| Insert mode | `:x` | saved, exited |
| Insert mode | `:q!` | file unchanged, exited |
| A command half typed | `:xa` | saved, exited |
| Visual mode | `:qa!` | file unchanged, exited |
| One file | `:b1 \| x` | saved, exited |
| One file | `:b1 \| q!` | file unchanged, exited |
| Second file edited too | `:b1 \| x` | tab's file saved, second untouched, stays with `E162` |
| Second file edited too | `:b1 \| q!` | both untouched, stays with `E162` |
| Not edited, another program wrote the file | `:x` | the other program's text kept, exited |
| Edited, another program wrote the file | `:x` | stays, vi asks `Do you really want to write to it (y/n)?` |
| Read-only file, edited | `:xa` | stays, `E505` on screen |
| Unnamed buffer, edited | `:xa` | stays, `E141` on screen |
| `.vimrc` remaps `:` | `:qa!` | stays, and the keys act as vi commands (`recording @a`) |
| Autosave, typing | | written while typing, `:q` exits |
| Autosave, typing in a second file | | that file not written, `:q` stays with `E162` |
| Autosave, `u` | | the file goes back |
| Autosave, another program wrote the file | | the other program's text kept, vi asks `(y/n)` |

Remapping `:` is the known limit: keys typed into vi obey the user's mappings, so stray keys can change the buffer, though none reached the disk in that test. vi here has no `+clientserver`, so there is no other way in that works on every build.

### Highlighting while editing

It depends on the editor, and any syntax pack is the user's to install (the human's choice). Ubuntu's preinstalled `vi` is vim-tiny, built with `-syntax`, so it shows no colours at all while editing, though the read-only tab does.

### Keys tide takes first

`C-q`, `C-\`, `C-]`, Alt+arrows and Alt+Shift+arrows. The human's rule: what tide takes has a way through, so editors have no key restrictions. `C-q C-q`, `C-q \` and `C-q ]` do that today (`sendToProgram`). Proposed: the menu sends Alt+arrows and Alt+Shift+arrows on as well, the same way, which it does not yet.

### Editors

Measured on 2026-11-01 on Ubuntu 26.04 in tmux, not in a tide pane and not on macOS, with each editor's defaults. `<file>` is the tab's file, quoted. `vi` needs no wait after `Esc` (step 0); the others are to be tried without one when added.

| Editor | Save | Discard | Save when another program wrote the file | Second file edited too |
|---|---|---|---|---|
| `vi` (vim-tiny 9.1) | `C-\ C-n`, `:b1 \| x`, `Enter` | `C-\ C-n`, `:b1 \| q!`, `Enter` | asks `(y/n)`, or exits keeping that text if not edited | stays, `E162` |
| `vim` 9.1 | as `vi` | as `vi` | as `vi` | second file not tried |
| `nvim` 0.11 | as `vi` | as `vi` | as `vi` | second file not tried |
| `hx` 25.07 | `Esc`, `:open <file>`, `Enter`, `:wq`, `Enter` | `Esc`, `:open <file>`, `Enter`, `:q!`, `Enter` | refuses and says so, even if not edited | save stays and says so; **discard exits and loses the second file's edits** |
| `nano` 8.7 | `C-c`, `C-x`, `y`, `Enter` | `C-c`, `C-x`, `n` | asks | holds one file by default |
| `emacs` 30.2 | `C-g`, `C-x C-s`, `C-x C-c` | `C-g`, `M-x kill-emacs`, `Enter` | asks | not tried, and these keys act on the current buffer |

To settle when each is added: `hx`'s discard, perhaps with `:bc!` and then `:q`, and `emacs` with a second file.

Tried and not supported, by the human's choice, since their save loses another program's write with no question:

| Editor | Save | Discard |
|---|---|---|
| `micro` 2.0.15 | `C-q`, `y` | `C-q`, `n` |
| `kak` 2024.05 | `Esc`, `:try %{ quit } catch %{ write-quit }` | `Esc`, `:quit!` |

## The filter

- `/` in the left pane opens a line at its foot. The list filters as each key is typed.
- `Enter` moves to the list with the filter still on. `Esc` clears it, from the line or the list.
- It searches agent names, worktree names and branches, their changed files, and full paths in the selected worktree only. File contents are not searched.
- Case does not matter. Every word typed must appear, so `tide tabs` finds `internal/tide/tabs.go`.
- A match keeps its parents, opened for as long as the filter is on. Clearing the filter brings the folds back.
- The cursor stays on its row while that row matches, and otherwise goes to the first match.
- While typing, plain keys such as `f` and `v` go into the filter.
- A monorepo's cost is not measured: each key must not wait for the filter to finish, and the latest text wins.

## The order of the sections

`tide.section_order`, default `awc`: a letter per section, `a` Agents, `w` Worktrees, `c` Code.

- It is a plain string checked by the table's `set`, not `choices`, since five sections make 120 orders.
- Refused, with a reason naming the letters: an unknown letter, a letter twice, a capital, an empty value.
- `d` and `p` are added when docs and plans are. A rollback puts back the config file as it was, so an older version never reads them.
- A section left out comes after the others, in the default order, so `ca` is Code, Agents, Worktrees. Leaving one out never hides it: a value of `awc` must neither hide docs when they arrive nor stop that update, which refuses a value the new version does not accept.
- A save in tide's settings tab applies at once. `wdx setting set` elsewhere applies when tide next starts, since settings are not in the project's stream.

## Measured for the build

- `git ls-files -co --exclude-standard` took 6 ms on this repository's 440 files; a monorepo is not measured.
- Chroma is MIT. Its latest GitHub release is a v3 alpha, so a stable v2 unless that has changed.
- Chroma v2.27.0, with `regexp2` (MIT), measured on 2026-11-02 on linux/amd64 with the release's flags: the binary goes from 28.0 to 31.5 MiB, 10.6 to 11.5 MiB gzipped. Its `lexers` package registers every language when the process starts, in every `wdx` process since all are one binary: 20 ms more per run (`wdx help`, 36 to 56 ms over 20 runs) and 5 MB more resident (22 to 27 MB). A holder was not measured by itself.
- Tokenising took 0.24 s for 32 KiB of Go and 1.9 s for 256 KiB, without the race detector, on repeated copies of `diff.go`.

## Implementation plan

Laid out on 2026-11-01. It covers the `Code` section, the highlighted read-only tab and editing with `vi`. Each step leaves `make check && make test` passing and can be its own commit, when the human asks for one. Start by rebasing `tide-code` onto `main`.

After this plan, each its own piece of work: the filter, `tide.section_order`, the other editors one row each, and the Alt+arrows through the menu.

### 0. Try `vi` in a real pane

Done on 2026-11-01, with `exec vi` in a shell terminal of a test home, in a real tide under tmux, on Ubuntu 26.04's vim-tiny 9.1. The right pane's attachment is the same code, but was not itself tried.

Worked: drawing, typing, Unicode and wide characters, a line longer than the pane, resizing the window, zoom, moving the focus away and back, `C-q C-q`, `C-q ]`, `C-q r` with vi open and in the middle of an edit, and exit, with the terminal `exited` and code 0.

Through a holder session's `Write`, as tide's attachment will send them: save and discard from insert mode and from a half-typed command, not edited with another program's write, edited with another program's write (stays, asking), a second file edited (stays, tab's file saved, second untouched) and a read-only file (stays).

What it changed:

- **Arrow keys in Ubuntu's `vi`.** In insert mode an arrow key wrote `D` or `B` into the text and opened a line. It does so in plain tmux too, so it is not tide: `/etc/vim/vimrc.tiny` has `set compatible`, which turns `esckeys` off, and sets it after `-N` and `--cmd`. So `vi` starts with `-c 'set esckeys'`, the narrowest fix, which leaves the rest of vi mode alone. On `vim`, whose build is not `vi`'s, this is checked when it is added, and Neovim has no such option.
- **No wait is needed.** `Esc Esc` and the command in one write worked every time (step 6 replaced `Esc Esc` by `C-\ C-n`), so `vi`'s `Save` and `Discard` are one write each, and step 9 needs no timer for it. The table's waits stay only if a later editor proves to need them.

### 1. The list's sections

Done on 2026-11-01. A refactor with no change in behaviour, in `internal/tide`. `agentList.onWorktrees` became a `section` (agents, worktrees), and `moveUp`, `moveDown`, `paneItems`, `projectScreen.update`, its `click` and `draw` go by it. `sectionCode` comes with the Code section in step 3, so that no constant stands unused until then; `tide.section_order` later builds on this.

### 2. The API for the tree and for contents

Done on 2026-11-01. Beyond the plan: a file in conflict is listed once, though `ls-files` gives one per stage; a nested repository, which `ls-files` gives as a directory, is left out; and anything else that is not a regular file is `invalid_path` too, asked before opening it, since opening a named pipe would wait forever. The client's methods come with the first caller, in step 3.

- `git.ListFiles` in `internal/git/read.go`, from `git ls-files -z -co --exclude-standard`, joining `TestReadsWriteNothing`'s table.
- `worktree.ListFiles` and `worktree.ReadFile` in `internal/worktree`. Paths go through `filepath.IsLocal`, then `filepath.EvalSymlinks` must stay inside the worktree (`invalid_path`). A directory is `invalid_path`, and a file gone is `not_found`.
- In `api/openapi.yaml`, then `make generate`:
  - `GET /v1/worktrees/{worktree}/files`: `worktree`, `paths`, `truncated`.
  - `GET /v1/worktrees/{worktree}/files/content?path=`: `worktree`, `path`, `content`, `binary`, `too_large`, `size`. Over 2 MiB, or with a NUL or text that is not UTF-8, there is no `content`, since a JSON string cannot carry it.
- Both join `worktreeResponses` in `internal/daemon/http.go`. `WorktreeResponseLogged` logs `content` as `content_bytes` and `paths` as a count, so no contents and no huge lists reach the log.
- Tests: domain tests for a symlink out of the worktree, `..`, binary, not UTF-8, too large and gitignored files; a daemon test per outcome.

### 3. The Code section

Done on 2026-11-01, with these differences from the plan. There is one cursor for the section rather than one per worktree: changing the worktree happens on the selector, which the cursor has to stay on for the next `→`, so a cursor kept per worktree could never come back into use. `Enter` on a file does nothing until step 4, and no row is marked as the current tab's until then. A child's `├` stands under its parent's name, as on the Agents list, where the mock above had it under the fold mark. `f` reads the tree again even when the stream brings nothing new.

New `internal/tide/code.go`.

- `codeTree`, a pure function from paths to nodes, and `codeRows`, nodes and open directories to rows, each with table tests.
- `agentList` gains the chosen worktree and, per worktree, its paths, open directories and cursor key.
- The selector row: `←→` and `h`/`l`, going round, skipping `missing`, with clicks on `<` and `>`. A removed worktree falls back to `main`.
- `Enter` on a directory folds, `←→` folds and moves to the parent as in the lists above.
- The tree is read when the section first has a chosen worktree, and again when `head` or `Files` in its status changes, as `diffSeen` does for a worktree's uncommitted view, and on `f`.
- The chosen worktree joins `Resume`, only by adding a field.
- Tests: action sequences over the keys, clicks and fallback; one for `C-q r`.

### 4. The read-only tab

Done on 2026-11-01, and tried in a real tide under tmux: the tree, the mark, wrapping, `:`, `/`, `?` and `C-q -`. `diffShown` became `textShown`, as it now says either. Decided while building: over a zoomed list, opening a file moves the zoom and focus to its tab, since the tab could not be seen otherwise; a folded directory carries the `◂` of the file it hides, as a folded parent does in Agents; CRLF line ends do not show; an empty file says so. The diffs' keys that act on changes do nothing here, and `e` is free for step 8.

- A `file:` tab prefix beside `diff:` and `pr:`, with `isFileTab`, and `isTextTab` for what diffs and files share. `isDiffTab`'s callers each take the one they mean. The title is `file: <worktree>/<name>`, with no mark.
- It reuses `diffView`, as `docs/IDE.md` proposes: a new `source` mode with `setSource` and `prepareSource`, line numbers, wrapping, `/`, `n`, `N`, `:` and paging. `diffKey` and the bar leave out what only diffs have: changes, comments, verify, commit, land.
- `Enter` on a file opens it and the focus stays in the list. It is read again when `diffSeen`'s file branch changes, when its tab comes to show and on `f`, by the existing `diffSync`.
- Binary, too large, or gone: the view says so. A worktree removed closes the tab, as in `worktreeRemoved`.
- `C-q r` keeps it: `Resume.Tabs` carries the tab, and an older tide drops what it does not know.
- Tests: action sequences, the messages, and that `c`, `v` and `C` do nothing there.

### 5. Highlighting

Done on 2026-11-02, and tried in a real tide under tmux: Go in colour, and a file over the limit plain. The human accepted Chroma's cost, measured above. Different from the plan: the text shows at once and its colours follow, since 256 KiB takes about 2 s; reading a file again with the same text keeps them and works out nothing; the first line is read for its interpreter, past `env`, as Chroma's `Analyse` knew only the shells on such a line, which only the real run showed; `node` is mapped by hand, as Chroma has no such name. Any failure in Chroma leaves the file plain. The colours: keyword purple, string green, comment dim, number yellow, type cyan, function blue.

- `go get` Chroma's latest stable v2, then `make notices`. Measure what it adds to the binary and tell the human before going on.
- New `internal/tide/highlight.go`: choose a lexer by file name, then by content for a shebang, and none means plain. Each token category goes through a new semantic method in `internal/style` (keyword, string, comment, number, type, function), in the 16 ANSI colours, so that it follows the terminal's theme, and `color: never` shows none.
- It runs in the `tea.Cmd` that loads the file, never in `Update`. Over 256 KiB it stays plain, so a big file does not stall.
- Wrapping keeps a colour across the break. Search reads the plain text, and its mark goes over the colours.
- Tests: tokens to styles for a few languages, wrapping, no escape codes with colour off, and text unchanged once stripped.

### 6. The editors' table and setting

Done on 2026-11-02, with these differences from the plan. Driving the real `vi` found that `Esc` on a command half typed runs it, in the compatible mode Ubuntu's `vi` starts in (`cpoptions` has `x`): a half-typed `:s/first/` deleted text on save. So `Save` and `Discard` start with `C-\ C-n`, which leaves any mode and drops the command line; the test covers it, which step 0 had not caught. `daemon.Settings` gets `editor` in step 7, with the code that reads it, rather than carrying a value nothing uses; until then `wdx setting set editor` saves it and nothing reads it. `deps` checks only that the editor is on the `PATH`, with no version.

- New `internal/editor`, data with no dependencies, so that config, deps, the daemon and tide can share it: `Names`, and for each its `Command(path)`, `Save` and `Discard`, what to write. Only `vi` for now, which runs `vi -c 'set esckeys' -- <path>`. It goes into `AGENTS.md`'s layout and `.golangci.yml`'s directions.
- `editor` in `internal/config`, default `vi`, with `choices`, its `set` refusing others. `daemon.Settings` carries it, so that `wdx setting set` applies to the next editor started.
- In `internal/deps`: a `vi` dependency, optional, with the feature `tide edit`, ready when found on the `PATH`; `Installable`, and `PlanFor` with `vim-tiny` on apt and nothing to do on macOS. `docs/DEPS.md` gets its row.
- A test drives the real `vi` through a PTY for each row of the measured table, waiting on the file's content, and fails rather than skips when `vi` is missing on Linux, so that it cannot silently prove nothing.

### 7. Running an editor in the daemon

- Migration `0013_editor_path.sql`: `ALTER TABLE terminals ADD COLUMN editor_path TEXT`.
- `terminal.KindEditor`, read from that column. The terminal flow's filters become `t.kind = 'shell' AND t.editor_path IS NULL`, and `ofKind` follows. `byKind` in `internal/daemon/projects.go` splits a third way.
- `daemon.Settings` carries `editor`, moved from step 6, so that `wdx setting set` applies to the next editor started.
- `Supervisor.CreateEditor`, on the existing `createRunning`, in the worktree's directory, through the login shell. The row is deleted in `recordExit` and `recordGone`, and in `Reconcile` when its holder is gone.
- `POST /v1/editors` with `worktree`, `project` or `dir`, and `path`: 201 `created`, or 200 `opened` for the editor already running on that file. It first looks for the program through the login shell, and fails with `dependency_missing`, naming it. Errors: `invalid_path`, `not_found`, `dependency_missing`.
- `Project.editors` in the stream, each with `id`, `terminal_id`, `worktree`, `path`, `program`. `project_in_use` gets `details.editors`, a project's close result `stopped_editors`, and `RemoveWorktree` stops those in the worktree and returns them.
- `docs/TERMINALS.md` and `docs/AGENT.md` say what the new rows are.
- Tests, with a test command in place of `vi`, set through the daemon's options as `AgentLaunch` is: created, opened, missing program; the row gone on exit, kill and restart; adoption across a daemon restart; not in `terminal list` or on any terminal path; project close and open; worktree removal; an older schema's rollback.

### 8. Editing in tide

- `e` on a file tab or a file row asks for the editor, and the focus moves to the tab. A refusal shows in the view.
- `Project.editors` decides what the tab is: with one for its worktree and path, `agentPane.draw` shows the terminal, `paneInput` goes to it and `attachedAgent` becomes `attachedTerminal`. The title is `edit: <worktree>/<name>`.
- When the editor leaves the stream, the tab detaches and `diffSync` reads the file anew.
- An editor with no tab gets one on the project's first event.
- The confirmations of `C-q w` and worktree removal name the editors.
- Tests: a sequence through `e`, typing into the attachment, exit and re-read; `C-q r` keeps it attached; a fresh start gets its tab back; two tides on one editor.

### 9. Closing with the popup

- `C-q -` and a click on `(-)`, with an editor running, select that tab and open a box with `s` save, `d` discard and `Esc` cancel.
- The answer sends `editor.Save` or `Discard` through the attachment's `session.Write`, once attached. Sending to an editor already gone is not an error.
- `closing` is remembered for that tab: when the editor leaves the stream the tab closes. Until then its screen stays, with the focus, for whatever stopped it.
- Tests: each answer with a test program that records its input; cancel; a stay; closing an inactive tab.

### 10. Docs and release

`docs/TIDE.md`, tide's help and the menu; `docs/CLI_TIDE.md`, with rows for the tab, the tree and `editor` in `wdx deps`; `docs/SETTINGS.md` if a limit applies; `docs/IDE.md`'s Code part pointing here. Then `make test-stress PKG=./internal/terminal`, since it changes how processes end. A dev release only when the human asks.

Not checked by this plan: macOS, for want of a machine and a CI job (`docs/REVIEW.md`), so `vi` there waits on someone trying it.

## Suggested, not part of this

- Highlighting in the diff tabs, which use the same text view.
- A git status letter on the tree's files.
- A toggle for gitignored files.
