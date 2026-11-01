-- How a project lands, by moving its base or by opening a PR.
ALTER TABLE project_landing ADD COLUMN method TEXT NOT NULL DEFAULT 'rebase' CHECK (method IN ('rebase', 'pr'));

-- What gh last said of a worktree's PR. Gh knows the truth, and this only lets a row
-- say it without the network, so nothing is lost when a row goes.
CREATE TABLE worktree_pull_requests (
	project_id  TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	worktree    TEXT NOT NULL, -- the checkout's path
	number      INTEGER NOT NULL,
	url         TEXT NOT NULL,
	state       TEXT NOT NULL CHECK (state IN ('open', 'merged', 'closed')),
	head        TEXT NOT NULL, -- the commit the PR is at
	updated_at  INTEGER NOT NULL,
	PRIMARY KEY (project_id, worktree)
) STRICT;
