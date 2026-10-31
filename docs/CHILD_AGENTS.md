# Child agents

Status: step 1 implemented; step 2 designed, not built.

An agent can be created under another agent of its project. Top-level agents have no parent.

Two steps, in this order, each landing on its own: child agents with tombstones, then an agent's worktree.

## Step 1: child agents

### CLI

```
wdx agent create [--parent-id <id> | --parent-name <name>] ...
```

- Neither flag makes a top-level agent.
- `--parent-id` alone uses the parent's project, wherever it is run from. `--parent-name` looks in the project of the project flags, or of the working directory. A parent outside that project is `not_found`.
- A child starts in its parent's dir.
- A parent can be in any state but removed. Children run independently: a parent that exits leaves them running.
- A parent is set only at creation, so cycles cannot happen. There is no depth limit.
- wdx sets `WDX_AGENT_ID` in the environment of an agent at each launch: mostly agents will create children, with `wdx agent create --parent-id "$WDX_AGENT_ID"`. Without the flag the new agent is top-level, whoever creates it.

`wdx agent list` shows the tree in the table, indented under `NAME`, with no parent column:

```
NAME            STATE       ACA          DIR               ID
ample-deer      running     claude_code  /home/me/dev/wdx  agent-9c0d…
swift-falcon    removed     claude_code  /home/me/dev/wdx  agent-1a2b…
├ calm-otter    exited (0)  claude_code  /home/me/dev/wdx  agent-5e6f…
└ quick-lynx    running     claude_code  /home/me/dev/wdx  agent-3c4d…
```

`agent get` and `agent create` print a `parent` line when there is one; `agent get` lists the children.

### Removing a parent: the tombstone

As warden did, a removed agent that still has children stays in the tree as a tombstone, so they are never orphaned.

- Removing an agent with no children deletes it, as today.
- Removing an agent with children stops it, under the same `--stop-running` question as today, and makes it a tombstone. Its children are untouched.
- A tombstone goes when its last child row is gone, and that can carry up the chain. A child that only exited still counts. There is no background reaper: it happens in the removal.
- A tombstone cannot be started or given children, and is not removed directly: `agent_removed`, 409, exit 1. One left with no children, by a daemon that died between the two deletions, can be.
- It keeps its name, so the name stays taken: a duplicate on screen would be ambiguous.
- Closing and opening the project ignores it; removing the project removes it.
- It keeps its agent row, terminal row and history files, so `Enter` on it in tide shows its last screen and history read-only, as for an exited agent. All of them are deleted when the tombstone goes, as a removal deletes them today.

### Storage

```sql
ALTER TABLE agents ADD COLUMN parent_agent_id TEXT REFERENCES agents (id) ON DELETE SET NULL;
ALTER TABLE agents ADD COLUMN removed_at INTEGER;
CREATE INDEX agents_parent_agent_id ON agents (parent_agent_id);
```

- Only adds, so no `breaks-older`.
- `SET NULL` rather than the usual `CASCADE`: a cascade would delete a child's `agents` row but leave its terminal row and holder.
- A tombstone keeps its terminal row, which holds the name, dir, exit code and times. A row left `stopped` becomes `exited`, so that opening the project does not relaunch it, in an older binary after a rollback either.

### API

- One query per project; the daemon builds the tree.
- `GET /v1/agents`, a project's `agents`: the top-level agents with nested `children`. `GET /v1/agents/{agent}`: the agent with its subtree.
- `stopped_agents`, `started_agents`, `details.agents` stay flat.
- Every agent has `parent_agent_id` and `removed_at`. A tombstone's `state` in the API is `removed`.
- `POST /v1/agents` takes `parent`, a reference like `project`.
- A script walking the top level of `agents` would miss children, so this lands as `feat!:`.

### tide

Design A of the Agents list preview, with the tree keys and folding in docs/TIDE_B4.md, "For child agents".

- `C-q c` creates a child of the agent under the cursor, or of the active agent when the agent pane has focus. `C-q a` stays top-level. The child becomes active with the cursor on it, opening any parent folded over it.
- The fold column shows only when some agent has children, so a flat list keeps its room. `←→ fold` joins the status line's keys then.
- A tombstone's row is muted, with the mark `†` and the detail `removed`. The heading's count leaves tombstones out.
- `C-q x` is not offered on a tombstone.
- The indent collapses to `⋯N` and the row's connector when a name would get under `minAgentName`, not at a fixed level: the 1:3 split leaves 24 cells at 100 columns, not the preview's 38.

### The run summary

For now, the facts the row already holds: final state, exit code, times, dir, ACA and session. A nullable `agents.summary` column, for what the agent did in words, comes with pipelines, when something can write it.

## Step 2: an agent's worktree

Not built before step 1. An agent's worktree is the project worktree holding its dir, derived rather than stored, and shown as `worktree` in the JSON and in `agent get`.

```
wdx agent create --worktree <name> ...
```

- Without the flag, a child runs in its parent's dir; a top-level agent follows today's rule, the working directory's checkout or else the clone.
- `main` names the clone. Other names match a project worktree by the base name of its path.
- A name that does not exist creates the worktree and registers it with the project.
- A new worktree always goes in `<project root>/.worktrees/<name>`. `.worktrees` is added to the repository's `.git/info/exclude` unless git already ignores it.
- A new worktree always gets a new branch named like it, so there is one name to keep track of. It starts from the HEAD of the checkout the agent would otherwise run in, so a child builds on its parent's commits. A branch of that name that already exists is an error; `main` cannot be created.
- Removing an agent never removes its worktree: it may hold work not landed.
- Two agents may share a worktree, as they share a checkout today.
- `C-q c` opens a popup asking for an optional worktree name; left empty, the child runs in its parent's worktree. Until this step, `C-q c` creates the child at once in its parent's dir.

## Later

Agents are created with no parameters for now (no prompt, model and the like). Those come after child agents.
