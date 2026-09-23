-- Chat widget parity with fadymondy.com (MH-382): appearance, the invitation,
-- page-dependent quick actions and the lead event are per widget, and the
-- visitor has an unread count of their own.
--
-- Added by ALTER, like 30_chat_context.sql: the CREATEs in internal/db/schema
-- are IF NOT EXISTS, so an existing database would never get new columns from
-- them. Every column has a default, so existing widgets keep behaving as they
-- did (no invitation, no quick actions, the SDK's own colours).
--
-- Values are validated in internal/inbox/widget_settings.go before they are
-- written; the CHECKs below are the backstop, above all for accent, which ends
-- up inside a stylesheet on the customer's page.

ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS greeting_ar          text    NOT NULL DEFAULT '';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS title                text    NOT NULL DEFAULT '';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS title_ar             text    NOT NULL DEFAULT '';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS accent               text    NOT NULL DEFAULT '';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS theme                text    NOT NULL DEFAULT 'auto';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS position             text    NOT NULL DEFAULT 'end';
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS invite               boolean NOT NULL DEFAULT false;
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS invite_after_seconds integer NOT NULL DEFAULT 25;
-- [{label, message, path, locale}] — see widget_settings.go for the shape.
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS quick_actions        jsonb   NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE chat_widgets ADD COLUMN IF NOT EXISTS lead_event           boolean NOT NULL DEFAULT true;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chat_widgets_accent_ck') THEN
        ALTER TABLE chat_widgets ADD CONSTRAINT chat_widgets_accent_ck
            CHECK (accent = '' OR accent ~ '^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chat_widgets_theme_ck') THEN
        ALTER TABLE chat_widgets ADD CONSTRAINT chat_widgets_theme_ck CHECK (theme IN ('auto', 'light', 'dark'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chat_widgets_position_ck') THEN
        ALTER TABLE chat_widgets ADD CONSTRAINT chat_widgets_position_ck CHECK (position IN ('start', 'end'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chat_widgets_invite_after_ck') THEN
        ALTER TABLE chat_widgets ADD CONSTRAINT chat_widgets_invite_after_ck CHECK (invite_after_seconds BETWEEN 5 AND 600);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chat_widgets_quick_actions_ck') THEN
        ALTER TABLE chat_widgets ADD CONSTRAINT chat_widgets_quick_actions_ck
            CHECK (jsonb_typeof(quick_actions) = 'array' AND jsonb_array_length(quick_actions) <= 8);
    END IF;
END $$;

-- Unread FOR THE VISITOR: team replies they have not seen yet. The widget shows
-- it on the launcher and clears it (POST /api/chat/{key}/read) when the panel
-- is open. The team's own `unread` flag is separate and unchanged.
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS unread_visitor integer NOT NULL DEFAULT 0;
