-- The browser runtime (MH-478): where a step's screenshot is kept.
--
-- A screenshot step used to come back as base64 inside the step's result —
-- text kept verbatim for a person and a model to read, capped at 200 kB, so a
-- screenshot was cut off mid-image. The PNG now goes to storage under the
-- workspace and the run, and the step keeps its key. Empty means none.
ALTER TABLE automation_run_steps ADD COLUMN IF NOT EXISTS screenshot_path text NOT NULL DEFAULT '';
