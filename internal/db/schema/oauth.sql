-- OAuth 2.1 for the user MCP (MH-391): what claude.ai and ChatGPT connectors
-- use to add Moharrik by URL, with no pasted token. Ported from fadymondy.com
-- (FM-283) with the multi-tenant difference: a grant is bound to ONE workspace
-- and capped by the approving member's role there, exactly like an mcp_token.
--
-- Every secret (codes, access and refresh tokens, client secrets) is stored as
-- its sha256, like mcp_tokens.token_hash.
--
-- Platform tables (no grant to the app role, RLS on with no policy, owner pool
-- only — 95_mcp_oauth.sql): oauth_clients, oauth_codes, oauth_tokens,
-- oauth_client_assertions. A client is not a tenant's: one claude.ai
-- registration can be granted into many workspaces.
--
-- Tenant table (app role, RLS by workspace): oauth_grants — the "connected
-- apps" a workspace lists and revokes in Settings → MCP.

-- A registered client: dynamic registration (RFC 7591), or a Client ID
-- Metadata Document, whose id is the https URL of the document.
CREATE TABLE IF NOT EXISTS oauth_clients (
    id                         text        PRIMARY KEY,
    client_name                text        NOT NULL DEFAULT '',
    client_uri                 text        NOT NULL DEFAULT '',
    -- JSON array of exact redirect URIs. Matched byte for byte.
    redirect_uris              text        NOT NULL DEFAULT '[]',
    -- none | client_secret_post | client_secret_basic | private_key_jwt
    token_endpoint_auth_method text        NOT NULL DEFAULT 'none',
    secret_hash                text        NOT NULL DEFAULT '',
    -- private_key_jwt clients (ChatGPT): where their public keys are.
    jwks_uri                   text        NOT NULL DEFAULT '',
    jwks                       text        NOT NULL DEFAULT '',
    -- Advisory: the address that registered it.
    ip                         text        NOT NULL DEFAULT '',
    created_at                 timestamptz NOT NULL DEFAULT now(),
    updated_at                 timestamptz NOT NULL DEFAULT now()
);

-- A connection: one client, one member, ONE workspace, a set of scopes. It is
-- created when the member approves on the consent screen and becomes active
-- when the client redeems the code (activated_at). Revoking it kills every
-- token under it.
CREATE TABLE IF NOT EXISTS oauth_grants (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    user_id      text        NOT NULL,
    client_id    text        NOT NULL,
    -- Shown in Settings without joining the platform table from the app pool.
    client_name  text        NOT NULL DEFAULT '',
    client_uri   text        NOT NULL DEFAULT '',
    -- Space-separated scope names, as approved.
    scopes       text        NOT NULL DEFAULT '',
    -- The audience (RFC 8707): the MCP endpoint's URL.
    resource     text        NOT NULL DEFAULT '',
    activated_at timestamptz,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, id)
);

CREATE INDEX IF NOT EXISTS oauth_grants_workspace_idx ON oauth_grants (workspace_id, created_at DESC);

-- An authorization code: single use, two minutes, bound to the client, the
-- redirect URI and the PKCE challenge.
CREATE TABLE IF NOT EXISTS oauth_codes (
    code_hash      text        PRIMARY KEY,
    grant_id       text        NOT NULL,
    client_id      text        NOT NULL,
    redirect_uri   text        NOT NULL,
    code_challenge text        NOT NULL,
    expires_at     timestamptz NOT NULL,
    used_at        timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS oauth_codes_sweep_idx ON oauth_codes (expires_at);

-- Access and refresh tokens. A refresh token is good once (rotation); seeing a
-- spent one again revokes the whole grant.
CREATE TABLE IF NOT EXISTS oauth_tokens (
    token_hash text        PRIMARY KEY,
    grant_id   text        NOT NULL,
    -- access | refresh
    kind       text        NOT NULL,
    scopes     text        NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (kind IN ('access', 'refresh'))
);

CREATE INDEX IF NOT EXISTS oauth_tokens_grant_idx ON oauth_tokens (grant_id);

-- private_key_jwt assertions already presented (jti replay protection).
CREATE TABLE IF NOT EXISTS oauth_client_assertions (
    client_id  text        NOT NULL,
    jti        text        NOT NULL,
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (client_id, jti)
);
