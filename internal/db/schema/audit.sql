-- The audit log (MH-392): every write by a member, an API token, an OAuth MCP
-- client or an agent, with who, what, when, from where, and a before/after
-- diff of the fields that changed.
--
-- Rows are written in two places, both below the handlers so no handler can
-- forget (internal/db/post/96_audit_log.sql, internal/audit):
--
--   kind = 'change'   one row per changed row of a tenant table, by the
--                     audit_row_change() trigger. The actor, address, user
--                     agent and operation come from the `app.audit` setting
--                     the connection wrapper sets before every statement
--                     (internal/tenant/rlsdb.go).
--   kind = 'request'  one row for a write request that changed no audited
--                     row — a refused one, a send, a sync — written by the
--                     REST middleware and the MCP dispatcher.
--
-- The app role may only SELECT it (96_audit_log.sql): the log cannot be
-- rewritten through the API that it records.
CREATE TABLE IF NOT EXISTS audit_log (
    id           text        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id text        NOT NULL,
    occurred_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    -- change | request
    kind         text        NOT NULL DEFAULT 'change',
    -- Ties the change rows of one request (or one MCP call) together.
    request_id   text        NOT NULL DEFAULT '',
    -- member | token | oauth | agent | admin
    actor_kind   text        NOT NULL DEFAULT '',
    -- users.id, mcp_tokens.id, oauth_grants.id, browser_agents.id
    actor_id     text        NOT NULL DEFAULT '',
    -- The email, token name, client name or browser name at the time.
    actor_name   text        NOT NULL DEFAULT '',
    -- The member a token, client or agent acts for (empty for an agent).
    user_id      text        NOT NULL DEFAULT '',
    -- web | mcp | graphql | agent | admin — the channel.
    via          text        NOT NULL DEFAULT '',
    -- The REST operation id, "mcp:<tool>", or "graphql".
    operation    text        NOT NULL DEFAULT '',
    -- create | update | delete for a change row; the operation for a request row.
    action       text        NOT NULL DEFAULT '',
    entity_type  text        NOT NULL DEFAULT '',
    entity_id    text        NOT NULL DEFAULT '',
    -- {"field": {"before": …, "after": …}} with secrets redacted and message
    -- bodies hidden (audit_redact).
    changes      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- The HTTP status of a request row (0 for change rows: they committed).
    status       integer     NOT NULL DEFAULT 0,
    ip           text        NOT NULL DEFAULT '',
    user_agent   text        NOT NULL DEFAULT '',
    CHECK (kind IN ('change', 'request'))
);

CREATE INDEX IF NOT EXISTS audit_log_workspace_time_idx ON audit_log (workspace_id, occurred_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS audit_log_request_idx ON audit_log (request_id) WHERE request_id <> '';
CREATE INDEX IF NOT EXISTS audit_log_actor_idx ON audit_log (workspace_id, actor_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS audit_log_entity_idx ON audit_log (workspace_id, entity_type, entity_id);

-- Retention (MH-392): rows older than retention_days are deleted by the daily
-- sweeper (internal/audit/retention.go). No row means the default, 365; an
-- admin changes it on the Audit log page. Its own table rather than a column on
-- workspaces, which is a generated resource.
CREATE TABLE IF NOT EXISTS audit_settings (
    workspace_id   text        PRIMARY KEY,
    retention_days integer     NOT NULL DEFAULT 365,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    CHECK (retention_days BETWEEN 30 AND 3650)
);
