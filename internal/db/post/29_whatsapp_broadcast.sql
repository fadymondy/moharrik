-- WhatsApp broadcasts (MH-320): consent that is its own, and a channel on campaigns.
--
-- Added by ALTER because the CREATEs in internal/db/schema are IF NOT EXISTS,
-- so an existing database would otherwise never get these columns.

-- WHATSAPP CONSENT IS NOT EMAIL CONSENT.
--
-- A contact who subscribed to a mailing list agreed to receive email. They did
-- not agree to be messaged on WhatsApp, which arrives on a personal phone and
-- is answered by a person. Treating list membership as WhatsApp consent is the
-- single worst shortcut available here: it is how a business gets its number
-- reported and banned, and it is a promise broken on the customer's behalf.
--
-- So it gets its own opt-in and its own opt-out. NULL opt-in means no consent —
-- the absence of a row is never consent.
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS whatsapp_opt_in_at  timestamptz;
-- Set when the contact says STOP. Kept separately from clearing the opt-in so
-- "they never agreed" and "they asked us to stop" stay distinguishable, and so
-- an opt-out cannot be undone by a form quietly re-ticking a box.
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS whatsapp_opt_out_at timestamptz;

-- Who can still be broadcast to. A partial index because that is the only
-- question this is ever asked.
CREATE INDEX IF NOT EXISTS contacts_whatsapp_reachable_idx
    ON contacts (workspace_id)
    WHERE whatsapp_opt_in_at IS NOT NULL AND whatsapp_opt_out_at IS NULL AND phone <> '';

-- Which channel a campaign goes out on. Defaulting to 'email' matters: every
-- row that already exists was an email campaign, and it must keep meaning that.
ALTER TABLE campaigns ADD COLUMN IF NOT EXISTS channel text NOT NULL DEFAULT 'email';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'campaigns_channel_ck') THEN
        ALTER TABLE campaigns ADD CONSTRAINT campaigns_channel_ck
            CHECK (channel IN ('email', 'whatsapp'));
    END IF;
END $$;
