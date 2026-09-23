-- Subscribe forms (MH-318): how a website collects a subscriber.
--
-- The double opt-in machinery already existed — pending status, a confirm
-- token, the confirmation email, /api/mail/confirm and /api/mail/unsubscribe.
-- What was missing was the entry point: the only way to create a subscriber
-- was an authenticated workspace call, so a customer's own site could not
-- actually subscribe anyone.
--
-- Shaped like chat_widgets (MH-132) on purpose. The same problem — a public
-- endpoint a browser posts to, with no session — already has an answer in this
-- codebase, and a second, different answer would be one more thing to get
-- wrong.
CREATE TABLE IF NOT EXISTS subscribe_forms (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    -- What the form collects (MH-390): 'subscribe' puts an address on a list;
    -- 'inquiry' files a lead (internal/leads). One table, one key namespace and
    -- one origin allow-list for both, because the risk is the same — a public
    -- key that lets a stranger's browser write into this workspace.
    kind            text        NOT NULL DEFAULT 'subscribe',
    -- Which list a submission lands on. The form is the public face of one
    -- list, so a site can have several (newsletter, product updates) without
    -- exposing which lists exist. NULL for an inquiry form, which has no list
    -- (the CHECK is in internal/db/post/32_leads.sql).
    list_id         text,
    name            text        NOT NULL,
    -- The value in the URL. Unique across the WHOLE table, not per workspace:
    -- the lookup happens before any workspace is known, exactly as
    -- chat_widgets.public_key does.
    public_key      text        NOT NULL,
    -- Comma-separated browser origins. EMPTY ALLOWS NOTHING — a form with no
    -- origins set is closed, not open. It is the same default the chat widget
    -- and the Mahaam feedback key use, and it is the safe direction for the
    -- mistake: a form that silently accepts posts from anywhere is a way to
    -- fill somebody's list with addresses they never asked for.
    allowed_origins text        NOT NULL DEFAULT '',
    enabled         boolean     NOT NULL DEFAULT true,
    -- What the visitor is told after posting. Kept here rather than in the
    -- snippet so it can be changed without every site re-embedding the script,
    -- and so it can be written in both languages.
    thanks_en       text        NOT NULL DEFAULT '',
    thanks_ar       text        NOT NULL DEFAULT '',
    created_by      text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (public_key)
);

CREATE INDEX IF NOT EXISTS subscribe_forms_workspace_idx ON subscribe_forms (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS subscribe_forms_list_idx ON subscribe_forms (workspace_id, list_id);
