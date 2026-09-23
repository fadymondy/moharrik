-- A campaign's audience can be narrowed to one language (MH-413, the
-- fadymondy.com FM-385 parity item).
--
-- campaigns.locale is the language the campaign is WRITTEN in; it only
-- changed rendering. audience_locale says who RECEIVES it: '' (every contact
-- on the list, which is what every existing campaign keeps meaning), or 'en' /
-- 'ar' for contacts in that language only (contacts.locale). The count shown
-- before sending, the too-large check and both send paths read the same rule.
--
-- Batch N's reserved block is 115–119. Idempotent.
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS audience_locale text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'campaigns_audience_locale_ck') THEN
        ALTER TABLE campaigns ADD CONSTRAINT campaigns_audience_locale_ck
            CHECK (audience_locale IN ('', 'en', 'ar'));
    END IF;
END $$;
