# tide: code, the editor and the left pane's filter

Status: built on 2026-11-02, steps 0 to 10 of the plan below, with `vi` the one editor, and then the filter; the Alt+arrows through the menu, the other five editors, creating, renaming and deleting files and directories, and `tide.section_order` on 2026-11-02. Designed with the human on 2026-11-01. The human chose the section and its selector, what `Enter` does, hiding gitignored files, the filter's keys and filtering as you type, highlighting, `vi` with save or discard, and the validated order setting. The other details came from the AI and stand until the human changes them.

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
- The worktree's name is always bold, so that the selector stands apart from the tree below it (the human's choice, 2026-11-03).
- `missing` worktrees are skipped.
- It starts on the main checkout. Each project remembers its choice, through `C-q r` too. When the chosen worktree is removed it goes back to the main checkout.
- Only worktrees: other branches, proposed in `docs/IDE.md`, are not part of this.

### The tree

- It holds what `git ls-files -co --exclude-standard` lists: tracked and untracked files, without gitignored ones (the human's choice, for now). Directories come from the paths, so an empty one does not show.
- A tracked file missing from disk is listed, and says so when opened.
- Directories come before files, and both are sorted by name. Directories start folded. Each worktree keeps its own folds and cursor.
- It looks and moves as the Agents list does, design A of its [design sheet](https://claude.ai/artifact/PNZzmDWdZeiMHY1PPBFLy6), by the human's choice: a green `▸` and bold name for the cursor, hidden while the pane has no focus; a tinted row and `◂` for the file in the current tab; `−` and `+` for folds, `├ └ │` for the tree, and past about level 8 the row shows its level (`⋯9 ├`). Names end in `…`. A changed file has git's letter and a changed directory a count, in [Changes in the tree](#changes-in-the-tree). With colour off the glyphs carry it all.
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

The first release supported `vi` alone; `vim`, `nvim`, `nano`, `hx` and `emacs` followed on 2026-11-02, each a row of what to run and what to send ([Editors](#editors)). A new editor needs no change for a rollback: `wdx update --rollback` puts back the config file as it was (`docs/RELEASING.md`).

Any terminal editor should work in a pane, as a pane is a terminal emulator; a GUI one does not. Not yet tried in a pane. [Editors](#editors) lists them.

### In `wdx deps`

Each supported editor is an optional dependency of its own, enabling `tide edit`, in `wdx deps list` with its status, and installed by `wdx deps install --name <editor>` as any other (`docs/DEPS.md`). So this much of the feature is on the CLI too. Packages:

| Editor | Ubuntu, Debian | macOS (brew names from memory, not yet checked on a Mac) |
|---|---|---|
| `vi` | `vim-tiny` | preinstalled |
| `vim` | `vim` | preinstalled |
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

`C-q`, `C-\`, `C-]`, Alt+arrows and Alt+Shift+arrows. The human's rule: what tide takes has a way through, so editors have no key restrictions. `C-q C-q`, `C-q \` and `C-q ]` do that (`sendToProgram`), and so does `C-q` then any Alt+arrow or Alt+Shift+arrow (built 2026-11-02). Building it found that the vt sent a modified arrow, editing or function key as `Esc` alone or without its modifiers; `screen` now encodes those as xterm does (docs/VT.md, #17), so Ctrl+arrows and Shift+arrows reach programs too.

### Editors

Measured first on 2026-11-01 in tmux, then built on 2026-11-02 and tested against the real programs (`internal/editor`'s tests, which skip one not installed, but `vi` on Linux) and tried in a tide pane, each saving a file edited with an arrow key: vim-tiny 9.1, vim 9.1, Neovim 0.11.6, nano 8.7.1, Helix 25.07.1 and GNU Emacs 30.2 on Ubuntu 26.04; not on macOS. `<file>` is the tab's file, relative to the worktree.

| Editor | Started with | Save | Discard | Another program wrote the file, edited | A second file edited too |
|---|---|---|---|---|---|
| `vi` (vim-tiny) | `-n -c 'set esckeys'` | `C-\ C-n`, `:b1 \| x`, `Enter` | `C-\ C-n`, `:b1 \| q!`, `Enter` | asks `(y/n)` | the tab's file saved or not; stays, `E162` |
| `vim` | as `vi`: a vimrc may set `compatible` | as `vi` | as `vi` | as `vi` | as `vi` |
| `nvim` | `-n`: Neovim has no `esckeys` | as `vi` | as `vi` | as `vi` | as `vi` |
| `nano` | nothing | `C-c`, `C-x`, `y`, `Enter` | `C-c`, `C-x`, `n` | asks | holds one file by default |
| `hx` | nothing | `CSI 27 u`, `:open '<file>'`, `:update`, `:quit-all` | `CSI 27 u`, `:open '<file>'`, `:buffer-close!`, `:quit-all` | refuses, says so | the tab's file saved or not; stays, `unsaved buffer remaining` |
| `emacs` | `-nw`, and `--eval` with two keys of wdx's | `CSI 7001 ~` | `CSI 7002 ~` | asks at the first key typed after it, `changed on disk; really edit the buffer?` | the tab's file saved or not; stays, asking to save the other |

What building them found:

- **Nothing beside the file.** vi, vim and Neovim start with `-n`, so no swap file. emacs is told to make no backup, auto-save or lock file. nano keeps a lock file, `.<file>.swp`, while it edits one: Ubuntu's `/etc/nanorc` sets `locking`, which no flag unsets but `--ignorercfiles`, which would drop the user's settings too, so it is left; nano removes it when it quits. hx writes none.
- **hx and Esc.** hx reads `Esc` followed at once by `:` as Alt+`:`, so it takes `Esc` as `CSI 27 u`, which it reads as `Esc` whatever follows. `:update` writes the file only if edited, as `:write` refuses a file another program wrote even when it was not. hx writes in the background, and `:quit` looks for files edited before that write is done, so it refused; `:quit-all` waits for it. The file's name goes in single quotes, or in double quotes, with `%` doubled, when it holds one.
- **emacs and C-g.** On a terminal, `C-g` throws away the keys typed after it, and so does leaving a prompt, so no keys typed after one can run a command. emacs is started with two keys of wdx's, sequences no terminal sends, decoded to commands that save or forget the edits of the buffer of the tab's file, whichever buffer is current, then quit as `C-x C-c` would, asking about any other buffer edited. From a prompt, they leave it, then do so. emacs refuses to edit a read-only file until told to, with `C-x C-q`; saving it then asks `write-protected; try to save anyway?`.
- **Neovim** runs its editor as a child process in a session of its own, which quits soon after the interface does. Its file message is off by default, so the tests wait for its status line. It cannot open its log when its state directory's path holds a comma, and writes `.nvimlog` into the working directory instead: the tests' names have none.
- **Keys after quitting.** The editor runs as `exec`, so a key sent after it quit, as nano's `y` when nothing was edited, reaches nothing.
- **macOS** is untried: Homebrew's names (`neovim`, `nano`, `helix`, `emacs`) are from memory, `vi` and `vim` come with it, and its own `nano` is pico since 12.3, whose keys may differ.

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

### As built

Built on 2026-11-02 in `internal/tide/filter.go`, and tried in a real tide under tmux: typing `tide tabs` with the letters that are also menu keys, the parents opened, opening the match from the list, and `Esc` bringing the folds back with the cursor on the folded directory. The list's hints say `/ filter`. Decided while building, by the AI:

- While the filter is on, the folds do not change: `←` only moves to the parent, `→`, `Enter` on a directory and a click on a fold mark do nothing. A matching agent whose children do not match shows no fold mark.
- A worktree shows when its name or branch matches, or a file it changed does, and then with only the files that match. Matched by its name alone, it keeps `+` and says how many files it has.
- `↑` and `↓` move the cursor while typing, so a match can be reached without leaving the line; `k` and `j` type.
- The line counts the rows that match, not the parents kept for them, and shows `…` while the Code section's files are being matched.
- A section with nothing matching says `no match`. The headings keep their totals.
- When the filter is cleared, the cursor stays on its row, or goes to the nearest one above it that holds it, such as its folded directory.
- A click on the line goes back to typing in it.
- Each project has its own filter for the run; `C-q r` does not keep it.
- The Code section's files are matched in a command, away from `Update`, and an answer for text typed before is dropped. The agents and worktrees are few, and are matched as they are drawn.

## Creating, renaming and deleting

Designed with the human on 2026-11-02, who chose the directory picker, a name typed alone, popups for all three, keys that act only in the Code section, and rename and delete with create.

| Key | Does |
|---|---|
| `n` | a new file |
| `N` | a new directory |
| `r` | rename the file or directory under the cursor |
| `d` | delete it |

- **Only in Code.** With the cursor in the section, these four come before the `C-q` menu's letters, which otherwise act without `C-q` in the project pane: `n` there is not the menu's new session, nor `r` its reload. `C-q n` and `C-q r` still are. Elsewhere the letters are the menu's.
- **New.** A popup picks the directory, as [Open Project › Local] picks one on disk: the worktree's directories, from the one under the cursor or the file's, going no higher than the worktree; `→` goes in, `←` up, typing filters, and the first row, `create the file here`, asks the name alone. A name taken, or one with a `/`, is refused in the popup, which stays to correct it. The cursor goes to what was made, its directories open, and a new file opens in the editor, as there is nothing to read in an empty file.
- **Rename** asks the new name, with the old one to edit, and renames in place: no moving to another directory.
- **Delete** asks, `keep it` selected first, saying how many files a directory holds and how many of them are untracked or changed, which git cannot bring back. It is for good: nothing goes to a trash. A symbolic link is deleted, not what it leads to.
- **Editors.** What an editor has open, or a directory holding it, is neither renamed nor deleted: the status line says which editor to close first, and the daemon refuses with `editor_running` in case tide's list is late. A file's read-only tab follows a rename and closes on a delete.
- **No git.** All three are plain file system changes: a renamed tracked file shows as deleted and its new name as untracked, and nothing writes the index, which background git must never do.
- **Empty directories.** git lists no directory holding no file, so `listWorktreeFiles` gives those apart, `empty_dirs`, from `ls-files --directory`: without them a new directory would be gone from the tree at once. One holding only an empty one shows alone.
- **API.** `POST`, `PATCH` and `DELETE` on `/v1/worktrees/{worktree}/files`, refusing a path out of the worktree or into `.git`, the worktree itself, and `.git` as a name (`invalid_path`, `invalid_name`), a name taken (`path_exists`) and a directory not there (`not_found`). The CLI has no command, as for the rest of the section: a script or an AI changes files itself (`docs/CLI_TIDE.md`).

## Changes in the tree

Status: designed with the human on 2026-11-02, and built the same day in two steps, the files' letters and then the directories' counts, with a space between a cut name and its letter. The human chose marking both what is not committed and what is committed against the base, git's letters rather than new glyphs, colouring the name as well as the letter, the colours (scheme 2 of a preview in the terminal: what is not committed by what changed, all that is committed in cyan), and a count on directories. The other details came from the AI and stand until the human changes them.

The tree shows which files a worktree changed, so that its changes can be found and opened without going through Worktrees.

```
 Code
 < auth-fix >
   + docs                     2     cyan: only committed changes below
   − internal                 5     red: a deletion not committed below
   ├ + git                    1
   └ − tide                   4
       ├ code.go              M     yellow: changed, not committed
       ├ newfile.go           ?     green: untracked
       ├ old.go               D     red: deleted, not committed
     ▸ ├ tabs.go              M     cyan: changed in a commit
       └ tide.go
     README.md
```

### What is marked

- **Not committed:** the worktree's `files`, staged, not staged and untracked, as the Worktrees section lists them: `M` changed, `A` added to the index, `?` untracked, `D` deleted, `U` in conflict.
- **Committed:** its `committed_files`, what its commits changed since it left the base, as its PR tab shows: `M`, `A`, `D`. On the main checkout the base is the default branch on `origin`, so these are the commits not pushed. A worktree with no base, or whose commits are not known, marks none.
- **Both:** a file with changes of both kinds shows its letter not committed, the newer state. A file added in a commit and then changed shows a yellow `M`, not that it is new in the PR.
- **Not in the tree:** a file deleted in a commit, or with `git rm`, is not in the list, so it has no row; the PR tab and Worktrees show it. A file deleted from disk but still in the index is listed, with its `D`.
- **No new reads.** Both lists are in the worktree's status, which tide already follows in the project's stream, so the marks cost no git and change on the same events as Worktrees: an edit not staged shows when the agent stops, or on `f`. A worktree not read yet (`reading…`) marks nothing.

### How it looks

- The letter sits at the row's right end, before the `◂` of the open tab, and the name is cut shorter to make room for it.
- **Colours,** the name and the letter alike, through `internal/style`:

  | | Colour | Style |
  |---|---|---|
  | `A`, `?` not committed | green | `Added` |
  | `M` not committed | yellow | `Warning` |
  | `D`, `U` not committed | red | `Removed` |
  | anything committed | cyan | new: `Committed` |

  So what is being worked on now has the usual colours, and everything already committed is one quieter colour that never mixes with them.
- The cursor's row keeps its green `▸`, and its name is bold in its change colour; the open tab's row keeps its tint. An unchanged file looks as now.
- With colour off the letter still says what changed, but not whether it is committed: the same letter means either.

### Directories

- A directory shows how many changed files are under it, at any depth, in place of a letter, folded or not, so a change can be followed down from the top.
- Its colour, name and count, is the strongest below it: anything not committed comes before anything committed, and among those red before green before yellow. All committed is cyan.
- The count includes changed files with no row, such as one deleted in a commit, as the Worktrees section counts them: it says how much changed below, not how many rows are marked.
- The counts are worked out from the two lists, walking up each changed path, so their cost grows with the changes, not with the tree: it is the same on a monorepo's 387K files.

### Built

- `internal/style`: `Committed`, cyan.
- `internal/tide/code.go`: `codeMarks`, the marks of the chosen worktree, a letter and colour per changed file and a count and colour per directory, worked out from its status each time the section is drawn, which costs as much as there are changes; `codeLine` draws them. The filter's tree shows them too. The Worktrees section's letters take their colour from the same place, `codeMark.paint`.
- Tests: the marks from statuses (not committed winning, the order of colours, counts at every level, a path deleted in a commit counted with no row), and the drawn rows with colour off (the letter at the end, the name cut for it) and on (the style of name and letter, the cursor's and the open tab's rows).
- `docs/CLI_TIDE.md`: a row. The CLI has no tree; `wdx worktree list` already lists the files not committed with their letters, and `wdx worktree diff` what is committed.
- Two steps, each its own commit if the human wants: the files' letters, then the directories' counts.

## The order of the sections

`tide.section_order`, default `awdpc`: a letter per section, `a` Agents, `w` Worktrees, `d` Docs (docs/DOCS.md), `p` Plans, `c` Code. Since 2026-11-03 (docs/TIDE_STATE.md) the default puts Docs before Code and leaves room for Plans between them.

- It is a plain string checked by the table's `set`, not `choices`, since five sections make hundreds of orders.
- Refused, with a reason naming the letters: an unknown letter, a letter twice, a capital, an empty value, and `p` alone, which would show nothing until plans come.
- `p` is accepted before plans are built, and shows nothing until then.
- `d` came with docs, on 2026-11-03; `p` was accepted on 2026-11-03, before plans. A rollback puts back the config file as it was, so an older version never reads them.
- A section left out is hidden, so `ca` shows Code, then Agents. Until 2026-11-03 a section left out came after the others; that change was breaking, as `awc` set before docs now hides Docs. The cursor never stays in a hidden section: it goes to the first row shown.
- A save in tide's settings tab applies at once. `wdx setting set` elsewhere applies when tide next starts, since settings are not in the project's stream.
- Built 2026-11-02. The first section's heading stays at the pane's top as the Agents heading did, and `↑` / `↓` go through the one list the sections make in their order, rather than each section knowing its neighbours.

## Measured for the build

- `git ls-files -co --exclude-standard` took 6 ms on this repository's 440 files. Large repositories were measured on 2026-11-02, and the list since comes from the index and one walk, with no limit, and its tree is built off tide's UI loop: docs/LARGE_REPOS.md.
- Chroma is MIT. Its latest GitHub release is a v3 alpha, so a stable v2 unless that has changed.
- Chroma v2.27.0, with `regexp2` (MIT), measured on 2026-11-02 on linux/amd64 with the release's flags: the binary goes from 28.0 to 31.5 MiB, 10.6 to 11.5 MiB gzipped. Its `lexers` package registers every language when the process starts, in every `wdx` process since all are one binary: 20 ms more per run (`wdx help`, 36 to 56 ms over 20 runs) and 5 MB more resident (22 to 27 MB). A holder was not measured by itself.
- Tokenising took 0.24 s for 32 KiB of Go and 1.9 s for 256 KiB, without the race detector, on repeated copies of `diff.go`.

## Implementation plan

Laid out on 2026-11-01. It covers the `Code` section, the highlighted read-only tab and editing with `vi`. Each step leaves `make check && make test` passing and can be its own commit, when the human asks for one. Start by rebasing `tide-code` onto `main`.

After this plan, each its own piece of work: the filter (built, see [As built](#as-built)), `tide.section_order`, the other editors one row each, and the Alt+arrows through the menu.

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

Done on 2026-11-02, with these differences from the plan. The migration is `0015_editor_path.sql`, as `0013` and `0014` were taken on `main` meanwhile, and it adds `editor` beside `editor_path`, since tide needs to know which editor runs to send it the right keys after the setting changed. The request is `openEditor`, since asking again returns the editor running. `Editor` in the API has `terminal_id` and no `id`, as an editor has no row of its own; it also has `state`. A closed project refuses it with `project_closed`, and an editor whose holder fails with `start_failed`. `vi` starts with `-n`, keeping no swap file, which would sit in the worktree beside the file where git and agents see it, and stay behind when the editor is hung up on; the `vi` test checks it. An editor keeps no terminal history, since what scrolled by in it is the file. `wdx project close` and `wdx worktree remove` do not print the editors they stopped in text yet; their `--json` has them. Not done: a test of an older binary's rollback, since the migration only adds columns that an older binary does not read.

- Migration `0013_editor_path.sql`: `ALTER TABLE terminals ADD COLUMN editor_path TEXT`.
- `terminal.KindEditor`, read from that column. The terminal flow's filters become `t.kind = 'shell' AND t.editor_path IS NULL`, and `ofKind` follows. `byKind` in `internal/daemon/projects.go` splits a third way.
- `daemon.Settings` carries `editor`, moved from step 6, so that `wdx setting set` applies to the next editor started.
- `Supervisor.CreateEditor`, on the existing `createRunning`, in the worktree's directory, through the login shell. The row is deleted in `recordExit` and `recordGone`, and in `Reconcile` when its holder is gone.
- `POST /v1/editors` with `worktree`, `project` or `dir`, and `path`: 201 `created`, or 200 `opened` for the editor already running on that file. It first looks for the program through the login shell, and fails with `dependency_missing`, naming it. Errors: `invalid_path`, `not_found`, `dependency_missing`.
- `Project.editors` in the stream, each with `id`, `terminal_id`, `worktree`, `path`, `program`. `project_in_use` gets `details.editors`, a project's close result `stopped_editors`, and `RemoveWorktree` stops those in the worktree and returns them.
- `docs/TERMINALS.md` and `docs/AGENT.md` say what the new rows are.
- Tests, with a test command in place of `vi`, set through the daemon's options as `AgentLaunch` is: created, opened, missing program; the row gone on exit, kill and restart; adoption across a daemon restart; not in `terminal list` or on any terminal path; project close and open; worktree removal; an older schema's rollback.

### 8. Editing in tide

Done on 2026-11-02, with these differences from the plan. `attachedAgent` became `attachedTerminal`, which also returns the history to read: an editor has none, so one not running, as while it stops, is not attached. `e` in the Code section first opens the file's tab, as `Enter` does. The read-only view's bar and `?` list `e`, and the Code section's hint says `e edit`. A refusal stays in the tab's bar until the next key, with `wdx deps install --name vi` for an editor not found. The editor's tab comes back only on the project's first event, so one closed afterwards stays closed until tide starts again. Tried in the tests with the test home's shell standing in for the editor, typed into through the attachment; the real `vi` in a real tide waits for step 10.

- `e` on a file tab or a file row asks for the editor, and the focus moves to the tab. A refusal shows in the view.
- `Project.editors` decides what the tab is: with one for its worktree and path, `agentPane.draw` shows the terminal, `paneInput` goes to it and `attachedAgent` becomes `attachedTerminal`. The title is `edit: <worktree>/<name>`.
- When the editor leaves the stream, the tab detaches and `diffSync` reads the file anew.
- An editor with no tab gets one on the project's first event.
- The confirmations of `C-q w` and worktree removal name the editors.
- Tests: a sequence through `e`, typing into the attachment, exit and re-read; `C-q r` keeps it attached; a fresh start gets its tab back; two tides on one editor.

### 9. Closing with the popup

Done on 2026-11-02, with these differences from the plan. `Esc` in the box keeps editing, and other keys do nothing. Keys waiting for the editor's attachment are dropped if it ends first. tide's tests stand in a shell for the editor, which shows what it was typed and stays, as `vi` does when something stops it, so they check vi's own keys arrived and the tab stays until it ends. That the keys work in `vi` is `internal/editor`'s test.

- `C-q -` and a click on `(-)`, with an editor running, select that tab and open a box with `s` save, `d` discard and `Esc` cancel.
- The answer sends `editor.Save` or `Discard` through the attachment's `session.Write`, once attached. Sending to an editor already gone is not an error.
- `closing` is remembered for that tab: when the editor leaves the stream the tab closes. Until then its screen stays, with the focus, for whatever stopped it.
- Tests: each answer with a test program that records its input; cancel; a stay; closing an inactive tab.

### 10. Docs and release

Done on 2026-11-02. `make test-stress PKG=./internal/terminal` failed `TestSweepSockets` once in 3: the test swept a lost terminal's socket before its watch ended, a gap that deleting an editor's row as a process ends had widened; it now waits for the watch to end (docs/FLAKES.md). Tried with the real `vi` in a real tide under tmux, with a test home: `e` from the tree, typing with an arrow key in insert mode, no swap file in the worktree, `C-q -` and `s` saving, the tab closing and the worktree showing the change; another program's write kept, with `vi` asking and the tab staying until `vi` was quit; quitting tide with `vi` holding unsaved text, and its tab back with the text on the next start; `d` discarding. It found one bug: a refusal longer than the tab's bar was dropped from it, with most of its keys, so a long reason showed nothing; the bar now cuts it short. A test home whose path is too long for a holder's socket fails to start the editor with `start_failed`, as any terminal does there.

`docs/TIDE.md`, tide's help and the menu; `docs/CLI_TIDE.md`, with rows for the tab, the tree and `editor` in `wdx deps`; `docs/SETTINGS.md` if a limit applies; `docs/IDE.md`'s Code part pointing here. Then `make test-stress PKG=./internal/terminal`, since it changes how processes end. A dev release only when the human asks.

Not checked by this plan: macOS, for want of a machine and a CI job (`docs/REVIEW.md`), so `vi` there waits on someone trying it.

## Suggested, not part of this

- Highlighting in the diff tabs, which use the same text view.
- A git status letter on the tree's files: designed on 2026-11-02, [Changes in the tree](#changes-in-the-tree).
- A toggle for gitignored files.
