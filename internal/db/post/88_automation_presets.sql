-- The preset library (MH-478 phase 3a): fadymondy.com's browser plugins,
-- copied as presets a workspace adds as its own workflows.
--
-- A workflow now carries what a plugin carries on fadymondy.com besides its
-- steps: the SITES it touches (shown before anyone runs it, and offered for a
-- browser's allowed sites when it is added), an ICON, whether it WRITES —
-- publishes, sends or submits as the signed-in account — and which preset it
-- came from.
--
-- The workspace PROFILE is the handful of values a form-filling preset needs
-- (name, email, phone, city, links). A preset's defaults are filled from it
-- when the preset is added; nothing reads it at run time.

ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS hosts text[] NOT NULL DEFAULT '{}';
ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS icon text NOT NULL DEFAULT '';
ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS writes boolean NOT NULL DEFAULT false;
ALTER TABLE automation_workflows ADD COLUMN IF NOT EXISTS preset text NOT NULL DEFAULT '';

ALTER TABLE automation_settings ADD COLUMN IF NOT EXISTS profile jsonb NOT NULL DEFAULT '{}'::jsonb;
