-- WhatsApp sends and agent send confirmations (MH-409).
--
-- whatsapp_sends is one row per message this workspace's number sent — a
-- reply from the inbox, a broadcast, or a message an agent started. It is what
-- the day's send count on whatsapp_status is counted from, and what pacing
-- reads: a number that sends in bursts is what WhatsApp's abuse detection looks
-- for, and the cost of that is the workspace's only number.
--
-- Foreign keys and row-level security: internal/db/post/60_mcp_surface.sql.
CREATE TABLE IF NOT EXISTS whatsapp_sends (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    -- reply | broadcast | direct
    kind         text        NOT NULL DEFAULT 'reply',
    -- Digits only, the way WhatsApp numbers are compared everywhere else.
    phone        text        NOT NULL DEFAULT '',
    ok           boolean     NOT NULL DEFAULT true,
    -- WhatsApp's id for the message when it went out; the refusal when not.
    external_id  text        NOT NULL DEFAULT '',
    error        text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- A message an agent (an MCP token) asked to send to a contact with no thread
-- yet. It goes out only when the same call is repeated with the one-time code
-- this row was issued for, bound to that exact contact and text, so an agent
-- cannot open a conversation with a customer on its own say-so in one step.
-- Only the code's hash is stored.
CREATE TABLE IF NOT EXISTS whatsapp_confirmations (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    code_hash    text        NOT NULL,
    contact_id   text        NOT NULL,
    -- sha256 of the exact text, so the confirmed send is the one that was asked.
    text_hash    text        NOT NULL,
    requested_by text        NOT NULL DEFAULT '',
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
