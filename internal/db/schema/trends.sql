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
    CHECK (kind IN ('rss', 'atom'))
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
