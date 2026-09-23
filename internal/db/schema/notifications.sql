-- Notification center: per-workspace webhook sources, and the items they (or,
-- later, a collector) deliver. Modelled on fadymondy.com's notification_center
-- (webhook_sources + notification_items), narrowed to what Moharrik needs today
-- — no polling plugins yet, so there is no notification_sources table here.
--
-- Every row carries workspace_id and is isolated by RLS (internal/db/post/20_rls.sql).
-- Links use composite foreign keys (workspace_id, x_id) -> x (workspace_id, id),
-- the same reason internal/db/schema/crm.sql gives: a single-column FK would let
-- a foreign-key check (which runs as the table owner and ignores RLS) point one
-- workspace's row at another workspace's.

-- A named webhook endpoint: POST /api/hooks/{id}, no session, no X-Workspace-ID —
-- the id in the path plus the HMAC signature over the raw body (checked against
-- secret_sealed) are the whole authorization. kind picks how the body is read;
-- unrecognised kinds fall back to the generic {title, body, kind, severity, ...}
-- shape.
CREATE TABLE IF NOT EXISTS webhook_sources (
    id               text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id     text        NOT NULL,
    name             text        NOT NULL,
    kind             text        NOT NULL DEFAULT 'generic',
    -- AES-256-GCM under a key derived from AUTH_SECRET (internal/notifications/secret.go,
    -- the same construction as internal/marketing's SMTP password). The plaintext is
    -- shown once, on creation or rotation, and never again.
    secret_sealed    text        NOT NULL DEFAULT '',
    enabled          boolean     NOT NULL DEFAULT true,
    delivery_count   bigint      NOT NULL DEFAULT 0,
    last_delivery_at timestamptz,
    -- The last REFUSED delivery and why (bad signature, disabled, oversized body),
    -- so "it says delivery failed" has an answer on the settings page. Never the
    -- secret or the signature itself.
    last_error       text        NOT NULL DEFAULT '',
    last_error_at    timestamptz,
    -- MH-381: true only for sources created before timestamped signing, which
    -- still accept the old body-only signature (see 31_webhook_signing.sql for
    -- why existing rows are true and new ones false). last_legacy_at is the last
    -- delivery that verified ONLY that way.
    legacy_signing   boolean     NOT NULL DEFAULT false,
    last_legacy_at   timestamptz,
    rotated_at       timestamptz NOT NULL DEFAULT now(),
    created_by       text        NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (kind IN ('generic', 'github', 'sentry'))
);

-- One row per delivered event. read_at/saved_at are separate (marking
-- everything read must not clear what was saved for later — fadymondy.com's
-- 2026-09-16 note on the same design). external_id + source_id together dedupe
-- a redelivery; when a source does not send one, the handler derives it from a
-- hash of the raw body, so an exact repeat still collapses to one row while two
-- distinct payloads do not.
CREATE TABLE IF NOT EXISTS notification_items (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    -- Nullable and ON DELETE SET NULL (17_notifications.sql): deleting a source
    -- must not take its history with it. source_name is a snapshot of the
    -- source's name at delivery time, so a deleted source's items keep a label.
    source_id    text,
    source_name  text        NOT NULL DEFAULT '',
    external_id  text        NOT NULL DEFAULT '',
    kind         text        NOT NULL DEFAULT '',
    severity     text        NOT NULL DEFAULT 'info',
    title        text        NOT NULL,
    body         text        NOT NULL DEFAULT '',
    actor        text        NOT NULL DEFAULT '',
    url          text        NOT NULL DEFAULT '',
    -- The payload as received, verbatim, so a badly normalised title can be
    -- fixed from the original without replaying the delivery.
    payload      text        NOT NULL DEFAULT '{}',
    occurred_at  timestamptz NOT NULL DEFAULT now(),
    read_at      timestamptz,
    saved_at     timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    CHECK (severity IN ('info', 'warning', 'error'))
);
