-- The MCP surface (MH-380, MH-386, MH-409): WhatsApp send log, agent send
-- confirmations, and when the WhatsApp webhook last delivered.
--
-- Batch C's reserved block is 60–64; everything is in this one file so that a
-- renumber is a rename. Every statement is idempotent.

-- When Evolution last reached the webhook with the right token. "Webhook
-- health" on whatsapp_status is this plus what Evolution says it is set to.
ALTER TABLE whatsapp_links ADD COLUMN IF NOT EXISTS last_hook_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_sends_workspace_fk') THEN
        ALTER TABLE whatsapp_sends ADD CONSTRAINT whatsapp_sends_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_sends_kind_ck') THEN
        ALTER TABLE whatsapp_sends ADD CONSTRAINT whatsapp_sends_kind_ck
            CHECK (kind IN ('reply', 'broadcast', 'direct'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_confirmations_workspace_fk') THEN
        ALTER TABLE whatsapp_confirmations ADD CONSTRAINT whatsapp_confirmations_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Composite, like every CRM link (crm.sql): the FK check runs as the owner
    -- and ignores RLS, so a single-column key could name another workspace's
    -- contact. Deleting the contact drops its pending confirmations.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_confirmations_contact_fk') THEN
        ALTER TABLE whatsapp_confirmations ADD CONSTRAINT whatsapp_confirmations_contact_fk
            FOREIGN KEY (workspace_id, contact_id) REFERENCES contacts (workspace_id, id) ON DELETE CASCADE;
    END IF;
END $$;

-- The day's count and the pacing check both read the newest sends of one workspace.
CREATE INDEX IF NOT EXISTS whatsapp_sends_workspace_created_idx
    ON whatsapp_sends (workspace_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS whatsapp_confirmations_code_uniq
    ON whatsapp_confirmations (workspace_id, code_hash);

ALTER TABLE whatsapp_sends ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_sends FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON whatsapp_sends;
CREATE POLICY tenant_isolation ON whatsapp_sends
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE whatsapp_confirmations ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_confirmations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON whatsapp_confirmations;
CREATE POLICY tenant_isolation ON whatsapp_confirmations
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
