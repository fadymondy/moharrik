-- The inbox's email channel (MH-133, extended by MH-555): several IMAP
-- mailboxes per workspace, each polled independently, the way a workspace can
-- already have several SMTP services (MH-412) — one workspace, several
-- inboxes feeding one thread list.
--
-- This is the shape for a FRESH database. An existing one still has the
-- MH-133 table (workspace_id as primary key, none of the columns below the
-- comma after username) — CREATE TABLE IF NOT EXISTS does not touch it, so
-- internal/db/post/150_mailboxes.sql carries the ALTER TABLE upgrade for
-- that case, column by column, ending in the same shape as this file. Every
-- statement there is written to be a no-op HERE, on a database that already
-- has every column this file declares.
--
-- id PK (not workspace_id) is what makes "several" possible; UNIQUE
-- (workspace_id, id) is the composite key every in-workspace foreign key here
-- points at, for the reason internal/db/schema/crm.sql gives.
CREATE TABLE IF NOT EXISTS mailboxes (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    -- What the settings page and MCP tools call it ("Support", "Sales").
    name            text        NOT NULL DEFAULT 'Mailbox',
    -- The workspace's first/only mailbox is primary by default; a new thread
    -- with nothing else to go on (e.g. a manually started conversation) uses
    -- it. Exactly one primary per workspace: internal/db/post/150_mailboxes.sql.
    is_primary      boolean     NOT NULL DEFAULT false,
    imap_host       text        NOT NULL,
    imap_port       integer     NOT NULL DEFAULT 993,
    username        text        NOT NULL,
    -- Sealed with AES-256-GCM under AUTH_SECRET (internal/secret). Never
    -- returned by any endpoint — audit_redact also redacts it generically
    -- because the column name ends in _sealed.
    password_sealed text        NOT NULL DEFAULT '',
    -- 'tls' connects with TLS from the start (993); 'starttls' upgrades a plain
    -- connection (143); 'none' is plaintext, which sends this password in the
    -- clear and exists only for a mail server on the same host or a trusted
    -- network.
    tls_mode        text        NOT NULL DEFAULT 'tls',
    folder          text        NOT NULL DEFAULT 'INBOX',
    -- Which of the workspace's SMTP services (MH-412) a reply on THIS
    -- mailbox's conversations goes out through. NULL = the workspace's
    -- default service (marketing.LoadSMTP), same as before MH-555.
    smtp_service_id text,
    enabled         boolean     NOT NULL DEFAULT true,
    last_polled_at  timestamptz,
    last_error      text        NOT NULL DEFAULT '',
    last_error_at   timestamptz,
    -- The last "test connection" check, kept separate from the poller's own
    -- last_error: a mailbox can poll fine and still be worth re-testing after
    -- a password change, and a failed test must not look like a failed poll.
    last_test_at    timestamptz,
    last_test_ok    boolean,
    last_test_error text        NOT NULL DEFAULT '',
    message_count   integer     NOT NULL DEFAULT 0,
    created_by      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    CHECK (tls_mode IN ('tls', 'starttls', 'none'))
);
