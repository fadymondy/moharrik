-- The account cycle (internal/account, internal/appsignin): the one-time links mailed for "confirm
-- your email" and "reset your password", and the apps' refresh tokens. Platform data, not tenant
-- data: no workspace_id, no grant to the app role, RLS on with no policy
-- (internal/db/post/221_auth_cycle.sql), so only the owner pool reads or writes them.

-- A mailed one-time link: verify an address, or reset a password. Only the hash is stored; a link
-- works once and expires. `email` is the address it was sent to, so a link for an address the
-- account no longer has is void.
CREATE TABLE IF NOT EXISTS auth_email_tokens (
    token_hash text        PRIMARY KEY,
    purpose    text        NOT NULL,
    user_id    text        NOT NULL,
    email      text        NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (purpose IN ('verify', 'reset'))
);

CREATE INDEX IF NOT EXISTS auth_email_tokens_user_idx ON auth_email_tokens (user_id, purpose);

-- An app's refresh token (internal/appsignin): the device token (a personal access token) expires
-- in days; this trades for a new pair. One row per signed-in install, rotated on every use.
-- signed_in_at is when the person actually proved who they are, carried across rotations, so
-- "sign out everywhere" (platform_users.sessions_valid_after) ends the whole chain.
CREATE TABLE IF NOT EXISTS app_refresh_tokens (
    token_hash   text        PRIMARY KEY,
    user_id      text        NOT NULL,
    pat_id       text        NOT NULL,
    device_name  text        NOT NULL DEFAULT '',
    signed_in_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS app_refresh_tokens_user_idx ON app_refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS app_refresh_tokens_pat_idx ON app_refresh_tokens (pat_id);
