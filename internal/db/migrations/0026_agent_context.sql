-- How full an agent's context is, as its ACA last told (the wdx doc CONTEXT_SIZE):
-- the tokens in it, and the model's window, null when the ACA does not tell. Kept
-- once its terminal ends; cleared when a restart begins a new session.
ALTER TABLE agents ADD COLUMN context_tokens INTEGER;
ALTER TABLE agents ADD COLUMN context_window INTEGER;
