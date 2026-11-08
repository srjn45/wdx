-- How much of each limit of an AI coding agent's account is used, as the ACA last
-- told through one of its agents (the wdx doc USAGE): a row per window and bucket of
-- models, the latest observation only. A limit is the account's, so the rows are by
-- ACA, not by agent; agent_id says which one it was read from, for the log.
CREATE TABLE aca_usage (
  aca          TEXT    NOT NULL,            -- the ACA's id
  window       TEXT    NOT NULL,            -- the window's key as the ACA gives it
  bucket       TEXT    NOT NULL DEFAULT '', -- the bucket's name as the ACA gives it; '' for the one every model draws on
  used_percent INTEGER NOT NULL,            -- 0 to 100, or above past the limit
  resets_at    INTEGER,                     -- unix ms; NULL when the ACA gives none
  limited      INTEGER NOT NULL DEFAULT 0,  -- 1 while the window is used up
  observed_at  INTEGER NOT NULL,            -- unix ms, when the ACA told it
  agent_id     TEXT    REFERENCES agents(id) ON DELETE SET NULL,
  PRIMARY KEY (aca, window, bucket)
) STRICT;
CREATE INDEX aca_usage_agent ON aca_usage(agent_id);
