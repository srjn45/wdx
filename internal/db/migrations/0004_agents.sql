-- A terminal row's kind says what it is for: a shell to type in, or the
-- process of an agent. The terminal commands and API deal with shells alone.
ALTER TABLE terminals ADD COLUMN kind TEXT NOT NULL DEFAULT 'shell' CHECK (kind IN ('shell', 'agent'));

-- An agent is an AI coding agent running in a terminal row of kind 'agent',
-- which holds its name, dir and process; it is always read joined with it.
-- Removing the terminal row removes the agent.
CREATE TABLE agents (
	id          TEXT PRIMARY KEY,
	terminal_id TEXT NOT NULL UNIQUE REFERENCES terminals (id) ON DELETE CASCADE,
	aca         TEXT NOT NULL,
	session_id  TEXT NOT NULL
) STRICT;
