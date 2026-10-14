-- Inbox channels beyond chat/email/WhatsApp: Telegram, Slack and Discord
-- (MH-563), and browser-workflow channels for networks with no usable API
-- (MH-564): LinkedIn, Facebook Page inbox, Instagram DMs, X DMs.
--
-- Shape: one row per CONNECTION, many per workspace (chat_widgets' shape, not
-- whatsapp_links' one-row-per-workspace one) — "as many of each as a
-- workspace wants".
--
-- Every bot/app secret is sealed at rest (internal/secret) and never returned
-- by any API. webhook_id is a SECOND, dedicated unguessable id — not the
-- row's own id — for the public delivery URL, the same indirection
-- whatsapp_links uses for hook_id (internal/db/schema/whatsapp.sql): the id
-- used in ordinary API responses and the id posted in a public URL are never
-- the same value.

CREATE TABLE IF NOT EXISTS telegram_channels (
    id                  text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id        text        NOT NULL,
    name                text        NOT NULL,
    -- The bot token from @BotFather. Sealed; never returned.
    bot_token_sealed    text        NOT NULL DEFAULT '',
    bot_username        text        NOT NULL DEFAULT '',
    webhook_id          text        NOT NULL,
    -- Telegram's setWebhook secret_token, echoed back in
    -- X-Telegram-Bot-Api-Secret-Token on every delivery (MH-563: "Telegram
    -- secret webhook paths"). Sealed; never returned.
    secret_token_sealed text        NOT NULL DEFAULT '',
    -- Comma-separated chat/group ids this bot will file messages from.
    -- Empty means every chat that can reach the bot (holding the bot token is
    -- already the gate; a group only reaches it once someone adds the bot).
    allowed_chat_ids    text        NOT NULL DEFAULT '',
    enabled             boolean     NOT NULL DEFAULT true,
    last_delivery_at    timestamptz,
    last_error          text        NOT NULL DEFAULT '',
    last_error_at       timestamptz,
    last_test_at        timestamptz,
    last_test_ok        boolean,
    created_by          text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (webhook_id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS slack_channels (
    id                    text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id          text        NOT NULL,
    name                  text        NOT NULL,
    -- A bot token (xoxb-…), for chat.postMessage and auth.test. Sealed.
    bot_token_sealed      text        NOT NULL DEFAULT '',
    -- The app's signing secret, for verifying X-Slack-Signature (MH-563:
    -- "Verify Slack signatures"). Sealed.
    signing_secret_sealed text        NOT NULL DEFAULT '',
    webhook_id            text        NOT NULL,
    team_id               text        NOT NULL DEFAULT '',
    team_name             text        NOT NULL DEFAULT '',
    bot_user_id           text        NOT NULL DEFAULT '',
    -- Comma-separated Slack channel ids to accept messages from. Empty means
    -- every channel/DM the bot is a member of.
    allowed_channel_ids   text        NOT NULL DEFAULT '',
    enabled               boolean     NOT NULL DEFAULT true,
    last_delivery_at      timestamptz,
    last_error            text        NOT NULL DEFAULT '',
    last_error_at         timestamptz,
    last_test_at          timestamptz,
    last_test_ok          boolean,
    created_by            text        NOT NULL DEFAULT '',
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (webhook_id),
    UNIQUE (workspace_id, name)
);

CREATE TABLE IF NOT EXISTS discord_channels (
    id                  text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id        text        NOT NULL,
    name                text        NOT NULL,
    -- A bot token, for the gateway connection and for posting replies. Sealed.
    bot_token_sealed    text        NOT NULL DEFAULT '',
    application_id      text        NOT NULL DEFAULT '',
    -- The Ed25519 public key Discord signs interactions with. Not a secret by
    -- design (it verifies Discord's signature; it does not authenticate us),
    -- so it is stored plainly, the way a webhook's own public key would be.
    public_key          text        NOT NULL DEFAULT '',
    bot_username        text        NOT NULL DEFAULT '',
    webhook_id          text        NOT NULL,
    -- Comma-separated guild and channel ids. Empty guild list means every
    -- guild the bot is in; empty channel list means every channel in an
    -- allowed guild. Neither list filters DMs: they are always delivered.
    allowed_guild_ids   text        NOT NULL DEFAULT '',
    allowed_channel_ids text        NOT NULL DEFAULT '',
    enabled             boolean     NOT NULL DEFAULT true,
    -- The gateway is the only way to receive ordinary channel/DM messages
    -- (MH-563: "the Discord gateway or interactions" — interactions alone
    -- only carries slash-command/component payloads). Last known state of
    -- this connection's gateway session.
    gateway_state       text        NOT NULL DEFAULT 'disconnected',
    last_delivery_at    timestamptz,
    last_error          text        NOT NULL DEFAULT '',
    last_error_at       timestamptz,
    last_test_at        timestamptz,
    last_test_ok        boolean,
    created_by          text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (webhook_id),
    UNIQUE (workspace_id, name),
    CHECK (gateway_state IN ('disconnected', 'connecting', 'connected'))
);

-- MH-564: a workflow channel is fed by a browser workflow's "Push to inbox"
-- step on a schedule, for a network with no usable API. network picks which
-- of the four read presets it is meant to run; workflow_id is the
-- automation_workflows row that does the reading (set once one exists — a
-- channel can be created before its workflow, the way a chat widget can).
CREATE TABLE IF NOT EXISTS workflow_channels (
    id                text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id      text        NOT NULL,
    name              text        NOT NULL,
    network           text        NOT NULL,
    workflow_id       text,
    -- Replies go out through the network's reply preset, with the same
    -- confirm-before-send rule as a publishing preset (MH-564). Off by
    -- default: pushing messages in is safe, posting on somebody's behalf is not.
    allow_replies     boolean     NOT NULL DEFAULT false,
    enabled           boolean     NOT NULL DEFAULT true,
    last_run_at       timestamptz,
    last_error        text        NOT NULL DEFAULT '',
    last_error_at     timestamptz,
    last_pushed_count integer     NOT NULL DEFAULT 0,
    created_by        text        NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, name),
    CHECK (network IN ('linkedin', 'facebook', 'instagram', 'x'))
);

-- Dedupe for the "Push to inbox" step: one row per (channel, thread, message)
-- already pushed, so re-running the same workflow never duplicates. Kept even
-- if the source message is later edited or removed on the network, because
-- the identity — not the content — is what must never repeat.
CREATE TABLE IF NOT EXISTS workflow_channel_pushes (
    id                  text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id        text        NOT NULL,
    workflow_channel_id text        NOT NULL,
    thread_id           text        NOT NULL,
    message_id          text        NOT NULL,
    conversation_id     text,
    created_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, workflow_channel_id, thread_id, message_id)
);
