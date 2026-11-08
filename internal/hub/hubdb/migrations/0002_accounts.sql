-- The accounts of the hub: its users, the invites and password resets that
-- let them in, their second factor and browser sessions, and the audit chain
-- of every act on them (the wdx doc HUB_ACCOUNTS). Every token, code and
-- password is stored as its hash, and the TOTP secret sealed.

-- A user is never deleted, only marked removed: the row keeps the username,
-- which is never given again.
CREATE TABLE users (
	id            TEXT PRIMARY KEY,
	username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
	password_hash TEXT, -- argon2id as a PHC string; NULL once removed
	created_at    INTEGER NOT NULL,
	last_login_at INTEGER,
	disabled_at   INTEGER,
	removed_at    INTEGER
) STRICT;

CREATE TABLE invites (
	id         TEXT PRIMARY KEY,
	token_hash BLOB NOT NULL UNIQUE,
	created_at INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,
	used_by    TEXT REFERENCES users (id) ON DELETE CASCADE,
	used_at    INTEGER
) STRICT;
CREATE INDEX invites_used_by ON invites (used_by);
CREATE INDEX invites_expires_at ON invites (expires_at);

CREATE TABLE password_resets (
	id            TEXT PRIMARY KEY,
	user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	token_hash    BLOB NOT NULL UNIQUE,
	second_factor INTEGER NOT NULL CHECK (second_factor IN (0, 1)),
	created_at    INTEGER NOT NULL,
	expires_at    INTEGER NOT NULL,
	used_at       INTEGER
) STRICT;
CREATE INDEX password_resets_user_id ON password_resets (user_id);
CREATE INDEX password_resets_expires_at ON password_resets (expires_at);

-- One factor per user. secret is the one confirmed, or being enrolled while
-- confirmed_at is NULL; pending_secret is a replacement being enrolled, so
-- the factor in use keeps working until the new one is confirmed.
CREATE TABLE totp_factors (
	user_id        TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
	secret         BLOB NOT NULL,
	pending_secret BLOB,
	confirmed_at   INTEGER,
	-- The last 30-second step a code was accepted for: a code of it or of
	-- one before is refused, so that each is used once.
	last_step      INTEGER NOT NULL DEFAULT 0,
	created_at     INTEGER NOT NULL
) STRICT;

CREATE TABLE recovery_codes (
	user_id   TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	code_hash BLOB NOT NULL,
	used_at   INTEGER,
	PRIMARY KEY (user_id, code_hash)
) STRICT;

CREATE TABLE web_sessions (
	id               TEXT PRIMARY KEY,
	user_id          TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
	token_hash       BLOB NOT NULL UNIQUE,
	second_factor_at INTEGER,
	address          TEXT NOT NULL,
	user_agent       TEXT NOT NULL,
	created_at       INTEGER NOT NULL,
	last_seen_at     INTEGER NOT NULL,
	expires_at       INTEGER NOT NULL
) STRICT;
CREATE INDEX web_sessions_user_id ON web_sessions (user_id);
CREATE INDEX web_sessions_expires_at ON web_sessions (expires_at);
CREATE INDEX web_sessions_last_seen_at ON web_sessions (last_seen_at);

-- Each row's hash chains it to the one before. user_id is no foreign key: a
-- cascade would delete rows from the chain, which must keep every one.
CREATE TABLE audit (
	id      INTEGER PRIMARY KEY,
	at      INTEGER NOT NULL,
	user_id TEXT,
	actor   TEXT NOT NULL CHECK (actor IN ('user', 'admin', 'hub')),
	action  TEXT NOT NULL,
	address TEXT,
	details TEXT NOT NULL CHECK (json_valid(details)),
	hash    BLOB NOT NULL
) STRICT;
CREATE INDEX audit_user_id ON audit (user_id);
