-- MH-562: importing chats/senders from a workspace's connections (WhatsApp,
-- an IMAP mailbox, and later Telegram/Slack/Discord/workflow channels —
-- MH-563/564) into contacts and inbox threads. Foreign keys and indexes are
-- added in internal/db/post/156_import.sql, after every schema fragment (the
-- same split internal/db/schema/crm.sql uses).
--
-- Every row carries workspace_id and is isolated by RLS (internal/db/post/20_rls.sql).

-- Who is in an imported group conversation, whether or not they were also
-- imported as a contact (MH-562: "participants optionally imported as
-- contacts" — this table remembers every participant either way, so the
-- thread's member list is always complete).
CREATE TABLE IF NOT EXISTS conversation_participants (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    conversation_id text        NOT NULL,
    contact_id      text,
    identity        text        NOT NULL,
    name            text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, conversation_id, identity)
);

-- One row per import run, so "background import with progress" (MH-562) is a
-- read, not a held connection — the same shape campaigns already use for
-- sent_count/failed_count.
CREATE TABLE IF NOT EXISTS import_jobs (
    id                     text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id           text        NOT NULL,
    source                 text        NOT NULL,
    connection_id          text        NOT NULL DEFAULT '',
    status                 text        NOT NULL DEFAULT 'running',
    total                  integer     NOT NULL DEFAULT 0,
    done                   integer     NOT NULL DEFAULT 0,
    contacts_created       integer     NOT NULL DEFAULT 0,
    contacts_reused        integer     NOT NULL DEFAULT 0,
    conversations_created  integer     NOT NULL DEFAULT 0,
    conversations_reused   integer     NOT NULL DEFAULT 0,
    messages_imported      integer     NOT NULL DEFAULT 0,
    skipped                integer     NOT NULL DEFAULT 0,
    last_error             text        NOT NULL DEFAULT '',
    created_by             text        NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CHECK (status IN ('running', 'done', 'failed'))
);
