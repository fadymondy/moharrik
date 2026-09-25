-- MH-600: grouping the email channel's conversations by sender needs to scan
-- "every conversation for this workspace's email channel with this
-- lower-cased customer_email" fast — the same access pattern
-- ListConversationsBySender (internal/inbox/service.go) runs once per group
-- and once more, filtered to one sender, when a group is opened. Postgres
-- cannot use an index on customer_email for that: the comparison is on
-- lower(customer_email), so this is a functional index on exactly that
-- expression, scoped to the email channel since no other channel groups by
-- sender.
--
-- Idempotent (IF NOT EXISTS) and non-locking: a plain CREATE INDEX (not
-- CONCURRENTLY) takes a lock, but this file reruns on every migrate, so it
-- must stay a no-op after the first run rather than ever re-attempt the
-- lock — matching every other index in internal/db/post.
CREATE INDEX IF NOT EXISTS conversations_email_sender_idx
    ON conversations (workspace_id, lower(customer_email))
    WHERE channel = 'email';
