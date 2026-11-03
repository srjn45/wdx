-- A plan's run (docs/PLANS.md). A plan runs once, so its run is keyed by the plan,
-- and a plan with no run is a draft. Every state of a run and a task is in the
-- CHECKs from the start, as changing one means building the table again.
CREATE TABLE plan_runs (
	plan_id     TEXT PRIMARY KEY REFERENCES docs (id) ON DELETE CASCADE,
	revision    INTEGER NOT NULL,
	base        TEXT NOT NULL,
	branch      TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('running', 'paused', 'done', 'cancelled')),
	parallel    INTEGER CHECK (parallel IS NULL OR parallel > 0),
	started_at  INTEGER NOT NULL,
	finished_at INTEGER,
	archived_at INTEGER
) STRICT;

-- A task of a run, written for every task of the plan when the run starts. agent_id
-- is SET NULL rather than CASCADE: removing a task's agent fails the task, which
-- the run keeps.
CREATE TABLE plan_tasks (
	plan_id     TEXT NOT NULL REFERENCES plan_runs (plan_id) ON DELETE CASCADE,
	task_id     TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('pending', 'running', 'landing', 'land_failed', 'failed', 'done', 'skipped')),
	failure     TEXT,
	conflicts   TEXT CHECK (conflicts IS NULL OR json_valid(conflicts)),
	agent_id    TEXT REFERENCES agents (id) ON DELETE SET NULL,
	slot        INTEGER,
	summary     TEXT,
	started_at  INTEGER,
	finished_at INTEGER,
	PRIMARY KEY (plan_id, task_id)
) STRICT;
CREATE INDEX plan_tasks_agent_id ON plan_tasks (agent_id);

-- The worktrees a run lends its tasks, one at a time. A slot is free when no task's
-- slot names it.
CREATE TABLE plan_slots (
	plan_id    TEXT NOT NULL REFERENCES plan_runs (plan_id) ON DELETE CASCADE,
	slot       INTEGER NOT NULL,
	path       TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (plan_id, slot)
) STRICT;
