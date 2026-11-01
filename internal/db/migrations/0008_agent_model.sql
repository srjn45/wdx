-- The name of the AI model an agent's ACA last said it runs, as its holder told,
-- read only while its terminal row is running.
ALTER TABLE agents ADD COLUMN model TEXT;
