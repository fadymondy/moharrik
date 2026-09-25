-- MH-604: group message bubbles need to know who sent each message. Today
-- messages carries no per-sender identity at all — internal/inbox/channel.go's
-- FileChannelMessage links the CONVERSATION's own contact_id using the
-- thread id, correct for a 1:1 DM but meaningless for a Telegram/Slack/
-- Discord group thread, where every sender shares one conversation.
--
-- conversations.is_group (set at conversation-creation time, from the
-- per-platform signal internal/channels now threads through: a Discord
-- guild channel, a non-private Telegram chat, a Slack channel/group id) lets
-- the UI show a sender avatar beside each bubble only where there is more
-- than one possible sender, per the task brief ("don't show it in 1:1
-- threads").
--
-- messages.sender_contact_id/sender_name/sender_external_id are filled only
-- for a group thread's inbound messages (FileChannelMessage resolves them
-- per message instead of once per conversation); a 1:1 thread leaves them
-- NULL/empty exactly as before, since the conversation-level contact already
-- says who that is.
--
-- Numbered 604 (the card's own number), same "not the next sequential slot"
-- reasoning 175_prospect_icp_link.sql's header explains — several branches
-- add post/ migrations off the same base, and a ticket number is one no
-- other card will also pick.

ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_group boolean NOT NULL DEFAULT false;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS sender_contact_id  text;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS sender_name        text NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS sender_external_id text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'messages_sender_contact_fk') THEN
        ALTER TABLE messages ADD CONSTRAINT messages_sender_contact_fk
            FOREIGN KEY (workspace_id, sender_contact_id) REFERENCES contacts (workspace_id, id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS messages_sender_contact_idx ON messages (workspace_id, sender_contact_id) WHERE sender_contact_id IS NOT NULL;
