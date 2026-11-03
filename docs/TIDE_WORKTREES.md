# tide: worktrees and diffs

Status: implemented. Designed with the human on 2026-11-01. "Proposed" marks what came from the AI and the human let stand.

Reviewing what agents changed without leaving tide: step 3 of the order in `docs/IDE.md`. Tabs are in `docs/TIDE_TABS.md`.

## The left pane

One scrolling list with two headings, Agents first and Worktrees after it.

```
 Agents                    2
 ● swift-falcon
 ● tests
 Worktrees                 4
 ▾ main
     M README.md
 ▾ auth-fix  ↑3 +120 −8  ● swift-falcon
     M internal/tide/tabs.go
     D internal/tide/old.go
     ? docs/AUTH.md
   tide-tabs  landed
   spike      ↑2 +40 −1
   old-try    missing
```

- Every worktree `git worktree list` gives is listed, whether wdx made it or not, which also shows the stale ones to clean up. `missing` marks one whose directory is gone.
- A worktree's children are its files with uncommitted changes: staged, unstaged and untracked, with git's status letter.
- A worktree starts folded, since it can have many files, and its row says how many. `←→` folds it, as in the Agents list.
- `Enter` on a file opens its file diff; `Enter` on a worktree opens its PR view. If that tab is open, it goes to it.

The worktree row shows the commits ahead of the base with their added and removed lines, `landed` when none are ahead and nothing is uncommitted, and the agents running there. A new worktree with nothing in it also says `landed`: it has nothing to lose.

`f` in the left pane, or `C-q f` there, reads every worktree again (docs/TIDE_MENU.md).

A stale worktree shows `missing`, and removing it is under "Landing and removing" below.

## The file diff

One tab per file, showing its uncommitted changes split in two: the whole committed file (`HEAD`) on the left, the whole file with its uncommitted changes on the right.

```
[● diff: auth-fix/tabs.go]
 HEAD                      │ uncommitted
 10 func open() {          │ 10 func open() {
 11   return old()         │ 11   return next()
                           │ 12   // and more
 12 }                      │ 13 }
```

Proposed:

- Both sides are aligned and scroll together, and the tab opens on the first change.
- Long lines wrap, keeping both sides aligned; zoom gives more width.
- A new file has an empty left side, a deleted one an empty right side. A binary file says so.
- A file with no uncommitted changes left, committed since for example, says so and its tab stays until closed.

## The PR view

One tab per worktree, showing only the changes, unified, of what is committed on it, from the merge-base with the base branch. So later commits on the base do not appear as removals, and it shows what a PR would.

- Uncommitted changes are not in it: those are the worktree's file rows.
- No branch selector for now.

Proposed:

- The base is the default branch named by `origin/HEAD`, else `main`, else `master`, as the local branch. A checkout on that branch itself compares with the same branch on `origin`, so it shows what is not pushed.
- With no such branch, or nothing ahead of it, the tab says so.
- Opening the tab downloads what a partial clone lacks, since the user asked for it (agreed). Measured beside `git fetch` and `git pull`, 726 of those and 2,983 such reads, on one clone: none failed. Nothing that runs by itself downloads.

## Keeping it live, without git locks

Agreed on 2026-11-01: no continuous git polling, because of long `git pull` and `git fetch` in big monorepos. No timer runs git; the events below do, on the architecture below. The cost: an edit not staged shows only at one of the events below, so an agent's edits appear when it stops rather than as it types. Watching a worktree's files is not proposed: it takes one watch per directory, which a monorepo can exhaust.

### The events

| Event | How it is seen | What is read again |
|---|---|---|
| A client starts following the project | the stream starts | the list and every worktree |
| An agent stops working: idle, waiting or ended | agents compared with those last seen, on each notice | its worktree |
| `index`, `HEAD` or `logs/HEAD` changed in a worktree's git directory | file watch | that worktree |
| The directory `<common>/worktrees` changed: a worktree added or removed | file watch | the list |
| The base branch's ref or reflog changed | file watch | the commit summary of every worktree |
| The user looks: the focus moves to the left pane, a diff tab opens, the refresh key (`f` in the left pane) | a request from tide | that worktree, or all of them |
| The watcher lost events | the watcher says so | the list and every worktree |

Creating an agent and opening a project need no event of their own: the agent's row comes from the database, a worktree made for it is seen by the watch on `worktrees`, and a project being followed is read when following starts.

What each git operation touched in a linked worktree, measured on git 2.53.0:

| Operation | In its git directory | In the common directory |
|---|---|---|
| edit, not staged | nothing | nothing |
| `add`, `restore`, `stash pop` | `index` | |
| `commit`, `reset --hard`, `stash`, `switch`, `rebase` | `index`, `logs/HEAD` | the branch's ref and reflog, if it moved |
| `reset --soft` | `logs/HEAD` | the branch's ref and reflog |
| `fetch`, nothing new | `FETCH_HEAD`, ignored | `origin/HEAD` |
| `update-ref` on the base | nothing | its ref and reflog |

So a repository takes 2 watches per worktree, on its git directory and its `logs`, plus 3: `worktrees` and the directories of the base's ref and reflog. Both are watched because git writes the reflog before it moves the ref, seen with `strace` on git 2.53.0, so a read on the reflog alone can find the old value, and only the ref's move says to read again. The ref alone is not enough either: once packed it may have no directory to watch. The watches also hold through a read, so a base that moves during one fires another. The main checkout's base is on `origin`, so its reflog is under `logs/refs/remotes/origin`, which is to be measured when built. A repository in the reftable format has none of these files: it gets the other events only, and the log says so.

### How it fits the daemon

No entry point calls anything new. The daemon's `notices` already says that something changed, from every API request that changes anything, from `terminal.Store.OnChange` and from an agent's status arriving from its holder; a stream then reads its project again and sends it if it differs. The tracker (proposed name: `worktree.Tracker`) does the same.

```
 API requests, store changes,       git run by anyone,        the user
 an agent's status                  anywhere                  in tide
        │                               │                        │
   notices.notify()                 file watch            POST refresh
        │                               │                        │
 agents compared with last seen         │                        │
        └───────────────────────────────┼────────────────────────┘
                                        ▼
                     tracker marks the worktree out of date
                                        ▼
   settle; wait while index.lock exists; up to 4 worktrees read at once
                                        ▼
                          read-only git; result kept in memory
                                        ▼  only if it differs
                                notices.notify()
                                        ▼
                  each stream reads the project, with what is kept, as today
                                        ▼
                                      tide
```

- A stream never runs git: it reads what the tracker keeps, so a notice from a keystroke costs no git.
- The tracker works for a project only while a stream follows it, counted across clients. tide follows only the project shown. When the last leaves, its watches and what it keeps are dropped.
- It cannot loop: its reads write nothing, so they fire no watch, and its own notice finds no agent changed.
- A request for a list or a diff, from `wdx worktree` or a diff tab, reads git itself, with the same read-only function.
- A stream asks with `?worktrees=true`, as tide does: a client that does not ask costs no git.
- `POST /v1/projects/{project}/worktrees/refresh` answers at once and, like a reload, does not wait for the daemon's other changes, so that a clone taking minutes does not hold it up.
- Waiting on `index.lock` is a courtesy, since a read is safe beside it: the lock's end fires the watch. A refresh the user asked for does not wait, so a lock left by a crashed git cannot hide changes for good.
- With no file watch available, as on some network file systems, the other events still work, and the log says so.
- Up to 4 worktrees are read at once, and each is told of as it is read; on a project's first read the list is told of first, each worktree `unread` and shown `reading…` until its turn. Changed on 2026-11-02 from one at a time, for large repositories: docs/LARGE_REPOS.md.
- The watches use `fsnotify`, under a BSD license.

### The lock rule

The daemon's background git must never write to a repository. It never takes `index.lock`, the lock that makes an agent's or the user's `git add` or `commit` fail with "Unable to create index.lock". It never runs `fetch`, `gc`, `worktree prune` or `worktree repair`.

Measured on git 2.53.0, with file timestamps changed so that the index was out of date:

| Command | Index |
|---|---|
| `GIT_OPTIONAL_LOCKS=1 git status` | rewritten |
| `GIT_OPTIONAL_LOCKS=0 git status` | untouched |
| `GIT_OPTIONAL_LOCKS=0 git diff HEAD` | **rewritten** |
| `GIT_OPTIONAL_LOCKS=0 git -c diff.autoRefreshIndex=false diff HEAD` | untouched |
| `GIT_OPTIONAL_LOCKS=0 git diff-index HEAD` | untouched |
| `GIT_OPTIONAL_LOCKS=0 git diff base...HEAD`, `rev-list`, `merge-base`, `show`, `worktree list`, `for-each-ref` | untouched |

`strace` of the safe `git status` and `git diff HEAD` showed neither creating any file. Then `reset --hard`, `pull`, `fetch`, `add` and `rm --cached` ran for 20 seconds beside reads in a tight loop, on a repository of 3,000 files:

| Reads beside them | Succeeded | Failed on a lock |
|---|---|---|
| none | 1975 | 0 |
| plain `git status`, `git diff HEAD` | 1417 | 47 |
| with both settings | 1520 | 0 |

That is a small repository, not a monorepo. A reader that creates no lock file cannot cause a lock failure whatever the size, but its CPU and disk load does grow with the repository.

So:

- `--no-optional-locks` or `GIT_OPTIONAL_LOCKS=0` alone is not enough: `git diff HEAD` refreshes the index regardless.
- The human's condition for all of this (2026-11-01): it must not disturb git commands run by a human or another agent.
- Reads go through one function of `internal/git` that sets `GIT_OPTIONAL_LOCKS=0`, `diff.autoRefreshIndex=false`, `core.fsmonitor=false` and `GIT_NO_LAZY_FETCH=1` on every command, so that a new read cannot forget them and no read starts a daemon or downloads anything.
- In a blobless partial clone, measured, `status` and `diff HEAD` changed nothing in `.git`, but `git diff base...HEAD` and `git show` of a missing file downloaded objects and changed 4 files there. With `GIT_NO_LAZY_FETCH=1` they fail instead and change nothing. So a worktree row whose `+ −` cannot be worked out shows only `↑3`.
- `core.untrackedCache=true` changed nothing in `.git` under the same settings. `core.fsmonitor` could not be measured: this Linux build of git has no fsmonitor daemon. The setting is there so that it need not be.
- A read that fails changes nothing: it is logged, and the next event tries again. One plain `git diff` died with a bus error, once, beside `reset` and `pull` in the contention test; it did not affect the other commands, and the cause is not known.
- A test makes the index out of date, runs each read, and checks that the index file did not change. It is seen failing without the settings.
- With an out-of-date index, git reads the files to compare them, each round, until something else refreshes the index. That is the price of never writing.

## The API and the CLI

- `GET /v1/worktrees` with `project` or `dir`, and `wdx worktree list`: every checkout, read now.
- `GET /v1/worktrees/{worktree}/diff`, and `wdx worktree diff --name`: the PR view's patch.
- `GET /v1/worktrees/{worktree}/files/diff?path=`, and `wdx worktree diff --name --path`: a file's patch with every line, which tide lays out in two columns.
- A name is `main` for the clone, else the last part of the path, with a number if that is taken, so that a name is one checkout.
- A path outside the worktree is `invalid_path`.

## A worktree's tab

`Enter` on a worktree opens one tab with two views, called by what it shows now:

- **`uncommitted: <name>`** while the worktree has anything not committed: every file's changes, staged, not staged and untracked, as one patch.
- **`pr: <name>`** once all is committed: its commits against the base. The tab moves there by itself, since nothing is left to show.
- `Tab` flips between them while both have something to show. Having turned to the commits, it stays there until flipped back, or there is nothing uncommitted left.
- Each view keeps its own place. Only the view shown is read.

### Which agent is in a worktree

An agent's `dir` is where it starts, and an agent can move, as into a worktree it made itself. So `current_dir` says where its AI CLI last said it worked: for Claude Code, the last `cwd` in its transcript, which `BackUp` reads as it goes through the new lines. It is only known when the backup runs, as when the agent goes idle or waiting, and is a best guess for one that works in several. An agent's row and tide's list go by `current_dir` when that is in a worktree, else `dir`. Other AI CLIs that do not say stay where they started.

So nothing depends on it: `C` and `s` offer every running agent of the project, those in the worktree first, then the one last sent to from it. What they send says where the worktree is, as the agent may be elsewhere. They refuse only with no agent running in the project.

`C`, on the uncommitted view, asks the agent to commit:

- A box opens with `Commit all uncommitted changes in <path>`, which can be edited. `Enter` sends it to the agent's inbox, and `Esc` cancels.
- The agent writes the message and follows the repository's rules, so wdx commits nothing itself. The tab follows by itself: a commit fires the watch.
- With several agents, `←` `→` choose.
- It warns when the agent chosen is working, as a commit then may take in unfinished work, and lets it be sent.
- A send that fails leaves the box open, with the text and why.

## Verified

`v` marks a file verified, and pressed again takes the mark away: in a file's diff, in a worktree's tab for the file of the current line, and on a file's row in the list. It is only a label, which committing and landing do not depend on. A `✓` shows it on the file's row and its diff, and `2/5 ✓` on the worktree's row and bar, counting what the tab shows.

Nothing watches for a file to change. A mark holds the id of the content that was on the screen, taken from the patch, in the table `verified_files`. Each read of a worktree, on the events above, compares it with `git hash-object`, which stores nothing, for marked files alone:

- A file that differs loses its mark, for good: going back to the same content does not bring it back.
- That id stays the same once committed, so a commit of what was verified keeps the mark.
- An edit between reading a diff and pressing `v` has an id that differs, so the mark goes at the next read: it cannot claim what was not seen.
- A file left in neither list, as when landed, or a worktree git no longer lists, loses its marks.
- A mark can outlast an edit until the next event: one by an agent shows when it stops or the user looks, as the file's row does.
- A symbolic link or a submodule keeps its mark, as it has no such content to compare.

## Comments and reviews

`c` comments on the current line, `e` and `d` edit and delete the comment on it, `Esc` cancels and `Enter` saves. A comment shows under its line, and on the worktree's row as `3 comments`. It belongs to the diff it was written on: uncommitted, which a file's diff is too, or committed, as their line numbers differ.

`s`, with comments to send, sends all of the worktree's together, with a note if any, so that an agent does not start acting on the first. With several agents `←` and `→` choose; with none in the project it refuses and they stay. `C` warns of comments not sent.

- Comments are in the table `review_comments`, so they survive tide and the daemon.
- A comment holds the line as it was seen, which the message quotes, so the agent can find it after the numbers moved. When the file changes it stays, saying `file changed`.
- A review follows its message (docs/SEND.md): `sent`, then `delivered` once typed in. If its message goes undelivered, as with an agent that starts a new conversation or is removed, its comments are drafts again.
- Once sent, it no longer shows in the diffs: the agent is about to change that code. It stays in the table, and `wdx worktree comments` lists it.
- A comment on a line no longer in the diff is counted above the lines, and `s` still sends it.
- A comment is one paragraph, as `Enter` saves it.
- Comments of a worktree git no longer lists go with it.

## Moving around a diff

The arrows move a current line, marked `▸`, and the view follows it.

| Key | Does | File diff | Worktree diff |
|---|---|---|---|
| `↑` `↓` | the line above or below | yes | yes |
| `PgUp` `PgDn`, `b` `Space` | a page up or down, for keyboards without the first two | yes | yes |
| `g` `G` | the first or last line | yes | yes |
| `[` `]` | the change before or after | yes | yes, by hunk |
| `{` `}` | the file before or after | | yes |
| `:` and a number | that line of the file, or the nearest | yes | |
| `/` and text | search, with no regular expressions; capitals make it exact | yes | yes |
| `n` `N` | the next or last match, going round | yes | yes |
| `Esc` | ends the search | yes | yes |
| `v` | marks the file verified, or not | yes | yes |
| `c` `e` `d` | comment on the current line; edit or delete the comment on it | yes | yes |
| `s` | sends the comments, together | yes | yes |
| `?` | shows these keys | yes | yes |

A click makes a line current, and the wheel scrolls with the current line staying in view.

A bar of shortcuts is at the bottom of a diff's tab, and only there, since `n` and `p` went unnoticed in the status line's hint. It lists only what can act now, and says where the current line is: `change 2 of 5 · line 120 of 900`, or `· file 1 of 3`. What does not fit goes, the least needed first, and `?` stays.

## Landing and removing: agreed with the human (2026-11-01)

"Proposed" marks what came from the AI and the human let stand. Three commits: removing, landing by rebase with its check, and landing by PR.

Landing and removing are the one place wdx writes to a repository. They run only when asked for, so they take git's locks like any command the user types, and the lock rule above still holds for everything that runs by itself. wdx never stashes and never uses `--force`.

### Landing

`L` is in the bar of a worktree's tab while it shows the commits against the base, with nothing uncommitted and something ahead; `commit first` otherwise. A file diff has no `L`, and the main checkout has none. wdx does the git steps. An agent is only asked to rebase.

`L` is refused while an agent in the worktree is working, as a rebase would change files under it. An idle or waiting agent does not stop it. Which agent is in a worktree is the best guess described above.

If the base has moved on, wdx first tries the rebase itself, as part of landing (agreed). A conflict aborts it, leaves everything as it was, lists the files and offers to send the rebase to an agent: a box like `C`'s, with `Rebase <branch> onto <base> in <path>`, the agent chosen as for `C`. Once it is done and the agent has stopped, `L` starts again; nothing lands by itself.

Proposed:

- `L` opens a box with the method, the commits and lines, how many files are verified and how many comments are not sent, for information only. Nothing depends on those.
- A base that is not a local branch, as `origin/main` with no `main`, cannot land by rebase: only by PR.
- The first `L` in a project asks for its method, check command and whether to push, saved in the table `project_landing`, and `m` in the box changes them. `wdx project landing --check <command>`, or `--no-check`, with `--push` or `--method` if wanted, does the same, and `wdx worktree land` refuses with `land_not_configured` until one of them has.
- The whole of a landing runs in the background, the rebase too, so that a long one does not hold up the daemon's other changes. It is kept in memory, and the worktree's `landing` says how far it got and how it ended. A daemon restarted forgets one running, so nothing lands that nobody is there to see; its check runs on in its terminal, which the next `L` replaces once it has ended.
- A rebase, merge, cherry-pick, revert or bisect in progress in the worktree refuses `L` with `worktree_busy`, so that aborting a stopped rebase can never be an agent's or a person's own.

The `rebase` method, once the worktree sits on the base:

1. Runs the check, if there is one, through `sh -c` in a wdx terminal named `land-<name>`, since it can take many minutes. Its exit code decides, and the worktree's row says `landing: check`. One that passes takes its terminal with it; one that fails stays to read, until the next landing replaces it. A terminal of that name still running is never touched: the check fails, saying so.
2. Moves the base to the commit that was checked, not to whatever the branch holds by then, with no merge commit. `git merge --ff-only` in the checkout that has the base, `git update-ref` with the old value when none has it. Local changes that overlap make git refuse, and nothing has moved.
3. If the base moved during the check, it stops and says so; `L` starts again, with a rebase.
4. Pushes only if the project asked for that.
5. Offers to remove the worktree, in the tide that started the landing.

An agent working in the base's checkout gets a warning, not a refusal: the fast-forward keeps its uncommitted changes, and an agent always working there would block landing for good.

The `pr` method pushes the branch and runs `gh pr create`; `gh` becomes an optional program in `wdx deps`. The row shows `PR #12 open`. `gh pr view` says when it merged, as a squash merge leaves the commits looking unlanded, and it runs only on `f` or when the tab opens, since nothing that runs by itself uses the network.

Proposed, for the `pr` method:

1. It does not rebase. A branch already pushed could then only go up with `--force`, which wdx never uses, and GitHub shows a conflict on the PR itself. Nor does a moved base stop it.
2. It runs the check first, as for `rebase`, since that is what the setting says: a project that leaves this to CI sets none.
3. `gh pr view` comes before anything is pushed, so that a repository `gh` cannot work with fails with nothing changed. A PR already open only gets the new commits, by a plain push; one that merged with the commit the worktree is on fails with `pr_failed`, as nothing is left to do; otherwise `gh pr create --fill --base <base> --head <branch>` opens one, its title and text from the commits.
4. A push refused, as when the branch on origin has moved, fails with `push_failed`; `gh` refusing with `pr_failed`, with what it said. Nothing is forced and nothing is retried.
5. It ends in `opened`, which is not `landed`: the worktree is not offered for removal until the PR has merged.

The method is the project's, and `wdx worktree land --method` overrides it once, without saving. `no_local_base` still refuses `rebase`, and `pr` too when a worktree has no base at all. A detached worktree has no branch to push: `detached_head`. No `gh` on the machine refuses at the start with `dependency_missing`.

What `gh` last said of a worktree's PR is kept in the table `worktree_pull_requests`, so that a row keeps saying it after a restart. `f` and opening a worktree's tab ask, in the background and one project at a time, for each worktree with commits ahead or a PR known, which also finds a PR opened some other way. Focus coming back to the left pane reads git, as before, but never asks `gh`.

Removing asks `gh` itself when commits are still ahead, as the request is the user's and a script has no tab to open. A PR counts as landed only if it merged with the very commit the worktree is on and nothing is uncommitted: a branch name used again would otherwise find an old PR that merged, and a commit made since would go with the directory. A base that a PR's "Update branch" moved has a different head, so it is refused until the worktree has it.

### Removing

`C-q x` on a worktree's row, and `wdx worktree remove --name`. Only a landed worktree can be removed: nothing ahead of its base and nothing uncommitted, or a PR that `gh` says merged. Anything else, including a worktree whose base or commits are not known, is refused, and the main checkout too. So a worktree given up on stays until removed with `git worktree remove --force` by hand (agreed): deleting commits is the mistake that cannot be undone.

Removing is not held up by what runs there:

- Each running agent in the worktree gets a message through its inbox (docs/SEND.md), saying the worktree is gone and to carry on in the project's clone. It goes once git has removed it, so that a refusal by git never sends an agent away from a worktree still there. An agent that cannot be told is logged and fails nothing.
- The confirmation lists those agents and the terminals there. Terminals are left running, in a directory that no longer exists.
- An agent whose `dir` is the worktree keeps it, so `agent restart`, when built, has to cope with a directory that is gone.

Then, proposed:

- `git worktree remove`, without `--force`, run from the repository's git directory, so a file written since the check makes git refuse. Ignored files such as `.env` go with the directory: git does not count them.
- The branch goes with `git branch -d`, which refuses what is not merged (agreed), so a commit made since the check keeps it. A squash-merged branch is kept too, as is the default branch, and the result says so. git judges "merged" by the clone's `HEAD`, so a clone on another branch can keep it as well.
- A worktree in the project's records but listed by no repository only loses that record. wdx's records of a removed worktree go in any case, as otherwise it would come back as `missing`.
- A `missing` worktree can always be cleared: only git's entry goes, as its files are already gone, and the branch follows the same rule. This replaces the note in the left pane section.
- Verified marks and comments go by the existing rule for a worktree git no longer lists.

### The CLI and the API

Proposed:

- `wdx worktree land --name`, which waits for the end and returns `landed`, or a code. Before it starts: `land_not_configured`, `agent_working`, `not_committed`, `nothing_to_land`, `no_local_base`, `landing_running`, `worktree_busy`, `worktree_is_clone`. After: `rebase_failed`, `check_failed`, `base_moved`, `fast_forward_failed`. Leaving does not stop it. `--method pr` returns `pr_opened` with the PR, or also `dependency_missing`, `detached_head`, `push_failed`, `pr_failed`.
- `wdx worktree remove --name`, which returns `removed` with what became of the branch and who was told, or `worktree_not_landed`, `worktree_is_clone`, `worktree_not_removed`. It asks nothing, as only what landed can go.
- `POST /v1/worktrees/{worktree}/land` starts it and returns, so that a long check does not hold up the daemon's other changes; `DELETE /v1/worktrees/{worktree}` removes.
