-- breaks-older: true
-- The oldest schema version a binary must know to use this database: the
-- last migration marked breaks-older. A binary that knows fewer migrations
-- than the database has opens it as long as it knows this one, so rolling
-- back across migrations that only add things keeps the data.
CREATE TABLE schema_readers (
	id     INTEGER PRIMARY KEY CHECK (id = 1),
	oldest INTEGER NOT NULL
) STRICT;
