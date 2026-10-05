-- MH-1069: a notification can be resolved ("dealt with") and snoozed ("not now").
--
--   resolved_at    when someone resolved it; NULL = still open. Independent of
--                  read_at (reading is not resolving) and saved_at.
--   snoozed_until  hidden from the actionable list until this instant; NULL or a
--                  past time = not snoozed. There is no job that "wakes" an item:
--                  snoozed is derived at read time (snoozed_until > now()), so an
--                  item reappears the moment its time passes, whatever was or was
--                  not running.
--
-- The default feed, the bell and every unread count use ONE definition of
-- "actionable": resolved_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= now()).
-- Idempotent and safe on production data: nullable columns only.

ALTER TABLE notification_items ADD COLUMN IF NOT EXISTS resolved_at   timestamptz;
ALTER TABLE notification_items ADD COLUMN IF NOT EXISTS snoozed_until timestamptz;

-- The actionable feed (the default view): open items, newest first.
CREATE INDEX IF NOT EXISTS notification_items_open_idx
    ON notification_items (workspace_id, occurred_at DESC)
    WHERE resolved_at IS NULL;
