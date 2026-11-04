-- What an agent is started with every time (the wdx doc AGENT_CREATE). Agents from
-- before ran with the user's own settings for their AI CLI, so they read 'ask';
-- new ones get what the request asks, 'full' by default.
ALTER TABLE agents ADD COLUMN requested_model TEXT;
ALTER TABLE agents ADD COLUMN permissions TEXT NOT NULL DEFAULT 'ask' CHECK (permissions IN ('full', 'ask'));
