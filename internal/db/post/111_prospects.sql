-- Prospect discovery (MH-397): foreign keys, indexes, row-level security. Idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'prospect_profiles_workspace_fk') THEN
        ALTER TABLE prospect_profiles ADD CONSTRAINT prospect_profiles_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'prospects_workspace_fk') THEN
        ALTER TABLE prospects ADD CONSTRAINT prospects_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- A prospect is a research suggestion scored; it goes with it.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'prospects_suggestion_fk') THEN
        ALTER TABLE prospects ADD CONSTRAINT prospects_suggestion_fk
            FOREIGN KEY (workspace_id, suggestion_id) REFERENCES research_companies (workspace_id, id) ON DELETE CASCADE;
    END IF;
    -- crm_company_id / crm_contact_id / crm_deal_id carry no foreign key, for
    -- the reason research_companies.crm_company_id does not: deleting the CRM
    -- record later must neither erase the review nor be refused by it. They
    -- are read through LEFT JOINs.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'prospect_feedback_prospect_fk') THEN
        ALTER TABLE prospect_feedback ADD CONSTRAINT prospect_feedback_prospect_fk
            FOREIGN KEY (workspace_id, prospect_id) REFERENCES prospects (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'prospect_drafts_prospect_fk') THEN
        ALTER TABLE prospect_drafts ADD CONSTRAINT prospect_drafts_prospect_fk
            FOREIGN KEY (workspace_id, prospect_id) REFERENCES prospects (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS prospects_rank_idx       ON prospects (workspace_id, status, score DESC, created_at DESC);
CREATE INDEX IF NOT EXISTS prospect_feedback_ws_idx ON prospect_feedback (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS prospect_drafts_p_idx    ON prospect_drafts (workspace_id, prospect_id, created_at DESC);
CREATE INDEX IF NOT EXISTS prospect_profiles_due    ON prospect_profiles (next_run_at) WHERE enabled;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['prospect_profiles', 'prospects', 'prospect_feedback', 'prospect_drafts'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (workspace_id = current_setting('app.workspace_id', true))
            WITH CHECK (workspace_id = current_setting('app.workspace_id', true))$p$, t);
    END LOOP;
END $$;
