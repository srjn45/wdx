-- What an agent's process is doing, as its holder last told, read only while its
-- terminal row is running. No CHECK on activity: a running holder can be newer
-- than its daemon, and a new value would mean rebuilding the table.
ALTER TABLE agents ADD COLUMN activity TEXT;
ALTER TABLE agents ADD COLUMN draft INTEGER;
