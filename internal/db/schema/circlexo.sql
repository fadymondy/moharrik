-- CircleXO hub integration (internal/circlexo, docs/circlexo.md). Platform data, not tenant-scoped:
-- read before a workspace is known (sign-in, webhooks, MCP token mapping), so owner pool only, no
-- RLS, no app-role grant. Nothing here
-- is used unless CIRCLEXO_ISSUER is set. (The workspace foreign key is added in post/168_circlexo.sql: schema files
-- apply in name order and workspaces.sql comes after this one.)

-- A hub account (its `sub`) and the Moharrik user it signs in as. Unique both ways: one hub account
-- is one Moharrik user, and a Moharrik user has at most one hub account.
CREATE TABLE IF NOT EXISTS circlexo_user_links (
    subject    text        PRIMARY KEY,
    user_id    text        NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    email      text        NOT NULL DEFAULT '',
    linked_via text        NOT NULL DEFAULT 'email' CHECK (linked_via IN ('email', 'created')),
    created_at timestamptz NOT NULL DEFAULT now()
);

-- A hub org and the workspace that serves it. Unique both ways. `status` goes inactive on
-- deprovision (org.app_removed): the workspace and its data stay, the link stops mapping.
CREATE TABLE IF NOT EXISTS circlexo_org_links (
    org_id       text        PRIMARY KEY,
    workspace_id text        NOT NULL UNIQUE,
    org_slug     text        NOT NULL DEFAULT '',
    status       text        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Moharrik sessions started from a hub sign-in, by the hub session id (`sid`) and the JWT issue time,
-- so session.revoked can end exactly those and not the user's other sessions (see guard.go).
CREATE TABLE IF NOT EXISTS circlexo_sessions (
    id         text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    sid        text        NOT NULL,
    user_id    text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issued_at  timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS circlexo_sessions_sid ON circlexo_sessions (sid);
CREATE INDEX IF NOT EXISTS circlexo_sessions_user ON circlexo_sessions (user_id, issued_at) WHERE revoked_at IS NOT NULL;

-- Webhook ids already processed (Standard Webhooks retries), shared by every API replica.
CREATE TABLE IF NOT EXISTS circlexo_webhook_events (
    webhook_id text        PRIMARY KEY,
    received_at timestamptz NOT NULL DEFAULT now()
);
