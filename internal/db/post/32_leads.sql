-- Lead intake (MH-390): inquiry forms and the leads they file.
--
-- Everything for this card is in this one file (the column on subscribe_forms,
-- the leads foreign keys, indexes and row-level security) so that renumbering
-- it against another branch that also claims 32 is a rename and nothing else.
-- Every statement is idempotent.

-- ---- subscribe_forms gains a kind ------------------------------------------
--
-- Added by ALTER because the CREATE in internal/db/schema is IF NOT EXISTS, so
-- an existing database would never get the column. Existing forms are all
-- subscribe forms, which is what the default says.
ALTER TABLE subscribe_forms ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'subscribe';

-- An inquiry form files a lead, not a subscription, so it has no list.
ALTER TABLE subscribe_forms ALTER COLUMN list_id DROP NOT NULL;

DO $$
BEGIN
    -- A subscribe form must still name a list, and an inquiry form must not:
    -- the public subscribe endpoint would otherwise meet a form with nowhere
    -- to put the address, and an inquiry form pointing at a list would look
    -- like it subscribes people when it does not.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'subscribe_forms_kind_ck') THEN
        ALTER TABLE subscribe_forms ADD CONSTRAINT subscribe_forms_kind_ck CHECK (
            (kind = 'subscribe' AND list_id IS NOT NULL) OR (kind = 'inquiry' AND list_id IS NULL)
        );
    END IF;
END $$;

-- ---- leads ------------------------------------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'leads_workspace_fk') THEN
        ALTER TABLE leads ADD CONSTRAINT leads_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, for the reason internal/db/schema/crm.sql gives: a foreign-key
    -- check runs as the table owner and ignores RLS, so a single-column key
    -- would let one workspace's lead point at another workspace's form,
    -- contact, company or deal. SET NULL on the one column, so deleting what a
    -- lead points at keeps the lead.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'leads_form_fk') THEN
        ALTER TABLE leads ADD CONSTRAINT leads_form_fk
            FOREIGN KEY (workspace_id, form_id) REFERENCES subscribe_forms (workspace_id, id) ON DELETE SET NULL (form_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'leads_contact_fk') THEN
        ALTER TABLE leads ADD CONSTRAINT leads_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE SET NULL (contact_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'leads_company_fk') THEN
        ALTER TABLE leads ADD CONSTRAINT leads_company_fk
            FOREIGN KEY (workspace_id, company_id) REFERENCES companies (workspace_id, id) ON DELETE SET NULL (company_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'leads_deal_fk') THEN
        ALTER TABLE leads ADD CONSTRAINT leads_deal_fk
            FOREIGN KEY (workspace_id, deal_id) REFERENCES deals (workspace_id, id) ON DELETE SET NULL (deal_id);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'leads_converted_by_fk') THEN
        ALTER TABLE leads ADD CONSTRAINT leads_converted_by_fk
            FOREIGN KEY (converted_by) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS leads_recent_idx ON leads (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS leads_status_idx ON leads (workspace_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS leads_email_idx ON leads (workspace_id, lower(email));

-- The public endpoint resolves the form on the owner pool (it has no workspace
-- until the key matches) and then writes the lead INSIDE that workspace on the
-- app pool, so this policy governs every row a visitor's submission creates.
ALTER TABLE leads ENABLE ROW LEVEL SECURITY;
ALTER TABLE leads FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON leads;
CREATE POLICY tenant_isolation ON leads
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
