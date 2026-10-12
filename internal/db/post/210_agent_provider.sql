-- Bring-your-own-agent research: foreign keys, indexes, row-level security,
-- and the provenance columns on what an agent's results become. Idempotent.

-- Who wrote a research run: Moharrik's own engine, or a caller's agent over MCP.
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS author_kind text NOT NULL DEFAULT 'engine';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS agent_name  text NOT NULL DEFAULT '';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS agent_model text NOT NULL DEFAULT '';

-- A prospect an agent found carries what it said: why now, and every way to
-- reach the person (email, LinkedIn, phone, website).
ALTER TABLE prospects ADD COLUMN IF NOT EXISTS author_kind      text  NOT NULL DEFAULT 'engine';
ALTER TABLE prospects ADD COLUMN IF NOT EXISTS why_now          text  NOT NULL DEFAULT '';
ALTER TABLE prospects ADD COLUMN IF NOT EXISTS contact_channels jsonb NOT NULL DEFAULT '{}';

-- Who does the research for this workspace: auto (the server when search is
-- configured, else the caller's agent), server, or agent.
ALTER TABLE research_settings ADD COLUMN IF NOT EXISTS provider_mode text NOT NULL DEFAULT 'auto';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_runs_author_kind_check') THEN
        ALTER TABLE research_runs ADD CONSTRAINT research_runs_author_kind_check CHECK (author_kind IN ('engine', 'agent'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'prospects_author_kind_check') THEN
        ALTER TABLE prospects ADD CONSTRAINT prospects_author_kind_check CHECK (author_kind IN ('engine', 'agent'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_settings_provider_mode_check') THEN
        ALTER TABLE research_settings ADD CONSTRAINT research_settings_provider_mode_check CHECK (provider_mode IN ('auto', 'server', 'agent'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_tasks_workspace_fk') THEN
        ALTER TABLE agent_tasks ADD CONSTRAINT agent_tasks_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_submissions_task_fk') THEN
        ALTER TABLE agent_submissions ADD CONSTRAINT agent_submissions_task_fk
            FOREIGN KEY (workspace_id, task_id) REFERENCES agent_tasks (workspace_id, id) ON DELETE CASCADE;
    END IF;
    -- run_id carries no foreign key, for the reason prospects.crm_* do not:
    -- deleting the run later must neither erase the task's history nor be
    -- refused by it.
END $$;

CREATE INDEX IF NOT EXISTS agent_tasks_queue_idx  ON agent_tasks (workspace_id, status, created_at);
CREATE INDEX IF NOT EXISTS agent_tasks_recent_idx ON agent_tasks (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS agent_submissions_rate_idx ON agent_submissions (workspace_id, token_id, created_at DESC);

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['agent_tasks', 'agent_submissions'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (workspace_id = current_setting('app.workspace_id', true))
            WITH CHECK (workspace_id = current_setting('app.workspace_id', true))$p$, t);
    END LOOP;
END $$;
