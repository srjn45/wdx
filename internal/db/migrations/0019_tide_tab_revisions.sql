-- A count of the changes to each project's tabs, which stream events and the answer
-- to replacing the tabs carry: tide tells by it an event sent before its own change
-- from one sent after (docs/TIDE_STATE.md). Triggers raise it, so every change
-- counts: tide's lists, the cascades, the tombstone's trigger and the removals and
-- renames through wdx.
CREATE TABLE tide_tab_revisions (
	project_id TEXT PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
	revision   INTEGER NOT NULL
) STRICT;

-- A project being removed is already gone when its tabs cascade: no row is made
-- for it then.
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
