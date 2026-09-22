-- Row-level security: the tenancy backstop.
--
-- The API talks to Postgres through two pools (internal/app/app.go):
--   * the APP pool (APP_DATABASE_URL) — a role that owns nothing, is not a
--     superuser and has no BYPASSRLS. Every generated handler, resolver and ORM
--     query goes through it. Before each statement the connection wrapper in
--     internal/tenant/rlsdb.go runs set_config('app.workspace_id', <ctx>, false),
--     so a request can only ever see its own workspace's rows.
--   * the OWNER pool (DATABASE_URL) — used only for tenant resolution, auth,
--     token lookup and the admin MCP. It bypasses RLS by being the table owner.
--
-- No workspace in context => the setting is '' => every policy is false.
-- current_setting(..., true) returns NULL when the setting was never defined,
-- which is also false. Default-deny either way.
--
-- Grants to the app role are applied by cmd/migrate (the role name comes from
-- APP_DATABASE_URL, not from this file).

ALTER TABLE workspaces ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspaces FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON workspaces;
CREATE POLICY tenant_isolation ON workspaces
    USING (id = current_setting('app.workspace_id', true))
    WITH CHECK (id = current_setting('app.workspace_id', true));

ALTER TABLE workspace_members ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_members FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON workspace_members;
CREATE POLICY tenant_isolation ON workspace_members
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON invitations;
CREATE POLICY tenant_isolation ON invitations
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE mcp_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_tokens FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON mcp_tokens;
CREATE POLICY tenant_isolation ON mcp_tokens
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Platform-only tables: RLS on with NO policy = nothing visible to the app role
-- even if a grant is ever added by mistake.
ALTER TABLE admin_mcp_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE admin_mcp_tokens FORCE ROW LEVEL SECURITY;

ALTER TABLE mail_lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE mail_lists FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON mail_lists;
CREATE POLICY tenant_isolation ON mail_lists
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON contacts;
CREATE POLICY tenant_isolation ON contacts
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE list_subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE list_subscriptions FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON list_subscriptions;
CREATE POLICY tenant_isolation ON list_subscriptions
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaigns FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON campaigns;
CREATE POLICY tenant_isolation ON campaigns
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE smtp_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE smtp_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON smtp_settings;
CREATE POLICY tenant_isolation ON smtp_settings
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE companies ENABLE ROW LEVEL SECURITY;
ALTER TABLE companies FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON companies;
CREATE POLICY tenant_isolation ON companies
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE pipeline_stages ENABLE ROW LEVEL SECURITY;
ALTER TABLE pipeline_stages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON pipeline_stages;
CREATE POLICY tenant_isolation ON pipeline_stages
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE deals ENABLE ROW LEVEL SECURITY;
ALTER TABLE deals FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON deals;
CREATE POLICY tenant_isolation ON deals
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE activities FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON activities;
CREATE POLICY tenant_isolation ON activities
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Notifications. webhook_sources is also read by the owner pool (a.SysDB) in
-- the one public route, POST /api/hooks/{id}, to resolve which workspace a
-- delivery belongs to before anything runs on the app pool — see
-- internal/notifications/webhook.go for why that is safe.
ALTER TABLE webhook_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_sources FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON webhook_sources;
CREATE POLICY tenant_isolation ON webhook_sources
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE notification_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE notification_items FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON notification_items;
CREATE POLICY tenant_isolation ON notification_items
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Inbox. chat_widgets is also read by the owner pool (a.SysDB) in the two public
-- routes, POST/GET /api/chat/{key}/messages, to resolve which workspace a visitor's
-- message belongs to before anything runs on the app pool — the same shape as
-- internal/notifications' webhook receiver and internal/marketing's mail links.
ALTER TABLE chat_widgets ENABLE ROW LEVEL SECURITY;
ALTER TABLE chat_widgets FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON chat_widgets;
CREATE POLICY tenant_isolation ON chat_widgets
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE conversations ENABLE ROW LEVEL SECURITY;
ALTER TABLE conversations FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON conversations;
CREATE POLICY tenant_isolation ON conversations
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE messages FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON messages;
CREATE POLICY tenant_isolation ON messages
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Trends. The collector runs on the app pool inside tenant.WithWorkspace for one
-- workspace at a time (internal/trends/collector.go), so these policies apply to
-- it exactly as they do to a request.
ALTER TABLE feed_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE feed_sources FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON feed_sources;
CREATE POLICY tenant_isolation ON feed_sources
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE topics ENABLE ROW LEVEL SECURITY;
ALTER TABLE topics FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON topics;
CREATE POLICY tenant_isolation ON topics
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE news_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE news_items FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON news_items;
CREATE POLICY tenant_isolation ON news_items
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Automation. The socket resolves a browser by token hash on the OWNER pool
-- (it arrives with no workspace in hand), then everything it does runs on the
-- app pool inside tenant.WithWorkspace — so these policies apply to a browser's
-- own traffic exactly as they do to a request from the web app.
ALTER TABLE browser_agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE browser_agents FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON browser_agents;
CREATE POLICY tenant_isolation ON browser_agents
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE agent_pairings ENABLE ROW LEVEL SECURITY;
ALTER TABLE agent_pairings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON agent_pairings;
CREATE POLICY tenant_isolation ON agent_pairings
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE automation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_runs;
CREATE POLICY tenant_isolation ON automation_runs
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE automation_run_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_run_steps FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_run_steps;
CREATE POLICY tenant_isolation ON automation_run_steps
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE automation_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_settings FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_settings;
CREATE POLICY tenant_isolation ON automation_settings
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- The inbox's email channel. The IMAP collector runs on the app pool inside
-- tenant.WithWorkspace, one workspace at a time, so this policy applies to it
-- exactly as it does to a request.
ALTER TABLE mailboxes ENABLE ROW LEVEL SECURITY;
ALTER TABLE mailboxes FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON mailboxes;
CREATE POLICY tenant_isolation ON mailboxes
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- The inbox's WhatsApp channel. The public webhook resolves the workspace from
-- the unguessable id in its URL on the OWNER pool, then everything it does runs
-- on the app pool inside that workspace — the same shape as a chat widget.
ALTER TABLE whatsapp_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE whatsapp_links FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON whatsapp_links;
CREATE POLICY tenant_isolation ON whatsapp_links
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Automation schedules. The scheduler reads its queue on the owner pool ("which
-- workspaces have work due" is cross-tenant by definition) and then does
-- everything for one schedule inside that schedule's own workspace.
ALTER TABLE automation_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_schedules FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_schedules;
CREATE POLICY tenant_isolation ON automation_schedules
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Saved workflows.
ALTER TABLE automation_workflows ENABLE ROW LEVEL SECURITY;
ALTER TABLE automation_workflows FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON automation_workflows;
CREATE POLICY tenant_isolation ON automation_workflows
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Research runs and their sources (MH-227). A run reads the public web and
-- writes what it found; the sources are the evidence for the summary, so they
-- are isolated exactly as strictly as the run itself.
ALTER TABLE research_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE research_runs FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON research_runs;
CREATE POLICY tenant_isolation ON research_runs
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

ALTER TABLE research_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE research_sources FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON research_sources;
CREATE POLICY tenant_isolation ON research_sources
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));

-- Subscribe forms (MH-318). The PUBLIC endpoint resolves a form on the owner
-- pool — it has no workspace in hand until the key is matched — and then does
-- everything else inside that workspace, so this policy still governs every
-- write a visitor's submission causes.
ALTER TABLE subscribe_forms ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscribe_forms FORCE ROW LEVEL SECURITY;
DROP POLICY IF EXISTS tenant_isolation ON subscribe_forms;
CREATE POLICY tenant_isolation ON subscribe_forms
    USING (workspace_id = current_setting('app.workspace_id', true))
    WITH CHECK (workspace_id = current_setting('app.workspace_id', true));
