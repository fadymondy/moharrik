-- Research runs (MH-227): ask a question, and get back an answer with its
-- sources — searched with SearXNG, read with Firecrawl, summarised by a model.
--
-- Every table carries workspace_id and is isolated by RLS
-- (internal/db/post/20_rls.sql), and links use composite foreign keys
-- (workspace_id, x_id) -> x (workspace_id, id), for the reason
-- internal/db/schema/crm.sql gives: a foreign-key check runs as the table
-- owner and ignores RLS, so a single-column FK would let one workspace point
-- a row at another workspace's.
--
-- THE SOURCES ARE THE POINT. A summary with no sources is a model's opinion,
-- and this is meant to answer "what is actually being said about X" — so every
-- page that went into the answer is kept, with its URL, in the order it was
-- ranked, whether or not it could be read.

CREATE TABLE IF NOT EXISTS research_runs (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    -- What was asked, in the operator's own words. Kept verbatim: the query
    -- sent to the search engine is derived from it and may be narrowed, and
    -- the two must not be confused when someone reads the run back.
    topic         text        NOT NULL,
    -- The query actually put to SearXNG.
    query         text        NOT NULL DEFAULT '',
    -- queued | running | ok | failed | cancelled
    status        text        NOT NULL DEFAULT 'queued',
    -- The model's answer. Empty when no model was configured, which is a
    -- supported way to run this: search + read + keep, and read it yourself.
    summary       text        NOT NULL DEFAULT '',
    -- Which provider answered, so a surprising summary can be traced to the
    -- model that wrote it rather than guessed at.
    model         text        NOT NULL DEFAULT '',
    error         text        NOT NULL DEFAULT '',
    -- How many results were found, and how many could actually be read. The
    -- gap between them is the most useful number on the page: it is how you
    -- tell "nothing was written about this" from "everything is behind a
    -- paywall".
    found_count   integer     NOT NULL DEFAULT 0,
    read_count    integer     NOT NULL DEFAULT 0,
    requested_by  text        NOT NULL DEFAULT '',
    started_at    timestamptz,
    finished_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE INDEX IF NOT EXISTS research_runs_workspace_idx ON research_runs (workspace_id, created_at DESC);
-- "Is this workspace already running one" is asked before every run.
CREATE INDEX IF NOT EXISTS research_runs_live_idx ON research_runs (workspace_id, status)
    WHERE status IN ('queued', 'running');

CREATE TABLE IF NOT EXISTS research_sources (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    run_id       text        NOT NULL,
    -- Where it came in the search results. Kept so the order on the page is
    -- the order the search engine gave, not whatever the database returns.
    position     integer     NOT NULL DEFAULT 0,
    url          text        NOT NULL,
    title        text        NOT NULL DEFAULT '',
    -- The search engine's own one-line description, kept even when the page
    -- could not be fetched — it is often the only thing left.
    snippet      text        NOT NULL DEFAULT '',
    -- The page as markdown. Trimmed: a run that stored ten full articles
    -- verbatim would be the biggest thing in the database within a week.
    content      text        NOT NULL DEFAULT '',
    -- Whether the page was actually read, and why not when it was not. A
    -- source that failed still belongs in the list: "these five pages exist
    -- and I could not read three of them" is an honest answer, and silently
    -- dropping them makes a thin summary look well-sourced.
    fetched      boolean     NOT NULL DEFAULT false,
    error        text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, run_id, position)
);

CREATE INDEX IF NOT EXISTS research_sources_run_idx ON research_sources (workspace_id, run_id, position);
