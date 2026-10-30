# Flaky tests

Every test seen failing now and then, so that patterns show over time. AGENTS.md says how tests stay deterministic; this file says where they have not.

## Adding a row

Add a row when a flake is seen, in the same change as its fix if there is one. A failure nobody can explain yet gets a row too, with `open` as its fix: its repeats are what show the pattern. When a test flakes again, update its row rather than adding another, unless the cause is a different one.

Never answer a flake by retrying the test: half of those below were bugs in wdx.

- **Seen:** where it failed: CI, the nightly stress job, a local run, and whether the machine was loaded.
- **How often:** the failures and runs known, such as 3 of 24.
- **Pattern:** one from the list below, or a new one added to it.
- **Bug in wdx:** whether users could hit it, not only the test.
- **Fix:** the commit, or `open`.

## The flakes

| First seen | Test | Seen | How often | Pattern | Bug in wdx | Fix |
|---|---|---|---|---|---|---|
| 2026-10-29 | `TestLost` | not recorded | once | kill order | no | b6d8c47 |
| 2026-10-29 | `TestOutputDrained` | review, loaded machine | once under load; 0 of 20 alone | product race | yes: `hello` waited for the vt, so under a flood a running terminal could be marked lost | 5d81ea7 |
| 2026-10-29 | `attach_test.go`, never failed by itself | review, a throwaway test | 1 of 30; 4 of 15 with `sleep 30 & exit 4` | product race | yes: a client could get `connection_lost` instead of `exited` | d6fe3bf |
| 2026-10-30 | `TestSweepSockets` | CI | not recorded | kill order | no | c6ac4c0 |
| 2026-10-30 | `TestUpdateUndoneInstall` | CI | not recorded | shared process | unclear: the fix retries in `wdx update` itself, but only parallel tests were seen to cause it | f53768f |
| 2026-10-30 | `TestHistoryPane` | local, loaded machine | 3 of 24 | product race | yes: output printed just before an exit was lost from history | 3940847 |

## Patterns

- **Kill order:** a test kills processes in an order that lets another outcome come first. Kill so that only the wanted state can come. 2 flakes, none a bug in wdx.
- **Shared process:** parallel tests share a process's environment, working directory and open files, as with `text file busy` (golang.org/issue/22315). 1 flake.
- **Product race:** the code itself races or gives up after a fixed time, and a loaded machine shows it. 3 flakes, all bugs in wdx. Suspect this first when a test fails only under load.
