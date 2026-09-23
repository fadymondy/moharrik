-- Workflow versions (MH-400): the current version number on the workflow, the
-- same-tenant foreign keys, RLS, and a version 1 for every workflow saved
-- before versions existed.

ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_workflow_versions_workspace_fk') THEN
        ALTER TABLE automation_workflow_versions ADD CONSTRAINT automation_workflow_versions_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, so a version can only ever belong to a workflow of its own
    -- workspace (the FK check runs as the owner and ignores RLS).
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_workflow_versions_workflow_fk') THEN
        ALTER TABLE automation_workflow_versions ADD CONSTRAINT automation_workflow_versions_workflow_fk
            FOREIGN KEY (workspace_id, workflow_id) REFERENCES automation_workflows (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS automation_workflow_versions_list_idx
    ON automation_workflow_versions (workspace_id, workflow_id, version DESC);

ALTER TABLE automation_workflow_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_workflow_versions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_workflow_versions;
CREATE POLICY tenant_isolation ON automation_workflow_versions
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Every workflow that has no history yet gets its current state as a version,
-- so the first edit has something to go back to. Idempotent: a workflow that
-- already has any version is left alone.
INSERT INTO automation_workflow_versions (workspace_id, workflow_id, version, name, description, steps, params, note, created_by, created_at)
SELECT w.workspace_id, w.id, w.version, w.name, w.description, w.steps, w.params,
       CASE WHEN w.created_by LIKE 'agent:%' THEN 'recorded' ELSE 'created' END,
       w.created_by, w.created_at
FROM automation_workflows w
WHERE NOT EXISTS (
    SELECT 1 FROM automation_workflow_versions v
    WHERE v.workspace_id = w.workspace_id AND v.workflow_id = w.id
);
