-- Automation rules (MH-427): same-tenant keys, indexes and row-level security.
--
-- Batch P's reserved block is 125–129. Every statement is idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_rules_workspace_fk') THEN
        ALTER TABLE automation_rules ADD CONSTRAINT automation_rules_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_rule_events_workspace_fk') THEN
        ALTER TABLE automation_rule_events ADD CONSTRAINT automation_rule_events_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, so a run can only ever belong to a rule of its own workspace,
    -- and goes when the rule does.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_rule_runs_rule_fk') THEN
        ALTER TABLE automation_rule_runs ADD CONSTRAINT automation_rule_runs_rule_fk
            FOREIGN KEY (workspace_id, rule_id) REFERENCES automation_rules (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

-- "Does any enabled rule listen to this?" — asked by every trigger.
CREATE INDEX IF NOT EXISTS automation_rules_listening_idx
    ON automation_rules (workspace_id, trigger) WHERE enabled;
-- The schedule trigger's queue.
CREATE INDEX IF NOT EXISTS automation_rules_due_idx
    ON automation_rules (next_run_at) WHERE enabled AND trigger = 'schedule';
-- The dispatcher's queue: pending events, oldest first.
CREATE INDEX IF NOT EXISTS automation_rule_events_pending_idx
    ON automation_rule_events (workspace_id, created_at) WHERE processed_at IS NULL;
-- The run log, newest first, per workspace and per rule; the rate limit and
-- the once-per-record check read the per-rule one.
CREATE INDEX IF NOT EXISTS automation_rule_runs_ws_idx
    ON automation_rule_runs (workspace_id, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS automation_rule_runs_rule_idx
    ON automation_rule_runs (workspace_id, rule_id, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS automation_rule_runs_subject_idx
    ON automation_rule_runs (workspace_id, rule_id, subject_id) WHERE NOT dry_run AND status IN ('ok', 'partial');

ALTER TABLE automation_rules ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_rules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_rules;
CREATE POLICY tenant_isolation ON automation_rules
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE automation_rule_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_rule_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_rule_events;
CREATE POLICY tenant_isolation ON automation_rule_events
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE automation_rule_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_rule_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_rule_runs;
CREATE POLICY tenant_isolation ON automation_rule_runs
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
