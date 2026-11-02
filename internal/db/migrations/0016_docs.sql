-- Docs a project keeps outside its repository, changed only through wdx so that it
-- sees every change (docs/DOCS.md). The content is in doc_revisions, one row per
-- change; revision here is the latest.
CREATE TABLE docs (
	id         TEXT PRIMARY KEY,
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	name       TEXT NOT NULL COLLATE NOCASE,
	format     TEXT NOT NULL, -- the file extension copies get, such as 'md'; '' for none
	labels     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(labels)),
	status     TEXT NOT NULL DEFAULT 'wip' CHECK (status IN ('wip', 'review', 'approved', 'rejected')),
	revision   INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL, -- of the row: a new revision, status, labels or name
	UNIQUE (project_id, name)
) STRICT;

-- Every revision's whole text, gzipped: docs are text, which shrinks four to five
-- times, and the database is copied whole at each update.
CREATE TABLE doc_revisions (
	doc_id     TEXT NOT NULL REFERENCES docs (id) ON DELETE CASCADE,
	revision   INTEGER NOT NULL, -- 1, 2, … per doc
	content    BLOB NOT NULL,
	size       INTEGER NOT NULL, -- of the text, before gzip
	created_at INTEGER NOT NULL,
	PRIMARY KEY (doc_id, revision)
) STRICT;
