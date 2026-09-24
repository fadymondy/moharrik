-- Automation rules (MH-427): the events, raised where the rows are written.
--
-- Each trigger below turns a write into an event in automation_rule_events —
-- but only when an enabled rule in that workspace listens to that event type,
-- so a workspace with no rules pays one indexed EXISTS per write and nothing
-- else. They are database triggers rather than calls in Go because every one
-- of these rows is written from several places (a deal is opened from the
-- board, from a conversation, from a converted lead and over MCP; a contact's
-- stage moves by hand and by five other triggers), and an event raised in Go
-- would be raised from some of them and forgotten in the rest.
--
-- The loop guard's input: a rule's actions run with the chain of rules that
-- caused them in the app.audit setting (key "rc", internal/tenant/audit.go),
-- which the connection wrapper sets before every statement. The event keeps
-- that chain; the dispatcher refuses to run a rule on an event its own chain
-- caused, and stops every chain at three rules deep.
--
-- Invoker's rights, not SECURITY DEFINER: under the app pool the statement
-- already runs in NEW.workspace_id (RLS guarantees it), and the owner pool
-- bypasses RLS. Nothing here widens what the writer could do.
--
-- Batch P's block is 125–129. Every statement is idempotent.

CREATE OR REPLACE FUNCTION rule_event(p_ws text, p_type text, p_subject text, p_data jsonb) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    a  jsonb;
    rc text := '';
BEGIN
    IF p_ws IS NULL OR NOT EXISTS (
        SELECT 1 FROM automation_rules WHERE workspace_id = p_ws AND enabled AND trigger = p_type) THEN
        RETURN;
    END IF;
    BEGIN
        a := nullif(current_setting('app.audit', true), '')::jsonb;
        rc := coalesce(a ->> 'rc', '');
    EXCEPTION WHEN others THEN
        rc := '';
    END;
    INSERT INTO automation_rule_events (workspace_id, type, subject_id, data, chain)
    VALUES (p_ws, p_type, coalesce(p_subject, ''), jsonb_strip_nulls(coalesce(p_data, '{}'::jsonb)),
            CASE WHEN rc = '' THEN '{}'::text[] ELSE string_to_array(rc, '>') END);
END $$;

-- Free text an event carries (a message, a note) is clipped: the full text is
-- on the record the event names.
CREATE OR REPLACE FUNCTION rule_clip(s text, n int) RETURNS text IMMUTABLE LANGUAGE sql AS $$
    SELECT CASE WHEN length(coalesce(s, '')) > n THEN left(s, n) || '…' ELSE coalesce(s, '') END
$$;

-- ---- lead.created ------------------------------------------------------------------
CREATE OR REPLACE FUNCTION rule_on_lead() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM rule_event(NEW.workspace_id, 'lead.created', NEW.id, jsonb_build_object(
        'lead_id', NEW.id, 'name', NEW.name, 'email', NEW.email, 'phone', NEW.phone, 'company', NEW.company,
        'message', rule_clip(NEW.message, 1000), 'form_name', NEW.form_name, 'locale', NEW.locale,
        'budget', NEW.budget, 'utm_source', NEW.utm_source, 'utm_medium', NEW.utm_medium,
        'utm_campaign', NEW.utm_campaign, 'status', NEW.status));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS leads_rule_event ON leads;
CREATE TRIGGER leads_rule_event AFTER INSERT ON leads FOR EACH ROW EXECUTE FUNCTION rule_on_lead();

-- ---- deal.created, deal.stage_changed, deal.won, deal.lost ---------------------------
CREATE OR REPLACE FUNCTION rule_deal_data(d deals, from_stage text, from_kind text) RETURNS jsonb LANGUAGE sql STABLE AS $$
    SELECT jsonb_build_object(
        'deal_id', d.id, 'title', d.title, 'value_minor', d.value_minor, 'value', round(d.value_minor / 100.0, 2),
        'currency', d.currency, 'stage_id', d.stage_id, 'stage', s.name, 'stage_kind', s.kind,
        'from_stage', from_stage, 'from_stage_kind', from_kind,
        'contact_id', d.contact_id, 'contact_email', c.email, 'contact_name', c.name,
        'company_id', d.company_id, 'company_name', co.name, 'owner_user_id', d.owner_user_id)
    FROM (SELECT 1) one
    LEFT JOIN pipeline_stages s ON s.workspace_id = d.workspace_id AND s.id = d.stage_id
    LEFT JOIN contacts c ON c.workspace_id = d.workspace_id AND c.id = d.contact_id
    LEFT JOIN companies co ON co.workspace_id = d.workspace_id AND co.id = d.company_id
$$;

CREATE OR REPLACE FUNCTION rule_on_deal() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    old_name text;
    old_kind text;
    new_kind text;
BEGIN
    IF TG_OP = 'INSERT' THEN
        PERFORM rule_event(NEW.workspace_id, 'deal.created', NEW.id, rule_deal_data(NEW, NULL, NULL));
        RETURN NULL;
    END IF;
    IF NEW.stage_id IS NOT DISTINCT FROM OLD.stage_id THEN
        RETURN NULL;
    END IF;
    SELECT name, kind INTO old_name, old_kind FROM pipeline_stages WHERE workspace_id = OLD.workspace_id AND id = OLD.stage_id;
    SELECT kind INTO new_kind FROM pipeline_stages WHERE workspace_id = NEW.workspace_id AND id = NEW.stage_id;
    PERFORM rule_event(NEW.workspace_id, 'deal.stage_changed', NEW.id, rule_deal_data(NEW, old_name, old_kind));
    IF new_kind = 'won' AND old_kind IS DISTINCT FROM 'won' THEN
        PERFORM rule_event(NEW.workspace_id, 'deal.won', NEW.id, rule_deal_data(NEW, old_name, old_kind));
    ELSIF new_kind = 'lost' AND old_kind IS DISTINCT FROM 'lost' THEN
        PERFORM rule_event(NEW.workspace_id, 'deal.lost', NEW.id, rule_deal_data(NEW, old_name, old_kind));
    END IF;
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS deals_rule_event ON deals;
CREATE TRIGGER deals_rule_event AFTER INSERT OR UPDATE OF stage_id ON deals FOR EACH ROW EXECUTE FUNCTION rule_on_deal();

-- ---- contact.lifecycle_changed -----------------------------------------------------
-- Every stage change is written to contact_lifecycle_events, by hand or by the
-- lifecycle triggers (106_lifecycle_icp.sql), so this one trigger hears them all.
CREATE OR REPLACE FUNCTION rule_on_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM rule_event(NEW.workspace_id, 'contact.lifecycle_changed', NEW.contact_id, (
        SELECT jsonb_build_object('contact_id', NEW.contact_id, 'email', c.email, 'name', c.name, 'phone', c.phone,
            'locale', c.locale, 'from_stage', NEW.from_stage, 'to_stage', NEW.to_stage, 'source', NEW.source)
        FROM (SELECT 1) one LEFT JOIN contacts c ON c.workspace_id = NEW.workspace_id AND c.id = NEW.contact_id));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS contact_lifecycle_rule_event ON contact_lifecycle_events;
CREATE TRIGGER contact_lifecycle_rule_event AFTER INSERT ON contact_lifecycle_events FOR EACH ROW EXECUTE FUNCTION rule_on_lifecycle();

-- ---- contact.opted_in --------------------------------------------------------------
-- By email: a subscription became 'subscribed' (a confirmed double opt-in, or a
-- single opt-in list). By WhatsApp: the contact's WhatsApp opt-in was recorded
-- and no STOP stands against it.
CREATE OR REPLACE FUNCTION rule_on_subscription() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status <> 'subscribed' OR (TG_OP = 'UPDATE' AND OLD.status = 'subscribed') THEN
        RETURN NULL;
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'contact.opted_in', NEW.contact_id, (
        SELECT jsonb_build_object('contact_id', NEW.contact_id, 'email', c.email, 'name', c.name, 'phone', c.phone,
            'locale', c.locale, 'channel', 'email', 'list_id', NEW.list_id, 'list_name', l.name)
        FROM (SELECT 1) one
        LEFT JOIN contacts c ON c.workspace_id = NEW.workspace_id AND c.id = NEW.contact_id
        LEFT JOIN mail_lists l ON l.workspace_id = NEW.workspace_id AND l.id = NEW.list_id));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS list_subscriptions_rule_event ON list_subscriptions;
CREATE TRIGGER list_subscriptions_rule_event AFTER INSERT OR UPDATE OF status ON list_subscriptions
    FOR EACH ROW EXECUTE FUNCTION rule_on_subscription();

CREATE OR REPLACE FUNCTION rule_on_whatsapp_opt_in() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.whatsapp_opt_in_at IS NULL OR NEW.whatsapp_opt_out_at IS NOT NULL THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.whatsapp_opt_in_at IS NOT NULL AND OLD.whatsapp_opt_out_at IS NULL THEN
        RETURN NULL; -- already opted in: nothing new happened
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'contact.opted_in', NEW.id, jsonb_build_object(
        'contact_id', NEW.id, 'email', NEW.email, 'name', NEW.name, 'phone', NEW.phone, 'locale', NEW.locale,
        'channel', 'whatsapp'));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS contacts_whatsapp_rule_event ON contacts;
CREATE TRIGGER contacts_whatsapp_rule_event AFTER INSERT OR UPDATE OF whatsapp_opt_in_at, whatsapp_opt_out_at ON contacts
    FOR EACH ROW EXECUTE FUNCTION rule_on_whatsapp_opt_in();

-- ---- campaign.sent -------------------------------------------------------------------
CREATE OR REPLACE FUNCTION rule_on_campaign() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status <> 'sent' OR OLD.status = 'sent' THEN
        RETURN NULL;
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'campaign.sent', NEW.id, (
        SELECT jsonb_build_object('campaign_id', NEW.id, 'subject', NEW.subject, 'channel', NEW.channel,
            'list_id', NEW.list_id, 'list_name', l.name, 'sent_count', NEW.sent_count, 'failed_count', NEW.failed_count,
            'recipient_count', NEW.recipient_count)
        FROM (SELECT 1) one LEFT JOIN mail_lists l ON l.workspace_id = NEW.workspace_id AND l.id = NEW.list_id));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS campaigns_rule_event ON campaigns;
CREATE TRIGGER campaigns_rule_event AFTER UPDATE OF status ON campaigns FOR EACH ROW EXECUTE FUNCTION rule_on_campaign();

-- ---- automation.run_failed -----------------------------------------------------------
CREATE OR REPLACE FUNCTION rule_on_run() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status <> 'failed' OR OLD.status = 'failed' THEN
        RETURN NULL;
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'automation.run_failed', NEW.id, (
        SELECT jsonb_build_object('run_id', NEW.id, 'error', rule_clip(NEW.error, 500), 'trigger', NEW.trigger,
            'workflow_id', nullif(NEW.workflow_id, ''), 'workflow_name', w.name, 'browser_id', NEW.agent_id, 'browser_name', b.name)
        FROM (SELECT 1) one
        LEFT JOIN automation_workflows w ON w.workspace_id = NEW.workspace_id AND w.id = NEW.workflow_id
        LEFT JOIN browser_agents b ON b.workspace_id = NEW.workspace_id AND b.id = NEW.agent_id));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS automation_runs_rule_event ON automation_runs;
CREATE TRIGGER automation_runs_rule_event AFTER UPDATE OF status ON automation_runs FOR EACH ROW EXECUTE FUNCTION rule_on_run();

-- ---- notification.received -----------------------------------------------------------
CREATE OR REPLACE FUNCTION rule_on_notification() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM rule_event(NEW.workspace_id, 'notification.received', NEW.id, (
        SELECT jsonb_build_object('item_id', NEW.id, 'source_name', NEW.source_name,
            'source_kind', coalesce(ws.kind, ns.kind, 'internal'), 'kind', NEW.kind, 'severity', NEW.severity,
            'title', NEW.title, 'body', rule_clip(NEW.body, 1000), 'url', NEW.url, 'actor', NEW.actor)
        FROM (SELECT 1) one
        LEFT JOIN webhook_sources ws ON ws.workspace_id = NEW.workspace_id AND ws.id = NEW.source_id
        LEFT JOIN notification_sources ns ON ns.workspace_id = NEW.workspace_id AND ns.id = NEW.feed_source_id));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS notification_items_rule_event ON notification_items;
CREATE TRIGGER notification_items_rule_event AFTER INSERT ON notification_items FOR EACH ROW EXECUTE FUNCTION rule_on_notification();

-- ---- conversation.created, message.received -----------------------------------------
-- A customer's message, on any channel. Mail a mailbox's first poll files from
-- weeks ago is not news (its created_at is the mail's own date), so nothing
-- older than a day raises an event — the same line the alerts draw (MH-378).
CREATE OR REPLACE FUNCTION rule_on_message() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    data  jsonb;
    first boolean;
BEGIN
    IF NEW.direction <> 'in' OR NEW.created_at < now() - interval '1 day' THEN
        RETURN NULL;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM automation_rules WHERE workspace_id = NEW.workspace_id AND enabled
                   AND trigger IN ('message.received', 'conversation.created')) THEN
        RETURN NULL;
    END IF;
    SELECT jsonb_build_object('conversation_id', c.id, 'message_id', NEW.id, 'channel', c.channel,
            'text', rule_clip(NEW.body, 1000), 'customer_name', c.customer_name, 'customer_email', c.customer_email,
            'customer_phone', c.customer_phone, 'contact_id', c.contact_id, 'assignee_user_id', c.assignee_user_id,
            'subject', c.subject, 'locale', c.locale)
      INTO data
      FROM conversations c WHERE c.workspace_id = NEW.workspace_id AND c.id = NEW.conversation_id;
    first := NOT EXISTS (SELECT 1 FROM messages m WHERE m.workspace_id = NEW.workspace_id
                         AND m.conversation_id = NEW.conversation_id AND m.id <> NEW.id);
    IF first THEN
        PERFORM rule_event(NEW.workspace_id, 'conversation.created', NEW.conversation_id, data);
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'message.received', NEW.conversation_id, data);
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS messages_rule_event ON messages;
CREATE TRIGGER messages_rule_event AFTER INSERT ON messages FOR EACH ROW EXECUTE FUNCTION rule_on_message();

-- ---- trend.topic -----------------------------------------------------------------------
-- A story is raised when it first scores and again each time its score climbs
-- into a higher band of ten, so "score at least 60" fires when it gets there
-- rather than on every recompute. A rule set to run once per record then runs
-- once per story.
CREATE OR REPLACE FUNCTION rule_on_trend() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.score <= 0 OR NEW.dismissed_at IS NOT NULL THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'UPDATE' AND floor(NEW.score / 10) <= floor(OLD.score / 10) THEN
        RETURN NULL;
    END IF;
    PERFORM rule_event(NEW.workspace_id, 'trend.topic', NEW.id, jsonb_build_object(
        'topic_id', NEW.id, 'title', NEW.title, 'summary', rule_clip(NEW.summary, 600), 'score', round(NEW.score::numeric, 1),
        'source_count', NEW.source_count, 'item_count', NEW.item_count,
        'reason', coalesce(NEW.reasons -> 0 ->> 'label', ''), 'outlets', array_to_string(NEW.outlets, ', ')));
    RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS trend_clusters_rule_event ON trend_clusters;
CREATE TRIGGER trend_clusters_rule_event AFTER INSERT OR UPDATE OF score ON trend_clusters FOR EACH ROW EXECUTE FUNCTION rule_on_trend();
