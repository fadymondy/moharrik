-- MH-664: reply-to-message quotes. A message can quote an earlier one:
--   reply_to_message_id  — the quoted row when it is in Moharrik (NULL if the
--                          original never reached us, e.g. older than the import)
--   reply_to_external_id — the channel's id for it (WhatsApp stanzaId, email
--                          Message-ID); what WhatsApp `quoted` and email
--                          In-Reply-To are built from
--   reply_to_body        — a snippet of the quoted text, kept so the quote still
--                          reads when the original is not loaded or not ours
--
-- Idempotent and safe on production data: nullable / defaulted columns only.

ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to_message_id  text;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to_external_id text NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to_body        text NOT NULL DEFAULT '';
