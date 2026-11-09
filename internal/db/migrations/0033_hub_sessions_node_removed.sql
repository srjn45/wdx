-- A session closed because the other node was removed from the account (the wdx
-- doc HUB_NODE_GONE): closed_reason node_removed. The reasons are a CHECK, so the
-- table is built again as 0029 did for tide_tabs, its columns as 0032 made them.
-- An older wdx only reads the reason to pass it on, so a rollback keeps working.
CREATE TABLE hub_sessions_new (
	id              TEXT PRIMARY KEY,
	direction       TEXT NOT NULL CHECK (direction IN ('incoming', 'outgoing')),
	node_id         TEXT NOT NULL,
	node_name       TEXT NOT NULL,
	opened_at       INTEGER NOT NULL,
	last_used_at    INTEGER NOT NULL,
	idle_timeout_ms INTEGER,
	closed_at       INTEGER,
	closed_reason   TEXT CHECK (closed_reason IN ('closed', 'idle', 'restart', 'remote_access_off', 'logged_out', 'node_removed')),
	CHECK ((closed_at IS NULL) = (closed_reason IS NULL))
) STRICT;
INSERT INTO hub_sessions_new (id, direction, node_id, node_name, opened_at, last_used_at, idle_timeout_ms, closed_at, closed_reason)
	SELECT id, direction, node_id, node_name, opened_at, last_used_at, idle_timeout_ms, closed_at, closed_reason FROM hub_sessions;
DROP TABLE hub_sessions;
ALTER TABLE hub_sessions_new RENAME TO hub_sessions;
CREATE INDEX hub_sessions_node_id ON hub_sessions (node_id);
