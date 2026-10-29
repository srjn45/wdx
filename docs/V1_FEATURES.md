# warden v1 feature inventory (rebuild backlog)

Every capability of the old warden (`~/dev/warden`), collected from its
`FEATURES.md`, `docs/FEATURES.md` and package list. This is a **backlog, not a
spec**: pick features one at a time when they are actually needed, redesign
them for the rebuild, and tick them off here.

**Priority hints:**
- **P0**: foundation, needed before anything else works
- **P1**: core daily use
- **P2**: valuable later
- **?**: reconsider whether it's needed at all

Status: `[ ]` not started · `[~]` in progress · `[x]` done · `[-]` dropped

---

## 1. Foundation (daemon, sessions, PTY)

- [x] P0 Daemon running as a local service (systemd user unit / launchd): see docs/DAEMON.md
- [x] P0 Local API over a Unix socket, with client/daemon version handshake (the CLI warns when the versions differ)
- [x] P0 PTY holder process per session: survives daemon restart, update and crash; authenticated re-adoption (terminals for now; docs/TERMINALS.md)
- [~] P0 Attach / detach a live session (native terminal attach, with screen snapshot on attach): terminals, full-window as tmux's, with their whole history to scroll through (docs/ATTACH.md, docs/ATTACH_VIEW.md); agents later
- [x] P0 Plain terminal sessions (`kind=terminal`, `$SHELL`, no AI CLI): `wdx terminal`, `wdx terminal attach` and `history`
- [~] P0 Session output tail / recent output: `wdx terminal history` prints what scrolled off a terminal's screen; still to do, the screen of a running terminal after it, so that the latest output is in it too (history gets the screen only when the shell exits)
- [ ] P0 Send text into a session
- [ ] P1 PTY observability: runtime status, `warden_pty_*` metrics, decision trace
- [~] P1 Bounded child cleanup (TERM → KILL grace, process-group kill, reap timeouts): terminals are hung up on, then killed with their process groups after 5 s
- [ ] P1 PTY event journal and stream

## 2. Projects

- [ ] P0 Projects as first-class entities (git repo, local path, open / close / new / list)
- [ ] P1 Zero-touch auto-registration (spawning in a directory registers or reopens its project)
- [~] P1 Close a project → hibernate members; reopen → restore them (no auto-spawn): terminals stop on close and start fresh shells on reopen
- [ ] P2 Project groups (named collections shown in the TUI tree)
- [ ] ? Open remote projects

## 3. Agent lifecycle

- [ ] P0 Spawn an agent (prompt / ticket / typed)
- [ ] P0 List agents; status and detail; live states (`pending`, `busy`, `idle`, `need-input`, `done`, `orphaned`, `rate_limited`)
- [ ] P0 Terminate / tear down (umbrella verb)
- [ ] P0 Delete / hard-purge
- [ ] P1 Restore an orphaned agent; recover orphaned agents with live panes
- [ ] P1 Agent naming (auto-generated: role conventions plus adjective-noun fallback) and rename
- [ ] P1 Tags (group / filter)
- [ ] P1 Model selection at spawn
- [ ] P1 Parent/child agent trees (`parent_id`, `child_agents`, `child_pipelines`)
- [ ] P1 Adopt an existing CLI session (e.g. a running Claude session)
- [ ] P1 Handoff: delegate to a new or existing agent, or retire self (self-rotation)
- [ ] P2 Fork an agent's session sideways
- [ ] P2 Roles: built-in persona catalog plus default flags; switch role on a running agent (relaunch re-injects)
- [ ] P2 Digest: catch-up summary of an agent
- [ ] P2 Live 3–5 word activity badge per agent (needs a cheap model)
- [ ] P2 Agent history and archive (browse and search ended agents)
- [ ] P2 Full-text search across agents
- [ ] P2 Export / import session metadata

## 4. Backends (AI CLI adapters)

- [ ] P0 Neutral backend contract (launch, resume, state detection, prompt injection, capabilities)
- [ ] P0 Claude Code adapter
- [ ] P1 Codex adapter
- [ ] P1 Antigravity / Gemini adapter
- [ ] P1 Cursor adapter
- [ ] P2 Aider, OpenCode, Crush and Goose adapters
- [ ] P1 Backend registry: detect installed CLIs, rescan, enable / disable, default backend
- [ ] P1 Generic, provider-neutral state classifier (idle / busy / awaiting approval)
- [ ] P2 Live model menu per backend; model-catalog policy (preview / apply / history / rollback)
- [ ] P2 Billing tiers per backend (free / subscription / pay-per-use) and tiered model routing (`auto` / `tier-1..3`)
- [ ] P2 Read provider quota / rate limits; `rate_limited` state and cooldowns

## 5. Coordination

- [ ] P0 Per-agent inbox: directed messages (send / read)
- [ ] P0 Shared context board: set / get / list / delete / append / compare-and-swap
- [ ] P0 MCP server (stdio shim) exposing coordination and lifecycle tools
- [ ] P0 CLI fallback for every MCP tool (enterprise MCP allowlists)
- [ ] P1 Push delivery of messages (PTY injection when idle; hooks)
- [ ] P1 Wait for a message (park / wake)
- [ ] P1 `/warden` skill for agents (prefer MCP, fall back to the CLI)
- [ ] P2 Peer awareness (grouped orchestrators learn their sibling agents)
- [ ] P2 Project memory (`.warden/memory.md`): show / edit, inject into spawns, auto-curate from digests

## 6. Approvals and permissions

- [ ] P1 Detect approval prompts; list pending approvals; answer without attaching
- [ ] P1 Workspace-trust prompt auto-answer
- [ ] P1 Auto-approve toggle and rule policy (tool / glob / regex / paths, per agent)
- [ ] P1 Identical-prompt circuit breaker (v1 auto-approved the same prompt for hours)
- [ ] P1 Set the permission mode of a running agent
- [ ] P2 Supervised spawn (gate every prompt)
- [ ] P2 Model-assisted recognition of unknown prompts plus learned prompt shapes (list / forget)

## 7. Worktrees and task types

- [ ] P1 Managed git worktree per agent (stored outside the repo)
- [ ] P1 List / prune orphaned worktrees / remove one
- [ ] P1 In-repo opt-out (agent writes directly in the repo)
- [ ] P2 Task types (`development`, `analysis`, `spike`, `pr-review`, `docs`, `debug-ci`, `tests`, …); scratch worktrees for analysis / spike

## 8. Git and check lifecycle

- [ ] P1 Commit (message generated from the diff, paths, amend)
- [ ] P1 Push with protected-branch rails, `--force-with-lease`
- [ ] P1 Sync / rebase onto base (continue / abort; non-zero exit on conflict)
- [ ] P1 Run project checks (compact failure output, works from subdirectories); list checks
- [ ] P1 Open / return the PR for an agent branch
- [ ] P2 Branch and CI status per agent
- [ ] P2 Guard hooks: git-guard (deny raw git mutations), check-guard, root-guard / boundary enforcement
- [ ] P2 Agent-native diff review; machine-readable review findings (neutral JSON)

## 9. Snapshots

- [ ] P2 Snapshot worktree plus transcript; list; restore (with rails)

## 10. Pipelines (DAG of agent jobs)

- [ ] P1 Pipeline spec (YAML): create, validate (offline), list built-in templates
- [ ] P1 Start / pause / resume / cancel / delete
- [ ] P1 Show a pipeline and its jobs (watch mode); show one job
- [ ] P1 Retry a failed job; edit a pending job; emit a job's handoff output
- [ ] P2 Stuck-job automatic brain consult after auto-retry

## 11. Scheduling

- [ ] P2 Schedules (cron / at / now) for agents or pipelines
- [ ] P2 List / show / test-fire / edit / enable / disable / delete
- [ ] P2 Link scheduled runs to sessions (`schedule_id`)

## 12. Plans and Autopilot

- [ ] P2 Plans: create / update / list / show / archive / restore / complete / delete; task status
- [ ] P2 Run a plan (`pipeline` or `autopilot` mode); restart a stuck plan
- [ ] P2 Autopilot topology: manager + workers + on-demand resolver ("brain")
- [ ] P2 Integration branch per plan; land worker branches (idempotent); final PR; complete on merge
- [ ] P2 Guardian heal loop (nudge → restart → rotate → backoff); overwatch backstop; progress watchdog
- [ ] P2 Ownership guard (only the run owner controls its agents); approvals routed to the manager's inbox
- [ ] P2 Cost-tier backend ladder (free → subscription → gated pay-per-use)
- [ ] P2 Run ledger (task state, landings, audit)
- [ ] P2 Brain-assisted task progress assessment
- [ ] ? Hub revision sync; portable plan backup bundles; replica PRs; legacy YAML import

## 13. TUI cockpit

- [ ] P0 Project → agent / terminal tree with live status and backend token
- [ ] P0 Attach / detach a session on the same screen (live pane)
- [ ] P1 Spawn / terminate / delete from the TUI (new-agent form with role / model picker)
- [ ] P1 Inspector (agent / pipeline detail)
- [ ] P1 Approvals cockpit
- [ ] P2 Digest view; pipeline view; plans in the project tree
- [ ] P2 Agent sub-trees (collapse / expand)
- [ ] ? Native tmux window when launched inside tmux

## 14. Web UI

- [ ] P2 Agent grid / fleet header / attention queue
- [ ] P2 In-browser attach terminal (xterm.js over WebSocket)
- [ ] P2 Pipelines tab with live DAG; terminals tab; context and messages; archive
- [ ] P2 Spawn modal; bulk actions; activity feed; keyboard shortcuts
- [x] P2 REST API docs (OpenAPI): `api/openapi.yaml`, the source the server and client are generated from
- [ ] ? Remote access (LAN / Tailscale / Cloudflare tunnel) with bearer tokens (incl. read-only)
- [ ] ? Docker / container deployment

## 15. Observability, cost and notifications

- [x] P0 Structured logs: JSON lines in daily files with retention, a `debug_id` per command shown on errors
- [x] P1 Command metrics: runs, successes and failures per command and error code: the daemon's `wdx.request.duration` over OTLP, shown with the logs in Grafana by `wdx observe`; see docs/OBSERVABILITY.md
- [ ] P1 Traces: a span per request with its git and SQL steps, keeping `debug_id` beside the trace id
- [ ] P1 Action audit log
- [ ] P1 Event stream (for the TUI / web)
- [ ] P1 Health / environment doctor
- [ ] P2 Live resource metrics plus history (per agent and fleet CPU / memory)
- [ ] P2 Memory-pressure spawn gate (`spawn_gate_max_agents`)
- [ ] P2 Context / token guard (gauge, alert, auto-`/compact`)
- [ ] P2 Crash / anomaly detection (OOM, infinite loop)
- [ ] P2 Notifications: desktop / webhook / Slack
- [ ] P2 Spend rollup and budget gate (soft cap on spawn)
- [ ] ? Token-savings ledger
- [ ] ? Fleet insights (deterministic stats plus optional narration)

## 16. Internal "Fast-Brain" (cheap model calls inside warden)

- [ ] ? Latency-bounded, fail-open gateway for small model calls (naming, activity badges, prompt reading, REPL planning)

## 17. Operator UX

- [x] P0 Config file `<home>/config.yaml` (YAML), read at startup; only `WDX_HOME` comes from the environment
- [ ] P1 Live hot-reload of the config file (a bad edit keeps the last good config)
- [x] P1 Shell completion (bash, zsh, fish; installed by the installer): see docs/DAEMON.md
- [ ] P2 Presets (saved spawn options) and prompt templates
- [ ] P2 Plugins: custom task types, lifecycle hooks (pre/post spawn, commit, check, teardown)
- [ ] ? Interactive REPL (`wd repl`) with `/` commands and guided forms
- [ ] ? First-run tutorial
- [ ] ? Install missing dependencies

## 18. Distribution, update and data safety

- [ ] P0 Release pipeline: GoReleaser, static binaries (linux / darwin × amd64 / arm64), checksums, signing, Homebrew tap, install script
- [ ] P0 Stable install layout (`versions/` plus a `current` symlink) and stable binary path for injected agent configs
- [ ] P0 Embedded forward-only migrations, a backup before each migration, and a boot guard (refuse a newer schema)
- [ ] P1 `warden update` (verify signature, atomic swap, restart daemon, re-adopt holders)
- [ ] P1 `warden rollback` (previous binary plus matching DB backup)
- [ ] P1 Version-skew handling (CLI / TUI / MCP shim ↔ daemon; holder protocol N-1)
- [ ] P1 `warden init` (data dir setup); factory reset (scoped)
- [ ] P2 Upgrade / rollback end-to-end test in CI
- [ ] P2 v1 → rebuild import tool (`warden import --from-v1`)
- [-] Waypoint update planner, migration journal, whole-store repair, agent-store offline repair: these existed because of the custom store and are mostly unnecessary with SQLite
