-- The inbox's WhatsApp channel (MH-134): one linked number per workspace.
--
-- Evolution API holds the phone session; this row holds the link between a
-- workspace and its instance there. The instance name is derived from the
-- workspace id, so two workspaces can never share a number and an instance
-- name cannot be guessed into.
CREATE TABLE IF NOT EXISTS whatsapp_links (
    workspace_id  text        PRIMARY KEY,
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
