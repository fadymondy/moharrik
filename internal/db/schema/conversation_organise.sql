-- MH-657: how a member organises the inbox, WhatsApp's chat-row menu.
--
-- conversation_prefs is PER MEMBER, like WhatsApp's own: one member pinning,
-- archiving, muting or starring a chat must not rearrange a teammate's inbox.
-- muted_until is when the mute ends ('9999-12-31' = always); a past value is
-- the same as not muted. Constraints, RLS and indexes live in
-- internal/db/post/180_conversation_organise.sql.
CREATE TABLE IF NOT EXISTS conversation_prefs (
    workspace_id    text        NOT NULL,
    conversation_id text        NOT NULL,
    user_id         text        NOT NULL,
    pinned_at       timestamptz,
    archived_at     timestamptz,
    muted_until     timestamptz,
    favourite       boolean     NOT NULL DEFAULT false,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, conversation_id, user_id)
);

-- A workspace's labels (name + a colour from the tag palette), SHARED by the
-- team: a label is how a team sorts its customers, not a private note.
CREATE TABLE IF NOT EXISTS conversation_labels (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    name         text        NOT NULL,
    color        text        NOT NULL DEFAULT 'gray',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE TABLE IF NOT EXISTS conversation_label_links (
    workspace_id    text        NOT NULL,
    conversation_id text        NOT NULL,
    label_id        text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, conversation_id, label_id)
);
