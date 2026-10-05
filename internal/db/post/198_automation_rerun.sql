-- A run started by "run again" remembers which run it repeated (MH-1067).
-- Nullable and unconstrained on purpose: runs are pruned, and a re-run must
-- outlive the run it was copied from. The reader resolves it through a
-- subselect, so a pruned original reads as "no link", never a dangling id.
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS rerun_of text;
CREATE INDEX IF NOT EXISTS automation_runs_rerun_of_idx ON automation_runs (workspace_id, rerun_of) WHERE rerun_of IS NOT NULL;
