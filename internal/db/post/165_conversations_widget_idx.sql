-- One Channels page (MH-571). Batch Y has no reserved post-migration block
-- (I-W, 90-164, are reserved per E:\Sites\moharrik-agent-rules.md), so this
-- uses the next free number, 165.
--
-- conversations.widget_id has existed since chat_widgets shipped
-- (internal/db/schema/inbox.sql) but, unlike mailbox_id and source_channel_id
-- added later for the same table, never got its own index. The aggregate
-- channels endpoint (internal/channels/aggregate.go) groups conversations by
-- widget_id per workspace exactly the way it already does for those two
-- columns, so give it the matching index rather than a full scan.
CREATE INDEX IF NOT EXISTS conversations_widget_idx
    ON conversations (workspace_id, widget_id) WHERE widget_id IS NOT NULL;
