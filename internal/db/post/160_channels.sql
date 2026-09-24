-- Telegram, Slack, Discord and workflow inbox channels (MH-563, MH-564).
-- Added by ALTER because conversations' CREATE in internal/db/schema is IF
-- NOT EXISTS, so an existing database would otherwise never get the columns.
-- Every statement is idempotent. Batch W's reserved post-migration number
-- (160-164): everything for these two cards is in this one file, so
-- renumbering it against another branch is a rename and nothing else.

-- source_channel_id names which specific connection row (a telegram_channels,
-- slack_channels, discord_channels or workflow_channels id) filed the
-- conversation — a workspace can have several of one kind. No FK: the target
-- table is polymorphic, the same reason chat_widgets' widget_id on
-- conversations has none either.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_channel_id text;

-- The platform's own identity for the thread — a Telegram chat id, a Slack
-- channel/DM id (with a thread_ts suffix when threaded), a Discord channel
-- id, or a workflow channel's thread id — used to find-or-create the
-- conversation on an inbound message, the same role customer_phone plays for
-- WhatsApp.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS external_thread_id text NOT NULL DEFAULT '';

-- The customer's handle/username on the platform (a Telegram @handle, a
-- Slack/Discord user id, a profile URL for a workflow channel) — displayed
-- when there is no email or phone to show instead.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS customer_handle text NOT NULL DEFAULT '';

ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_channel_check;
ALTER TABLE conversations ADD CONSTRAINT conversations_channel_check
    CHECK (channel IN ('chat', 'email', 'whatsapp', 'telegram', 'slack', 'discord', 'linkedin', 'facebook', 'instagram', 'x'));

-- One conversation per (workspace, channel, connection, thread) — the same
-- shape as WhatsApp's one-per-number, generalised to several connections of
-- one kind: two Slack workspaces linked to this one workspace never collapse
-- each other's DM-id namespace into the same conversation.
CREATE UNIQUE INDEX IF NOT EXISTS conversations_external_thread_uq
    ON conversations (workspace_id, channel, coalesce(source_channel_id, ''), external_thread_id)
    WHERE external_thread_id <> '';

CREATE INDEX IF NOT EXISTS conversations_source_channel_idx
    ON conversations (workspace_id, source_channel_id) WHERE source_channel_id IS NOT NULL;

-- RLS: the public webhooks (Telegram, Slack, Discord interactions) resolve
-- their workspace on the OWNER pool from the unguessable webhook_id in the
-- URL, exactly like WhatsApp's hook_id, then do everything else on the app
-- pool inside that workspace — so these policies govern them exactly as they
-- govern a request from the web app.
ALTER TABLE telegram_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE telegram_channels FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON telegram_channels;
CREATE POLICY tenant_isolation ON telegram_channels
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE slack_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE slack_channels FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON slack_channels;
CREATE POLICY tenant_isolation ON slack_channels
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE discord_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE discord_channels FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON discord_channels;
CREATE POLICY tenant_isolation ON discord_channels
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE workflow_channels ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_channels FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON workflow_channels;
CREATE POLICY tenant_isolation ON workflow_channels
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE workflow_channel_pushes ENABLE ROW LEVEL SECURITY;
ALTER TABLE workflow_channel_pushes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON workflow_channel_pushes;
CREATE POLICY tenant_isolation ON workflow_channel_pushes
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
