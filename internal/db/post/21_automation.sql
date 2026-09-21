-- Automation: same-tenant foreign keys and indexes (after every schema
-- fragment). The post files are applied in name order, so this runs after
-- 20_rls.sql — which is fine, because a policy does not depend on a foreign
-- key. The policies for these tables live in 20_rls.sql with every other
-- table's, so there is one place to read "what is isolated".

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'browser_agents_workspace_fk') THEN
        ALTER TABLE browser_agents ADD CONSTRAINT browser_agents_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_pairings_workspace_fk') THEN
        ALTER TABLE agent_pairings ADD CONSTRAINT agent_pairings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_runs_workspace_fk') THEN
        ALTER TABLE automation_runs ADD CONSTRAINT automation_runs_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_run_steps_workspace_fk') THEN
        ALTER TABLE automation_run_steps ADD CONSTRAINT automation_run_steps_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_settings_workspace_fk') THEN
        ALTER TABLE automation_settings ADD CONSTRAINT automation_settings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;

    -- Composite, same-workspace links. A foreign-key check runs as the table
    -- owner and ignores RLS, so a single-column reference would let one
    -- workspace point a run at another workspace's browser.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_runs_agent_fk') THEN
        ALTER TABLE automation_runs ADD CONSTRAINT automation_runs_agent_fk
            FOREIGN KEY (workspace_id, agent_id) REFERENCES browser_agents (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'automation_run_steps_run_fk') THEN
        ALTER TABLE automation_run_steps ADD CONSTRAINT automation_run_steps_run_fk
            FOREIGN KEY (workspace_id, run_id) REFERENCES automation_runs (workspace_id, id) ON DELETE CASCADE;
    END IF;
    -- A pairing points at the browser it minted, once it has one.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_pairings_agent_fk') THEN
        ALTER TABLE agent_pairings ADD CONSTRAINT agent_pairings_agent_fk
            FOREIGN KEY (workspace_id, agent_id) REFERENCES browser_agents (workspace_id, id) ON DELETE SET NULL (agent_id);
    END IF;
END $$;

-- One name per workspace keeps a list of browsers readable; a revoked one frees
-- its name again.
CREATE UNIQUE INDEX IF NOT EXISTS browser_agents_name_uniq ON browser_agents (workspace_id, lower(name))
    WHERE revoked_at IS NULL;
