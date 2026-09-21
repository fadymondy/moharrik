-- Schedules: same-tenant foreign keys and the scheduler's own index.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_schedules_workspace_fk') THEN
        ALTER TABLE automation_schedules ADD CONSTRAINT automation_schedules_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, so one workspace cannot schedule another workspace's browser.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_schedules_agent_fk') THEN
        ALTER TABLE automation_schedules ADD CONSTRAINT automation_schedules_agent_fk
            FOREIGN KEY (workspace_id, agent_id) REFERENCES browser_agents (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

-- The scheduler's queue: enabled schedules whose time has come, soonest first.
CREATE INDEX IF NOT EXISTS automation_schedules_due_idx
    ON automation_schedules (next_run_at) WHERE enabled;

-- One name per workspace keeps the list readable.
CREATE UNIQUE INDEX IF NOT EXISTS automation_schedules_name_uniq
    ON automation_schedules (workspace_id, lower(name));
