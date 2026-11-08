-- A project's docs going up to the hub (the wdx doc HUB_DOCS_PUSH): whether the
-- project is synced, and when a change of it last reached the hub, NULL if never.
ALTER TABLE projects ADD COLUMN hub_sync INTEGER NOT NULL DEFAULT 0 CHECK (hub_sync IN (0, 1));
ALTER TABLE projects ADD COLUMN hub_pushed_at INTEGER;

-- The changes of synced projects' docs waiting for the hub, written by the doc
-- store in each change's own transaction and sent in seq order. A row says what
-- changed; the content and metadata are read when it is sent. doc_id has no
-- foreign key: a removed row outlives its doc.
CREATE TABLE hub_outbox (
	seq        INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	doc_id     TEXT NOT NULL,
	change     TEXT NOT NULL CHECK (change IN ('revision', 'metadata', 'removed')),
	revision   INTEGER, -- the revision a revision row sends
	created_at INTEGER NOT NULL,
	failed     TEXT, -- the code the hub refused it with
	CHECK ((revision IS NOT NULL) = (change = 'revision'))
) STRICT;

CREATE INDEX hub_outbox_project_id ON hub_outbox (project_id);
CREATE INDEX hub_outbox_doc_id ON hub_outbox (doc_id);
