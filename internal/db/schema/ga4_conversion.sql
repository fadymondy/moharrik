-- GA4 conversions (MH-449): a workspace's own GA4 web stream, and the funnel
-- events Moharrik sends to it through the Measurement Protocol.
--
--   ga4_conversion_settings  one row per workspace: the measurement id (G-…),
--                            the Measurement Protocol API secret (SEALED, never
--                            returned by the API), whether events are sent,
--                            whether the embed snippets carry the GA4 tag, and
--                            whether events are marked for DebugView.
--   ga4_conversion_events    the outbox: one row per funnel moment (a lead
--                            created, qualified or converted; a deal won),
--                            written by triggers on leads and deals whichever
--                            code path made the change, and sent by the
--                            in-process sender (internal/conversions). A moment
--                            is recorded once per lead or deal: re-qualifying
--                            a lead does not count it twice.
--
-- Nothing personal is sent or stored here: the event name, the lead's or
-- deal's id (to find its GA client id at send time), and the outcome. Isolated
-- by RLS; keys, triggers and policies are in internal/db/post/130_ga4_conversions.sql.

CREATE TABLE IF NOT EXISTS ga4_conversion_settings (
    workspace_id      text        PRIMARY KEY,
    measurement_id    text        NOT NULL DEFAULT '',
    api_secret_sealed text        NOT NULL DEFAULT '',
    events_enabled    boolean     NOT NULL DEFAULT false,
    tag_in_embeds     boolean     NOT NULL DEFAULT false,
    debug_mode        boolean     NOT NULL DEFAULT false,
    updated_by        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    CHECK (measurement_id = '' OR measurement_id ~ '^G-[A-Z0-9]{4,20}$')
);

CREATE TABLE IF NOT EXISTS ga4_conversion_events (
    id              text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id    text        NOT NULL,
    subject_kind    text        NOT NULL,
    subject_id      text        NOT NULL,
    event_name      text        NOT NULL,
    -- pending → sent, or failed (Google refused it, or it kept failing), or
    -- skipped (sending was switched off, or the setup is incomplete, by the
    -- time the sender reached it).
    status          text        NOT NULL DEFAULT 'pending',
    attempts        integer     NOT NULL DEFAULT 0,
    -- The sender's lease and backoff: a row is picked up once this has passed.
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    error           text        NOT NULL DEFAULT '',
    -- Whether the visitor's GA client id was known (the event joins their
    -- journey) or a pseudonymous one was used (counted, not attributed).
    attributed      boolean,
    debug           boolean     NOT NULL DEFAULT false,
    occurred_at     timestamptz NOT NULL DEFAULT now(),
    sent_at         timestamptz,
    UNIQUE (workspace_id, subject_kind, subject_id, event_name),
    CHECK (subject_kind IN ('lead', 'deal')),
    CHECK (status IN ('pending', 'sent', 'failed', 'skipped'))
);
