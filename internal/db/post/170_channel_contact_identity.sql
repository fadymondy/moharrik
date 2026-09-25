-- MH-583: Telegram, Slack and Discord inbound senders resolve/create a
-- contact through contacts.Upsert with ONLY a platform identity — no legacy
-- email/phone/handle at all (a chat id is none of those). The plain
-- contacts_identity_chk from 155_contact_identity.sql required one of those
-- three columns to be non-empty, which made that call fail every time: a
-- CHECK constraint cannot see another table, so it had no way to know a
-- contact_identities row was about to exist for the very same insert.
--
-- Replaced with a deferred constraint trigger: DEFERRABLE INITIALLY DEFERRED
-- means it fires at COMMIT, not at the INSERT statement itself, so
-- contacts.Upsert's own transaction — which inserts the contact row and THEN
-- writes its contact_identities rows, in that order, before committing —
-- still satisfies it.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'contacts_identity_chk') THEN
        ALTER TABLE contacts DROP CONSTRAINT contacts_identity_chk;
    END IF;
END $$;

CREATE OR REPLACE FUNCTION contacts_identity_guard() RETURNS trigger AS $$
BEGIN
    IF NEW.email <> '' OR NEW.phone <> '' OR NEW.handle <> '' THEN
        RETURN NEW;
    END IF;
    IF EXISTS (
        SELECT 1 FROM contact_identities
        WHERE workspace_id = NEW.workspace_id AND contact_id = NEW.id
    ) THEN
        RETURN NEW;
    END IF;
    -- Raised as the check_violation the old CHECK was, under its name, so
    -- crm.linkErr keeps answering 422 with the rule instead of a 500.
    RAISE EXCEPTION 'a contact needs an email, a phone number, a handle or another identity'
        USING ERRCODE = 'check_violation', CONSTRAINT = 'contacts_identity_chk';
END;
$$ LANGUAGE plpgsql;

-- Guarded, unlike a bare DROP+CREATE: every file in this directory reruns on
-- every migrate.Run (internal/testdb/testdb.go calls it before EACH test in a
-- package), and DROP TRIGGER/CREATE CONSTRAINT TRIGGER takes an ACCESS
-- EXCLUSIVE lock on contacts even when the trigger is already exactly this
-- one — which then queues behind, and stalls, ordinary reads/writes other
-- tests are making on contacts at that moment. Skipping when it already
-- exists keeps this a real no-op after the first run, the same way
-- 155_contact_identity.sql's old CHECK guard was.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'contacts_identity_guard_trg') THEN
        CREATE CONSTRAINT TRIGGER contacts_identity_guard_trg
            AFTER INSERT OR UPDATE ON contacts
            DEFERRABLE INITIALLY DEFERRED
            FOR EACH ROW EXECUTE FUNCTION contacts_identity_guard();
    END IF;
END $$;
