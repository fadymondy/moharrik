-- YouTube per enabled channel (MH-441, MH-442), ported from fadymondy.com's
-- single-owner tables (db/youtube.postgres.sql) and scoped to a workspace and
-- one google_resources row each.
--
--   youtube_channels    the channel's own numbers and the new-comment watermark
--   youtube_videos      every upload, private / unlisted / scheduled included
--   youtube_comments    top-level comment threads (top comments, new comments)
--   youtube_analytics   cached YouTube Analytics answers per scope and window (MH-442)
--   youtube_quota       the Data API quota ledger, per Pacific day and workspace
--
-- The quota ledger is written and read on the OWNER pool only (a global cap is
-- a cross-tenant sum by definition), with the workspace named in every
-- statement; the app role has no grant on it. Everything else is RLS-isolated
-- (internal/db/post/83_youtube.sql).

CREATE TABLE IF NOT EXISTS youtube_channels (
    workspace_id         text        NOT NULL,
    resource_id          text        NOT NULL,
    channel_id           text        NOT NULL DEFAULT '',
    title                text        NOT NULL DEFAULT '',
    handle               text        NOT NULL DEFAULT '',
    avatar_url           text        NOT NULL DEFAULT '',
    uploads_playlist_id  text        NOT NULL DEFAULT '',
    subscriber_count     bigint      NOT NULL DEFAULT 0,
    subscribers_hidden   boolean     NOT NULL DEFAULT false,
    video_count          bigint      NOT NULL DEFAULT 0,
    view_count           bigint      NOT NULL DEFAULT 0,
    -- Comments published before this are never announced: the first sync
    -- seeds it, so connecting does not flood the notification center.
    comments_seen_until  timestamptz,
    -- The uploads walk reached the end of the playlist last time.
    uploads_complete     boolean     NOT NULL DEFAULT false,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id)
);

CREATE TABLE IF NOT EXISTS youtube_videos (
    workspace_id       text        NOT NULL,
    resource_id        text        NOT NULL,
    video_id           text        NOT NULL,
    title              text        NOT NULL DEFAULT '',
    description        text        NOT NULL DEFAULT '',
    tags               jsonb       NOT NULL DEFAULT '[]',
    thumbnail_url      text        NOT NULL DEFAULT '',
    published_at       timestamptz,
    -- status.publishAt: when a scheduled video goes public.
    scheduled_at       timestamptz,
    privacy_status     text        NOT NULL DEFAULT '',
    upload_status      text        NOT NULL DEFAULT '',
    duration_seconds   bigint      NOT NULL DEFAULT 0,
    view_count         bigint      NOT NULL DEFAULT 0,
    -- NULL when YouTube does not say: likes hidden, comments turned off.
    like_count         bigint,
    comment_count      bigint,
    -- Newest first in the uploads playlist, 0-based.
    position           bigint      NOT NULL DEFAULT 0,
    -- Still in the uploads playlist at the last complete walk.
    present            boolean     NOT NULL DEFAULT true,
    comments_synced_at timestamptz,
    -- The video's comments are turned off (commentsDisabled).
    comments_disabled  boolean     NOT NULL DEFAULT false,
    synced_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, video_id)
);

CREATE TABLE IF NOT EXISTS youtube_comments (
    workspace_id       text        NOT NULL,
    resource_id        text        NOT NULL,
    comment_id         text        NOT NULL,
    video_id           text        NOT NULL DEFAULT '',
    author             text        NOT NULL DEFAULT '',
    author_channel_id  text        NOT NULL DEFAULT '',
    author_avatar_url  text        NOT NULL DEFAULT '',
    text               text        NOT NULL DEFAULT '',
    like_count         bigint      NOT NULL DEFAULT 0,
    reply_count        bigint      NOT NULL DEFAULT 0,
    -- The channel has replied in the thread.
    answered           boolean     NOT NULL DEFAULT false,
    -- From an order=relevance read of the video: a top comment.
    top                boolean     NOT NULL DEFAULT false,
    published_at       timestamptz,
    notified_at        timestamptz,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, comment_id)
);

CREATE TABLE IF NOT EXISTS youtube_analytics (
    workspace_id  text        NOT NULL,
    resource_id   text        NOT NULL,
    -- 'channel' or a video id.
    scope         text        NOT NULL,
    days          integer     NOT NULL,
    payload       jsonb       NOT NULL DEFAULT '{}',
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, scope, days)
);

CREATE TABLE IF NOT EXISTS youtube_quota (
    -- The Pacific day: YouTube's quota resets at midnight Pacific time.
    day           date        NOT NULL,
    workspace_id  text        NOT NULL,
    units         bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (day, workspace_id)
);

-- YouTube Reporting (MH-443): the report jobs this workspace's channel has
-- (created once per report family) and every daily report downloaded, kept
-- beyond Google's 60 days. Numbered with the rest of YouTube (83), whose
-- files re-apply idempotently on every deploy.
CREATE TABLE IF NOT EXISTS youtube_report_jobs (
    workspace_id       text        NOT NULL,
    resource_id        text        NOT NULL,
    job_id             text        NOT NULL,
    report_type_id     text        NOT NULL DEFAULT '',
    name               text        NOT NULL DEFAULT '',
    google_created_at  timestamptz,
    -- This workspace created it (rather than finding it already there).
    created_here       boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (workspace_id, resource_id, job_id)
);

CREATE TABLE IF NOT EXISTS youtube_reports (
    workspace_id       text        NOT NULL,
    resource_id        text        NOT NULL,
    report_id          text        NOT NULL,
    job_id             text        NOT NULL DEFAULT '',
    report_type_id     text        NOT NULL DEFAULT '',
    -- The day the report covers (Google: one day per report).
    start_time         timestamptz,
    end_time           timestamptz,
    google_created_at  timestamptz,
    -- The CSV, as downloaded (capped at download).
    content            text        NOT NULL DEFAULT '',
    row_count          bigint      NOT NULL DEFAULT 0,
    bytes              bigint      NOT NULL DEFAULT 0,
    downloaded_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, resource_id, report_id)
);
