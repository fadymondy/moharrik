-- YouTube (MH-441/442): foreign keys, indexes, RLS. Idempotent.

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['youtube_channels', 'youtube_videos', 'youtube_comments', 'youtube_analytics'] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = t || '_resource_fk') THEN
            EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (workspace_id, resource_id)
                REFERENCES google_resources (workspace_id, id) ON DELETE CASCADE', t, t || '_resource_fk');
        END IF;
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('DROP POLICY IF EXISTS tenant_isolation ON %I', t);
        EXECUTE format($p$CREATE POLICY tenant_isolation ON %I
            USING (workspace_id = current_setting('app.workspace_id', true))
            WITH CHECK (workspace_id = current_setting('app.workspace_id', true))$p$, t);
    END LOOP;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'youtube_quota_workspace_fk') THEN
        ALTER TABLE youtube_quota ADD CONSTRAINT youtube_quota_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS youtube_videos_order_idx ON youtube_videos (workspace_id, resource_id, published_at DESC, video_id DESC);
CREATE INDEX IF NOT EXISTS youtube_comments_video_idx ON youtube_comments (workspace_id, resource_id, video_id, answered, like_count DESC);

-- The ledger is the owner pool's alone. FORCE RLS with no policy for the app
-- role means that even a stray grant could not read another tenant's use.
ALTER TABLE youtube_quota ENABLE ROW LEVEL SECURITY;
ALTER TABLE youtube_quota FORCE ROW LEVEL SECURITY;

-- Per-step sync status (the channel, its uploads, its comments, YouTube
-- Analytics): one step failing no longer reads as the whole sync failing,
-- and each says why. Added after 83 first shipped, so ALTER, idempotently.
ALTER TABLE youtube_channels ADD COLUMN IF NOT EXISTS step_status jsonb NOT NULL DEFAULT '{}';
