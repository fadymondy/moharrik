-- Collected notification sources (MH-384): things the notification center POLLS,
-- beside the webhook sources that post to it. Ported from fadymondy.com's
-- notification_sources (internal/center/collect.go), made per workspace.
--
-- The kinds here are the ones Moharrik can read without anybody's OAuth app:
-- Bluesky (the public AppView), YouTube (a channel's public feed) and any
-- RSS/Atom feed. Gmail and the social networks that need an OAuth app or the
-- browser plugins are listed by the API as "not configured" and never become
-- rows, so there is no source row that cannot collect.
--
-- Every row carries workspace_id and is isolated by RLS
-- (internal/db/post/101_notification_collect.sql).
CREATE TABLE IF NOT EXISTS notification_sources (
    id                text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id      text        NOT NULL,
    kind              text        NOT NULL,
    name              text        NOT NULL,
    -- What to read: a Bluesky handle, a YouTube channel id, a feed URL.
    target            text        NOT NULL,
    enabled           boolean     NOT NULL DEFAULT true,
    interval_minutes  integer     NOT NULL DEFAULT 60,
    -- Alert the members (tab + web push) for new items. push_kinds narrows it
    -- to some item kinds (comma separated, e.g. "reply,mention"); empty = all.
    push_enabled      boolean     NOT NULL DEFAULT false,
    push_kinds        text        NOT NULL DEFAULT '',
    next_run_at       timestamptz,
    last_run_at       timestamptz,
    last_collected_at timestamptz,
    -- '' (never run), ok, failed.
    last_status       text        NOT NULL DEFAULT '',
    last_error        text        NOT NULL DEFAULT '',
    last_item_count   integer     NOT NULL DEFAULT 0,
    created_by        text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (kind IN ('bluesky', 'youtube', 'rss')),
    CHECK (interval_minutes IN (15, 20, 30, 60, 120, 180, 240, 360, 720, 1440)),
    CHECK (last_status IN ('', 'ok', 'failed'))
);
