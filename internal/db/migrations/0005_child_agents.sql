-- An agent can be created under another of its project. SET NULL rather than
-- CASCADE: a cascade would delete a child's row here and leave its terminal
-- row and holder running. Removing an agent is the agent package's work.
ALTER TABLE agents ADD COLUMN parent_agent_id TEXT REFERENCES agents (id) ON DELETE SET NULL;
CREATE INDEX agents_parent_agent_id ON agents (parent_agent_id);

-- A removed agent that still has children stays as a tombstone until they are
-- gone, its terminal row keeping its name, how it ended and its history.
ALTER TABLE agents ADD COLUMN removed_at INTEGER;
