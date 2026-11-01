-- Where an agent last worked, as its ACA's own record says: an agent can move, as
-- into a worktree it made itself, while its terminal's dir stays where it starts.
ALTER TABLE agents ADD COLUMN current_dir TEXT;
