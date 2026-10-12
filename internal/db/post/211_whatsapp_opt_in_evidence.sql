-- WhatsApp opt-in evidence: how a contact agreed, and the proof.
--
-- 29_whatsapp_broadcast.sql gave WhatsApp consent its own opt-in. It recorded
-- WHEN, never HOW: a box ticked on a form and "they posted 'WhatsApp me on
-- 010…' in a public group" looked the same. An opt-in recorded by a member or
-- an agent now carries where it came from, the evidence (a link to the post
-- or message, and/or a note), and the member who recorded it. The contacts
-- table is audited (96_audit_log.sql), so every recording, with its actor, is
-- in the audit log as well.
--
-- A STOP still wins: none of this touches whatsapp_opt_out_at, and the code
-- that writes these columns refuses a contact who said STOP.
--
-- Empty source means "recorded without one" (the form path, the older button).

ALTER TABLE contacts ADD COLUMN IF NOT EXISTS whatsapp_opt_in_source       text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS whatsapp_opt_in_evidence_url text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS whatsapp_opt_in_note         text NOT NULL DEFAULT '';
ALTER TABLE contacts ADD COLUMN IF NOT EXISTS whatsapp_opt_in_by           text;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_whatsapp_opt_in_source_ck') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_whatsapp_opt_in_source_ck
            CHECK (whatsapp_opt_in_source IN ('', 'public_request', 'inbound_message', 'verbal', 'form'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_whatsapp_opt_in_by_fk') THEN
        ALTER TABLE contacts ADD CONSTRAINT contacts_whatsapp_opt_in_by_fk
            FOREIGN KEY (whatsapp_opt_in_by) REFERENCES users (id) ON DELETE SET NULL;
    END IF;
END $$;
