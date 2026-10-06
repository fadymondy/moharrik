-- MH-1169: a markdown brief per contact, company and lead that the AI reads:
-- who they are, how we speak to them, what matters to them. Drafted by the AI
-- from the record's own history, then edited by hand.
--
--   ai_context             the brief; '' = none written yet.
--   ai_context_updated_at  when it was last saved; NULL = never.
--   ai_context_updated_by  the member who saved it; '' = none.
--
-- Idempotent and safe on production data: additive columns with defaults only.

ALTER TABLE contacts  ADD COLUMN IF NOT EXISTS ai_context            text NOT NULL DEFAULT '';
ALTER TABLE contacts  ADD COLUMN IF NOT EXISTS ai_context_updated_at timestamptz;
ALTER TABLE contacts  ADD COLUMN IF NOT EXISTS ai_context_updated_by text NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN IF NOT EXISTS ai_context            text NOT NULL DEFAULT '';
ALTER TABLE companies ADD COLUMN IF NOT EXISTS ai_context_updated_at timestamptz;
ALTER TABLE companies ADD COLUMN IF NOT EXISTS ai_context_updated_by text NOT NULL DEFAULT '';
ALTER TABLE leads     ADD COLUMN IF NOT EXISTS ai_context            text NOT NULL DEFAULT '';
ALTER TABLE leads     ADD COLUMN IF NOT EXISTS ai_context_updated_at timestamptz;
ALTER TABLE leads     ADD COLUMN IF NOT EXISTS ai_context_updated_by text NOT NULL DEFAULT '';
