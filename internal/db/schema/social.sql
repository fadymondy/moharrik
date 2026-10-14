-- Sign in with Google and Apple (internal/social). Platform data, not tenant-scoped: read before a
-- workspace is known (sign-in), so owner pool only, no app-role grant, RLS on with no policy
-- (internal/db/post/220_social.sql). The foreign keys to users are added there too, because the
-- auth plugin's users table is declared in 0001_users.sql and post files run after every schema file.

-- One provider account (its stable `sub`) and the Moharrik user it signs in as. A provider account
-- is one Moharrik user; a Moharrik user has at most one account per provider. The e-mail is the
-- provider's address as of the last sign-in — it is NOT how the row is found again (the subject is),
-- because a person can change it at the provider, and an Apple private-relay address
-- (@privaterelay.appleid.com) only ever matches itself.
CREATE TABLE IF NOT EXISTS user_identities (
    provider       text        NOT NULL CHECK (provider IN ('google', 'apple')),
    subject        text        NOT NULL,
    user_id        text        NOT NULL,
    email          text        NOT NULL DEFAULT '',
    email_verified boolean     NOT NULL DEFAULT false,
    -- Apple sends the person's name only on the FIRST consent, never again: kept here.
    display_name   text        NOT NULL DEFAULT '',
    -- How the row came to be: matched to an existing account by verified e-mail, linked by its
    -- signed-in owner, or a new account created by this sign-in.
    linked_via     text        NOT NULL DEFAULT 'created' CHECK (linked_via IN ('created', 'email', 'link')),
    -- Apple: the client the tokens were issued to (the Services ID on the web, the bundle id in the
    -- app) and the refresh token from the authorization-code exchange, sealed with AUTH_SECRET
    -- (internal/secret). Kept only to revoke the person's authorization when they unlink Apple or
    -- delete their account (App Store guideline 5.1.1(v)); never used to sign anyone in.
    client_id      text        NOT NULL DEFAULT '',
    refresh_token  text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject),
    UNIQUE (user_id, provider)
);

-- A sign-in in flight: started by GET /api/auth/oauth/{provider}, finished by the provider's
-- callback. Server-side because Apple's callback is a cross-site POST (response_mode=form_post)
-- that carries no SameSite=Lax cookie, so the state cannot be checked against one there. Only
-- hashes of the state and of the browser binding are stored.
CREATE TABLE IF NOT EXISTS social_login_states (
    state_hash    text        PRIMARY KEY,
    provider      text        NOT NULL,
    -- sha256 of the random value in the browser's moharrik_social cookie: the finish step (a
    -- top-level GET, which does carry Lax cookies) must come from the browser that started.
    browser_hash  text        NOT NULL,
    nonce         text        NOT NULL,
    code_verifier text        NOT NULL DEFAULT '',
    -- A local path to land on afterwards (already checked to be one).
    next_path     text        NOT NULL DEFAULT '',
    -- 'login', 'link' (attach to the signed-in user) or 'reauth' (prove it is them, for deletion).
    intent        text        NOT NULL DEFAULT 'login',
    -- The Moharrik user signed in in this browser when the sign-in started, or ''. Only that
    -- lets an existing password account be linked by e-mail (the CircleXO rule, sso.go).
    signed_in_user  text        NOT NULL DEFAULT '',
    -- Set by the callback once the provider vouched for the person: who to sign in at finish.
    result_user   text        NOT NULL DEFAULT '',
    result_error  text        NOT NULL DEFAULT '',
    finish_hash   text,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS social_login_states_expires ON social_login_states (expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS social_login_states_finish ON social_login_states (finish_hash) WHERE finish_hash IS NOT NULL;

-- Apple id_token nonces already used by the native endpoints, so a captured token cannot be
-- replayed within its lifetime.
CREATE TABLE IF NOT EXISTS social_used_nonces (
    nonce_hash text        PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
