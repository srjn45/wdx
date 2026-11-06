-- The commit of a stacked plan's base, plan/<after>, that its branch sits on: the
-- base's tip when it started, moved on by each rebase onto it, and NULL once its PR
-- goes on the default branch, as for every plan not stacked (docs/PLANS.md, Step 7
-- in detail). Only added: older binaries keep working.
ALTER TABLE plan_runs ADD COLUMN base_commit TEXT;
