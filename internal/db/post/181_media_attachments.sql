-- MH-658: inbound WhatsApp media (images, video, voice notes, documents,
-- stickers, locations, contact cards) reuses message_attachments (MH-556)
-- rather than a new table — same storage, same signed-URL download endpoint,
-- same rendering path email attachments already use.
--
-- Batch's reserved block is 181-183. Idempotent and safe on production data.
--
-- kind classifies an attachment for rendering, since content_type alone
-- cannot tell a WhatsApp sticker (image/webp) from an ordinary image, or a
-- voice note (audio/ogg; codecs=opus) from a shared audio file: 'image',
-- 'video', 'audio', 'voice', 'document', 'sticker', 'location', 'contact'.
-- Existing (email) rows default to 'document' — the generic file-chip
-- rendering they already had.
ALTER TABLE message_attachments ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'document';

-- status: 'ready' once bytes are stored, 'failed' when the download from
-- Evolution timed out, exceeded the size cap, or errored — the message is
-- still filed, with a placeholder attachment row the thread can show instead
-- of silently dropping the message (card's "done when").
ALTER TABLE message_attachments ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'ready';

-- meta carries the small, non-binary payloads that aren't a downloadable
-- file at all: a location's {lat,lng,name,address} or a contact card's
-- {name,number}. Empty object for every ordinary binary attachment.
ALTER TABLE message_attachments ADD COLUMN IF NOT EXISTS meta jsonb NOT NULL DEFAULT '{}'::jsonb;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'message_attachments_kind_check'
    ) THEN
        ALTER TABLE message_attachments
            ADD CONSTRAINT message_attachments_kind_check
            CHECK (kind IN ('image', 'video', 'audio', 'voice', 'document', 'sticker', 'location', 'contact'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'message_attachments_status_check'
    ) THEN
        ALTER TABLE message_attachments
            ADD CONSTRAINT message_attachments_status_check
            CHECK (status IN ('ready', 'failed'));
    END IF;
END $$;

-- data was NOT NULL: a failed/location/contact row has none, or a tiny JSON
-- placeholder rather than real file bytes. bytea is nullable so a failed
-- download does not need a fake payload just to satisfy the column.
ALTER TABLE message_attachments ALTER COLUMN data DROP NOT NULL;
