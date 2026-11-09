# Guidance for AI agents

This file applies to every AI working on this repo (Claude, Codex, Cursor, Antigravity, ...).

## Workflow

- Never start coding until the human explicitly says so.
- Brainstorm with the human first: work out the complete solution (structure, tables, commands, names, edge cases) and get it approved before writing any code.
- Work on one feature at a time. Do not add scope the human did not ask for; suggest it instead.
- Question whether each thing is needed before designing it: what problem it solves, for whom, and what happens if it is never built. A feature, setting or option that cannot answer that is left out. The planned direction (docs/IDE.md) is a picture, not a backlog to work through.
- Go slowly and in small steps. The human must understand every part of the product well enough to maintain and debug it without an AI: a quick implementation nobody fully understands is how the previous attempt, warden, became throwaway. A large feature is split into small steps, each designed, built and understood before the next; a step that grows while being built stops for a new design.
- The CLI and tide stay in sync, but not one to one: the CLI also serves scripts and AIs, tide a person at a screen. A feature that touches either is checked against the other in the same design, and is on both unless there is a reason. `docs/CLI_TIDE.md` lists each feature on both sides and why they differ; a change to what either can do updates its row.
- A change a user can notice updates the website's docs (`site/src/content/docs/docs/`) in the same commit: its guide or TIDE page, the page's frontmatter `commands:` for a new command, `since: <next version>` on a page about a new feature or `<Since v="<next version>" />` on a section, the reference pages with `make site-ref`, and `README.md` when its quick start, its AI CLIs or the docs it links change. `TestReferenceCurrent` and `TestGuideCoverage` fail when the reference or a command's page is missed, `TestComponentsImported` when an MDX page uses a component such as `<Since />` without importing it, and the site's `readme.test.js` when the README's install command, quick start or docs links no longer match the site. The TIDE demo is checked against TIDE at each stable release, not per change.
- Commit only when the human asks. Before committing, run `make check && make test` in one shell. CI runs both on every push to `main` and every pull request.
- A test that fails now and then, seen in any run, is fixed then and there, before the work goes on, whether or not the change at hand caused it. It is fixed when a 3x3 passes: three `make test` running at once, three times over, every one of the nine passing. Keep each run's whole output, so that a failure's message is not lost. A 3x3 that fails means the flake is not fixed yet; one that passes means the work goes on, taking the flake as fixed. The row in `docs/FLAKES.md` says what was found and fixed.
- `make test-changed` is a hunt for flaky tests, run by hand now and then when the human asks, never while developing: not on a commit, push or rebase, nor to check a flake's fix, which is the 3x3's job. Name the commits since the last run, as `origin/main..HEAD` is empty once they are pushed, and give it a worktree of its own, since it builds the code again and again and the checkout goes on changing: `git worktree add --detach .worktrees/changed HEAD && (cd .worktrees/changed && nice make test-changed RANGE=<last run>..HEAD); git worktree remove --force .worktrees/changed`. If it fails, finding out why comes before the next feature, with a row in `docs/FLAKES.md`. Never release while one is running or has failed.

## Releases

- Commit messages follow Conventional Commits (`feat:`, `fix:`, `perf:`, `docs:`, `test:`, `refactor:`, `chore:`): the next version and `CHANGELOG.md` are worked out from them, so the type must say what the change is.
- A change a user or script can notice breaks them: the `--json` output, error codes, exit codes, commands and flags, settings, the API. Mark it with `!` (`feat!:`) and say what breaks in a `BREAKING CHANGE:` footer.
- After adding, removing or upgrading a dependency, run `make notices` and commit `THIRD-PARTY-NOTICES.md` with it; never edit it by hand. A dependency must be under a permissive license (Apache-2.0, MIT, BSD, ISC); ask the human before adding any other.
- The website under `site/` has Node dependencies of its own, in `site/package.json` with exact versions; they never reach the binary or `THIRD-PARTY-NOTICES.md`. They follow the same license rule, except the site's fonts (SIL OFL 1.1).
- Never edit `CHANGELOG.md` or move a tag. Release only when the human asks (`make release-dev`, `make release`); see docs/RELEASING.md.

## Code style

- Keep the code clean and readable by a human.
- `make lint` (in `make check`) enforces part of this file: depguard the dependency directions, forbidigo the environment and test rules, sloglint the logging rules, paralleltest `t.Parallel()`. Fix a finding rather than silencing it; a `//nolint` names its linter and says why. Ask the human before turning a linter off in `.golangci.yml`.
- Prefer self-explanatory code over comments. Comment only what the code cannot say: the why, a non-obvious constraint, a workaround.
- Use good names. When a name for a variable, type, function or package is not obvious, discuss it with the human.

## Project layout

warden manages AI CLI agents in PTYs, plus projects, messaging, a TUI and later pipelines. The CLI is `wdx`. The hub, `wdx-hub`, is a second program in the same module, a server that later steps fill with accounts, pages and the nodes' links (docs/HUB_ARCH.md); it builds on the generic packages, never on wdx's own.

```
api/openapi.yaml   the API contract; everything in internal/api is generated from it
api/hub.yaml       the hub's API contract, generated into internal/hubapi; each operation's one tag, web, nodes or admin, says which endpoint serves it
cmd/wdx/        entry point
cmd/wdx-hub/    the hub's entry point: loads hubconfig and runs hubcli
internal/api/   generated types, server interface and client, plus the error codes
internal/hubapi/  the hub's generated types, strict server and client, plus its error codes and headers
internal/clikit/  the framework every wdx program's commands run on, given the program's name, codes and settings through clikit.Env: --json with one object, Problem Details and exit codes, the help layout from the annotations, completions, prompts, the command received/started/returned log lines
internal/clikit/clikittest/  checks a program's commands as the cli's tests do: every run logged from received to returned, every command's help complete, its examples parsed
internal/cli/   wdx's commands on clikit: parse args, call the daemon, print; no business logic
internal/client/   calls the daemon's API over its socket
internal/daemon/   serves the API over the Unix socket on top of the domain
internal/settings/  the settings engine behind a program's table of rows: read from a config file and the environment, every problem with its line, written back from the table, one setting changed under a lock, with secrets shown as [secret]
internal/config/   wdx's settings on internal/settings: its table of rows, loaded once at startup from their sources, and the config file written from them
internal/sqlite/  opens SQLite databases and runs an application's numbered, append-only migrations with the breaks-older rule; backup and restore, random ids, logged statements; knows nothing of wdx's tables
internal/sqlite/sqlitetest/  Seed puts a migrated database at a path, from a copy migrated once per test binary and Open; db/dbtest and the hub's tests use it
internal/db/    wdx's one database on internal/sqlite: its embedded migrations, Open and Known
internal/db/dbtest/  puts a migrated database where a test home's daemon or store opens one, so that each need not migrate its own
internal/git/   thin wrapper over the git CLI
internal/gh/    thin wrapper over GitHub's CLI, which runs only when asked, as it uses the network; gh/ghtest stands in for it in tests
internal/holder/   the process that owns a terminal's PTY and shell (wdx holder), its screen, spool and history, its protocol, clients and inbox; `AttachConn` attaches over the connection it is handed, which local attach dials to the holder's socket and a remote client gets from the host daemon's relay; docs/TERMINALS.md, docs/ATTACH.md, docs/SEND.md
internal/holder/holdertest/  lets a test binary be its own holder, for the tests that start terminals
internal/pty/   opens pseudo-terminals and starts commands in them
internal/proc/  finds the user's processes by an entry of their environment and stops them: an agent's leftovers (the wdx doc LEFTOVER_PROCESSES)
internal/termview/  the Bubble Tea pane that shows an attached terminal; docs/ATTACH_VIEW.md
internal/screen/  a terminal's screen kept by a headless vt, for the holder and termview, and what is read from it over time (`ScreenState`, the motion fallback); the only user of the vt; docs/VT.md, docs/AGENT_STATE.md
internal/tide/   the terminal IDE (wdx tide), a client of the daemon as the cli is: its panes' layout, focus and zoom, keys and mouse, the projects; `Options.DialTerminal` attaches its terminals and `Options.Node` names the host of a remote TIDE, the only two things in it that know it is remote; docs/TIDE.md
internal/logging/  JSON-line logs in daily files; the logger travels in the context
internal/service/  finds the daemon installed as a systemd or launchd service
internal/telemetry/  sends the daemon's metrics to an OpenTelemetry collector
internal/style/  colors text output on a terminal with semantic styles
internal/editor/  the terminal editors wdx runs on a file: how to start each, and what to type to save or discard; docs/TIDE_CODE.md
internal/deps/   the programs wdx needs besides itself (git, a clipboard tool, docker or podman, the editors), the features each enables, and whether they are ready; docs/DEPS.md
internal/observe/  runs Grafana with the logs and metrics in a container (wdx observe); docs/OBSERVABILITY.md
assets/brand/  the wdx mark and wordmark as SVG, in the site's colours; its README says which file is for what; site/public/favicon.svg is a copy of its favicon
site/          the website on GitHub Pages (Astro): the landing page and its tide demo, a replica in the browser that runs nothing (site/src/tide-demo/); make site, make site-dev; the wdx doc SITE
scripts/sitegen/ writes the website's reference pages and site/src/data/commands.json from wdx's help, settings, error codes and CHANGELOG.md (make site-ref); TestReferenceCurrent fails when they are stale, TestGuideCoverage when a command is in no docs page (an MDX page's frontmatter commands:), TestComponentsImported when an MDX page uses a component it does not import
scripts/install.sh installs the binary and the service; docs/DAEMON.md explains both
scripts/release.sh tags releases, builds them and pushes them; cliff.toml and docs/RELEASING.md explain the versions
scripts/test-changed.sh repeats the tests of the test files changed that a change can reach, on a machine kept busy (make test-changed, by hand now and then)
scripts/changedtests/ picks those tests, from the diff and what each test uses
scripts/ci-range.sh prints the commits a CI run is about
scripts/test-contention.sh runs the tests several times at once on a machine kept busy (make test-contention, in CI and nightly)
scripts/test-update.sh updates a wdx built from the tree to a release dist.sh builds of it and rolls it back (make test-update, in CI)
scripts/vt-sync.sh rebases the vt fork onto upstream and moves go.mod to it (make vt-sync); docs/VT.md
scripts/tide-lag-sample.sh gathers, on a machine where tide lags, what the wdx doc TIDE_LAG asks for: the holder logs, the wdx processes and a CPU sample of tide (sample on macOS, perf on Linux)
scripts/notices.sh writes THIRD-PARTY-NOTICES.md, which every release archive carries with LICENSE and NOTICE
.github/workflows/ci.yml  runs make check and the tests on every push to main and every PR; docs/RELEASING.md
.github/workflows/site.yml  tests, builds and deploys the website on GitHub Pages
.github/workflows/release.yml  publishes the GitHub Release of every pushed tag, through scripts/publish.sh
.github/workflows/stress.yml  runs the tests over and over nightly (make test-stress), to catch the ones that fail only now and then
.golangci.yml   the linters make lint runs
internal/update/  replaces the installed wdx with a release and rolls it back (wdx update); docs/RELEASING.md
internal/codesign/  signs a wdx binary on macOS with a self-signed certificate in a keychain of its own in the home, so the permissions macOS grants wdx survive updates (the wdx doc MAC_SIGNING)
internal/atomicfile/  writes a file whole or not at all: a synced temporary file renamed into place
internal/identity/  the name, version and paths every package agrees on: the home's files, sockets, environment variables
internal/gittest/  makes git behave the same in every test run, and creates the repositories tests use
internal/runtest/  gives a test binary a scope of its own: a directory, a clean environment, executables no fork holds busy, and a check at its end that it left nothing behind
internal/waittest/  gives every wait in the tests one deadline, from the test binary's -timeout
internal/logtest/  shows the end of a test home's logs (daemon, CLI, holders) when the test fails
internal/aca/   what differs between the AI CLIs agents run, behind AiCodingAgent, the list of them and the stand-ins for what one has not written yet; aca/acatest is two for tests; docs/AGENT.md; docs/ACA.md lists every place specific to an ACA, in this package or not, and a change to one updates it
internal/fswatch/  one inotify instance for all the daemon watches, which hands each change to those who asked for its path, the kernel allowing a user few; the wdx doc WATCHERS
internal/worktree/  reads what git knows of a project's checkouts, and keeps it up to date from events for the streams that ask, never on a timer and never writing to a repository or asking GitHub but for landing and removing a worktree, or reading its PR, when asked to; docs/TIDE_WORKTREES.md
third_party/cancelreader/  a library's copy with a race fixed, which go.mod uses in its place; its README says what changed and when to drop it
third_party/cpace/  a copy of filippo.io/cpace, a module of its own that go.mod uses in its place and make test also tests; its README says why it is copied (experimental, unmaintained, a 2021 draft), what it uses and when to drop it
internal/hub/hubconfig/  the hub's settings on internal/settings (WDX_HUB_<KEY> over the file), its home's layout, and the hub's names: wdx-hub, WDX_HUB_, wdx-hub.sock
internal/hub/hubdb/  the hub's one database (hub.db) on internal/sqlite: its embedded migrations, Open and Known
internal/hub/ca/  the hub's certificate authority: made at the first start, it issues the node endpoint's certificate, and later each node's
internal/hub/server/  serves api/hub.yaml on the web and node endpoints and the admin socket, with the daemon's request logging, validation and problem mapping
internal/hub/account/  the Store of users, invites, password resets, the TOTP factor, recovery codes and browser sessions, with every rule about them; no HTTP; the wdx doc HUB_ACCOUNTS
internal/hub/audit/  the tamper-evident chain of every act: Append inside the act's transaction, Verify and Head
internal/hub/seal/  AES-256-GCM sealing under the hub's master key, with additional data binding a sealed value to its row
internal/hub/docsync/  applies the doc changes nodes push, each once, by doc id and revision, with its audit row: each doc's revisions sealed under a data key of its own, wrapped under the master key; no HTTP; the wdx doc HUB_DOCS_PUSH
internal/hub/pages/  the sign-in pages on the web endpoint, embedded templates and CSS with no script, and their protections: the session cookie, the cross-origin check, the security headers, the rate limits, the client's address
internal/hub/node/  the hub's nodes and node logins, both flows with every rule about them, and the registry of control connections that cuts a node off; no HTTP; the wdx doc HUB_LOGIN
internal/hub/relay/  the streams between two nodes of one user: joins their two WebSockets once both arrived and copies bytes it cannot read, counting them, never logging them; the wdx doc HUB_TUNNEL
internal/hub/hubcli/  wdx-hub's commands on clikit: serve, version, setting, backup, user, audit
internal/hub/hubtest/  serves a hub in a test's process on 127.0.0.1, with a user, a browser that approves logins and a doc pushed as a node pushes one; the one hub package wdx's tests may import
internal/hublink/  the node's link to a hub: its login files in <home>/hub/, both login flows, the control connection and its certificate's renewal, the hub's node routes, and the pusher that sends the doc outbox to the hub in batches, through an Outbox it declares and the daemon hands it; streams to and from another node of the account as a net.Conn, and the node's state it reports; only the daemon uses it, and remote for it
internal/remote/  remote access between one's own nodes: TLS 1.3 between their node keys, each pinning the other's, CPace with the host's code, the sessions both ways, their idle timeout and the slowing of wrong codes; only the daemon uses it; the wdx doc HUB_TUNNEL
internal/authcode/  the host's authenticator code in <home>/hub/: the TOTP secret, the last step accepted, the recovery key's hash, and the checks on them
internal/nodeaudit/  the node's tamper-evident audit chain (hub_audit) of what matters about access to this machine: Append inside the act's transaction, Verify
deploy/hub/     runs wdx-hub in a container (Dockerfile, compose.yaml) or as a systemd service (wdx-hub.service); its README says how
internal/doc/   a project's docs and plans, their revisions and their copies on disk (docs/DOCS.md); for a synced project the store writes each change to the hub outbox inside the change's own transaction, and its Outbox reads it for hublink
internal/<domain>/  one package per feature (e.g. project, terminal, agent): types, Store, tests
```

- Dependencies point one way: cmd -> cli -> client -> api, cli -> clikit, api -> clikit, clikit -> style/settings/logging, clikittest -> clikit/pty/waittest, cli -> service, cli -> observe, cli -> style, cli -> deps, cli -> codesign, cli -> update -> db, cli -> holder, cli -> aca, cli -> termview -> holder/screen, cli -> tide -> client/style, and cli -> daemon -> domain -> db/git/gh/holder -> pty/screen, terminal -> proc, agent -> aca/terminal/screen/holder, plan -> doc/git/worktree, worktree -> fswatch, agent -> fswatch, which uses nothing but fsnotify, aca -> screen, daemon -> hublink -> hubapi/atomicfile/identity/logging, daemon -> remote -> hublink/third_party/cpace/authcode/nodeaudit/atomicfile/db, daemon -> authcode/nodeaudit, nodeaudit -> db, and cli/daemon -> telemetry; every package may use identity, and editor, which uses nothing; config, settings, db, sqlite, update, aca, agent, doc and codesign and authcode -> atomicfile; config -> settings, which uses only atomicfile and logging; db -> sqlite, and every package that may use db may use sqlite, which uses only atomicfile and logging; config -> aca, for a setting per ACA; config -> style, for tide's themes and the color mode. The cli runs the daemon but never calls the domain: every command goes through the API, except `daemon`, `observe`, `setting` and `update`, which manage the daemon, the observability stack, the config file and the installed binary themselves, and the hidden `holder`, which the daemon runs for each terminal, and `codesign`, which the install script runs to sign the release it downloaded. Every package may use logging. depguard enforces the directions it can: the cli never reaches the domain nor fswatch, only the cli uses daemon, only the cli and tide use client, only cmd and cli use config, clikit uses no identity, api, config, client, daemon or domain package, only cmd, the cli, config and clikit, and the hub's hubcli and hubconfig, use settings, only screen uses the vt, the holder never uses aca, editor uses nothing, neither the cli nor tide uses proc, tide uses neither db nor sqlite, only the daemon uses hublink, and remote for it, only the daemon uses remote, and remote uses no other domain package. The hub: cmd/wdx-hub -> hubcli -> hubconfig/hubapi/clikit/style/settings, hubcli -> server only to run it, as cli -> daemon, server -> hubapi/hubconfig/hubdb/ca/account/audit/node/docsync/relay/pages/sqlite/telemetry, pages -> account/audit/node/hubconfig, account -> audit/seal/sqlite, node -> audit/sqlite, docsync -> audit/seal/sqlite, hubapi -> clikit, hubconfig -> settings, hubdb -> sqlite, ca -> atomicfile, and seal uses the standard library only; of identity the hub uses only Version and Commit, which the same ldflags set for both binaries. depguard keeps the hub apart: nothing under internal/hub or internal/hubapi uses the cli, daemon, client, config, db or a domain package, only cmd/wdx-hub and internal/hub use internal/hub, hublink uses nothing of it, and of the hub wdx imports only hubtest, in its tests; internal/hubapi is used besides only by hublink and the daemon, which serves the nodes hublink gives it; seal imports nothing but the standard library.
- Name packages by what they provide, never by layer (no models/, services/, utils/).
- Add interfaces only where a consumer needs them, not up front.
- Tests use real dependencies (a temp SQLite DB, the real git binary), not mocks.

## Database

- One SQLite database (`~/.wdx/wdx.db`) holds every table. Never add another DB file. The hub's `hub.db`, in its own home, is the one other database: the hub is a program of its own, which may run where no wdx does.
- Migrations live in `internal/db/migrations`, and the hub's in `internal/hub/hubdb/migrations`; they are numbered, and append-only: never edit a committed migration. Every rule of this section holds for both.
- Prefer migrations that only add (a table, an index, a column that is nullable or has a default): the previous version keeps working on the database, so `wdx update --rollback` keeps the data. A migration that changes or removes what older binaries use starts with the line `-- breaks-older: true`; rolling back across it restores the database backup taken at the update.
- Conventions: STRICT tables, INTEGER unix-millisecond timestamps, NULL for absent values, prefixed random ids (`proj-`, `wt-`), indexed foreign keys with ON DELETE CASCADE (`agents.parent_agent_id` uses SET NULL, and its migration says why), user-facing names `COLLATE NOCASE`, JSON in TEXT columns with a `json_valid` CHECK.

## API

- The spec comes first: change `api/openapi.yaml`, then run `make generate`. Never edit `api.gen.go`; a test fails when it is stale.
- Resources are plural nouns under `/v1`, with standard methods and statuses: 201 when something was created, 409 for a conflict, 422 for a value that cannot be used, 400 for a request the spec does not allow.
- A `{project}` in a path is its name, or `id:` and its id; names cannot contain `:`.
- Paths in requests are absolute: the daemon's working directory means nothing to a client.
- `info.version` of the spec is an integer, `api.CurrentVersion`, raised with every `feat!:` change of the API, beside `api.OldestCompatible`, the oldest this build still talks to, both kept by hand in `internal/api/headers.go`. Through a session between nodes each side sends it in `Wdx-Api-Version`, and one older than the other's `OldestCompatible` is refused with `node_version_unsupported`, naming the side to update.
- Every operation is classified in `remoteOperations` (`internal/daemon/nodestreams.go`), served through a session or refused with its reason, and a test fails on one in neither: a new operation is a decision, never a silent opening. Nothing under `/v1/hub`, no setting, no daemon stop or reload and no tide state is served through one, so a remote TIDE keeps its view in memory; the attach relay (`attachTerminal`) is. A refused or unknown operation is `route_not_allowed`.
- Errors are Problem Details (`application/problem+json`) with a stable `code` from the `ErrorCode` enum. `internal/api/codes.go` gives each code its HTTP status, CLI exit code and description; the daemon maps domain errors to codes in one place.
- Requests are validated against the spec before a handler runs. Requests that change anything run one at a time, and to their end even when the client leaves.

## CLI conventions

Every command serves both humans and scripts or AIs.

- Only commands are positional; every input is a flag. An entity is named by `--id` or `--name`, exactly one of them.
- Every prompt choice that makes sense without a human has a flag. Without that flag and without a terminal (or with `--no-input` / `no_input: true` in the config file), fail when the question comes up, with a hint naming the flags.
- Every command supports `--json`, which never prompts, except `tide`, interactive by nature, and `wdx-hub serve`, whose stdout is its log, which fail with a `usage` error: the flags that answer a command's prompts are required up front (a `usage` error otherwise). stdout then holds exactly one object and nothing else, the same the API returns:
  - success: the API's body, such as `{"result": "created", "project": {...}}`, with the key named after the entity and extras such as `dropped`;
  - failure: Problem Details, as the API returns them: `{"type": "urn:wdx:error:name_taken", "title": "...", "status": 409, "detail": "...", "code": "name_taken", "hint": "...", "details": {"project": {...}}, "debug_id": "dbg-..."}`. Errors only the CLI reports (`usage`, `daemon_unavailable`, `already_running`, `internal`) take the same shape.
- `result` names the most significant thing that happened: `created`, `registered`, `cloned`, `opened`, `closed`, `removed`. Read-only commands have no `result`; they return the entity under its own key (`project`, or `projects` for a list).
- `--node <name>` is a persistent flag on every group the daemon serves (`agent`, `terminal`, `project`, `doc`, `plan`, `worktree`, `message`, and `version`) and on `tide`, and a new such group gets it. It runs the command on that node of the account through a session: `clientOf` dials the daemon's connect route, a stream to the host, whose output, `--json` and exit codes are the command's. No command's `RunE` knows it is remote; what differs lives in `clientOf`, the flag's handling and the shared helpers. Its codes are declared once, on the flag (`clikit.FlagErrors`), and help shows them under "With --node". Paths the daemon receives must be absolute with it, the host's; files the CLI reads or writes itself (`--path`, `--out`) stay the client's; a project taken from the working directory needs the project flags. Text mode writes `on node <name>` to stderr first, and the warning that the daemon runs another release is not given: the API version is what nodes agree on. Where `Env.AgentID` is set it fails with `remote_not_for_agents`, before anything is asked. A command that cannot run remotely says so with the `noNode` annotation. `terminal attach`, `agent attach` and `wdx tide --node` attach through the host daemon's relay, `POST /v1/terminals/{terminal}/attach`, an Upgrade it joins to the holder's socket; without `--node` they dial the holder's socket themselves, as before, so that an attach outlives a daemon restart.
- A command that cannot reach the daemon fails with `daemon_unavailable` (exit 3) and the hint `run wdx daemon start`; it never starts the daemon itself. A daemon of another version gets a warning on stderr.
- Paths given on the command line are made absolute before they are sent: the daemon does not share the working directory.
- Prompts stay in the CLI: on a conflict it asks, then sends the request again with the answer (`on_conflict`, `on_missing_checkout`).
- JSON keys are snake_case, absent values are `null`, timestamps are RFC 3339. Leave internal fields out: everything in the JSON is a contract.
- Text mode prints results to stdout and errors to stderr; the first line says what happened.
- Exit codes: 0 ok, 1 rejected (conflict, not found, cancelled), 2 usage, 3 system failure.
- The domain returns typed errors and knows nothing about output; the daemon maps them to codes, and `internal/api/codes.go` gives each code its exit code. The cli renders both formats from one response value. Hints are added by the cli, not written into domain messages.
- Every command documents itself with annotations: `output` (what it prints), `results`, `errors` (codes besides `usage` and `internal`) and `jsonRequires` (the flags that answer its prompts; the check is shared, not written per command). `--help` renders them in our own layout (never cobra's template), or as one JSON object with `--json`; subcommands are listed alphabetically. Every usage error hints at the command's `--help`.
- Every leaf command has `Example`s built with `clikit.Examples(description, command line, ...)`: the common use first, then the flag combinations that answer prompts or that `--json` needs. `TestExamples` runs each through the command's flag parsing and rules with `clikittest.CheckExamples`, so they cannot go stale; keep them free of shell quoting.
- A flag that takes a value names it in backticks in its description (``"`path` to a local git repository"``); help shows it as `--local <path>`.
- `Long` does not repeat `Short`: help prints the summary on its first line, after the command path.
- Every flag that takes a value registers its completion with `clikit.CompleteFlag`, `cobra.NoFileCompletions` when it has none; `TestCommandDocs` checks it, through `clikittest.CheckDocs`. cobra itself gets them only in a run that completes, as it keeps every flag given to it for the life of the process. Completions that ask the daemon never prompt, wait at most `completionTimeout` and offer nothing when it does not answer.
- Reference that is about no single command is a help topic (`wdx help json`, `settings`, `logs`), not a section of the root help.

## Text output style

- Text is styled only through `internal/style`, by what it is (`Success`, `Warning`, `Failure`, `Name`, `Muted`, `Command`, `Flag`, `Hint`, `Remote` for what runs on another node...), never with raw escape codes or colors in commands.
- Results take the stdout style in `Text(w, st)`; what goes to stderr (errors, warnings, prompts) uses `clikit.StderrStyle(cmd)`. Tables use `style.Table`, since `text/tabwriter` counts escape codes as characters.
- Color follows `--color`, then the `color` setting (`auto`, `always`, `never`); `auto` colors a stream only when it is a terminal and `NO_COLOR` / `TERM=dumb` are not set. Never with `--json`, and never in logs: the logged response is the plain value.
- Text errors go to stderr as `wdx: <message>  [<code>]`, then `hint:` and `debug_id:` lines.
- Styling never changes the words: with color off the text reads the same.

## Config

- Every setting is a row in the table in `internal/config`: key, description, default (or required) and parser. The engine that reads, checks and writes the table is `internal/settings`. The `wdx help settings` topic and the logs are built from it.
- `main` is the only place that reads the environment: it loads the `Config` once and passes it to `cli.Run`. Nothing else calls `os.Getenv`. The config file is read only through `internal/config`, by `main` and by the `cli` code that rewrites it or has the daemon read it again (docs/SETTINGS.md).
- An invalid or missing required setting fails every command at startup with a `usage` error listing all of them.
- `cli` holds the `Config`; domain packages get only the values they need as arguments and never import `config`.
- Only the home comes from the environment (`WDX_HOME`, default `~/.wdx`), since the config file lives in it. One exception is `NO_COLOR` and `TERM=dumb`, conventions of terminals rather than wdx settings: `main` reads them into `Env.NoColor`. Another is `WDX_AGENT_ID`, which wdx sets for the agents it runs: `main` reads it into `Env.AgentID`, so that `--node` is refused for them. The last is the fixed list of variables terminals inherit from the daemon (`PATH`, the locale, the ssh agent, the desktop session; docs/TERMINALS.md): `main` reads them into `Env.ShellEnv`, and they are never logged. Every other setting is read from `<home>/config.yaml`, nested keys making the dotted key (`log: {level: debug}` is `log.level`); a missing file means all defaults.
- The config file is written from the table, never by hand or from a template: `wdx setting write` lists every setting with its description as the comment, keeping the values set, and `wdx update` runs the new release's, as `settings write`, a name that must keep working (docs/SETTINGS.md), to write it anew. So a description reads as a sentence once capitalized, and a removed or renamed setting needs nothing more: its key is commented out at the end of the file.
- `wdx setting set` changes one setting in the file and has the daemon read the file again. A setting the daemon reads must take effect then: add it to `daemon.Settings`, or to what the `Reload` function in `runForeground` applies.
- Sources are layered, later winning: defaults, then the config file, then the environment. Unknown keys, duplicates and values that are not a single value are errors, reported with the file and line.

## Logging

The log must tell the story of any run: what was asked, every step taken and why, and what was returned.

- Get the logger with `logging.FromContext(ctx)`; it already carries the run's `debug_id`. Never use slog's default logger or a global, and pass `ctx` to every goroutine so its lines keep the id.
- `cli.Run` logs, through `clikit.Run`, `command received` (raw args and settings), `command started` (resolved command and every flag) and `command returned` (exit, outcome, the error `code` when it failed and the exact response). Failures are logged there at error level; don't log the same error again on its way up.
- The daemon logs `request received` and `request returned` (status, the error `code` when it failed and the exact response) for every request, under the debug id the client sent in `Wdx-Debug-Id`, so one id traces a command through both processes.
- Log each step where it happens, at info: lookups and what they matched, decisions and their reason, every change to the database or disk, git commands, and how each question was answered. At error level, log what a failure knows that its returned error does not (git's stderr, a failed cleanup).
- Debug is for detail: SQL with arguments, git output.
- A tab completion (`__complete`) is the one run logged at debug level from received to returned, through `logging.Quiet`; it asks the daemon to do the same for its requests with `Wdx-Log-Level: debug`. Failures stay at error.
- Messages are short lowercase statements of what happened (`project registered`); keys are snake_case and reused (`project_id`, `path`, `name`, `duration_ms`, `err`).
- Never log the environment beyond wdx's own settings, prompt text or agent output. URL credentials are redacted by the logger.
- Logging never fails a command and never writes to stdout or stderr.

## Metrics

- Only the daemon measures: it lives long enough to send metrics, which `wdx` runs do not. It sends them over OTLP/HTTP to `otel.endpoint`, by default `http://localhost:4318` where `wdx observe` listens, and nothing when that is empty.
- Metrics are named `wdx.<noun>...` in OpenTelemetry's dotted style, with the unit in `WithUnit` (seconds for durations), never in the name.
- Labels take values from a fixed set (an operation, a status, an error code), never ids, names, paths or anything a user types.
- A collector that is down or refuses the metrics is logged once and never fails or slows a request.
- Tests receive the metrics with a collector served in the test, not a fake meter.
- Every other test sends no metrics: test homes set `otel.endpoint` to empty, so nothing reaches a collector the developer runs.

## Testing

- Every `wdx` run in the cli tests is checked, by `clikittest.WantLogged`, to be logged from `command received` to `command returned`, with the logged response equal to what was printed, and every request it sent logged by the daemon under the same debug id.
- A command fails the tests when it returns a result or error code its help does not list, and `TestCommandDocs` checks, with `clikittest.CheckDocs`, every command's help is complete; keep both in sync rather than loosening them.
- Every command has integration tests that call `cli.Run` with `--json` against a home from `newHome`, which a daemon serves in the test process: one test per outcome, asserting `result` or `code`, and the exit code. Domain edge cases stay in domain tests; text output gets one smoke test per command.
- Starting the daemon in the background runs the `wdx` binary the cli tests build in `TestMain`; tests that do so stop it when they end.
- Every test calls `t.Parallel()`. Never use `t.Setenv`, `os.Chdir` or shared mutable globals; build settings with `config.Default()` and pass them in through `cli.Env`.
- All state lives in `t.TempDir()`: DB, repos, home. Never touch the network, `~/.wdx` or fixed ports. Unix sockets are the one exception: they, and homes that hold one, go in `runtest.Dir(t)`, since a path under `t.TempDir()` can exceed the 104 bytes macOS allows.
- A test binary that starts processes or uses `runtest.Dir` runs in a scope of its own: `os.Exit(runtest.Main(m))` in its `TestMain` (after `holdertest.RunIfHolder()`). Its temporary files are in one directory, locked for as long as it lives, so that no other run removes them; its environment says nothing of the machine, with an empty `HOME`; and it fails if a process or file of its is left when its last test ends. A run that died is cleaned up by the next, with the processes it left. Nothing but such a run's own lock decides that: never an age, and never a pattern of process names, which would match other agents' runs.
- Daemon tests run an in-process daemon and call it through the generated client, checking every request is logged from received to returned.
- git runs with the user's config ignored, and nothing in the background (`gittest.Isolate`, which `runtest` calls).
- Each prompt answer has a test that types it into a PTY given as the command's stdin (`typed`, `answering`): without one, stdin is not a terminal and nothing is asked.
- Tests wait on what they need (a file the shell creates, a state, the bytes read), never a fixed sleep: a project's or its terminals' state by following its stream (`waitProject`), not by polling; that nothing happened by a later change that must come first, never by waiting a while. They wait and until `waittest.Deadline(t)` (what is left of the test binary's `-timeout`), never a fixed limit a busy machine may need more than; a timeout a test must shorten or lengthen is a field of `cli.Env.Timeouts`, not a global. How long wdx's own processes wait for each other is `holder.Limits` and `StopReport`, which tests lengthen with `holdertest.Limits` and `Patience`, shortening only the one they test. A test's outcome never depends on how long something took: it checks the order things happened in, or a state only the wanted behaviour can reach. `make lint` forbids `time.Sleep` and `time.Since` in tests.
- Tests are light on memory and CPU, not only fast: wdx is developed on the machine it runs on. Stream large files rather than reading them whole, and keep loops no larger than the check needs.
- A test that drives processes into a state orders its steps so that only that state can come: to make a terminal lost, kill the holder and wait until it is gone before its shell, or the holder may report the shell's end first. A step another process races with is a flake waiting for a loaded machine.
- Parallel tests share one process: its environment, working directory and open files. A child forked by one test holds every file another test has open for writing until it execs, so running a binary just written fails with `text file busy` (golang.org/issue/22315): write executables with `runtest.WriteExecutable` and `runtest.CopyExecutable`, which no fork can run during. Code that starts a binary it has just written retries, since another process may hold it there.
- A failure explains itself: every test home calls `logtest.OnFailure`, so a failing test shows what the daemon, the CLI and the holders logged, and a wait that gives up says what it last saw, not only what it waited for.
- Time a test would wait out is injectable: a timeout, a grace period or a timer is an option, a field of `cli.Env.Timeouts` or a function the test replaces (as tide's `tick`), so tests shorten it rather than sleep through it.
- `make test-stress` runs them `COUNT` times at 1 and 4 CPUs (`PKG` narrows them), as the nightly stress workflow does: run it on a package whose tests you changed in how they wait or start processes. `make test-contention` runs them `RUNS` times at once on a machine kept busy, as when several agents test at the same time; in CI too. Nightly, with one more run under wdx's real limits (`WDX_TEST_PRODUCTION_LIMITS=1`), whose failures are what a user on a loaded machine could hit, and a failure opens an issue.
- `make test-changed` runs `TIMES` times (20) at 1 and 4 CPUs, on a machine kept busy, the tests of the test files you changed that a change can reach: a changed test, or one using a changed helper, type or variable of its package; a changed `TestMain` or `init` reaches all of those files' tests. So one that fails only now and then is found (see Workflow). Runs that overlap share one set of busy loops. It does not cover a change only to code a test reaches, nor other tests of the same file, so run `make test-stress PKG=` on a package whose concurrency you changed.
- A test for a bug is seen failing without its fix: break the code, watch it fail on what it checks, and put the code back. A test that cannot fail proves nothing.
- Before a test reads a result, ask what happens if the other process is slow: whether what it reads may not be there yet, or the step it acts during may be over. Wait for the state, or hold the step open.
- A test seen failing now and then gets a row in `docs/FLAKES.md`, even when it is fixed at once or not understood yet. Read that file first when one fails, and never answer a flake by retrying the test: many have been bugs in wdx.
- Run tests with `make test`: race detector, shuffled order, four packages at a time, and under a 3 GB memory cap where `systemd-run` can set one, as in CI.
