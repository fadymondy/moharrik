-- WhatsApp notifications for members (MH-411).
--
-- A member opts in with their OWN number, verified by a code sent from the
-- workspace's WhatsApp connection, and chooses which events reach them, their
-- quiet hours and a daily cap. Events are batched so a burst becomes one
-- message. A second delivery channel beside web push (MH-378) for the same
-- events. Ported from fadymondy.com's internal/whatsapp feed/batch/store, made
-- per member and per workspace.
--
-- Foreign keys, row-level security and the widened whatsapp_sends kinds:
-- internal/db/post/116_member_whatsapp.sql.

-- One member's settings in one workspace.
CREATE TABLE IF NOT EXISTS member_whatsapp (
    workspace_id    text        NOT NULL,
    user_id         text        NOT NULL,
    -- Digits, country code first. Pending until verified_at is set.
    phone           text        NOT NULL DEFAULT '',
    verified_at     timestamptz,
    -- The one-time verification code: its hash only, when it expires, how
    -- many wrong tries it has had, and when it was sent (one a minute).
    code_hash       text        NOT NULL DEFAULT '',
    code_expires_at timestamptz,
    code_attempts   integer     NOT NULL DEFAULT 0,
    code_sent_at    timestamptz,
    -- Paused without forgetting the number.
    enabled         boolean     NOT NULL DEFAULT true,
    -- Which events: automation_failed | lead | notification.
    kinds           text[]      NOT NULL DEFAULT '{automation_failed,lead}',
    -- Quiet hours as minutes after local midnight; equal means none. The
    -- window may wrap midnight (22:00-07:00 is 1320-420).
    quiet_start     integer     NOT NULL DEFAULT 1320,
    quiet_end       integer     NOT NULL DEFAULT 420,
    -- IANA zone; empty = the workspace's.
    timezone        text        NOT NULL DEFAULT '',
    -- Messages (not events) per local day.
    daily_cap       integer     NOT NULL DEFAULT 20,
    -- How long events are gathered after the first one before they go as one
    -- message. 0 = at the next tick.
    batch_minutes   integer     NOT NULL DEFAULT 5,
    -- The language the messages are written in.
    locale          text        NOT NULL DEFAULT 'en',
    last_attempt_at timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id),
    CHECK (quiet_start BETWEEN 0 AND 1439 AND quiet_end BETWEEN 0 AND 1439),
    CHECK (daily_cap BETWEEN 1 AND 200),
    CHECK (batch_minutes BETWEEN 0 AND 120),
    CHECK (locale IN ('en', 'ar'))
);

-- The outbox: one row per event per member. Pending while sent_at is NULL.
CREATE TABLE IF NOT EXISTS member_whatsapp_events (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    user_id      text        NOT NULL,
    kind         text        NOT NULL,
    title        text        NOT NULL,
    body         text        NOT NULL DEFAULT '',
    -- A path inside the app (/{locale}/w/{slug}/...), made absolute on send.
    link         text        NOT NULL DEFAULT '',
    -- The source row, so a rescan never queues the same event twice.
    dedupe_key   text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    sent_at      timestamptz,
    message_id   text        NOT NULL DEFAULT '',
    UNIQUE (workspace_id, user_id, dedupe_key)
);

-- The send log: one row per message attempted to a member. The daily cap
-- counts the ones that went.
CREATE TABLE IF NOT EXISTS member_whatsapp_messages (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    user_id      text        NOT NULL,
    -- batch | test
    kind         text        NOT NULL DEFAULT 'batch',
    events       integer     NOT NULL DEFAULT 0,
    ok           boolean     NOT NULL DEFAULT true,
    error        text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Where the feed has read each source up to, per workspace. The first scan
-- sets it to "now", so switching notifications on never sends the backlog.
CREATE TABLE IF NOT EXISTS member_whatsapp_marks (
    workspace_id text        NOT NULL,
    source       text        NOT NULL,
    mark         timestamptz NOT NULL,
    PRIMARY KEY (workspace_id, source)
);
