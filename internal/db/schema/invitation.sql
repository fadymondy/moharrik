-- Hand-written Postgres DDL for Invitation (see workspace.sql for why).
-- Only the SHA-256 of the invite token is stored; the plaintext is shown once.
CREATE TABLE IF NOT EXISTS invitations (
    id           text PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    email        text        NOT NULL,
    role         text        NOT NULL,
    token_hash   text        NOT NULL UNIQUE,
    invited_by   text        NOT NULL,
    expires_at   timestamptz NOT NULL,
    accepted_at  timestamptz,
    accepted_by  text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (role IN ('admin', 'manager', 'member', 'client'))
);
