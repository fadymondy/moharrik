-- The trends digest (MH-389): trends reach the rest of the product.
--
-- A member opts in to an email of the workspace's top trend stories — daily
-- or weekly, at an hour in their own time zone, in their language — sent
-- through the workspace's SMTP service. The same top stories answer "trends"
-- (or "ترندات") sent from a member's verified WhatsApp number to the
-- workspace's own. Ported from fadymondy.com's morning digest trend section
-- (internal/radar/digest/trends.go) and its /fm trends command
-- (internal/whatsapp/actions.go), made per member and per workspace.
--
-- Foreign keys and row-level security: internal/db/post/127_trend_digest.sql.

-- One member's choice in one workspace.
CREATE TABLE IF NOT EXISTS member_trend_digest (
    workspace_id    text        NOT NULL,
    user_id         text        NOT NULL,
    -- off | daily | weekly
    frequency       text        NOT NULL DEFAULT 'off',
    -- The local hour it goes out, and for weekly the day (0 = Sunday).
    hour            integer     NOT NULL DEFAULT 8,
    weekday         integer     NOT NULL DEFAULT 0,
    -- IANA zone; empty = the workspace's.
    timezone        text        NOT NULL DEFAULT '',
    locale          text        NOT NULL DEFAULT 'en',
    -- How many stories.
    top             integer     NOT NULL DEFAULT 5,
    -- The period last claimed (d:2026-09-24 or w:2026-09-20), so a digest
    -- goes once per period whatever the ticker does.
    last_period     text        NOT NULL DEFAULT '',
    last_sent_at    timestamptz,
    last_status     text        NOT NULL DEFAULT '',
    last_error      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id),
    CHECK (frequency IN ('off', 'daily', 'weekly')),
    CHECK (hour BETWEEN 0 AND 23),
    CHECK (weekday BETWEEN 0 AND 6),
    CHECK (locale IN ('en', 'ar')),
    CHECK (top BETWEEN 3 AND 10)
);

-- Every digest and WhatsApp trends answer sent (or attempted) to a member.
CREATE TABLE IF NOT EXISTS trend_digest_sends (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    user_id      text        NOT NULL,
    -- digest | test | whatsapp
    kind         text        NOT NULL,
    period       text        NOT NULL DEFAULT '',
    -- sent | empty | failed
    status       text        NOT NULL,
    topics       integer     NOT NULL DEFAULT 0,
    error        text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (kind IN ('digest', 'test', 'whatsapp')),
    CHECK (status IN ('sent', 'empty', 'failed'))
);
