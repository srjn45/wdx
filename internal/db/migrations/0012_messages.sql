-- Every message sent to a terminal or an agent, kept until its holder says it typed
-- it: the holder's own inbox ends with the holder. The text can hold secrets, so it
-- is cleared once delivered.
CREATE TABLE messages (
	id           TEXT PRIMARY KEY,
	terminal_id  TEXT NOT NULL REFERENCES terminals (id) ON DELETE CASCADE,
	text         TEXT NOT NULL,
	instant      INTEGER NOT NULL,
	-- pending: on its way. delivered: typed in. held: its holder died and what it was
	-- meant for is gone, so it waits for a human. unconfirmed: handed to a holder too
	-- old to say.
	state        TEXT NOT NULL CHECK (state IN ('pending', 'delivered', 'held', 'unconfirmed')),
	session_id   TEXT, -- the agent's conversation when it was sent; NULL for a shell
	holder_pid   INTEGER, -- the holder it was last forwarded to
	attempts     INTEGER NOT NULL DEFAULT 0, -- more than one: it may have come twice
	created_at   INTEGER NOT NULL,
	forwarded_at INTEGER,
	delivered_at INTEGER
) STRICT;
CREATE INDEX messages_terminal_id ON messages (terminal_id, state);

-- A review follows its message. Sent comments keep the state 'sent': a CHECK cannot
-- change without rebuilding the table, and the message says whether it was delivered.
ALTER TABLE review_comments ADD COLUMN message_id TEXT REFERENCES messages (id) ON DELETE SET NULL;
CREATE INDEX review_comments_message_id ON review_comments (message_id);

-- A message that goes undelivered gives its comments back, however it goes: discarded,
-- or with its agent.
CREATE TRIGGER messages_gone_comments_back BEFORE DELETE ON messages
WHEN OLD.state IN ('pending', 'held')
BEGIN
	UPDATE review_comments SET state = 'draft', message_id = NULL, agent_id = NULL, sent_at = NULL
	WHERE message_id = OLD.id AND state = 'sent';
END;
