-- The docs the nodes push, sealed at rest (the wdx doc HUB_DOCS_PUSH). A doc
-- keeps the node's id, which is random and global; it is filed by its owner and
-- its project's repository key. Its data key is sealed under master_key with
-- the doc's id as additional data, and each revision's gzipped text under the
-- data key with the doc's id and revision.

-- A user is never deleted, only marked removed; wdx-hub user remove deletes
-- their docs itself.
CREATE TABLE docs (
	id         TEXT PRIMARY KEY,
	owner_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	repo_key   TEXT NOT NULL,
	kind       TEXT NOT NULL CHECK (kind IN ('doc', 'plan')),
	name       TEXT NOT NULL COLLATE NOCASE,
	folder     TEXT NOT NULL,
	format     TEXT NOT NULL,
	labels     TEXT NOT NULL CHECK (json_valid(labels)),
	status     TEXT NOT NULL,
	revision   INTEGER NOT NULL, -- the latest stored
	data_key   BLOB NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	UNIQUE (owner_id, repo_key, name)
) STRICT;

CREATE TABLE doc_revisions (
	doc_id     TEXT NOT NULL REFERENCES docs (id) ON DELETE CASCADE,
	revision   INTEGER NOT NULL,
	content    BLOB NOT NULL,
	size       INTEGER NOT NULL, -- of the text, before gzip
	created_at INTEGER NOT NULL, -- when the hub stored it
	PRIMARY KEY (doc_id, revision)
) STRICT;
