-- Workflows: the tenant foreign key, one name per workspace, and the link from
-- a run back to the workflow that produced it.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_workflows_workspace_fk') THEN
        ALTER TABLE automation_workflows ADD CONSTRAINT automation_workflows_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS automation_workflows_name_uniq
    ON automation_workflows (workspace_id, lower(name));

-- Which workflow a run came from, and which workflow a schedule runs. Added by
-- ALTER because those CREATEs are IF NOT EXISTS, so an existing database would
-- otherwise never get the column.
ALTER TABLE automation_runs ADD COLUMN IF NOT EXISTS workflow_id text NOT NULL DEFAULT '';
ALTER TABLE automation_schedules ADD COLUMN IF NOT EXISTS workflow_id text NOT NULL DEFAULT '';
ALTER TABLE automation_schedules ADD COLUMN IF NOT EXISTS params jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS automation_runs_workflow_idx
    ON automation_runs (workspace_id, workflow_id, created_at DESC) WHERE workflow_id <> '';
