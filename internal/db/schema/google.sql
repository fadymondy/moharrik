-- Google accounts a workspace has connected (MH-439): the foundation for GA4,
-- Search Console and YouTube (MH-440..447).
--
-- Every workspace is a business or product, so each connects its OWN Google
-- accounts through user OAuth. A service account cannot work multi-tenant:
-- every tenant would have to add Moharrik's service account to their property
-- by hand. Several accounts per workspace are allowed (a brand account for
-- YouTube, a marketing account for GA4), one row per (workspace, Google
-- account), with the scopes Google actually granted.
--
-- Isolated by RLS like every other tenant table (internal/db/post/40_google.sql).

CREATE TABLE IF NOT EXISTS google_connections (
    id                   text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id         text        NOT NULL,
    -- Google's stable account id (the id_token's "sub"). The email can change;
    -- this cannot, so it is what makes a reconnect update the same row.
    google_sub           text        NOT NULL,
    email                text        NOT NULL DEFAULT '',
    -- The offline grant, sealed with internal/secret under
    -- moharrik/google-refresh/v1. No read path selects it: only the token
    -- source (internal/google/token.go) and disconnect ever read this column.
    refresh_token_sealed text        NOT NULL DEFAULT '',
    -- What Google granted, exactly as its token endpoint reported it. The
    -- products a connection serves are derived from this, never assumed from
    -- what was asked for: Google's granular consent lets a box be unticked.
    granted_scopes       text[]      NOT NULL DEFAULT '{}',
    -- '' = usable; 'reauth' = Google refused the stored grant (revoked,
    -- expired, password change) and someone must reconnect.
    status               text        NOT NULL DEFAULT '',
    auth_error           text        NOT NULL DEFAULT '',
    connected_by         text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id),
    UNIQUE (workspace_id, google_sub),
    CHECK (status IN ('', 'reauth'))
);

-- A connect attempt in flight. The callback carries no X-Workspace-ID, so the
-- workspace, the user and the product travel here, keyed by the sha256 of the
-- state Google echoes back. Single use (the callback deletes it before doing
-- anything else) and short-lived (expires_at, 10 minutes).
CREATE TABLE IF NOT EXISTS google_oauth_states (
    state_hash      text        PRIMARY KEY,
    -- The PKCE verifier, sealed (moharrik/google-oauth-verifier/v1).
    verifier_sealed text        NOT NULL,
    workspace_id    text        NOT NULL,
    user_id         text        NOT NULL,
    -- analytics | search_console | youtube | contacts (MH-585)
    product         text        NOT NULL,
    -- The scopes this attempt needs granted; a grant missing any is refused.
    scopes          text[]      NOT NULL DEFAULT '{}',
    -- Where the browser goes afterwards. Built by the server from the
    -- workspace's slug and the page locale, never taken from the request, so
    -- the callback cannot be turned into an open redirect.
    return_to       text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    CHECK (product IN ('analytics', 'search_console', 'youtube', 'contacts'))
);
