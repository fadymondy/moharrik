-- Connecting a browser by approval (MH-478): the extension asks, a signed-in
-- member approves it on the site, the extension collects its token.
--
-- This replaced the six-digit code a person read off the extension and typed
-- into the app (MH-61). That flow needed the extension to know the workspace
-- up front and a person to carry a code between two screens; this one is the
-- device-authorization shape fadymondy.com uses (FM-89): the extension opens
-- the approval page itself, and the member picks the workspace there.
--
-- A request has NO workspace until it is approved — the extension does not
-- know which workspace it will belong to, and a request must not be readable
-- by any workspace before one claims it. So this is a PLATFORM table, like
-- admin_mcp_tokens: no grant to the app role, RLS on with no policy (see
-- internal/db/post/85_automation_connect.sql), and every statement on it runs
-- on the owner pool with an explicit predicate on a hash nobody can guess.
--
-- The row holds no usable secret at rest. The code, the poll token and the
-- browser key are stored hashed; the minted agent token lives here only
-- between approval and the extension collecting it, then is blanked.
CREATE TABLE IF NOT EXISTS agent_connects (
    id                text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    -- sha256 of the code shown in the popup and in the approval page's address.
    -- It names WHICH request is being looked at; it is not enough to approve it.
    code_hash         text        NOT NULL,
    -- sha256 of the poll token, which never leaves the extension. Polling uses
    -- this, never the code: whoever saw the code could otherwise race the
    -- extension and collect the browser's token.
    poll_hash         text        NOT NULL,
    -- sha256 of the browser key. The extension puts the key in the approval
    -- page's URL FRAGMENT, which a browser never sends to any server and which
    -- exists only in the tab the extension opened. Approving needs it, so a
    -- person who only glimpsed the code — over a shoulder, in a screenshot —
    -- cannot approve this browser into THEIR workspace and then drive it.
    key_hash          text        NOT NULL,
    fingerprint       text        NOT NULL DEFAULT '',
    platform          text        NOT NULL DEFAULT '',
    browser_version   text        NOT NULL DEFAULT '',
    extension_version text        NOT NULL DEFAULT '',
    -- What the extension suggests calling this browser. From an
    -- unauthenticated caller, so shown as a claim and never trusted.
    requested_name    text        NOT NULL DEFAULT '',
    -- Advisory only, from the proxy header. It makes an unexpected request
    -- look unexpected; it proves nothing.
    ip                text        NOT NULL DEFAULT '',
    -- pending | approved | claimed | denied
    status            text        NOT NULL DEFAULT 'pending',
    -- Set when a member approves: the workspace it joined and the browser row.
    workspace_id      text,
    agent_id          text        NOT NULL DEFAULT '',
    -- users.id of whoever approved or denied it.
    decided_by        text        NOT NULL DEFAULT '',
    decided_at        timestamptz,
    -- The minted token, readable exactly once by the extension that started
    -- this request, then blanked. Empty at every other moment.
    token_once        text        NOT NULL DEFAULT '',
    expires_at        timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (code_hash),
    UNIQUE (poll_hash),
    CHECK (status IN ('pending', 'approved', 'claimed', 'denied'))
);

CREATE INDEX IF NOT EXISTS agent_connects_sweep_idx ON agent_connects (expires_at);
