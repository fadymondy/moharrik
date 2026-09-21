-- Trends: same-tenant foreign keys and indexes (after every schema fragment).
-- 19 sorts after 18_inbox.sql and before 20_rls.sql.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feed_sources_workspace_fk') THEN
        ALTER TABLE feed_sources ADD CONSTRAINT feed_sources_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'topics_workspace_fk') THEN
        ALTER TABLE topics ADD CONSTRAINT topics_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'news_items_workspace_fk') THEN
        ALTER TABLE news_items ADD CONSTRAINT news_items_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Deleting a feed keeps what it already collected; source_name is the label
    -- that survives, so the link is allowed to dangle.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'news_items_source_fk') THEN
        ALTER TABLE news_items ADD CONSTRAINT news_items_source_fk
            FOREIGN KEY (workspace_id, source_id) REFERENCES feed_sources (workspace_id, id) ON DELETE SET NULL (source_id);
    END IF;
END $$;

-- One feed per URL per workspace, and a second poll of the same entry is the same row.
CREATE UNIQUE INDEX IF NOT EXISTS feed_sources_url_uniq ON feed_sources (workspace_id, lower(url));
CREATE UNIQUE INDEX IF NOT EXISTS news_items_identity_uq ON news_items (workspace_id, source_id, external_id) WHERE source_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS news_items_recent_idx ON news_items (workspace_id, published_at DESC);
CREATE INDEX IF NOT EXISTS news_items_saved_idx ON news_items (workspace_id, published_at DESC) WHERE saved_at IS NOT NULL;
-- "Items matching this topic", answered from the array itself.
CREATE INDEX IF NOT EXISTS news_items_topics_idx ON news_items USING GIN (topic_ids);
CREATE UNIQUE INDEX IF NOT EXISTS topics_name_uniq ON topics (workspace_id, lower(name));
-- The collector's queue: enabled feeds, least recently fetched first.
CREATE INDEX IF NOT EXISTS feed_sources_due_idx ON feed_sources (last_fetched_at NULLS FIRST) WHERE enabled;
