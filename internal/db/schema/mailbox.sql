-- The inbox's email channel (MH-133): one mailbox per workspace, polled over
-- IMAP, with replies going out through the workspace's existing SMTP settings.
--
-- Polling rather than an inbound webhook or an MX record: it needs no provider
-- account, no DNS and no public endpoint, so a workspace can point it at the
-- support@ mailbox it already has. Same reasoning as the trends collector.
--
-- One row per workspace (workspace_id is the primary key). Isolated by RLS like
-- every other table here.
CREATE TABLE IF NOT EXISTS mailboxes (
    workspace_id    text        PRIMARY KEY,
    imap_host       text        NOT NULL,
    imap_port       integer     NOT NULL DEFAULT 993,
    username        text        NOT NULL,
    -- Sealed with AES-256-GCM under AUTH_SECRET, exactly like a webhook
    -- source's signing secret. It is never returned by any endpoint.
    password_sealed text        NOT NULL DEFAULT '',
    -- 'tls' connects with TLS from the start (993); 'starttls' upgrades a plain
    -- connection (143); 'none' is plaintext, which sends this password in the
    -- clear and exists only for a mail server on the same host or a trusted
    -- network. The SMTP settings offer the same three, and the UI says plainly
    -- what 'none' costs.
    tls_mode        text        NOT NULL DEFAULT 'tls',
    folder          text        NOT NULL DEFAULT 'INBOX',
    enabled         boolean     NOT NULL DEFAULT true,
    last_polled_at  timestamptz,
    last_error      text        NOT NULL DEFAULT '',
    last_error_at   timestamptz,
    message_count   integer     NOT NULL DEFAULT 0,
    created_by      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (tls_mode IN ('tls', 'starttls', 'none'))
);
