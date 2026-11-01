-- Files a human marked verified, each with the content they looked at: the mark
-- holds only while the file has that content. It is a label, nothing depends on it.
CREATE TABLE verified_files (
	project_id  TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	worktree    TEXT NOT NULL, -- the checkout's path: git lists some wdx did not register
	file        TEXT NOT NULL, -- relative to it
	content_id  TEXT NOT NULL, -- git's blob id, or "deleted"
	verified_at INTEGER NOT NULL,
	PRIMARY KEY (project_id, worktree, file)
) STRICT;
