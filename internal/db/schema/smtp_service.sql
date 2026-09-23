-- Several SMTP services per workspace (MH-412).
--
-- One smtp_settings row per workspace meant a large campaign and the
-- double-opt-in confirmation emails shared one provider's rate limits and one
-- sender reputation: a big send delayed confirmations, and a spam complaint on
-- a campaign hurt transactional deliverability. Now a workspace names several
-- services; one is the DEFAULT (transactional mail: confirmations, inbox
-- replies, test emails) and one is the CAMPAIGN DEFAULT (a campaign that names
-- no service of its own). They may be the same row.
--
-- The password is AES-GCM sealed (internal/marketing/secret.go) and never
-- returned by any API, REST or MCP.
--
-- Foreign keys, the one-default indexes, row-level security and the copy of
-- the legacy smtp_settings row: internal/db/post/115_smtp_services.sql.
CREATE TABLE IF NOT EXISTS smtp_services (
    id                  text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id        text        NOT NULL,
    name                text        NOT NULL,
    host                text        NOT NULL,
    port                bigint      NOT NULL DEFAULT 587,
    username            text        NOT NULL DEFAULT '',
    password_sealed     text        NOT NULL DEFAULT '',
    from_name           text        NOT NULL DEFAULT '',
    from_email          text        NOT NULL,
    tls_mode            text        NOT NULL DEFAULT 'starttls',
    -- Transactional mail goes through this one.
    is_default          boolean     NOT NULL DEFAULT false,
    -- A campaign with no service of its own goes through this one.
    is_campaign_default boolean     NOT NULL DEFAULT false,
    -- The last test send: when, whether it worked, and the server's refusal.
    last_test_at        timestamptz,
    last_test_ok        boolean,
    last_test_error     text        NOT NULL DEFAULT '',
    created_by          text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    CHECK (tls_mode IN ('none', 'starttls', 'tls'))
);
