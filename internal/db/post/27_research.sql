-- Research runs: the tenant foreign keys (MH-227).
--
-- Composite, for the reason internal/db/schema/crm.sql gives: a foreign-key
-- check runs as the table OWNER and ignores RLS, so a single-column FK would
-- happily let one workspace point a row at another workspace's run.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_runs_workspace_fk') THEN
        ALTER TABLE research_runs ADD CONSTRAINT research_runs_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_sources_workspace_fk') THEN
        ALTER TABLE research_sources ADD CONSTRAINT research_sources_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;

    -- A source belongs to its run, and goes when the run goes.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_sources_run_fk') THEN
        ALTER TABLE research_sources ADD CONSTRAINT research_sources_run_fk
            FOREIGN KEY (workspace_id, run_id) REFERENCES research_runs (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;
