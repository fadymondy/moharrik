-- The trends digest (MH-389): keys, indexes and row-level security.
--
-- Batch P's block is 125–129. Every statement is idempotent.

DO $$
BEGIN
    -- A member's choice belongs to a MEMBERSHIP: someone removed from the
    -- workspace stops getting its digest the moment their membership goes.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'member_trend_digest_member_fk') THEN
        ALTER TABLE member_trend_digest ADD CONSTRAINT member_trend_digest_member_fk
            FOREIGN KEY (workspace_id, user_id) REFERENCES workspace_members (workspace_id, user_id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'trend_digest_sends_workspace_fk') THEN
        ALTER TABLE trend_digest_sends ADD CONSTRAINT trend_digest_sends_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

-- The ticker's queue: who has a digest on at all.
CREATE INDEX IF NOT EXISTS member_trend_digest_on_idx ON member_trend_digest (workspace_id) WHERE frequency <> 'off';
CREATE INDEX IF NOT EXISTS trend_digest_sends_member_idx ON trend_digest_sends (workspace_id, user_id, created_at DESC);

ALTER TABLE member_trend_digest ENABLE ROW LEVEL SECURITY;
ALTER TABLE member_trend_digest FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON member_trend_digest;
CREATE POLICY tenant_isolation ON member_trend_digest
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE trend_digest_sends ENABLE ROW LEVEL SECURITY;
ALTER TABLE trend_digest_sends FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON trend_digest_sends;
CREATE POLICY tenant_isolation ON trend_digest_sends
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
