-- How many messages wait in the inbox of an agent's holder, as it last told, read
-- only while its terminal row is running.
ALTER TABLE agents ADD COLUMN queued INTEGER;
