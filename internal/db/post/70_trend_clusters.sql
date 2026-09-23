-- Trends center (MH-385) and sources (MH-387): constraints, indexes and RLS for
-- the columns and tables added at the end of internal/db/schema/trends.sql.
--
-- Numbered 70: batch E's reserved block (70-74). Every statement is idempotent;
-- moharrik-migrate replays the whole directory on every deploy.

-- An API source is kind 'api'. The inline CHECK in the CREATE TABLE only
-- reaches new databases, so existing ones get it replaced here.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feed_sources_kind_check'
               AND pg_get_constraintdef(oid) NOT LIKE '%api%') THEN
        ALTER TABLE feed_sources DROP CONSTRAINT feed_sources_kind_check;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feed_sources_kind_check') THEN
        ALTER TABLE feed_sources ADD CONSTRAINT feed_sources_kind_check CHECK (kind IN ('rss', 'atom', 'api'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feed_sources_provider_check') THEN
        ALTER TABLE feed_sources ADD CONSTRAINT feed_sources_provider_check CHECK (provider IN ('feed', 'hn', 'devto'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'feed_sources_interval_check') THEN
        ALTER TABLE feed_sources ADD CONSTRAINT feed_sources_interval_check CHECK (interval_minutes IN (360, 720, 1440));
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'trend_clusters_workspace_fk') THEN
        ALTER TABLE trend_clusters ADD CONSTRAINT trend_clusters_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'trend_settings_workspace_fk') THEN
        ALTER TABLE trend_settings ADD CONSTRAINT trend_settings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
    -- Same-tenant link from an item to its story. A story that is rebuilt away
    -- leaves its items unplaced, and the next pass places them again.
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'news_items_cluster_fk') THEN
        ALTER TABLE news_items ADD CONSTRAINT news_items_cluster_fk
            FOREIGN KEY (workspace_id, cluster_id) REFERENCES trend_clusters (workspace_id, id) ON DELETE SET NULL (cluster_id);
    END IF;
END $$;

-- Cross-source dedupe: one article per workspace, however many feeds (or
-- guids inside one feed) carry it. Rows from before this migration have an
-- empty canonical_url, are outside the index, and are backfilled by the
-- collector (trends.BackfillCanonical), which removes their duplicates first.
CREATE UNIQUE INDEX IF NOT EXISTS news_items_canonical_uq
    ON news_items (workspace_id, canonical_url) WHERE canonical_url <> '';
CREATE INDEX IF NOT EXISTS news_items_cluster_idx ON news_items (workspace_id, cluster_id);
CREATE INDEX IF NOT EXISTS news_items_unplaced_idx ON news_items (workspace_id) WHERE cluster_id IS NULL;
-- The raw list's keyset: (published_at, id), newest first.
CREATE INDEX IF NOT EXISTS news_items_page_idx ON news_items (workspace_id, published_at DESC, id DESC);

-- The story list's keyset: (day, score, id), newest day first, best first.
CREATE INDEX IF NOT EXISTS trend_clusters_page_idx ON trend_clusters (workspace_id, day DESC, score DESC, id DESC);
CREATE INDEX IF NOT EXISTS trend_clusters_open_idx ON trend_clusters (workspace_id, day DESC, score DESC)
    WHERE reviewed_at IS NULL AND dismissed_at IS NULL AND saved_at IS NULL;

-- The collector's queue keeps feed_sources_due_idx (19_trends.sql): "due" is
-- now last_fetched_at + the source's own interval, and timestamptz + interval
-- is not immutable, so it cannot be an index expression; the partial index on
-- enabled sources still bounds the scan.

ALTER TABLE trend_clusters ENABLE ROW LEVEL SECURITY;
ALTER TABLE trend_clusters FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON trend_clusters;
CREATE POLICY tenant_isolation ON trend_clusters
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE trend_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE trend_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON trend_settings;
CREATE POLICY tenant_isolation ON trend_settings
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
