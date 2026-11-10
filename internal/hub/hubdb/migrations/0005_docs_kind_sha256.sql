-- A doc and a plan of one name are two docs on the hub, as on the node, and each
-- revision keeps the sha256 of its text, so that another text under a revision
-- stored is told from the same one sent again (the wdx doc ARCH_STD_FIXES, A10
-- and A11). 0004's uniqueness is a constraint of the table, whose index SQLite
-- cannot drop, so docs is built again with kind in it; doc_revisions is built
-- again too, referencing the new table, as dropping docs would delete its rows,
-- and gains content_sha256, NULL for the revisions stored before. The CHECK on
-- kind goes: the kinds are a list that grows, kept in Go (docsync), where a new
-- one needs no rebuild. The ids, and so the data keys and revisions sealed to
-- them, stay as they were.
--
-- An older hub works on the tables: their columns are 0004's, content_sha256
-- aside, which it never names, and it refuses a name taken in another kind
-- itself. So a rollback keeps the database.
CREATE TABLE docs_new (
	id         TEXT PRIMARY KEY,
	owner_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	repo_key   TEXT NOT NULL,
	kind       TEXT NOT NULL,
	name       TEXT NOT NULL COLLATE NOCASE,
	folder     TEXT NOT NULL,
	format     TEXT NOT NULL,
	labels     TEXT NOT NULL CHECK (json_valid(labels)),
	status     TEXT NOT NULL,
	revision   INTEGER NOT NULL, -- the latest stored
	data_key   BLOB NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE (owner_id, repo_key, kind, name)
) STRICT;
INSERT INTO docs_new (id, owner_id, repo_key, kind, name, folder, format, labels, status, revision, data_key, created_at, updated_at)
	SELECT id, owner_id, repo_key, kind, name, folder, format, labels, status, revision, data_key, created_at, updated_at FROM docs;

CREATE TABLE doc_revisions_new (
	doc_id         TEXT NOT NULL REFERENCES docs_new (id) ON DELETE CASCADE,
	revision       INTEGER NOT NULL,
	content        BLOB NOT NULL,
	size           INTEGER NOT NULL, -- of the text, before gzip
	content_sha256 TEXT,             -- of the text, in hex; NULL when stored before 0005
	created_at     INTEGER NOT NULL, -- when the hub stored it
	PRIMARY KEY (doc_id, revision)
) STRICT;
INSERT INTO doc_revisions_new (doc_id, revision, content, size, created_at)
	SELECT doc_id, revision, content, size, created_at FROM doc_revisions;

DROP TABLE doc_revisions;
DROP TABLE docs;
ALTER TABLE docs_new RENAME TO docs;
ALTER TABLE doc_revisions_new RENAME TO doc_revisions;
