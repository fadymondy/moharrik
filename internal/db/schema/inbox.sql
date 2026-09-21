-- Inbox: one conversation per customer, whatever channel they used, and the
-- messages in it. Chat is the channel P0 ships; email and WhatsApp reuse the
-- same two tables (channel + external_id) when their transports land.
--
-- Every row carries workspace_id and is isolated by RLS (internal/db/post/20_rls.sql).
-- Links use composite foreign keys (workspace_id, x_id) -> x (workspace_id, id),
-- for the reason internal/db/schema/crm.sql gives: a foreign-key check runs as
-- the table owner and ignores RLS, so a single-column FK would let one
-- workspace point a row at another workspace's.

-- An embeddable chat widget: POST /api/chat/{public_key}/messages, no session.
-- The key in the URL plus the Origin allow-list are the authorization, the same
-- shape internal/notifications uses for POST /api/hooks/{id}. allowed_origins is
-- a comma-separated list; empty means the widget is blocked (the Mahaam feedback
-- key's own "no origins set = refuse everything" default, which is the safe one).
CREATE TABLE IF NOT EXISTS chat_widgets (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    name            text        NOT NULL,
    public_key      text        NOT NULL,
    allowed_origins text        NOT NULL DEFAULT '',
    enabled         boolean     NOT NULL DEFAULT true,
    greeting        text        NOT NULL DEFAULT '',
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (public_key)
);

-- One conversation per customer per channel. contact_id links it to the CRM
-- once the customer is known; until then customer_name/customer_email hold what
-- the visitor typed, so an anonymous chat is still answerable.
CREATE TABLE IF NOT EXISTS conversations (
    id               text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id     text        NOT NULL,
    channel          text        NOT NULL DEFAULT 'chat',
    widget_id        text,
    contact_id       text,
    customer_name    text        NOT NULL DEFAULT '',
    customer_email   text        NOT NULL DEFAULT '',
    -- The visitor's own handle, stored so the widget can resume its thread
    -- across page loads without a session: it is random and unguessable.
    visitor_token    text        NOT NULL DEFAULT '',
    subject          text        NOT NULL DEFAULT '',
    status           text        NOT NULL DEFAULT 'open',
    assignee_user_id text,
    last_message_at  timestamptz NOT NULL DEFAULT now(),
    -- Unread FOR THE TEAM: set on an inbound message, cleared when someone opens
    -- the thread. The visitor's own unread state is not tracked; their widget
    -- polls and sees everything.
    unread           boolean     NOT NULL DEFAULT true,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (channel IN ('chat', 'email', 'whatsapp')),
    CHECK (status IN ('open', 'snoozed', 'closed'))
);

-- direction is from the workspace's point of view: 'in' is the customer
-- writing, 'out' is the team replying. author_user_id is NULL for 'in'.
CREATE TABLE IF NOT EXISTS messages (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    conversation_id text        NOT NULL,
    direction       text        NOT NULL,
    author_user_id  text,
    body            text        NOT NULL,
    -- The provider's id (email Message-ID, WhatsApp id) so a redelivery is the
    -- same row; empty for chat, which has no upstream id.
    external_id     text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (direction IN ('in', 'out'))
);
