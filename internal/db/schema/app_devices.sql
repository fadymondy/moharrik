-- The Moharrik apps (docs/apps-plan.md): signing an app in through the browser,
-- the phones that receive native push, and what each member wants pushed.

-- App sign-in (internal/appsignin): the desktop and mobile apps sign in in the
-- SYSTEM browser, never an embedded one (RFC 8252). The signed-in web page asks
-- for a one-time code bound to the app's PKCE challenge and redirects back to
-- the app (a loopback port or moharrik://auth/callback); the app trades code +
-- verifier for its own device token. Platform data (not tenant-scoped): owner
-- pool only, no RLS, no app-role grant. Only the code's hash is stored.
CREATE TABLE IF NOT EXISTS app_signin_codes (
    code_hash      text        PRIMARY KEY,
    user_id        text        NOT NULL,
    redirect_uri   text        NOT NULL,
    code_challenge text        NOT NULL,
    device_name    text        NOT NULL,
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS app_signin_codes_expires ON app_signin_codes (expires_at);

-- The device token an app holds is a personal access token in the auth
-- plugin's own table and format (togo-framework/auth pat.go), so the plugin's
-- middleware authenticates it with no new code path. The plugin creates this
-- table at boot; it is declared here too, identically, so a migration (and a
-- test database) never depends on the API having booted first.
CREATE TABLE IF NOT EXISTS personal_access_tokens (
    id         text PRIMARY KEY,
    user_id    text NOT NULL,
    name       text NOT NULL,
    token_hash text NOT NULL UNIQUE,
    abilities  text NOT NULL DEFAULT '',
    expires_at text,
    created_at text NOT NULL
);

-- A phone that receives native push (FCM; iOS through Firebase's APNs bridge).
-- One row per app install, owned by a USER, not a workspace: one registration
-- reaches the member from every workspace they belong to, and who in a
-- workspace is alerted is still decided per workspace (alerts.Push, through
-- realtime.Event.For and push_prefs). Platform data like the codes above:
-- owner pool only, every query carries its own user predicate.
CREATE TABLE IF NOT EXISTS device_tokens (
    id            text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    user_id       text        NOT NULL,
    platform      text        NOT NULL,
    -- The FCM registration token. Unique: re-registering the same install (or
    -- another person signing in on it) moves the row, never duplicates it.
    token         text        NOT NULL UNIQUE,
    -- The device token (PAT) this install signed in with, so signing the app
    -- out removes its push registration too.
    pat_id        text,
    name          text        NOT NULL DEFAULT '',
    app_version   text        NOT NULL DEFAULT '',
    -- The language the lock-screen text is written in.
    locale        text        NOT NULL DEFAULT 'ar',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_ok_at    timestamptz,
    last_error    text        NOT NULL DEFAULT '',
    last_error_at timestamptz,
    CHECK (platform IN ('ios', 'android')),
    CHECK (locale IN ('en', 'ar'))
);

CREATE INDEX IF NOT EXISTS device_tokens_user_idx ON device_tokens (user_id);

-- What a member wants PUSHED to their devices (web push and the apps) in one
-- workspace. No row = everything on. Only push: the open web tab's in-page
-- alerts are the tab's own business.
CREATE TABLE IF NOT EXISTS push_prefs (
    workspace_id  text        NOT NULL,
    user_id       text        NOT NULL,
    -- A customer's new message.
    messages      boolean     NOT NULL DEFAULT true,
    -- Only messages in conversations assigned to me (and unassigned ones are quiet).
    assigned_only boolean     NOT NULL DEFAULT false,
    -- A conversation assigned to me by someone else.
    assignments   boolean     NOT NULL DEFAULT true,
    -- A new notification-center item.
    notifications boolean     NOT NULL DEFAULT true,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);
