-- How a project lands its worktrees. No row means it was never asked.
CREATE TABLE project_landing (
	project_id    TEXT PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
	check_command TEXT, -- run before landing; NULL for none
	push          INTEGER NOT NULL, -- whether a base that moved is pushed to origin
	updated_at    INTEGER NOT NULL
) STRICT;
