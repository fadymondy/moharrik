-- Contact lifecycle + ideal-customer scoring (MH-395).
--
-- Everything for this card is in this one file. Every statement is idempotent.

-- ---- columns ------------------------------------------------------------------

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS lifecycle    text        NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS lifecycle_at timestamptz;
-- Where a company is: the region criterion is judged on it.
ALTER TABLE companies ADD COLUMN IF NOT EXISTS country text NOT NULL DEFAULT '';

-- The scores live in icp_scores (internal/db/schema/icp.sql), not on the
-- contact and company rows: a rescore rewrites them wholesale, and on the rows
-- themselves every rescore would have been an audited change to every contact
-- (MH-392's trigger records each row write). An earlier draft of this card put
-- them on the rows; a database that ran it drops those columns here.
DROP TRIGGER IF EXISTS contacts_icp_touch ON contacts;
DROP TRIGGER IF EXISTS companies_icp_touch ON companies;
ALTER TABLE contacts DROP COLUMN IF EXISTS icp_score;
ALTER TABLE contacts DROP COLUMN IF EXISTS icp_reasons;
ALTER TABLE contacts DROP COLUMN IF EXISTS icp_stale;
ALTER TABLE contacts DROP COLUMN IF EXISTS icp_scored_at;
ALTER TABLE companies DROP COLUMN IF EXISTS icp_score;
ALTER TABLE companies DROP COLUMN IF EXISTS icp_reasons;
ALTER TABLE companies DROP COLUMN IF EXISTS icp_stale;
ALTER TABLE companies DROP COLUMN IF EXISTS icp_scored_at;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_lifecycle_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_lifecycle_ck
            CHECK (lifecycle IN ('', 'subscriber', 'lead', 'qualified', 'customer'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'icp_scores_workspace_fk') THEN
        ALTER TABLE icp_scores ADD CONSTRAINT icp_scores_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'icp_scores_contact_fk') THEN
        ALTER TABLE icp_scores ADD CONSTRAINT icp_scores_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'icp_scores_company_fk') THEN
        ALTER TABLE icp_scores ADD CONSTRAINT icp_scores_company_fk
            FOREIGN KEY (workspace_id, company_id) REFERENCES companies (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_lifecycle_events_workspace_fk') THEN
        ALTER TABLE contact_lifecycle_events ADD CONSTRAINT contact_lifecycle_events_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, for the reason internal/db/schema/crm.sql gives.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_lifecycle_events_contact_fk') THEN
        ALTER TABLE contact_lifecycle_events ADD CONSTRAINT contact_lifecycle_events_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contact_lifecycle_events_actor_fk') THEN
        ALTER TABLE contact_lifecycle_events ADD CONSTRAINT contact_lifecycle_events_actor_fk
            FOREIGN KEY (actor_user_id) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'icp_profiles_workspace_fk') THEN
        ALTER TABLE icp_profiles ADD CONSTRAINT icp_profiles_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'icp_profiles_updated_by_fk') THEN
        ALTER TABLE icp_profiles ADD CONSTRAINT icp_profiles_updated_by_fk
            FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS contact_lifecycle_events_contact_idx ON contact_lifecycle_events (workspace_id, contact_id, created_at DESC);
CREATE INDEX IF NOT EXISTS contacts_lifecycle_idx ON contacts (workspace_id, lifecycle);
CREATE UNIQUE INDEX IF NOT EXISTS icp_scores_contact_uniq ON icp_scores (workspace_id, contact_id) WHERE contact_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS icp_scores_company_uniq ON icp_scores (workspace_id, company_id) WHERE company_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS icp_scores_rank_idx ON icp_scores (workspace_id, score DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS icp_scores_stale_idx ON icp_scores (workspace_id) WHERE stale;
CREATE INDEX IF NOT EXISTS icp_scores_scored_idx ON icp_scores (workspace_id, scored_at);

-- ---- lifecycle: moved forward by real events -----------------------------------
--
-- The order a contact moves in. An automatic move only ever goes forward: a
-- customer who subscribes to the newsletter stays a customer. A member can set
-- any stage by hand (internal/icp.SetLifecycle), recorded as 'manual'.
CREATE OR REPLACE FUNCTION crm_lifecycle_rank(s text) RETURNS int IMMUTABLE LANGUAGE sql AS $$
    SELECT CASE s WHEN 'subscriber' THEN 1 WHEN 'lead' THEN 2 WHEN 'qualified' THEN 3 WHEN 'customer' THEN 4 ELSE 0 END
$$;

-- These run as whoever made the change. On the app pool that is the RLS role
-- with the change's own workspace set, and they only touch rows of that
-- workspace (p_ws is the changed row's workspace_id), so the policies admit them.
CREATE OR REPLACE FUNCTION crm_advance_lifecycle(p_ws text, p_contact text, p_to text, p_source text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    v_from text;
BEGIN
    IF p_contact IS NULL THEN
        RETURN;
    END IF;
    SELECT lifecycle INTO v_from FROM contacts WHERE workspace_id = p_ws AND id = p_contact FOR UPDATE;
    IF NOT FOUND OR crm_lifecycle_rank(v_from) >= crm_lifecycle_rank(p_to) THEN
        RETURN;
    END IF;
    UPDATE contacts SET lifecycle = p_to, lifecycle_at = now() WHERE workspace_id = p_ws AND id = p_contact;
    INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source)
    VALUES (p_ws, p_contact, v_from, p_to, p_source);
END $$;

-- Confirmed on a mailing list -> subscriber.
CREATE OR REPLACE FUNCTION crm_lifecycle_on_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'subscribed' AND (TG_OP = 'INSERT' OR OLD.status IS DISTINCT FROM 'subscribed') THEN
        PERFORM crm_advance_lifecycle(NEW.workspace_id, NEW.contact_id, 'subscriber', 'subscribed');
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS list_subscriptions_lifecycle ON list_subscriptions;
CREATE TRIGGER list_subscriptions_lifecycle AFTER INSERT OR UPDATE OF status ON list_subscriptions
    FOR EACH ROW EXECUTE FUNCTION crm_lifecycle_on_subscription();

-- A lead converted into this contact -> lead.
CREATE OR REPLACE FUNCTION crm_lifecycle_on_lead() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.contact_id IS NOT NULL AND NEW.converted_at IS NOT NULL
       AND (TG_OP = 'INSERT' OR OLD.converted_at IS NULL OR OLD.contact_id IS DISTINCT FROM NEW.contact_id) THEN
        PERFORM crm_advance_lifecycle(NEW.workspace_id, NEW.contact_id, 'lead', 'lead_converted');
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS leads_lifecycle ON leads;
CREATE TRIGGER leads_lifecycle AFTER INSERT OR UPDATE OF contact_id, converted_at ON leads
    FOR EACH ROW EXECUTE FUNCTION crm_lifecycle_on_lead();

-- A deal opened with this contact -> qualified; a deal won -> customer.
CREATE OR REPLACE FUNCTION crm_lifecycle_on_deal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_kind text;
BEGIN
    IF NEW.contact_id IS NULL THEN
        RETURN NULL;
    END IF;
    SELECT kind INTO v_kind FROM pipeline_stages WHERE workspace_id = NEW.workspace_id AND id = NEW.stage_id;
    IF v_kind = 'won' AND (TG_OP = 'INSERT' OR OLD.stage_id IS DISTINCT FROM NEW.stage_id OR OLD.contact_id IS DISTINCT FROM NEW.contact_id) THEN
        PERFORM crm_advance_lifecycle(NEW.workspace_id, NEW.contact_id, 'customer', 'deal_won');
    ELSIF v_kind = 'open' AND (TG_OP = 'INSERT' OR OLD.contact_id IS DISTINCT FROM NEW.contact_id) THEN
        PERFORM crm_advance_lifecycle(NEW.workspace_id, NEW.contact_id, 'qualified', 'deal_opened');
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS deals_lifecycle ON deals;
CREATE TRIGGER deals_lifecycle AFTER INSERT OR UPDATE OF stage_id, contact_id ON deals
    FOR EACH ROW EXECUTE FUNCTION crm_lifecycle_on_deal();

-- ---- scores: one row per contact and company, marked stale when a fact changes ----

-- Every contact and company has its score row from the start, so a change that
-- lands while a rescore runs always has a row to mark (a rescore locks the
-- rows it scores; the mark waits for it, then marks the row stale again).
CREATE OR REPLACE FUNCTION crm_icp_new_subject() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'contacts' THEN
        INSERT INTO icp_scores (workspace_id, contact_id) VALUES (NEW.workspace_id, NEW.id) ON CONFLICT DO NOTHING;
    ELSE
        INSERT INTO icp_scores (workspace_id, company_id) VALUES (NEW.workspace_id, NEW.id) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS contacts_icp_new ON contacts;
CREATE TRIGGER contacts_icp_new AFTER INSERT ON contacts FOR EACH ROW EXECUTE FUNCTION crm_icp_new_subject();
DROP TRIGGER IF EXISTS companies_icp_new ON companies;
CREATE TRIGGER companies_icp_new AFTER INSERT ON companies FOR EACH ROW EXECUTE FUNCTION crm_icp_new_subject();

-- Marks a contact's score, its company's, and the company's people's (a
-- company's deal or activity is a signal for everyone who works there).
CREATE OR REPLACE FUNCTION crm_icp_mark(p_ws text, p_contact text, p_company text) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    v_company text := p_company;
BEGIN
    IF p_contact IS NOT NULL THEN
        UPDATE icp_scores SET stale = true WHERE workspace_id = p_ws AND contact_id = p_contact AND NOT stale;
        IF v_company IS NULL THEN
            SELECT company_id INTO v_company FROM contacts WHERE workspace_id = p_ws AND id = p_contact;
        END IF;
    END IF;
    IF v_company IS NOT NULL THEN
        UPDATE icp_scores SET stale = true WHERE workspace_id = p_ws AND NOT stale
          AND (company_id = v_company OR contact_id IN (SELECT id FROM contacts WHERE workspace_id = p_ws AND company_id = v_company));
    END IF;
END $$;

-- A contact's own facts (title, tags, company, WhatsApp consent) are its
-- company's too: the company's signals read its people.
CREATE OR REPLACE FUNCTION crm_icp_contact_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.title, NEW.tags, NEW.company_id, NEW.whatsapp_opt_in_at, NEW.whatsapp_opt_out_at)
       IS DISTINCT FROM (OLD.title, OLD.tags, OLD.company_id, OLD.whatsapp_opt_in_at, OLD.whatsapp_opt_out_at) THEN
        PERFORM crm_icp_mark(NEW.workspace_id, NEW.id, NEW.company_id);
        IF OLD.company_id IS DISTINCT FROM NEW.company_id AND OLD.company_id IS NOT NULL THEN
            PERFORM crm_icp_mark(OLD.workspace_id, NULL, OLD.company_id);
        END IF;
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS contacts_icp_changed ON contacts;
CREATE TRIGGER contacts_icp_changed AFTER UPDATE ON contacts
    FOR EACH ROW EXECUTE FUNCTION crm_icp_contact_touch();

-- A company's own facts, which are also its people's.
CREATE OR REPLACE FUNCTION crm_icp_company_touch() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.name, NEW.domain, NEW.industry, NEW.size, NEW.country, NEW.notes)
       IS DISTINCT FROM (OLD.name, OLD.domain, OLD.industry, OLD.size, OLD.country, OLD.notes) THEN
        PERFORM crm_icp_mark(NEW.workspace_id, NULL, NEW.id);
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS companies_icp_changed ON companies;
CREATE TRIGGER companies_icp_changed AFTER UPDATE ON companies
    FOR EACH ROW EXECUTE FUNCTION crm_icp_company_touch();

CREATE OR REPLACE FUNCTION crm_icp_on_deal() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM crm_icp_mark(OLD.workspace_id, OLD.contact_id, OLD.company_id);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        PERFORM crm_icp_mark(NEW.workspace_id, NEW.contact_id, NEW.company_id);
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS deals_icp ON deals;
CREATE TRIGGER deals_icp AFTER INSERT OR DELETE OR UPDATE OF stage_id, contact_id, company_id ON deals
    FOR EACH ROW EXECUTE FUNCTION crm_icp_on_deal();

-- Subscriptions, conversations and leads: each belongs to one contact.
CREATE OR REPLACE FUNCTION crm_icp_on_contact_row() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM crm_icp_mark(OLD.workspace_id, OLD.contact_id, NULL);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        PERFORM crm_icp_mark(NEW.workspace_id, NEW.contact_id, NULL);
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS list_subscriptions_icp ON list_subscriptions;
CREATE TRIGGER list_subscriptions_icp AFTER INSERT OR DELETE OR UPDATE OF status ON list_subscriptions
    FOR EACH ROW EXECUTE FUNCTION crm_icp_on_contact_row();
DROP TRIGGER IF EXISTS conversations_icp ON conversations;
CREATE TRIGGER conversations_icp AFTER INSERT OR DELETE OR UPDATE OF contact_id ON conversations
    FOR EACH ROW EXECUTE FUNCTION crm_icp_on_contact_row();
DROP TRIGGER IF EXISTS leads_icp ON leads;
CREATE TRIGGER leads_icp AFTER INSERT OR DELETE OR UPDATE OF contact_id, converted_at ON leads
    FOR EACH ROW EXECUTE FUNCTION crm_icp_on_contact_row();

-- Activities: on a contact, a company or a deal.
CREATE OR REPLACE FUNCTION crm_icp_on_activity() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v_ws text;
    v_contact text;
    v_company text;
    v_deal text;
BEGIN
    IF TG_OP = 'DELETE' THEN
        v_ws := OLD.workspace_id; v_contact := OLD.contact_id; v_company := OLD.company_id; v_deal := OLD.deal_id;
    ELSE
        v_ws := NEW.workspace_id; v_contact := NEW.contact_id; v_company := NEW.company_id; v_deal := NEW.deal_id;
    END IF;
    PERFORM crm_icp_mark(v_ws, v_contact, v_company);
    IF v_deal IS NOT NULL THEN
        PERFORM crm_icp_mark(v_ws, d.contact_id, d.company_id) FROM deals d WHERE d.workspace_id = v_ws AND d.id = v_deal;
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS activities_icp ON activities;
CREATE TRIGGER activities_icp AFTER INSERT OR DELETE ON activities
    FOR EACH ROW EXECUTE FUNCTION crm_icp_on_activity();

-- A score row for every contact and company that exists already.
INSERT INTO icp_scores (workspace_id, contact_id)
SELECT c.workspace_id, c.id FROM contacts c
WHERE NOT EXISTS (SELECT 1 FROM icp_scores s WHERE s.workspace_id = c.workspace_id AND s.contact_id = c.id);
INSERT INTO icp_scores (workspace_id, company_id)
SELECT co.workspace_id, co.id FROM companies co
WHERE NOT EXISTS (SELECT 1 FROM icp_scores s WHERE s.workspace_id = co.workspace_id AND s.company_id = co.id);

-- ---- backfill: the stage each existing contact has already reached ---------------
--
-- Only contacts that have never had a stage: a re-run changes nothing. The
-- furthest stage the CRM already shows them at, one history row each.
WITH reached AS (
    SELECT c.workspace_id, c.id,
           CASE
             WHEN EXISTS (SELECT 1 FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
                          WHERE d.workspace_id = c.workspace_id AND d.contact_id = c.id AND st.kind = 'won') THEN 'customer'
             WHEN EXISTS (SELECT 1 FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
                          WHERE d.workspace_id = c.workspace_id AND d.contact_id = c.id AND st.kind = 'open') THEN 'qualified'
             WHEN EXISTS (SELECT 1 FROM leads l WHERE l.workspace_id = c.workspace_id AND l.contact_id = c.id AND l.converted_at IS NOT NULL) THEN 'lead'
             WHEN EXISTS (SELECT 1 FROM list_subscriptions s WHERE s.workspace_id = c.workspace_id AND s.contact_id = c.id AND s.status = 'subscribed') THEN 'subscriber'
             ELSE ''
           END AS stage
    FROM contacts c
    WHERE c.lifecycle = ''
      AND NOT EXISTS (SELECT 1 FROM contact_lifecycle_events e WHERE e.workspace_id = c.workspace_id AND e.contact_id = c.id)
), moved AS (
    UPDATE contacts c SET lifecycle = r.stage, lifecycle_at = now()
    FROM reached r WHERE c.workspace_id = r.workspace_id AND c.id = r.id AND r.stage <> ''
    RETURNING c.workspace_id, c.id, c.lifecycle
)
INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source)
SELECT workspace_id, id, '', lifecycle, 'backfill' FROM moved;

-- ---- row-level security ------------------------------------------------------------

ALTER TABLE contact_lifecycle_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE contact_lifecycle_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contact_lifecycle_events;
CREATE POLICY tenant_isolation ON contact_lifecycle_events
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE icp_scores ENABLE ROW LEVEL SECURITY;
ALTER TABLE icp_scores FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON icp_scores;
CREATE POLICY tenant_isolation ON icp_scores
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE icp_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE icp_profiles FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON icp_profiles;
CREATE POLICY tenant_isolation ON icp_profiles
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
