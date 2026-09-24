-- Collected notification sources (MH-384): same-tenant keys, RLS, and the
-- link from an item to the source that collected it.
--
-- 101 sorts before 10_constraints.sql (string order), which is fine: every
-- table it touches exists after the schema phase, and nothing here depends on
-- another post file.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notification_sources_workspace_fk') THEN
        ALTER TABLE notification_sources ADD CONSTRAINT notification_sources_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notification_sources_creator_fk') THEN
        ALTER TABLE notification_sources ADD CONSTRAINT notification_sources_creator_fk
            FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT;
    END IF;
END $$;

-- Which collected source an item came from. Nullable and SET NULL for the same
-- reason as source_id: deleting a source keeps its history (source_name is the
-- label). Added by ALTER because notification_items already exists.
ALTER TABLE notification_items ADD COLUMN IF NOT EXISTS feed_source_id text;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notification_items_feed_source_fk') THEN
        ALTER TABLE notification_items ADD CONSTRAINT notification_items_feed_source_fk
            FOREIGN KEY (workspace_id, feed_source_id) REFERENCES notification_sources (workspace_id, id) ON DELETE SET NULL (feed_source_id);
    END IF;
END $$;

-- A notification seen on two polls is one row.
CREATE UNIQUE INDEX IF NOT EXISTS notification_items_feed_identity_uq
    ON notification_items (workspace_id, feed_source_id, external_id)
    WHERE feed_source_id IS NOT NULL AND external_id <> '';
-- The source and kind facets, and the per-source filter.
CREATE INDEX IF NOT EXISTS notification_items_feed_source_idx ON notification_items (workspace_id, feed_source_id, occurred_at DESC) WHERE feed_source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS notification_items_source_idx ON notification_items (workspace_id, source_id, occurred_at DESC) WHERE source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS notification_items_kind_idx ON notification_items (workspace_id, kind, occurred_at DESC);
-- The collector's queue.
CREATE INDEX IF NOT EXISTS notification_sources_due_idx ON notification_sources (next_run_at) WHERE enabled;
CREATE INDEX IF NOT EXISTS notification_sources_workspace_idx ON notification_sources (workspace_id, created_at DESC);

-- notification_sources is read by the owner pool (a.SysDB) only to find which
-- sources are due across workspaces; every read and write of a source's own
-- rows and items then runs on the app pool, scoped to that one workspace
-- (internal/notifications/collect.go), the same shape as the trends collector.
ALTER TABLE notification_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_sources FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON notification_sources;
CREATE POLICY tenant_isolation ON notification_sources
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
