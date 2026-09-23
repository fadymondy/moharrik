-- Contact lifecycle and the ideal-customer profile (MH-395).
--
-- The lifecycle and score columns on contacts and companies, the triggers that
-- move a contact's stage on real events and mark scores stale, the backfill and
-- row-level security are in internal/db/post/106_lifecycle_icp.sql (one file, so
-- a renumber is a rename). Every row carries workspace_id.

-- Every change of a contact's lifecycle stage, and what caused it: a real event
-- (a confirmed subscription, a converted lead, a deal opened or won), a member's
-- hand (with who), or the one-time backfill from what the CRM already held.
CREATE TABLE IF NOT EXISTS contact_lifecycle_events (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    contact_id    text        NOT NULL,
    from_stage    text        NOT NULL DEFAULT '',
    to_stage      text        NOT NULL,
    source        text        NOT NULL,
    actor_user_id text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CHECK (from_stage IN ('', 'subscriber', 'lead', 'qualified', 'customer')),
    CHECK (to_stage IN ('', 'subscriber', 'lead', 'qualified', 'customer')),
    CHECK (source IN ('subscribed', 'lead_converted', 'deal_opened', 'deal_won', 'manual', 'backfill'))
);

-- One ideal-customer profile per workspace: the criteria and their weights, as
-- data (internal/icp). No row = the default profile.
CREATE TABLE IF NOT EXISTS icp_profiles (
    workspace_id text        PRIMARY KEY,
    criteria     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    updated_by   text,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Each contact's and each company's score against the profile, and the reasons
-- it was made of (internal/icp.Reason). Derived data: rewritten by
-- internal/icp.Rescore whenever stale is set (by the triggers in the post
-- file, when a fact under the score changes) or the score is a day old.
-- Kept off the contact and company rows so a rescore never counts as an edit
-- of them. Exactly one of contact_id / company_id.
CREATE TABLE IF NOT EXISTS icp_scores (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    contact_id   text,
    company_id   text,
    score        smallint,
    reasons      jsonb       NOT NULL DEFAULT '[]'::jsonb,
    stale        boolean     NOT NULL DEFAULT true,
    scored_at    timestamptz,
    CHECK ((contact_id IS NULL) <> (company_id IS NULL)),
    CHECK (score BETWEEN 0 AND 100)
);
