-- The account cycle (internal/account): sign-in identities from Google and
-- Apple, the one-time links mailed for "verify your email" and "reset your
-- password", and the apps' refresh tokens. Platform data, not tenant data: no
-- workspace_id, no grant to the app role (migrate.TenantTables is an
-- allow-list), RLS on with no policy (internal/db/post/220_account.sql), so
-- only the owner pool reads or writes them.

-- One provider account signed in to one Moharrik account. The provider's
-- stable subject — never the email — is what a later sign-in matches, so an
-- address that changes hands at Google or Apple cannot take the account over.
CREATE TABLE IF NOT EXISTS user_identities (
    provider      text        NOT NULL,
    subject       text        NOT NULL,
    user_id       text        NOT NULL,
    -- The address the provider vouched for when it was linked (Apple's relay
    -- address for "hide my email"). Shown on the account page only.
    email         text        NOT NULL DEFAULT '',
    -- Apple's refresh token, sealed (secret.DomainAppleRefresh): deleting the
    -- account revokes it at Apple (App Store Review Guideline 5.1.1(v)).
    refresh_token text        NOT NULL DEFAULT '',
    -- The client id the refresh token was issued to (the bundle id for the
    -- iOS app, the Services ID for the web); revocation must name the same one.
    client_id     text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject),
    CHECK (provider IN ('google', 'apple'))
);

CREATE INDEX IF NOT EXISTS user_identities_user_idx ON user_identities (user_id);

-- A mailed one-time link: verify an address, or reset a password. Only the
-- hash is stored; a link works once and expires. `email` is the address it
-- was sent to, so a link for an address the account no longer has is void.
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

-- An app's refresh token (internal/appsignin): the device token (a personal
-- access token) expires in days; this trades for a new pair. One row per
-- signed-in install, rotated on every use. signed_in_at is when the person
-- actually proved who they are, carried across rotations, so "sign out
-- everywhere" (platform_users.sessions_valid_after) ends the whole chain.
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

-- A Google or Apple sign-in in progress: what the callback needs to finish it.
-- Keyed by the hash of the state sent to the provider, which is also kept in
-- an HttpOnly cookie, so a callback only completes in the browser that began
-- it. Used once; ten minutes.
CREATE TABLE IF NOT EXISTS auth_oauth_states (
    state_hash    text        PRIMARY KEY,
    provider      text        NOT NULL,
    nonce         text        NOT NULL,
    -- Google's PKCE verifier (Apple's web flow has none).
    verifier      text        NOT NULL DEFAULT '',
    -- Web sign-in: where to land afterwards (a path on this site).
    next          text        NOT NULL DEFAULT '',
    -- App sign-in (internal/appsignin): the app's redirect, its PKCE
    -- challenge and state, and the device name. Empty for the web.
    app_redirect  text        NOT NULL DEFAULT '',
    app_challenge text        NOT NULL DEFAULT '',
    app_state     text        NOT NULL DEFAULT '',
    device        text        NOT NULL DEFAULT '',
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
