-- Who a chat widget says the visitor is talking to, per widget. The header
-- and "… is typing" used the workspace's name, which is right for a business
-- and wrong for a personal site: fadymondy.com's widget belongs to the "3x1"
-- workspace but is Fady talking. Empty (the default) keeps the workspace's
-- name and picture, so every existing widget looks the same as before.
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get the columns.
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS display_name       text NOT NULL DEFAULT '';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS display_avatar_url text NOT NULL DEFAULT '';
