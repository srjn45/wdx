-- A terminal is a shell in a PTY, run by its holder process, in a project.
-- The row holds what was asked and what happened; the holder is the truth
-- about the processes. pid is the shell's, holder_pid the holder's.
CREATE TABLE terminals (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	name       TEXT NOT NULL COLLATE NOCASE,
	dir        TEXT NOT NULL,
	shell      TEXT NOT NULL,
	state      TEXT NOT NULL CHECK (state IN ('starting', 'running', 'exited', 'stopped', 'failed', 'lost')),
	pid        INTEGER,
	holder_pid INTEGER,
	exit_code  INTEGER,
	error      TEXT,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	started_at INTEGER,
	ended_at   INTEGER,
	UNIQUE (project_id, name)
) STRICT;

CREATE INDEX terminals_project_id ON terminals (project_id);
