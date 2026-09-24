-- The research evidence store (MH-388): what a run READ, kept as quotes and
-- claims that can be searched across every run, plus the companies those
-- quotes mention (suggested to the CRM, never added to it without review),
-- the schedules that run research on their own, and the daily budget that
-- bounds them.
--
-- Tenant tables like the rest: workspace_id on every row, RLS in
-- internal/db/post/110_research_evidence.sql, and composite foreign keys
-- (workspace_id, x_id) for the reason internal/db/schema/crm.sql gives.

-- One quote or claim, with where it came from.
--
-- `quote` is ALWAYS verbatim page text. With no model configured it is all
-- there is (kind = 'quote'): a sentence picked from the page because it is
-- about the question or reports a signal. With a model, `claim` is the
-- model's one-line reading of it (kind = 'claim') — and the claim is only kept
-- when its quote is actually on the page, so a claim can never outrun its
-- source.
CREATE TABLE IF NOT EXISTS research_evidence (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    run_id          text        NOT NULL,
    -- The research_sources row it was read from (NULL only if that row was
    -- never stored, which a run logs).
    source_id       text,
    kind            text        NOT NULL DEFAULT 'quote',
    quote           text        NOT NULL,
    claim           text        NOT NULL DEFAULT '',
    source_url      text        NOT NULL,
    source_title    text        NOT NULL DEFAULT '',
    source_domain   text        NOT NULL DEFAULT '',
    -- The company the quote is about, as the page names it, and the key it is
    -- grouped under (a domain when one is known, else the normalised name).
    company_name    text        NOT NULL DEFAULT '',
    company_domain  text        NOT NULL DEFAULT '',
    company_key     text        NOT NULL DEFAULT '',
    -- hiring | funding | launch | expansion | partnership | leadership |
    -- technology | regulatory, or '' for a finding that is only on-topic.
    signal_type     text        NOT NULL DEFAULT '',
    confidence      real        NOT NULL DEFAULT 0,
    -- When the page says it was published, if it says; captured is when we read it.
    published_at    timestamptz,
    captured_at     timestamptz NOT NULL DEFAULT now(),
    -- 'heuristic' or 'model:<provider>'. Never a model's name on a regex's guess.
    extractor       text        NOT NULL DEFAULT 'heuristic',
    content_hash    text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    -- The same sentence twice in one run is one piece of evidence; the same
    -- sentence in two runs is two (each run keeps its own record).
    UNIQUE (workspace_id, run_id, content_hash),
    CHECK (kind IN ('quote', 'claim')),
    CHECK (quote <> '' AND source_url <> '' AND content_hash <> ''),
    CHECK (confidence >= 0 AND confidence <= 1)
);

-- A company research found, offered to the CRM for review.
--
-- status: suggested (waiting for a person) | accepted (a person added it; the
-- CRM company is linked) | dismissed (a person said no — remembered, so the
-- same company is not suggested again by the next run) | linked (it was
-- already in the CRM when research found it; nothing to review).
CREATE TABLE IF NOT EXISTS research_companies (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    company_key     text        NOT NULL,
    name            text        NOT NULL,
    domain          text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'suggested',
    crm_company_id  text,
    evidence_count  integer     NOT NULL DEFAULT 0,
    strongest       real        NOT NULL DEFAULT 0,
    signal_types    text[]      NOT NULL DEFAULT '{}',
    first_run_id    text        NOT NULL DEFAULT '',
    last_run_id     text        NOT NULL DEFAULT '',
    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    reviewed_by     text        NOT NULL DEFAULT '',
    reviewed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, company_key),
    CHECK (status IN ('suggested', 'accepted', 'dismissed', 'linked')),
    CHECK (company_key <> '' AND name <> '')
);

-- Research that runs on its own.
CREATE TABLE IF NOT EXISTS research_schedules (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    name          text        NOT NULL,
    topic         text        NOT NULL,
    -- search: ask the search engine; urls: read these pages.
    mode          text        NOT NULL DEFAULT 'search',
    urls          text[]      NOT NULL DEFAULT '{}',
    -- daily | weekly
    cadence       text        NOT NULL DEFAULT 'daily',
    enabled       boolean     NOT NULL DEFAULT true,
    next_run_at   timestamptz NOT NULL DEFAULT now(),
    last_run_id   text        NOT NULL DEFAULT '',
    last_run_at   timestamptz,
    -- '' | started | over_budget | busy | not_configured | failed
    last_status   text        NOT NULL DEFAULT '',
    last_note     text        NOT NULL DEFAULT '',
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (mode IN ('search', 'urls')),
    CHECK (cadence IN ('daily', 'weekly')),
    CHECK (last_status IN ('', 'started', 'over_budget', 'busy', 'not_configured', 'failed'))
);

-- The daily budget for research nobody pressed a button for: scheduled runs
-- (and, with MH-397, discovery). A person asking a question is not bounded by
-- it; a clock is. One row per workspace, created on first use.
CREATE TABLE IF NOT EXISTS research_settings (
    workspace_id      text        PRIMARY KEY,
    daily_run_budget  integer     NOT NULL DEFAULT 3,
    runs_today        integer     NOT NULL DEFAULT 0,
    budget_day        date        NOT NULL DEFAULT (now() AT TIME ZONE 'UTC')::date,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (daily_run_budget >= 0 AND daily_run_budget <= 50),
    CHECK (runs_today >= 0)
);
