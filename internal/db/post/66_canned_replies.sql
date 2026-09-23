-- Canned replies (MH-383): foreign keys, the picker's order, and row-level
-- security. The table is internal/db/schema/canned_reply.sql.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'canned_replies_workspace_fk') THEN
        ALTER TABLE canned_replies ADD CONSTRAINT canned_replies_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- The author may leave; the reply stays the workspace's.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'canned_replies_created_by_fk') THEN
        ALTER TABLE canned_replies ADD CONSTRAINT canned_replies_created_by_fk
            FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS canned_replies_order_idx ON canned_replies (workspace_id, position, lower(title));

ALTER TABLE canned_replies ENABLE ROW LEVEL SECURITY;
ALTER TABLE canned_replies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON canned_replies;
CREATE POLICY tenant_isolation ON canned_replies
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
