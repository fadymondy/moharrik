-- Where a chat started (MH-377): the page, the browser and the language, kept
-- on the conversation when the widget opens it — the first things worth
-- knowing when answering.
--
-- Added by ALTER because the conversations CREATE in internal/db/schema is
-- IF NOT EXISTS, so an existing database would otherwise never get these
-- columns (the same reason 23_whatsapp.sql adds customer_phone here).
--
-- NOT NULL DEFAULT '': every conversation that already exists, and every email
-- or WhatsApp one, simply has none — the inbox shows the fields only when set.
-- Lengths are bounded in internal/inbox/visitor_context.go, before the insert.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS page_url   text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS user_agent text NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS locale     text NOT NULL DEFAULT '';
