-- MH-524: the rule run log's reasons (and, inside `actions`, each action's
-- summary/error) were English sentences only, so the log read wrong on an
-- Arabic page. reason_code/reason_args give the run's own reason a stable
-- code + values a UI can translate; the English `reason` column is kept
-- unchanged for MCP list_rule_runs and for runs logged before this shipped.
-- Actions live in the `actions` jsonb column already, so summary_code/
-- summary_args/error_code/error_args need no schema change — the code just
-- starts writing them.
--
-- Idempotent: safe to run again, and safe on a production database that
-- already has rows in automation_rule_runs (they get the '' / '{}' default,
-- which the reader falls back to the plain English text for).
ALTER TABLE automation_rule_runs ADD COLUMN IF NOT EXISTS reason_code text NOT NULL DEFAULT '';
ALTER TABLE automation_rule_runs ADD COLUMN IF NOT EXISTS reason_args jsonb NOT NULL DEFAULT '{}';
