-- Research evidence (MH-388): the run's new columns, foreign keys, search
-- index and row-level security. Idempotent.

-- What a run was, and how far it got.
--   mode       search (a question put to the search engine) | urls (read these pages)
--   origin     manual | schedule | discovery (MH-397)
--   stages     [{stage, status, count, note, started_at, finished_at}], in order
--   cost       usage, never a price: search queries, pages read, model calls, tokens
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS mode           text    NOT NULL DEFAULT 'search';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS urls           text[]  NOT NULL DEFAULT '{}';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS origin         text    NOT NULL DEFAULT 'manual';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS schedule_id    text;
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS stage          text    NOT NULL DEFAULT '';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS stages         jsonb   NOT NULL DEFAULT '[]';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS cost           jsonb   NOT NULL DEFAULT '{}';
ALTER TABLE research_runs ADD COLUMN IF NOT EXISTS evidence_count integer NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_runs_mode_check') THEN
        ALTER TABLE research_runs ADD CONSTRAINT research_runs_mode_check CHECK (mode IN ('search', 'urls'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_runs_origin_check') THEN
        ALTER TABLE research_runs ADD CONSTRAINT research_runs_origin_check CHECK (origin IN ('manual', 'schedule', 'discovery'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_runs_schedule_fk') THEN
        -- No ON DELETE action: deleting a schedule first detaches its runs
        -- (research.DeleteSchedule), which keeps the runs. SET NULL (column)
        -- would say it in one line, but needs Postgres 15.
        ALTER TABLE research_runs ADD CONSTRAINT research_runs_schedule_fk
            FOREIGN KEY (workspace_id, schedule_id) REFERENCES research_schedules (workspace_id, id);
    END IF;

    -- Evidence belongs to its run and goes with it; its source likewise.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_evidence_workspace_fk') THEN
        ALTER TABLE research_evidence ADD CONSTRAINT research_evidence_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_evidence_run_fk') THEN
        ALTER TABLE research_evidence ADD CONSTRAINT research_evidence_run_fk
            FOREIGN KEY (workspace_id, run_id) REFERENCES research_runs (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_evidence_source_fk') THEN
        ALTER TABLE research_evidence ADD CONSTRAINT research_evidence_source_fk
            FOREIGN KEY (workspace_id, source_id) REFERENCES research_sources (workspace_id, id) ON DELETE CASCADE;
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_companies_workspace_fk') THEN
        ALTER TABLE research_companies ADD CONSTRAINT research_companies_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- crm_company_id has no foreign key on purpose: a CRM company deleted
    -- later must not take the review record with it (CASCADE) or be refused
    -- (NO ACTION). It is only ever written by accepting, inside the
    -- workspace, and read through a LEFT JOIN that shows a deleted company
    -- as gone.

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_schedules_workspace_fk') THEN
        ALTER TABLE research_schedules ADD CONSTRAINT research_schedules_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'research_settings_workspace_fk') THEN
        ALTER TABLE research_settings ADD CONSTRAINT research_settings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- Full-text search across every run. 'simple' rather than 'english': the
-- evidence is as often Arabic as English, and a stemmer for one language
-- mangles the other.
ALTER TABLE research_evidence ADD COLUMN IF NOT EXISTS search tsvector
    GENERATED ALWAYS AS (to_tsvector('simple', quote || ' ' || claim || ' ' || source_title || ' ' || company_name)) STORED;

CREATE INDEX IF NOT EXISTS research_evidence_search_idx   ON research_evidence USING gin (search);
CREATE INDEX IF NOT EXISTS research_evidence_recent_idx   ON research_evidence (workspace_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS research_evidence_run_idx      ON research_evidence (workspace_id, run_id);
CREATE INDEX IF NOT EXISTS research_evidence_company_idx  ON research_evidence (workspace_id, company_key) WHERE company_key <> '';
CREATE INDEX IF NOT EXISTS research_evidence_signal_idx   ON research_evidence (workspace_id, signal_type) WHERE signal_type <> '';
CREATE INDEX IF NOT EXISTS research_companies_status_idx  ON research_companies (workspace_id, status, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS research_schedules_due_idx     ON research_schedules (next_run_at) WHERE enabled;
CREATE INDEX IF NOT EXISTS research_runs_schedule_idx     ON research_runs (workspace_id, schedule_id) WHERE schedule_id IS NOT NULL;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['research_evidence', 'research_companies', 'research_schedules', 'research_settings'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (workspace_id = current_setting('app.workspace_id', true))
            WITH CHECK (workspace_id = current_setting('app.workspace_id', true))$p$, t);
    END LOOP;
END $$;
