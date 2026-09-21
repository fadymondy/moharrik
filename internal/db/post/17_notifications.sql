-- Notifications: same-tenant foreign keys, identity/index. Schema fragments
-- apply in name order (17 sorts after 16_crm.sql, before 20_rls.sql).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'webhook_sources_workspace_fk') THEN
        ALTER TABLE webhook_sources ADD CONSTRAINT webhook_sources_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'webhook_sources_creator_fk') THEN
        ALTER TABLE webhook_sources ADD CONSTRAINT webhook_sources_creator_fk
            FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notification_items_workspace_fk') THEN
        ALTER TABLE notification_items ADD CONSTRAINT notification_items_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- A deleted source keeps its items (source_label is the record); the link
    -- itself is allowed to dangle, which is why this FK is same-tenant but
    -- ON DELETE SET NULL rather than CASCADE.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notification_items_source_fk') THEN
        ALTER TABLE notification_items ADD CONSTRAINT notification_items_source_fk
            FOREIGN KEY (workspace_id, source_id) REFERENCES webhook_sources (workspace_id, id) ON DELETE SET NULL (source_id);
    END IF;
END $$;

-- A redelivery of the same event from the same source is the same row.
CREATE UNIQUE INDEX IF NOT EXISTS notification_items_identity_uq ON notification_items (workspace_id, source_id, external_id) WHERE external_id <> '';
CREATE INDEX IF NOT EXISTS notification_items_recent_idx ON notification_items (workspace_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS notification_items_unread_idx ON notification_items (workspace_id, occurred_at DESC) WHERE read_at IS NULL;
CREATE INDEX IF NOT EXISTS notification_items_saved_idx ON notification_items (workspace_id, occurred_at DESC) WHERE saved_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS webhook_sources_workspace_idx ON webhook_sources (workspace_id, created_at DESC);
