-- The node's tamper-evident record of what matters about access to this machine
-- (the wdx doc HUB_TUNNEL, "The node's audit chain"): its authenticator code, remote
-- access and the sessions into it. Each row's hash is the SHA-256 of the row before's
-- and its own fields, so a row changed, removed or inserted is found by wdx hub audit
-- verify. node_id is the other node of a session, NULL for an act of this one alone.
CREATE TABLE hub_audit (
	id      INTEGER PRIMARY KEY,
	at      INTEGER NOT NULL,
	action  TEXT NOT NULL,
	node_id TEXT,
	details TEXT NOT NULL CHECK (json_valid(details)),
	hash    BLOB NOT NULL
) STRICT;
