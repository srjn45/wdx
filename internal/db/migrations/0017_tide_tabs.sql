-- The tabs tide has open in each project, in order, so that it comes back to them
-- and a removal through wdx closes them whether tide runs or not (docs/TIDE_STATE.md).
-- A tab names what it shows: an agent, its info, the settings, a doc, or a
-- worktree's file, file diff or committed changes.
CREATE TABLE tide_tabs (
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	position   INTEGER NOT NULL CHECK (position >= 0),
	kind       TEXT NOT NULL CHECK (kind IN ('agent', 'info', 'settings', 'file', 'diff', 'pr', 'doc')),
	agent_id   TEXT REFERENCES agents (id) ON DELETE CASCADE,
	doc_id     TEXT REFERENCES docs (id) ON DELETE CASCADE,
	-- The worktree's name, main for the clone. No foreign key: git lists checkouts
	-- wdx never registered, and one deleted by hand stays as a missing tab.
	worktree   TEXT,
	path       TEXT, -- relative to the worktree
	PRIMARY KEY (project_id, position),
	CHECK ((agent_id IS NOT NULL) = (kind IN ('agent', 'info'))),
	CHECK ((doc_id IS NOT NULL) = (kind = 'doc')),
	CHECK ((worktree IS NOT NULL) = (kind IN ('file', 'diff', 'pr'))),
	CHECK ((path IS NOT NULL) = (kind IN ('file', 'diff')))
) STRICT;
CREATE INDEX tide_tabs_agent_id ON tide_tabs (agent_id);
CREATE INDEX tide_tabs_doc_id ON tide_tabs (doc_id);

-- An agent removed while it has children stays as a tombstone, which no cascade
-- reaches: its tabs go when it becomes one.
CREATE TRIGGER agent_tombstoned_tabs_closed AFTER UPDATE OF removed_at ON agents
WHEN NEW.removed_at IS NOT NULL
BEGIN
	DELETE FROM tide_tabs WHERE agent_id = NEW.id;
END;
