-- WhatsApp notifications for members (MH-411): keys, row-level security, and
-- the two new kinds of message the workspace's number sends.
--
-- Batch N's reserved block is 115–119. Every statement is idempotent.

DO $$
BEGIN
    -- A member's settings belong to a MEMBERSHIP: someone removed from the
    -- workspace stops being messaged the moment their membership goes.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'member_whatsapp_member_fk') THEN
        ALTER TABLE member_whatsapp ADD CONSTRAINT member_whatsapp_member_fk
            FOREIGN KEY (workspace_id, user_id) REFERENCES workspace_members (workspace_id, user_id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'member_whatsapp_events_member_fk') THEN
        ALTER TABLE member_whatsapp_events ADD CONSTRAINT member_whatsapp_events_member_fk
            FOREIGN KEY (workspace_id, user_id) REFERENCES member_whatsapp (workspace_id, user_id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'member_whatsapp_messages_member_fk') THEN
        ALTER TABLE member_whatsapp_messages ADD CONSTRAINT member_whatsapp_messages_member_fk
            FOREIGN KEY (workspace_id, user_id) REFERENCES member_whatsapp (workspace_id, user_id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'member_whatsapp_marks_workspace_fk') THEN
        ALTER TABLE member_whatsapp_marks ADD CONSTRAINT member_whatsapp_marks_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;

    -- whatsapp_sends learns two kinds: notify (a batched notification to a
    -- member) and verify (a member's verification code). 60_mcp_surface.sql
    -- now creates the wide constraint on a fresh database; an existing one
    -- still has the narrow one, which is replaced here.
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_sends_kind_ck'
               AND pg_get_constraintdef(oid) NOT LIKE '%notify%') THEN
        ALTER TABLE whatsapp_sends DROP CONSTRAINT whatsapp_sends_kind_ck;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'whatsapp_sends_kind_ck') THEN
        ALTER TABLE whatsapp_sends ADD CONSTRAINT whatsapp_sends_kind_ck
            CHECK (kind IN ('reply', 'broadcast', 'direct', 'notify', 'verify'));
    END IF;
END $$;

-- The flush reads one member's pending events, oldest first.
CREATE INDEX IF NOT EXISTS member_whatsapp_events_pending_idx
    ON member_whatsapp_events (workspace_id, user_id, created_at) WHERE sent_at IS NULL;
CREATE INDEX IF NOT EXISTS member_whatsapp_messages_member_idx
    ON member_whatsapp_messages (workspace_id, user_id, created_at DESC);

ALTER TABLE member_whatsapp ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_whatsapp FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON member_whatsapp;
CREATE POLICY tenant_isolation ON member_whatsapp
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE member_whatsapp_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_whatsapp_events FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON member_whatsapp_events;
CREATE POLICY tenant_isolation ON member_whatsapp_events
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE member_whatsapp_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_whatsapp_messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON member_whatsapp_messages;
CREATE POLICY tenant_isolation ON member_whatsapp_messages
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE member_whatsapp_marks ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_whatsapp_marks FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON member_whatsapp_marks;
CREATE POLICY tenant_isolation ON member_whatsapp_marks
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
