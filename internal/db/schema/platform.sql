-- Platform tables for the superadmin panel (MH-1184): account flags and the admin audit trail.
-- NOT tenant tables: no workspace_id, no grant to the app role (migrate.TenantTables is an
-- allow-list) and RLS on with no policy (internal/db/post/205_platform_admin.sql), so only the
-- owner pool (internal/platform, internal/admin) reads or writes them.

-- Platform-level state of an account. The auth plugin owns `users` and reads it whole, so
-- Moharrik keeps its own columns here. sessions_valid_after: a session issued before it is
-- refused (an admin's "sign out everywhere").
CREATE TABLE IF NOT EXISTS platform_users (
    user_id              text PRIMARY KEY,
    suspended_at         timestamptz,
    suspended_reason     text,
    sessions_valid_after timestamptz,
    last_seen_at         timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- Append-only record of what platform admins did, over REST and the admin MCP.
CREATE TABLE IF NOT EXISTS admin_audit (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    actor_id     text        NOT NULL,
    actor_email  text        NOT NULL DEFAULT '',
    action       text        NOT NULL,
    target_type  text        NOT NULL DEFAULT '',
    target_id    text        NOT NULL DEFAULT '',
    target_label text        NOT NULL DEFAULT '',
    changes      jsonb,
    ip           text        NOT NULL DEFAULT '',
    via          text        NOT NULL DEFAULT 'rest',
    at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS admin_audit_at_idx ON admin_audit (at DESC);
