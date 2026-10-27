-- A project is one repository. Every clone of it and every worktree of those
-- clones is a row in project_worktrees. Clones are matched by origin_key
-- (the normalized origin_url); worktrees of one clone share git_common_dir.
CREATE TABLE projects (
	id         TEXT PRIMARY KEY,
	name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
	origin_url TEXT,
	origin_key TEXT UNIQUE,
	state      TEXT NOT NULL CHECK (state IN ('open', 'closed')),
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	CHECK ((origin_url IS NULL) = (origin_key IS NULL))
) STRICT;

CREATE TABLE project_worktrees (
	id             TEXT PRIMARY KEY,
	project_id     TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	path           TEXT NOT NULL UNIQUE,
	git_common_dir TEXT NOT NULL,
	created_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX project_worktrees_project_id ON project_worktrees (project_id);
CREATE INDEX project_worktrees_git_common_dir ON project_worktrees (git_common_dir);
