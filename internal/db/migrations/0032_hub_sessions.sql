-- Sessions between two nodes of one account (the wdx doc HUB_TUNNEL, "Sessions").
-- hub_known_nodes is the other nodes' keys, pinned at the first session with each:
-- TLS between two nodes refuses any other key for that node, whatever the hub says.
CREATE TABLE hub_known_nodes (
	node_id       TEXT PRIMARY KEY,
	name          TEXT NOT NULL COLLATE NOCASE, -- its name when pinned
	public_key    BLOB NOT NULL,
	first_seen_at INTEGER NOT NULL
) STRICT;

-- One row per session, incoming (another node's into this one) or outgoing. node_id
-- is the hub's id of the other node, with no foreign key: a closed session outlives
-- the pin, which logout deletes. node_name is its name when the session opened, for
-- the list while the hub cannot be asked. idle_timeout_ms is the host's timeout, as
-- it said at the open, for an outgoing session; an incoming one goes by the setting.
CREATE TABLE hub_sessions (
	id              TEXT PRIMARY KEY,
	direction       TEXT NOT NULL CHECK (direction IN ('incoming', 'outgoing')),
	node_id         TEXT NOT NULL,
	node_name       TEXT NOT NULL,
	opened_at       INTEGER NOT NULL,
	last_used_at    INTEGER NOT NULL,
	idle_timeout_ms INTEGER,
	closed_at       INTEGER,
	closed_reason   TEXT CHECK (closed_reason IN ('closed', 'idle', 'restart', 'remote_access_off', 'logged_out')),
	CHECK ((closed_at IS NULL) = (closed_reason IS NULL))
) STRICT;

CREATE INDEX hub_sessions_node_id ON hub_sessions (node_id);
