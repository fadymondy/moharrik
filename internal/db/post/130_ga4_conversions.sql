-- GA4 conversions (MH-449): keys, row-level security, the lead's GA client id
-- and consent, and the triggers that record each funnel moment in the outbox.
--
-- Batch Q's reserved block is 130–134. Everything for this card is in this one
-- file, so renumbering it against another branch is a rename. Every statement
-- is idempotent.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ga4_conversion_settings_workspace_fk') THEN
        ALTER TABLE ga4_conversion_settings ADD CONSTRAINT ga4_conversion_settings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'ga4_conversion_events_workspace_fk') THEN
        ALTER TABLE ga4_conversion_events ADD CONSTRAINT ga4_conversion_events_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- The sender's queue: what is due, across workspaces (it runs on the owner pool).
CREATE INDEX IF NOT EXISTS ga4_conversion_events_due_idx
    ON ga4_conversion_events (next_attempt_at) WHERE status = 'pending';
-- The settings page's "recent events".
CREATE INDEX IF NOT EXISTS ga4_conversion_events_recent_idx
    ON ga4_conversion_events (workspace_id, occurred_at DESC);

ALTER TABLE ga4_conversion_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE ga4_conversion_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON ga4_conversion_settings;
CREATE POLICY tenant_isolation ON ga4_conversion_settings
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE ga4_conversion_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE ga4_conversion_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON ga4_conversion_events;
CREATE POLICY tenant_isolation ON ga4_conversion_events
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- The visitor's GA client id ("1234567890.1712345678", read from the _ga cookie
-- by the inquiry snippet) and their Consent Mode answers for ads, as the page's
-- dataLayer held them when the form was sent. Empty = not known. Only the
-- snippet fills these; a lead without them is still counted in GA4, under a
-- pseudonymous client id, with ads consent DENIED.
ALTER TABLE leads ADD COLUMN IF NOT EXISTS ga_client_id text NOT NULL DEFAULT '';
ALTER TABLE leads ADD COLUMN IF NOT EXISTS ga_ad_user_data text NOT NULL DEFAULT '';
ALTER TABLE leads ADD COLUMN IF NOT EXISTS ga_ad_personalization text NOT NULL DEFAULT '';

-- ---- the triggers -------------------------------------------------------------
--
-- On the tables, not in the handlers, for the reason deal_stage_events gives:
-- a lead is qualified from the Leads page, over MCP and over GraphQL, and a deal
-- is won by dragging it on the board, through the generated CRUD, or by MCP.
-- A trigger sees all of them. It records only while the workspace has events
-- switched on, so switching them on later does not replay the past.
--
-- They run as whoever made the change: on the app pool that is the RLS role with
-- this workspace set, and the settings row and the outbox row are both this
-- workspace's, so the policies above admit them.

CREATE OR REPLACE FUNCTION ga4_record_conversion(ws text, kind text, subject text, ev text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    dbg boolean;
BEGIN
    SELECT s.debug_mode INTO dbg FROM ga4_conversion_settings s
    WHERE s.workspace_id = ws AND s.events_enabled AND s.measurement_id <> '' AND s.api_secret_sealed <> '';
    IF NOT FOUND THEN
        RETURN;
    END IF;
    INSERT INTO ga4_conversion_events (workspace_id, subject_kind, subject_id, event_name, debug)
    VALUES (ws, kind, subject, ev, dbg)
    ON CONFLICT (workspace_id, subject_kind, subject_id, event_name) DO NOTHING;
END $$;

CREATE OR REPLACE FUNCTION ga4_lead_conversion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM ga4_record_conversion(NEW.workspace_id, 'lead', NEW.id, 'generate_lead');
        IF NEW.status = 'qualified' THEN
            PERFORM ga4_record_conversion(NEW.workspace_id, 'lead', NEW.id, 'qualify_lead');
        END IF;
    ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
        IF NEW.status = 'qualified' THEN
            PERFORM ga4_record_conversion(NEW.workspace_id, 'lead', NEW.id, 'qualify_lead');
        ELSIF NEW.status = 'converted' THEN
            PERFORM ga4_record_conversion(NEW.workspace_id, 'lead', NEW.id, 'close_convert_lead');
        END IF;
    END IF;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS leads_ga4_conversion ON leads;
CREATE TRIGGER leads_ga4_conversion AFTER INSERT OR UPDATE OF status ON leads
    FOR EACH ROW EXECUTE FUNCTION ga4_lead_conversion();

-- A deal is won when it enters a stage of kind 'won' (from anything else).
CREATE OR REPLACE FUNCTION ga4_deal_conversion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.stage_id IS NOT DISTINCT FROM OLD.stage_id THEN
        RETURN NULL;
    END IF;
    IF EXISTS (SELECT 1 FROM pipeline_stages st
               WHERE st.workspace_id = NEW.workspace_id AND st.id = NEW.stage_id AND st.kind = 'won')
       AND (TG_OP = 'INSERT' OR NOT EXISTS (
               SELECT 1 FROM pipeline_stages st
               WHERE st.workspace_id = OLD.workspace_id AND st.id = OLD.stage_id AND st.kind = 'won')) THEN
        PERFORM ga4_record_conversion(NEW.workspace_id, 'deal', NEW.id, 'deal_won');
    END IF;
    RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS deals_ga4_conversion ON deals;
CREATE TRIGGER deals_ga4_conversion AFTER INSERT OR UPDATE OF stage_id ON deals
    FOR EACH ROW EXECUTE FUNCTION ga4_deal_conversion();
