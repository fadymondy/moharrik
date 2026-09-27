-- MH-656: the WhatsApp-style thread.
--
-- messages.status is an outbound message's delivery state, the ticks inside
-- the bubble: '' (unknown — every message from before this file, and every
-- inbound one), 'sent' (the channel accepted it), 'delivered', 'read'.
-- WhatsApp moves it forward from Evolution's messages.update receipts; it is
-- never moved backwards (internal/inbox/receipts.go).
--
-- link_previews' table is in internal/db/schema/link_previews.sql so it exists
-- before migrate.Run grants the app role DML on it (TenantTables).
--
-- Idempotent and safe on production data.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'messages_status_ck') THEN
        ALTER TABLE messages ADD CONSTRAINT messages_status_ck
            CHECK (status IN ('', 'sent', 'delivered', 'read', 'failed'));
    END IF;
END $$;

ALTER TABLE link_previews ENABLE ROW LEVEL SECURITY;
ALTER TABLE link_previews FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON link_previews;
CREATE POLICY tenant_isolation ON link_previews
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
