# Large repositories

How fast tide shows a large repository's worktrees and files, and what makes it slow. Tier 1 below, agreed with the human and built. Tier 2 is research, for later.

The aim is general, not one repository: whatever makes it faster must lose nothing, write nothing to the user's repository and need nothing set up by the user. The lock rule of docs/TIDE_WORKTREES.md holds throughout.

## What was measured (2026-11-02)

On Linux, git 2.53.0, 8 cores, with wdx's read settings (`GIT_OPTIONAL_LOCKS=0`, `core.fsmonitor=false`, `diff.autoRefreshIndex=false`). Clean checkouts; each read run 2 or 3 times.

| | linux | llvm-project |
|---|---|---|
| files | 97,348 | 189,401 |
| `status --untracked-files=all` | 0.38–0.64 s | 0.78–0.89 s |
| the same with `--untracked-files=no` | 0.28–0.35 s | |
| `ls-files` (the index only) | 53–89 ms | 99–156 ms |
| `ls-files -co --exclude-standard` | 0.29–0.42 s | 0.62–0.78 s |
| `ls-files -o --directory --exclude-standard` | 0.25–0.37 s | 0.59–0.69 s |
| `diff base...HEAD`, `worktree list` | 15–18 ms | |
| the files request, 4.1 MB and 10.4 MB of JSON | 0.67 s | 1.19 s |

- **A stale index** (files touched, their content unchanged: 38K files on linux, 36K on llvm): `status` took 10.6–16.1 s and 7.4–10.1 s, on every read, as our reads may not write the refreshed index back. A user's `git status` paid it once (12.4 s, 7.4 s) and wrote it back, and the reads were fast again. Builds, code generators, formatters and tools that rewrite files unchanged make an index stale.
- **The untracked cache** changed nothing, even filled in the index: `status` and `ls-files` took the same with and without it.
- **Reading at once**, `status` on llvm (0.83 s alone): 2 at once took 1.1 s, 4 took 1.3 s, 8 took 2.0 s, against 1.7, 3.3 and 6.6 s one after another. On one checkout, its files in the page cache: separate worktrees gain less on their first read.
- **tide**, on linux's 97K paths: decoding the answer 147 ms and matching the filter 46–246 ms, both off the UI loop; building the tree **135 ms on the UI loop**, so tide does not answer keys meanwhile.
- **A repository at work** (macOS, 387,474 files, no fsmonitor): `git status` takes about 5 s. Its worktrees were read one after the other, so tide took visibly long to show them, and the Code section too.

## Tier 1: agreed with the human (2026-11-02)

Agreed as below: no limit on the file list (next section), `unread` with `reading…`, and 4 reads at once as a constant, since settings the user must understand cost more than they give here.

### 1. Worktrees read at once

Today the tracker (`tracked.read`) and `worktree.Read` (`GET /v1/worktrees`, the CLI's `worktree list`) read each worktree after the last, and the stream tells of none until all are read: 10 worktrees of a repository whose `status` takes 5 s show after about 50 s.

- Both read up to **4 worktrees at once**, a constant (`parallelReads`), not a setting: 4 gained 2.5 times on 8 cores, and more crowd the user's own git and builds.
- The tracker tells of each worktree as it is read, not once all are. The first read of a project first tells of the list, with each worktree **not read yet**; the rows then fill in one by one.
- The API's `WorktreeStatus` gains `unread`: `true` until its files and commits were first read, its counts then empty. An addition, so nothing breaks. `worktree list` and `GET /v1/worktrees` read all before answering, so for them it is always `false`. Named so while building, as `read` was agreed: false is what every status built elsewhere has, so the name says what is the exception.
- tide shows a worktree not read yet with `reading…` in place of its counts. It can be selected and its Code section opened meanwhile.
- They are read in the list's order, `main` first.
- What a read does to one worktree is unchanged: the same git commands, the put-off when its index is locked, the marks and comments that follow. Only that several run at once, each writing its own `Status`.
- Each worktree read is logged at info with its `duration_ms`, so a slow repository shows where the time goes without debug logs.

### 2. The Code section's files with one walk instead of two

Today `ListFiles` runs `ls-files -co --exclude-standard` (a walk of the whole checkout, plus the index) and then `ls-files -o --directory --exclude-standard` (a second walk) for the empty directories.

Instead, three reads give the same list:

1. `ls-files -z`: the tracked files, from the index alone (0.1 s on llvm).
2. `ls-files -z -o --directory --exclude-standard`: the one walk. It lists each untracked file in a tracked directory, and each directory holding nothing tracked as one entry ending in `/`, whether it holds untracked files, nothing, or a repository of its own.
3. Only when 2 listed such directories: `ls-files -z -o --exclude-standard -- <those directories>`, a walk of those directories alone, for the untracked files in them. Many directories are passed in groups, within the length a command line allows.

The paths are 1 and 3 and the files of 2, sorted. An entry of 2 holding no files in 3, and no `.git`, is an empty directory, as today. On llvm, git's part goes from about 1.3 s to about 0.75 s.

A test on a repository with each case compares the two ways: a tracked file deleted from disk, an untracked file in a tracked directory, an untracked directory with files several levels down, an empty directory, an ignored file, a repository inside, a file in conflict.

### 3. tide builds the tree off its UI loop

- The tree is built in the command that asks for the files, not when its answer arrives, so the UI loop only swaps it in.
- That command gets the list it replaces; when the new one is the same, it builds nothing, and the folds and the cursor stay as they are.

### The CLI and tide

`worktree list` and `GET /v1/worktrees` gain the parallel reads and `unread`, always `false` there. The Code section's list has no CLI command (docs/CLI_TIDE.md). docs/CLI_TIDE.md gets no new row: nothing either can do changes.

## Found while measuring: the read limit holds nothing

`internal/git`'s reads keep at most 8 MiB of git's output (`maxPatch`) and say when they cut it. They do not: `boundedBuffer` embeds `bytes.Buffer`, whose `ReadFrom` `io.Copy` calls in place of `Write`, so the whole output is kept. llvm's file list is 9.98 MB and came back whole, `truncated` false. The test of the limit calls `Write` itself and so cannot see it. Every read is affected: diffs too.

Fixing it alone would cut llvm's file list at 8 MiB and show `too many files`.

Agreed with the human: the file list has no limit, as a repository of 387K files did load whole, only slowly. The fix makes the limit hold for every other read, diffs among them, and the three reads of the file list keep all git says. `WorktreeFiles.truncated` stays in the API, always `false`, so that nothing breaks; tide's `too many files` goes. Its own commit, a `fix:`, before tier 1.

## Tier 2: research, not agreed

To be prototyped and measured before any design:

- **A private index for our reads**: the daemon keeps a copy of each worktree's index in wdx's home, refreshes it and reads with `GIT_INDEX_FILE`, so a stale index costs one refresh, not every read, and the user's index and its lock are never touched.
- **wdx as the fsmonitor of its own reads**: `-c core.fsmonitor=<wdx's hook>` on our reads only, the daemon watching the worktree's files and telling git what changed. Its token lives in the private index. One stream per worktree on macOS; on Linux one watch per directory, within what the system allows (65,536 here; linux has about 6K directories, llvm 15K), and none past it.

## Tier 1 as built (2026-11-02)

In the order of its commits:

1. **The read limit.** `boundedBuffer` keeps its buffer as a field, so `io.Copy` goes through `Write`; `readWhole` reads with no limit, for the file list. `TestReadKeepsUpToItsRoom` reads through a real git, where the old buffer kept everything.
2. **One walk.** `git.ListFiles` returns the files and the directories shown whole; `UntrackedDirs` is gone. `TestListFilesAsTwoWalks` compares it with the two walks on every case above, and with a name that is pathspec magic, `*:(magic`, which `GIT_LITERAL_PATHSPECS` keeps literal. The directories are given to git in groups of up to 64 KiB.
3. **At once.** `atOnce` in `internal/worktree` runs the reads of the tracker and of `Read`, up to `parallelReads`. The tracker tells of each checkout as it is read, and first of the list when any is `Unread`; one put off for `index.lock` stays `unread` until read. `worktree read` is logged at info with `duration_ms`. `TestTrackerReadsCheckoutsAtOnce` holds the clone's read open and sees the worktree read meanwhile.
4. **Off the UI loop.** `readCodeFiles` builds the tree in its command, from `newCodeLoaded`, and builds nothing for a list like the one shown, which keeps the tree and what the filter matched.
