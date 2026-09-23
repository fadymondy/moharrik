-- What a workspace reads from its connected Google accounts (MH-440): the GA4
-- properties, Search Console sites and YouTube channels each account can see,
-- and which of them this workspace has switched on.
--
-- Discovery fills this table from Google (Analytics Admin accountSummaries,
-- webmasters/v3/sites, YouTube channels.list mine=true); a person or the
-- auto-enable rule sets `enabled`. Everything the GA4, Search Console and
-- YouTube cards store hangs off a row here, so disconnecting the account
-- (which deletes its connection) takes its data with it.
--
-- Isolated by RLS (internal/db/post/80_google_resources.sql).

CREATE TABLE IF NOT EXISTS google_resources (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    connection_id   text        NOT NULL,
    -- ga4_property | gsc_site | yt_channel
    kind            text        NOT NULL,
    -- As Google names it: "properties/123456789", "sc-domain:example.com" or
    -- "https://example.com/", "UCxxxxxxxxxxxxxxxxxxxxxx".
    external_id     text        NOT NULL,
    display_name    text        NOT NULL DEFAULT '',
    -- The GA4 account a property sits in; the permission level of a site.
    parent_name     text        NOT NULL DEFAULT '',
    -- Whatever else discovery learnt that the pages show (a channel's avatar
    -- and handle, a property's type). Never a token.
    details         jsonb       NOT NULL DEFAULT '{}',
    -- Returned by the latest discovery. A resource the account lost access to
    -- is kept (with its data) and shown as unavailable rather than deleted.
    available       boolean     NOT NULL DEFAULT true,
    enabled         boolean     NOT NULL DEFAULT false,
    -- Switched on by the "only one of its kind" rule rather than by a person.
    auto_enabled    boolean     NOT NULL DEFAULT false,
    -- When a person last switched it on or off. Once anyone has chosen for a
    -- kind, the auto-enable rule never overrides them.
    chosen_at       timestamptz,
    last_synced_at  timestamptz,
    next_sync_at    timestamptz NOT NULL DEFAULT now(),
    -- Set while a sync runs, so two never run at once for one resource.
    sync_started_at timestamptz,
    -- Consecutive failed syncs: the backoff grows with it.
    failures        integer     NOT NULL DEFAULT 0,
    -- Google said slow down (429 / quota): no on-demand call before this.
    backoff_until   timestamptz,
    -- The last sync's failure, in words an admin can act on ('' = fine).
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, connection_id, kind, external_id),
    CHECK (kind IN ('ga4_property', 'gsc_site', 'yt_channel'))
);

-- One row per (connection, product): when discovery last ran for it, what it
-- found and why it failed. A 403 "API not enabled" lands here and on the page.
CREATE TABLE IF NOT EXISTS google_discoveries (
    workspace_id    text        NOT NULL,
    connection_id   text        NOT NULL,
    -- analytics | search_console | youtube
    product         text        NOT NULL,
    discovered_at   timestamptz NOT NULL DEFAULT now(),
    found           integer     NOT NULL DEFAULT 0,
    error           text        NOT NULL DEFAULT '',
    PRIMARY KEY (workspace_id, connection_id, product),
    CHECK (product IN ('analytics', 'search_console', 'youtube'))
);
