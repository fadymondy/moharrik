-- Schedules that run a saved workflow (MH-403): the values for its SECRET
-- parameters, sealed.
--
-- A schedule runs with nobody present, so it has to keep the values it was
-- given — including a password. The plain values stay in params (jsonb) where
-- the page can show them; the secret ones are sealed with AES-GCM under
-- AUTH_SECRET (internal/secret) into this column and are never sent back to
-- any page or model. A database dump alone does not reveal them.
ALTER TABLE automation_schedules ADD COLUMN IF NOT EXISTS secret_values text NOT NULL DEFAULT '';
