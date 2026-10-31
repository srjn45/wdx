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
| 2026-10-31 | `TestOpenGitCloneKilled` | local, loaded machine | once in `make test`; 3 of 6 in `make test-stress`; 191 of 300 at `-cpu=1`; 0 of 300 at the default CPUs | product race | yes: cancelled after the clone is moved into place but before it is registered, `cloneAndOpen` returns an error and leaves the checkout, with no project | 18d0933: a cancel is not heeded once the clone is in place; the test holds the clone under way with a remote that never answers, so it no longer skips |
| 2026-10-31 | `TestNewTerminal` | local, `make test-stress`, loaded machine | 1 of 6 twice; hung until the deadline, 58 min once | product race | yes: when the answer to `C-q t` came before the stream brought the tab, tide lost the selection and stayed on the old terminal | 9923373 |
| 2026-10-31 | `TestTide` | local, `make test-stress`, loaded machine | 5 of 120 | check too early | no: the daemon logs the end of a stream only after its client has gone, and the test read the log once when the command returned | 4b7873d: it waits for the request to be logged, and fails at its deadline if it never is |
| 2026-10-31 | `TestTerminalSendDropped` | local, `make test-changed`, loaded machine | once, in the first run of the new test | product race | yes: messages dropped when the shell ended were logged by the goroutine writing to the PTY, which the holder could outlive, so a message could be lost with no trace | in the same change as the test: the holder logs them as it handles the shell's end, before the daemon can release it |
| 2026-10-31 | `TestProjectAndDaemonWorkSeesBothKinds` | local, never failed: took 10 s or 60 s instead of 0.04 s | 8 of 14 | product race | yes: a stop sent as the connection to the holder dropped was lost, so stopping waited out the grace and killed the shell instead of hanging up | 79f8585: the watcher asks again on the connection it dials |
| 2026-10-31 | `TestUpdateInterrupted` | local, a run of the `cli` tests alone, repeated, on a loaded machine | 1 of 6, likely 1 more whose output was lost | step raced | no: the signal came after the 140 ms switch had ended, as the test read the line late, so there was nothing to hold | 4b7873d: a request in flight holds the switch open until the signal is said to be held |
| 2026-10-31 | `TestTerminalAttachTitle` | local, `make test-stress`, loaded machine | 1 of 10 runs of it | check too early | no: attach had written the title back, which the test read off the terminal only later | 4b7873d: it waits for those bytes |

## Patterns

- **Kill order:** a test kills processes in an order that lets another outcome come first. Kill so that only the wanted state can come. 2 flakes, none a bug in wdx.
- **Shared process:** parallel tests share a process's environment, working directory and open files, as with `text file busy` (golang.org/issue/22315). 1 flake.
- **Check too early:** a test reads, once, what another process writes only later. Wait for the state instead, keeping the check as strict. 2 flakes, not bugs in wdx.
- **Step raced:** a test acts during a step of another process that it does not hold, which a loaded machine lets end first. Hold the step open until the test has acted. 1 flake, not a bug in wdx.
- **Product race:** the code itself races or gives up after a fixed time, and a loaded machine shows it. 6 flakes, all bugs in wdx. Suspect this first when a test fails only under load.
