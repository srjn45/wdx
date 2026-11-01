-- Comments a human leaves on a worktree's diffs, kept until they are sent together
-- as a review: no agent may be running to send them to.
CREATE TABLE review_comments (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	worktree   TEXT NOT NULL, -- the checkout's path: git lists some wdx did not register
	file       TEXT NOT NULL, -- relative to it
	line       INTEGER NOT NULL,
	side       TEXT NOT NULL CHECK (side IN ('new', 'old')), -- the file now, or as it was
	view       TEXT NOT NULL CHECK (view IN ('uncommitted', 'committed')),
	quote      TEXT NOT NULL, -- the line as it was seen
	content_id TEXT NOT NULL, -- the file's content as it was seen
	body       TEXT NOT NULL,
	state      TEXT NOT NULL CHECK (state IN ('draft', 'sent')),
	agent_id   TEXT, -- who it was sent to; no foreign key, as it outlives the agent
	created_at INTEGER NOT NULL,
	sent_at    INTEGER
) STRICT;
CREATE INDEX review_comments_project_id ON review_comments (project_id);
