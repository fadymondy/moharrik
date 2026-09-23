-- Snooze until a time, and when a thread was closed (MH-383).
--
-- Added by ALTER because the conversations CREATE in internal/db/schema is IF
-- NOT EXISTS, so an existing database would otherwise never get the columns.
-- Every statement is idempotent.

-- A snoozed thread wakes (status back to open, unread) once this passes. NULL
-- for every other status; a snooze with no time is allowed and waits for the
-- customer's next message, which always wakes a thread.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS snoozed_until timestamptz;

-- When the thread was last closed: the inbox stats' time to resolution. NULL
-- while it is open or snoozed, and for threads closed before this column
-- existed (the stats count those as resolved but leave them out of the time).
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS closed_at timestamptz;

CREATE INDEX IF NOT EXISTS conversations_snooze_due_idx ON conversations (snoozed_until) WHERE status = 'snoozed' AND snoozed_until IS NOT NULL;
-- The stats read a window of conversations by when they started.
CREATE INDEX IF NOT EXISTS conversations_created_idx ON conversations (workspace_id, created_at DESC);
