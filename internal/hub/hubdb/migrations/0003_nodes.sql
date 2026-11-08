-- The nodes of the accounts, the machines that logged in with wdx hub login,
-- and the logins that make them (the wdx doc HUB_LOGIN). Every code is stored
-- as its hash.

-- A node removed is kept, revoked, so that its certificate is refused as
-- revoked; its name is free for another node of the account.
CREATE TABLE nodes (
	id              TEXT PRIMARY KEY,
	user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	name            TEXT NOT NULL COLLATE NOCASE,
	public_key      BLOB NOT NULL, -- Ed25519, 32 bytes
	os              TEXT NOT NULL,
	version         TEXT NOT NULL, -- of wdx, as it last connected
	remote_access   INTEGER NOT NULL DEFAULT 0 CHECK (remote_access IN (0, 1)),
	code_setup      INTEGER NOT NULL DEFAULT 0 CHECK (code_setup IN (0, 1)),
	created_at      INTEGER NOT NULL,
	last_seen_at    INTEGER,
	-- When its current certificate ends, so that renewal is decided without
	-- reading it; NULL until the login that made it issued the first.
	cert_expires_at INTEGER,
	revoked_at      INTEGER
) STRICT;
CREATE INDEX nodes_user_id ON nodes (user_id);
CREATE UNIQUE INDEX nodes_user_name ON nodes (user_id, name) WHERE revoked_at IS NULL;

-- A login lives 10 minutes. code_hash is the device code's from the start of
-- a device login, and the authorization code's once a browser login is
-- approved; user_code_hash is the device login's user code's.
CREATE TABLE node_logins (
	id             TEXT PRIMARY KEY,
	flow           TEXT NOT NULL CHECK (flow IN ('browser', 'device')),
	code_hash      BLOB UNIQUE,
	pkce_challenge TEXT,
	callback       TEXT,
	user_code_hash BLOB UNIQUE,
	name           TEXT NOT NULL,
	os             TEXT NOT NULL,
	version        TEXT NOT NULL,
	public_key     BLOB NOT NULL,
	status         TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'denied')),
	user_id        TEXT REFERENCES users (id) ON DELETE CASCADE,
	node_id        TEXT REFERENCES nodes (id) ON DELETE CASCADE,
	address        TEXT NOT NULL, -- the address that started it
	created_at     INTEGER NOT NULL,
	expires_at     INTEGER NOT NULL,
	-- When the device login was last polled, for slow_down.
	polled_at      INTEGER
) STRICT;
CREATE INDEX node_logins_user_id ON node_logins (user_id);
CREATE INDEX node_logins_node_id ON node_logins (node_id);
CREATE INDEX node_logins_expires_at ON node_logins (expires_at);

-- The node an act was by or on; NULL for the acts of no node.
ALTER TABLE audit ADD COLUMN node_id TEXT;
CREATE INDEX audit_node_id ON audit (node_id);
