-- The inbox's WhatsApp channel (MH-134): the numbers a workspace has linked.
--
-- Evolution API holds each phone session; a row holds the link between a
-- workspace and one instance there. A workspace may link several numbers
-- (one per brand or team, a second one for the assistant), each its own row,
-- instance, webhook and pairing. The instance name is derived from the
-- workspace id (and, after the first number, the row's id), so two workspaces
-- can never share a number and an instance name cannot be guessed into.
--
-- An existing database still has workspace_id as the primary key; it is moved
-- to id by internal/db/post/225_whatsapp_multi_number.sql.
CREATE TABLE IF NOT EXISTS whatsapp_links (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  text        NOT NULL,
    -- What the team calls this number ("Sales", "Assistant"). Optional.
    label         text        NOT NULL DEFAULT '',
    -- The number new messages, campaigns and notifications go from when
    -- nobody picks one. Exactly one per workspace once any number is linked.
    is_default    boolean     NOT NULL DEFAULT false,
    -- Most messages this number may send in one day (workspace time zone);
    -- 0 means no cap beyond the pacing every send already keeps.
    daily_cap     integer     NOT NULL DEFAULT 0,
    -- The Evolution instance name. Derived, not chosen: see internal/whatsapp.
    instance      text        NOT NULL,
    -- The unguessable id in the public webhook URL. Unique across the table,
    -- because the hook resolves the workspace FROM it, before any workspace is
    -- known — the same shape as a webhook source id or a chat widget key.
    hook_id       text        NOT NULL,
    -- Sealed at rest. Evolution sends it back in a header on every delivery,
    -- and it is the whole authorization for a public endpoint.
    token_sealed  text        NOT NULL DEFAULT '',
    -- close | connecting | open, as last reported. The truth is Evolution's
    -- connectionState, which the page reads live.
    state         text        NOT NULL DEFAULT 'close',
    -- The number that actually ended up linked, once a phone has scanned.
    phone         text        NOT NULL DEFAULT '',
    last_error    text        NOT NULL DEFAULT '',
    last_error_at timestamptz,
    linked_at     timestamptz,
    created_by    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (hook_id),
    UNIQUE (instance)
);
