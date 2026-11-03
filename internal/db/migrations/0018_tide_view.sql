-- What tide shows besides its tabs, so that it comes back as it was left
-- (docs/TIDE_STATE.md): for tide as a whole, and for each project. Only tide
-- reads it, so the daemon keeps it as the JSON tide sends.
CREATE TABLE tide_view (
	id         INTEGER PRIMARY KEY CHECK (id = 1), -- one row
	view       TEXT NOT NULL CHECK (json_valid(view) AND json_type(view) = 'object'),
	updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE tide_project_views (
	project_id TEXT PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
	view       TEXT NOT NULL CHECK (json_valid(view) AND json_type(view) = 'object'),
	updated_at INTEGER NOT NULL
) STRICT;
