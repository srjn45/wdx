-- What an agent's process asks while its activity is waiting, as its holder last
-- told (the wdx doc CLAUDE_PROMPTS): the prompt's kind, question and options as
-- JSON; null while none is read. The text can hold what a person would not want
-- kept, so it stays only while the prompt is open: cleared with the activity when
-- the terminal ends.
ALTER TABLE agents ADD COLUMN prompt TEXT CHECK (prompt IS NULL OR json_valid(prompt));
