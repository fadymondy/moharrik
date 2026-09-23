-- The audit log (MH-392): RLS, redaction and the row-change trigger.
-- See internal/db/schema/audit.sql for the table and what writes it.

-- Workspace-scoped like every tenant table. The app role is granted SELECT
-- ONLY (migrate.ensureAppRole): the rows are written by the trigger below
-- (SECURITY DEFINER, as the owner) and by the owner pool, never by the API's
-- own role — so nothing the API does can rewrite the log it is recorded in.
ALTER TABLE audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_log FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_log;
CREATE POLICY tenant_isolation ON audit_log
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE audit_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON audit_settings;
CREATE POLICY tenant_isolation ON audit_settings
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- No foreign key from audit_log to workspaces, on purpose: deleting a
-- workspace fires the delete trigger of every row it cascades to, and each of
-- those would insert a row pointing at the workspace being deleted — a foreign
-- key would then fail the delete itself. The retention sweeper removes the
-- rows of workspaces that no longer exist instead.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'audit_settings_workspace_fk') THEN
        ALTER TABLE audit_settings ADD CONSTRAINT audit_settings_workspace_fk
            FOREIGN KEY (workspace_id) REFERENCES workspaces (id) ON DELETE CASCADE;
    END IF;
END $$;

/*
audit_redact is the one place that decides what of a value may be kept.

  - a key that names a secret (password, token, secret, hash, sealed, key,
    cookie, signature, …) → "[redacted]", whatever the value;
  - a key that names a message body (body, html, text, content, message,
    snippet, payload, result, raw) → "[hidden]": that it changed is recorded,
    never what it says;
  - objects and arrays are walked, so a secret nested in a jsonb column (a
    workflow step typing a password) is caught by its own key;
  - a text column holding a JSON document is parsed and walked the same way;
  - a value that looks like one of Moharrik's own tokens is redacted under
    any key;
  - a long string is clipped to 300 characters.

IMMUTABLE and recursive; k is NULL for array elements.
*/
CREATE OR REPLACE FUNCTION audit_redact(k text, v jsonb) RETURNS jsonb
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    out jsonb;
    kk  text;
    vv  jsonb;
    s   text;
    parsed jsonb;
BEGIN
    IF v IS NULL OR v = 'null'::jsonb THEN
        RETURN v;
    END IF;
    IF k IS NOT NULL THEN
        IF lower(k) ~ '(^|_)(secret|secrets|password|passwd|pass|pwd|token|tokens|hash|sealed|key|keys|private|credential|credentials|cookie|cookies|otp|verifier|signature|cipher|ciphertext|encrypted|authorization|bearer|jwks)(_|$)' THEN
            RETURN '"[redacted]"'::jsonb;
        END IF;
        IF lower(k) ~ '^(body|html|text|content|message|snippet|payload|result|raw)(_[a-z0-9]+)?$' THEN
            RETURN '"[hidden]"'::jsonb;
        END IF;
    END IF;
    CASE jsonb_typeof(v)
    WHEN 'object' THEN
        out := '{}'::jsonb;
        FOR kk, vv IN SELECT e.key, e.value FROM jsonb_each(v) e LOOP
            out := out || jsonb_build_object(kk, audit_redact(kk, vv));
        END LOOP;
        RETURN out;
    WHEN 'array' THEN
        SELECT coalesce(jsonb_agg(audit_redact(NULL, e.value) ORDER BY e.ord), '[]'::jsonb)
          INTO out FROM jsonb_array_elements(v) WITH ORDINALITY AS e(value, ord);
        RETURN out;
    WHEN 'string' THEN
        s := v #>> '{}';
        IF s ~ '^(mhr_|mhra_|mho_|mhc_|mhcs_|mha_)' THEN
            RETURN '"[redacted]"'::jsonb;
        END IF;
        IF left(ltrim(s), 1) IN ('{', '[') AND length(s) < 200000 THEN
            BEGIN
                parsed := s::jsonb;
                IF jsonb_typeof(parsed) IN ('object', 'array') THEN
                    RETURN audit_redact(NULL, parsed);
                END IF;
            EXCEPTION WHEN others THEN
                NULL; -- not JSON after all: a plain string
            END;
        END IF;
        IF length(s) > 300 THEN
            RETURN to_jsonb(left(s, 300) || '…');
        END IF;
        RETURN v;
    ELSE
        RETURN v;
    END CASE;
END $$;

/*
audit_row_change is the AFTER ROW trigger on every audited tenant table
(migrate.AuditedTables attaches it). It records nothing unless the statement
ran under an actor: the connection wrapper puts one in `app.audit` for
requests by a member, a token, an OAuth client or an agent, and leaves it
empty for the system's own work (collectors, sweepers, the owner pool), so
background jobs do not bury the log.

An UPDATE that changed nothing but bookkeeping columns is not recorded.
*/
CREATE OR REPLACE FUNCTION audit_row_change() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE
    raw     text;
    actor   jsonb;
    o       jsonb;
    n       jsonb;
    rec     jsonb;
    diff    jsonb := '{}'::jsonb;
    col     text;
    ws      text;
    act     text;
BEGIN
    raw := current_setting('app.audit', true);
    IF raw IS NULL OR raw = '' THEN
        RETURN NULL;
    END IF;
    BEGIN
        actor := raw::jsonb;
    EXCEPTION WHEN others THEN
        RETURN NULL;
    END;

    IF TG_OP = 'INSERT' THEN
        n := to_jsonb(NEW); act := 'create';
    ELSIF TG_OP = 'UPDATE' THEN
        o := to_jsonb(OLD); n := to_jsonb(NEW); act := 'update';
    ELSE
        o := to_jsonb(OLD); act := 'delete';
    END IF;
    rec := coalesce(n, o);

    FOR col IN SELECT jsonb_object_keys(rec) LOOP
        IF col IN ('id', 'workspace_id', 'created_at', 'updated_at', 'last_used_at', 'last_seen_at') THEN
            CONTINUE;
        END IF;
        IF TG_OP = 'UPDATE' THEN
            IF (o -> col) IS NOT DISTINCT FROM (n -> col) THEN
                CONTINUE;
            END IF;
            diff := diff || jsonb_build_object(col, jsonb_build_object(
                'before', audit_redact(col, o -> col), 'after', audit_redact(col, n -> col)));
        ELSIF TG_OP = 'INSERT' THEN
            IF (n -> col) IS NULL OR (n -> col) = 'null'::jsonb OR (n -> col) = '""'::jsonb THEN
                CONTINUE;
            END IF;
            diff := diff || jsonb_build_object(col, jsonb_build_object('after', audit_redact(col, n -> col)));
        ELSE
            IF (o -> col) IS NULL OR (o -> col) = 'null'::jsonb OR (o -> col) = '""'::jsonb THEN
                CONTINUE;
            END IF;
            diff := diff || jsonb_build_object(col, jsonb_build_object('before', audit_redact(col, o -> col)));
        END IF;
    END LOOP;

    IF TG_OP = 'UPDATE' AND diff = '{}'::jsonb THEN
        RETURN NULL;
    END IF;

    IF TG_TABLE_NAME = 'workspaces' THEN
        ws := rec ->> 'id';
    ELSE
        ws := rec ->> 'workspace_id';
    END IF;
    IF ws IS NULL OR ws = '' THEN
        RETURN NULL;
    END IF;

    INSERT INTO audit_log (workspace_id, kind, request_id, actor_kind, actor_id, actor_name, user_id,
                           via, operation, action, entity_type, entity_id, changes, ip, user_agent)
    VALUES (ws, 'change',
            coalesce(actor ->> 'rid', ''), coalesce(actor ->> 'k', ''), coalesce(actor ->> 'id', ''),
            coalesce(actor ->> 'n', ''), coalesce(actor ->> 'u', ''), coalesce(actor ->> 'v', ''),
            coalesce(actor ->> 'op', ''), act, TG_TABLE_NAME, coalesce(rec ->> 'id', ''), diff,
            coalesce(actor ->> 'ip', ''), coalesce(actor ->> 'ua', ''));
    RETURN NULL;
END $$;
