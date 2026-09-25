-- MH-600: email becomes one conversation per sender, like a WhatsApp contact.
-- internal/inbox/imap.go's findThread now threads every inbound email from
-- the same (workspace, lower(customer_email)) into the ONE conversation for
-- that sender, dropping the old Message-ID/subject/mailbox-scoped matching
-- that let one person end up with several open email threads. That change
-- only stops NEW splits; this migration folds conversations that already
-- split before it shipped.
--
-- Idempotent (post/ reruns on every `go run ./cmd/migrate` and before every
-- DB-backed test): after the first run each (workspace, sender) has exactly
-- one email conversation, so the fold loop's GROUP BY ... HAVING count(*) > 1
-- finds nothing and every step below is a no-op.

-- ---------------------------------------------------------------------------
-- 1. messages.subject: an email message now keeps its OWN subject line even
--    after its conversation folds into another (the UI shows it only when it
--    differs from the previous message's). Backfill from each message's OWN
--    conversation's subject BEFORE the fold below runs, while that still
--    names the subject the message actually arrived under — after the fold,
--    conversations.subject tracks only the latest message and would backfill
--    every older message with the wrong one.
-- ---------------------------------------------------------------------------
ALTER TABLE messages ADD COLUMN IF NOT EXISTS subject text NOT NULL DEFAULT '';

UPDATE messages m SET subject = c.subject
FROM conversations c
WHERE c.workspace_id = m.workspace_id AND c.id = m.conversation_id
  AND c.channel = 'email' AND m.subject = '' AND c.subject <> '';

-- ---------------------------------------------------------------------------
-- 2. Fold. One pass per (workspace, lower(customer_email)) email group that
--    still has more than one conversation. Survivor = the one with the
--    newest last_message_at (tie-broken by id, so the pick is deterministic
--    rather than whatever order Postgres happens to return rows in).
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    grp RECORD;
    survivor_id text;
BEGIN
    FOR grp IN
        SELECT workspace_id, lower(customer_email) AS sender
        FROM conversations
        WHERE channel = 'email' AND customer_email <> ''
        GROUP BY workspace_id, lower(customer_email)
        HAVING count(*) > 1
    LOOP
        SELECT id INTO survivor_id FROM conversations
        WHERE workspace_id = grp.workspace_id AND channel = 'email' AND lower(customer_email) = grp.sender
        ORDER BY last_message_at DESC, id ASC
        LIMIT 1;

        -- Repoint everything that references a loser BEFORE any loser is
        -- deleted. messages carries the thread itself; the rest are the
        -- other tables in this codebase with a conversation_id column
        -- (there are no separate notes/tags/assignment tables to fold —
        -- those live as plain columns on conversations, coalesced below).
        UPDATE messages SET conversation_id = survivor_id
        WHERE workspace_id = grp.workspace_id AND conversation_id IN (
            SELECT id FROM conversations
            WHERE workspace_id = grp.workspace_id AND channel = 'email'
              AND lower(customer_email) = grp.sender AND id <> survivor_id
        );

        UPDATE conversation_participants SET conversation_id = survivor_id
        WHERE workspace_id = grp.workspace_id AND conversation_id IN (
            SELECT id FROM conversations
            WHERE workspace_id = grp.workspace_id AND channel = 'email'
              AND lower(customer_email) = grp.sender AND id <> survivor_id
        );

        UPDATE workflow_channel_pushes SET conversation_id = survivor_id
        WHERE workspace_id = grp.workspace_id AND conversation_id IN (
            SELECT id FROM conversations
            WHERE workspace_id = grp.workspace_id AND channel = 'email'
              AND lower(customer_email) = grp.sender AND id <> survivor_id
        );

        UPDATE contact_context_imports SET conversation_id = survivor_id
        WHERE workspace_id = grp.workspace_id AND conversation_id IN (
            SELECT id FROM conversations
            WHERE workspace_id = grp.workspace_id AND channel = 'email'
              AND lower(customer_email) = grp.sender AND id <> survivor_id
        );

        -- Coalesce the survivor's scalar columns from whichever loser has a
        -- value it is missing; the most recently active loser wins when more
        -- than one could fill a gap. The survivor's own existing value is
        -- never overwritten.
        UPDATE conversations dst SET
            contact_id         = coalesce(dst.contact_id, src.contact_id),
            deal_id            = coalesce(dst.deal_id, src.deal_id),
            assignee_user_id   = coalesce(dst.assignee_user_id, src.assignee_user_id),
            mailbox_id         = coalesce(dst.mailbox_id, src.mailbox_id),
            source_channel_id  = coalesce(dst.source_channel_id, src.source_channel_id),
            snoozed_until      = coalesce(dst.snoozed_until, src.snoozed_until),
            closed_at          = coalesce(dst.closed_at, src.closed_at),
            photo_fetched_at   = coalesce(dst.photo_fetched_at, src.photo_fetched_at),
            customer_name      = CASE WHEN dst.customer_name = '' THEN src.customer_name ELSE dst.customer_name END,
            customer_phone     = CASE WHEN dst.customer_phone = '' THEN src.customer_phone ELSE dst.customer_phone END,
            customer_handle    = CASE WHEN dst.customer_handle = '' THEN src.customer_handle ELSE dst.customer_handle END,
            locale             = CASE WHEN dst.locale = '' THEN src.locale ELSE dst.locale END,
            page_url           = CASE WHEN dst.page_url = '' THEN src.page_url ELSE dst.page_url END,
            user_agent         = CASE WHEN dst.user_agent = '' THEN src.user_agent ELSE dst.user_agent END,
            contact_link       = CASE WHEN dst.contact_link = '' THEN src.contact_link ELSE dst.contact_link END,
            photo_url          = CASE WHEN dst.photo_url = '' THEN src.photo_url ELSE dst.photo_url END,
            photo_source       = CASE WHEN dst.photo_source = '' THEN src.photo_source ELSE dst.photo_source END,
            photo_fetch_status = CASE WHEN dst.photo_fetch_status = '' THEN src.photo_fetch_status ELSE dst.photo_fetch_status END
        FROM (
            SELECT * FROM conversations
            WHERE workspace_id = grp.workspace_id AND channel = 'email'
              AND lower(customer_email) = grp.sender AND id <> survivor_id
            ORDER BY last_message_at DESC
            LIMIT 1
        ) src
        WHERE dst.workspace_id = grp.workspace_id AND dst.id = survivor_id;

        -- Recompute last_message_at/unread from what actually survives the
        -- fold, rather than trusting the pre-fold values (a loser could have
        -- carried the true newest message_at recomputation input, or an
        -- unread flag the survivor did not have). preview is not stored —
        -- ConversationOut.Preview is already derived from the latest message
        -- at read time, so it needs no recompute here.
        UPDATE conversations c SET
            last_message_at = (
                SELECT max(m.created_at) FROM messages m
                WHERE m.workspace_id = grp.workspace_id AND m.conversation_id = survivor_id
            ),
            unread = EXISTS (
                SELECT 1 FROM conversations x
                WHERE x.workspace_id = grp.workspace_id AND x.channel = 'email'
                  AND lower(x.customer_email) = grp.sender AND x.unread
            )
        WHERE c.workspace_id = grp.workspace_id AND c.id = survivor_id;

        DELETE FROM conversations
        WHERE workspace_id = grp.workspace_id AND channel = 'email'
          AND lower(customer_email) = grp.sender AND id <> survivor_id;
    END LOOP;
END $$;

-- ---------------------------------------------------------------------------
-- 3. Back it with a real constraint: one email conversation per (workspace,
--    sender), enforced going forward instead of merely produced by the loop
--    above and by findThread's SELECT-then-INSERT (which is not itself
--    race-proof). customer_email <> '' guard matches conversations_email_
--    sender_idx (172_email_sender_groups.sql) so several blank-address rows
--    (a draft, a bounce with no reply-to) never collide.
-- ---------------------------------------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS conversations_email_sender_uq
    ON conversations (workspace_id, lower(customer_email))
    WHERE channel = 'email' AND customer_email <> '';
