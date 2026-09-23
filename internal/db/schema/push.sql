-- Web Push subscriptions (MH-378): one row per member per browser/device per
-- workspace, so a customer's message reaches a member whose Moharrik tab is
-- closed.
--
-- Standards Web Push (RFC 8030) with VAPID (RFC 8292) and message encryption
-- (RFC 8291): the endpoint is the push service's URL for this browser, and the
-- two keys are what the payload is encrypted to. The keys are sealed at rest
-- (internal/alerts/secret.go); the endpoint alone cannot deliver anything,
-- because a push service only accepts a message signed with this server's
-- VAPID key and nobody else can read one encrypted to these keys.
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    user_id       text        NOT NULL,
    endpoint      text        NOT NULL,
    -- JSON {"p256dh": ..., "auth": ...}, AES-256-GCM sealed.
    keys_sealed   text        NOT NULL,
    user_agent    text        NOT NULL DEFAULT '',
    -- The language the member used when enabling it: what the lock-screen
    -- title is written in.
    locale        text        NOT NULL DEFAULT 'en',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    -- The last push the service accepted, and the last one it refused (a fixed
    -- sentence and status, never a response body).
    last_ok_at    timestamptz,
    last_error    text        NOT NULL DEFAULT '',
    last_error_at timestamptz,
    UNIQUE (workspace_id, id),
    -- One browser is one device per workspace: re-enabling on the same browser
    -- (or another member signing in on it) moves the row, never duplicates it.
    UNIQUE (workspace_id, endpoint),
    CHECK (locale IN ('en', 'ar'))
);

CREATE INDEX IF NOT EXISTS push_subscriptions_member_idx ON push_subscriptions (workspace_id, user_id);
