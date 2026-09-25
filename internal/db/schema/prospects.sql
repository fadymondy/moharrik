-- Prospect discovery (MH-397): research turned into scored companies and
-- leads, with the evidence behind each, reviewed by a person, and drafts of
-- outreach that Moharrik never sends.
--
-- Tenant tables: workspace_id on every row, RLS in internal/db/post/111_prospects.sql,
-- composite foreign keys (workspace_id, x_id).

-- The workspace's ideal customer, as discovery searches for and scores it.
-- One per workspace. (MH-395's lifecycle/ICP scoring of CONTACTS is a
-- separate card; this one scores companies research found.)
--
-- MH-514: the *who* — industries, company sizes, regions, keywords — moved to
-- the workspace's CRM ICP (icp_profiles, internal/icp) so it is described
-- once. The link to icp_profiles is logical (same workspace_id), not a
-- schema-level foreign key — see internal/db/post/514_prospect_icp_link.sql
-- for why a hard FK would force an empty icp_profiles row into existence on
-- prospect discovery's first read, wrongly flipping icp_profiles'
-- "configured" flag on. internal/prospects.GetProfile/SaveProfile read/write
-- the who through internal/icp, not the columns below. industries/keywords/
-- regions are kept here — a rename, not a drop, so the pre-514 history is
-- not lost — but nothing reads them anymore; do not add a new reader.
CREATE TABLE IF NOT EXISTS prospect_profiles (
    workspace_id      text        PRIMARY KEY,
    -- What the workspace sells, in a sentence or two: the outreach draft's pitch.
    offer             text        NOT NULL DEFAULT '',
    -- Dead since MH-514: read from icp_profiles.criteria instead.
    industries        text[]      NOT NULL DEFAULT '{}',
    keywords          text[]      NOT NULL DEFAULT '{}',
    regions           text[]      NOT NULL DEFAULT '{}',
    -- Still live: discovery-only, not part of the CRM ICP.
    exclude_keywords  text[]      NOT NULL DEFAULT '{}',
    -- signal type -> weight 0..3 (0 ignores the signal).
    signal_weights    jsonb       NOT NULL DEFAULT '{}',
    -- What discovery searches, one per pass, in turn. Empty = built from the
    -- industries, keywords, regions and the strongest signals.
    queries           text[]      NOT NULL DEFAULT '{}',
    next_query        integer     NOT NULL DEFAULT 0,
    enabled           boolean     NOT NULL DEFAULT false,
    cadence           text        NOT NULL DEFAULT 'daily',
    next_run_at       timestamptz NOT NULL DEFAULT now(),
    last_run_id       text        NOT NULL DEFAULT '',
    last_run_at       timestamptz,
    last_status       text        NOT NULL DEFAULT '',
    last_note         text        NOT NULL DEFAULT '',
    digest_at         timestamptz NOT NULL DEFAULT 'epoch',
    updated_by        text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (cadence IN ('daily', 'weekly')),
    CHECK (last_status IN ('', 'started', 'over_budget', 'busy', 'not_configured', 'failed', 'no_queries'))
);

-- A company discovery found, scored against the profile.
CREATE TABLE IF NOT EXISTS prospects (
    id                  text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id        text        NOT NULL,
    -- The research suggestion it is (research_companies): its name, domain
    -- and evidence live there.
    suggestion_id       text        NOT NULL,
    -- new (waiting for a person) | accepted | rejected
    status              text        NOT NULL DEFAULT 'new',
    score               real        NOT NULL DEFAULT 0,
    -- [{dimension, points, reason}] — every point explained.
    reasons             jsonb       NOT NULL DEFAULT '[]',
    scored_at           timestamptz NOT NULL DEFAULT now(),
    first_run_id        text        NOT NULL DEFAULT '',
    -- The person to write to, when the pages name one, and where they did.
    contact_name        text        NOT NULL DEFAULT '',
    contact_title       text        NOT NULL DEFAULT '',
    contact_email       text        NOT NULL DEFAULT '',
    contact_source_url  text        NOT NULL DEFAULT '',
    crm_company_id      text,
    crm_contact_id      text,
    crm_deal_id         text,
    reject_reason       text        NOT NULL DEFAULT '',
    decided_by          text        NOT NULL DEFAULT '',
    decided_at          timestamptz,
    digested_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, suggestion_id),
    CHECK (status IN ('new', 'accepted', 'rejected')),
    CHECK (score >= 0 AND score <= 100)
);

-- A person's verdict, and what the prospect looked like when they gave it:
-- the features the next scoring compares against.
CREATE TABLE IF NOT EXISTS prospect_feedback (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    prospect_id   text        NOT NULL,
    verdict       text        NOT NULL,
    reason        text        NOT NULL DEFAULT '',
    -- {"signals": [...], "terms": [...], "regions": [...]}
    features      jsonb       NOT NULL DEFAULT '{}',
    score_at      real        NOT NULL DEFAULT 0,
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (verdict IN ('accepted', 'rejected'))
);

-- An outreach draft. Stored so it can be read again; never sent by Moharrik.
CREATE TABLE IF NOT EXISTS prospect_drafts (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    prospect_id   text        NOT NULL,
    locale        text        NOT NULL DEFAULT 'en',
    subject       text        NOT NULL DEFAULT '',
    body          text        NOT NULL,
    -- 'template' (no model configured) or 'model:<provider>'
    generator     text        NOT NULL DEFAULT 'template',
    evidence_ids  text[]      NOT NULL DEFAULT '{}',
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (locale IN ('en', 'ar'))
);
