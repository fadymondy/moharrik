-- Workflow canvas layouts (MH-480): same-tenant foreign keys and RLS.
--
-- Numbered 120 (batch O's block). The post files sort as strings, so this runs
-- before 20_rls.sql; it needs nothing from any other post file — the tables
-- and the (workspace_id, id) key it references are made in internal/db/schema.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_workflow_layouts_workspace_fk') THEN
        ALTER TABLE automation_workflow_layouts ADD CONSTRAINT automation_workflow_layouts_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, so a layout can only ever belong to a workflow of its own
    -- workspace, and goes when the workflow does.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_workflow_layouts_workflow_fk') THEN
        ALTER TABLE automation_workflow_layouts ADD CONSTRAINT automation_workflow_layouts_workflow_fk
            FOREIGN KEY (workspace_id, workflow_id) REFERENCES automation_workflows (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

ALTER TABLE automation_workflow_layouts ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_workflow_layouts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_workflow_layouts;
CREATE POLICY tenant_isolation ON automation_workflow_layouts
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- The Executions tab reads automation_runs by workflow through
-- automation_runs_workflow_idx, made in 25_automation_workflows.sql. It is not
-- touched here: this file sorts before 25, where that column is added.
