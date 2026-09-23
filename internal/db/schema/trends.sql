-- Trends: the feeds a workspace watches, the topics it cares about, and the
-- items collected from them.
--
-- Every row carries workspace_id and is isolated by RLS (internal/db/post/20_rls.sql).
-- Links use composite foreign keys (workspace_id, x_id) -> x (workspace_id, id),
-- for the reason internal/db/schema/crm.sql gives.

-- One feed to poll. RSS and Atom are the same table: the parser reads both, and
-- `kind` only records which shape it turned out to be.
CREATE TABLE IF NOT EXISTS feed_sources (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    name            text        NOT NULL,
    url             text        NOT NULL,
    kind            text        NOT NULL DEFAULT 'rss',
    enabled         boolean     NOT NULL DEFAULT true,
    last_fetched_at timestamptz,
    -- The last FAILED fetch and why, so "why is nothing coming in" has an
    -- answer on the page rather than only in the logs.
    last_error      text        NOT NULL DEFAULT '',
    last_error_at   timestamptz,
    item_count      bigint      NOT NULL DEFAULT 0,
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (kind IN ('rss', 'atom', 'api'))
);

-- A thing worth watching: a name and the words that mean it. An item matches a
-- topic when any keyword appears in its title or summary (case-insensitive).
CREATE TABLE IF NOT EXISTS topics (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    name         text        NOT NULL,
    keywords     text[]      NOT NULL DEFAULT '{}',
    enabled      boolean     NOT NULL DEFAULT true,
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

-- One collected item. topic_ids is an array rather than a join table: an item
-- matches a handful of topics at most, the only query is "items matching this
-- topic", and a GIN index answers that directly.
CREATE TABLE IF NOT EXISTS news_items (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    source_id    text,
    -- A snapshot of the source's name, so a deleted feed's items keep a label.
    source_name  text        NOT NULL DEFAULT '',
    -- The feed's own id for the entry (guid, or the link when there is none):
    -- a second poll of the same feed is the same row, not a duplicate.
    external_id  text        NOT NULL,
    title        text        NOT NULL,
    url          text        NOT NULL DEFAULT '',
    summary      text        NOT NULL DEFAULT '',
    author       text        NOT NULL DEFAULT '',
    topic_ids    text[]      NOT NULL DEFAULT '{}',
    published_at timestamptz NOT NULL DEFAULT now(),
    saved_at     timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

-- ---- MH-385 / MH-387: topics that are stories, and sources that schedule themselves ----------
--
-- The feed reader above becomes a trends center: items are clustered into
-- STORIES (trend_clusters), each scored with its reasons, summarised, and
-- reviewed. The keyword `topics` above stay what they were — watch lists — and
-- become one input to a story's rank (relevance). Foreign keys, indexes and RLS
-- are in internal/db/post/70_trend_clusters.sql.

-- How a source is collected: an RSS/Atom feed, or a public API where there is
-- no feed worth reading (internal/trends/api.go). params carries the API's
-- list or tag; catalog_key names the catalog entry it was added from.
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS provider         text   NOT NULL DEFAULT 'feed';
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS params           jsonb  NOT NULL DEFAULT '{}';
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS catalog_key      text   NOT NULL DEFAULT '';
-- Each source keeps its own schedule (6, 12 or 24 hours) instead of one
-- global ticker deciding for all of them.
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS interval_minutes integer NOT NULL DEFAULT 720;
-- ok | failed | over_budget | '' (never run). last_error keeps the reason.
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS last_status      text   NOT NULL DEFAULT '';
ALTER TABLE feed_sources ADD COLUMN IF NOT EXISTS last_added       integer NOT NULL DEFAULT 0;

-- The article link with scheme, www, tracking parameters and fragment gone
-- (trends.CanonicalURL): one article is one row, whichever feed carried it.
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS canonical_url text    NOT NULL DEFAULT '';
-- The publisher as shown. For most feeds it is the source's name; an
-- aggregator (Google News) names the publisher of each row.
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS outlet        text    NOT NULL DEFAULT '';
-- API sources: position on the source's list, points/reactions, comments.
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS rank          integer NOT NULL DEFAULT 0;
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS points        integer NOT NULL DEFAULT 0;
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS comments      integer NOT NULL DEFAULT 0;
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS provider      text    NOT NULL DEFAULT 'feed';
-- The story it belongs to; NULL until the clusterer has placed it.
ALTER TABLE news_items ADD COLUMN IF NOT EXISTS cluster_id    text;

-- One story: items from any number of sources about the same thing.
CREATE TABLE IF NOT EXISTS trend_clusters (
    id              text             PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text             NOT NULL,
    -- The UTC day of the story's latest item: the list's date filter and the
    -- first part of its keyset. A story that gets new coverage moves up.
    day             date             NOT NULL DEFAULT CURRENT_DATE,
    title           text             NOT NULL DEFAULT '',
    summary         text             NOT NULL DEFAULT '',
    -- "extractive" (deterministic) or "provider/model" when a model wrote it.
    summary_source  text             NOT NULL DEFAULT 'extractive',
    -- "deterministic" or "deterministic+embed:<model>".
    cluster_method  text             NOT NULL DEFAULT 'deterministic',
    source_count    integer          NOT NULL DEFAULT 0,
    item_count      integer          NOT NULL DEFAULT 0,
    outlets         text[]           NOT NULL DEFAULT '{}',
    -- feed_sources ids of the items, for the source filter.
    source_ids      text[]           NOT NULL DEFAULT '{}',
    score           double precision NOT NULL DEFAULT 0,
    -- JSON array of {kind, points, label, detail, count}: the reason for the
    -- rank, largest part first. The page renders these as chips.
    reasons         jsonb            NOT NULL DEFAULT '[]',
    first_seen_at   timestamptz      NOT NULL DEFAULT now(),
    last_seen_at    timestamptz      NOT NULL DEFAULT now(),
    -- The review workflow. A story is open until one of these is set.
    reviewed_at     timestamptz,
    dismissed_at    timestamptz,
    saved_at        timestamptz,
    reviewed_by     text,
    created_at      timestamptz      NOT NULL DEFAULT now(),
    updated_at      timestamptz      NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

-- A workspace's collection budget: how many fetches a day its sources may
-- spend, whatever their intervals ask for. Counted per UTC day.
CREATE TABLE IF NOT EXISTS trend_settings (
    workspace_id        text        PRIMARY KEY,
    fetch_budget_per_day integer    NOT NULL DEFAULT 60,
    budget_day          date        NOT NULL DEFAULT CURRENT_DATE,
    fetches_today       integer     NOT NULL DEFAULT 0,
    updated_at          timestamptz NOT NULL DEFAULT now()
);
