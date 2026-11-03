-- Plans are kept as docs, to share their revisions and copies (docs/PLANS.md). A
-- plan's name is stored as plan:<name>, which no doc's can be, so that the table's
-- UNIQUE (project_id, name) keeps the two apart with nothing rebuilt.
ALTER TABLE docs ADD COLUMN kind TEXT NOT NULL DEFAULT 'doc' CHECK (kind IN ('doc', 'plan'));

-- The plan of plans that lists this plan. SET NULL rather than CASCADE: removing a
-- parent frees its children, it does not remove them.
ALTER TABLE docs ADD COLUMN parent_id TEXT REFERENCES docs (id) ON DELETE SET NULL;
CREATE INDEX docs_parent_id ON docs (parent_id);
