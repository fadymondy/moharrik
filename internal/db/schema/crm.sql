-- CRM: companies, a per-workspace pipeline, deals and an activity timeline.
-- Every row carries workspace_id and is isolated by RLS (internal/db/post/20_rls.sql).
-- The CRM columns on `contacts` (company, phone, title, owner, tags) are added in
-- internal/db/post/16_crm.sql: schema fragments apply in name order, and this file
-- sorts before mail_marketing.sql, which creates contacts.
--
-- Links between CRM rows use composite foreign keys (workspace_id, x_id) ->
-- x (workspace_id, id). A foreign-key check runs as the table owner and ignores
-- RLS, so a single-column FK would let one workspace point a deal at another
-- workspace's contact; the composite key makes that impossible in the database.

CREATE TABLE IF NOT EXISTS companies (
    id            text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    name          text        NOT NULL,
    domain        text        NOT NULL DEFAULT '',
    industry      text        NOT NULL DEFAULT '',
    size          text        NOT NULL DEFAULT '',
    notes         text        NOT NULL DEFAULT '',
    owner_user_id text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (size IN ('', '1-10', '11-50', '51-200', '201-1000', '1000+'))
);

-- The stage kind drives closing: a deal in a won/lost stage is closed.
CREATE TABLE IF NOT EXISTS pipeline_stages (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    name         text        NOT NULL,
    position     bigint      NOT NULL DEFAULT 0,
    kind         text        NOT NULL DEFAULT 'open',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (kind IN ('open', 'won', 'lost'))
);

-- value_minor is in the currency's minor unit (cents); currency defaults to the
-- workspace's. position orders cards inside a stage column.
CREATE TABLE IF NOT EXISTS deals (
    id             text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id   text             NOT NULL,
    title          text             NOT NULL,
    value_minor    bigint           NOT NULL DEFAULT 0,
    currency       text             NOT NULL DEFAULT 'USD',
    stage_id       text             NOT NULL,
    position       double precision NOT NULL DEFAULT 0,
    contact_id     text,
    company_id     text,
    owner_user_id  text,
    expected_close date,
    closed_at      timestamptz,
    created_by     text             NOT NULL,
    created_at     timestamptz      NOT NULL DEFAULT now(),
    updated_at     timestamptz      NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (value_minor >= 0),
    CHECK (currency ~ '^[A-Z]{3}$')
);

-- A note, call, meeting or task on a contact, a company and/or a deal.
CREATE TABLE IF NOT EXISTS activities (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    kind         text        NOT NULL,
    body         text        NOT NULL,
    due_at       timestamptz,
    done_at      timestamptz,
    contact_id   text,
    company_id   text,
    deal_id      text,
    created_by   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (kind IN ('note', 'call', 'meeting', 'task')),
    CHECK (contact_id IS NOT NULL OR company_id IS NOT NULL OR deal_id IS NOT NULL)
);
