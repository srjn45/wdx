-- A plan's tab, kept as a doc's is: kind plan, its plan's id in doc_id, so that
-- removing the plan closes it (docs/PLANS.md). The kinds are a CHECK, so the table
-- is built again, its columns as they were: an older tide passes over a kind it
-- does not know, so a rollback keeps working. No table refers to tide_tabs; the
-- trigger that does is made again around it, as renaming a table checks every
-- trigger against the schema, and the triggers on it, which go with it, are made
-- again after, as 0019 made them. Copying the rows fires none: the tabs do not
-- change.
DROP TRIGGER agent_tombstoned_tabs_closed;

CREATE TABLE tide_tabs_new (
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	position   INTEGER NOT NULL CHECK (position >= 0),
	kind       TEXT NOT NULL CHECK (kind IN ('agent', 'info', 'settings', 'file', 'diff', 'pr', 'doc', 'plan')),
	agent_id   TEXT REFERENCES agents (id) ON DELETE CASCADE,
	doc_id     TEXT REFERENCES docs (id) ON DELETE CASCADE,
	-- The worktree's name, main for the clone. No foreign key: git lists checkouts
	-- wdx never registered, and one deleted by hand stays as a missing tab.
	worktree   TEXT,
	path       TEXT, -- relative to the worktree
	PRIMARY KEY (project_id, position),
	CHECK ((agent_id IS NOT NULL) = (kind IN ('agent', 'info'))),
	CHECK ((doc_id IS NOT NULL) = (kind IN ('doc', 'plan'))),
	CHECK ((worktree IS NOT NULL) = (kind IN ('file', 'diff', 'pr'))),
	CHECK ((path IS NOT NULL) = (kind IN ('file', 'diff')))
) STRICT;
INSERT INTO tide_tabs_new (project_id, position, kind, agent_id, doc_id, worktree, path)
	SELECT project_id, position, kind, agent_id, doc_id, worktree, path FROM tide_tabs;
DROP TABLE tide_tabs;
ALTER TABLE tide_tabs_new RENAME TO tide_tabs;
CREATE INDEX tide_tabs_agent_id ON tide_tabs (agent_id);
CREATE INDEX tide_tabs_doc_id ON tide_tabs (doc_id);

-- An agent removed while it has children stays as a tombstone, which no cascade
-- reaches: its tabs go when it becomes one.
CREATE TRIGGER agent_tombstoned_tabs_closed AFTER UPDATE OF removed_at ON agents
WHEN NEW.removed_at IS NOT NULL
BEGIN
	DELETE FROM tide_tabs WHERE agent_id = NEW.id;
END;

-- As 0019_tide_tab_revisions made them.
CREATE TRIGGER tide_tab_inserted AFTER INSERT ON tide_tabs
WHEN EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
BEGIN
	INSERT INTO tide_tab_revisions (project_id, revision) VALUES (NEW.project_id, 1)
	ON CONFLICT (project_id) DO UPDATE SET revision = revision + 1;
END;

CREATE TRIGGER tide_tab_updated AFTER UPDATE ON tide_tabs
WHEN EXISTS (SELECT 1 FROM projects WHERE id = NEW.project_id)
BEGIN
	INSERT INTO tide_tab_revisions (project_id, revision) VALUES (NEW.project_id, 1)
	ON CONFLICT (project_id) DO UPDATE SET revision = revision + 1;
END;

CREATE TRIGGER tide_tab_deleted AFTER DELETE ON tide_tabs
WHEN EXISTS (SELECT 1 FROM projects WHERE id = OLD.project_id)
BEGIN
	INSERT INTO tide_tab_revisions (project_id, revision) VALUES (OLD.project_id, 1)
	ON CONFLICT (project_id) DO UPDATE SET revision = revision + 1;
END;
