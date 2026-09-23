package server

/*
The audit log (MH-392), against the real stack.

TestAuditEveryWrite is the proof that the log is implemented once rather than
per handler: it walks EVERY mutating route on the router in the workspace tier
and calls each as the workspace owner, then calls every write MCP tool, and
asserts that each request left at least one row under its X-Request-Id — a
change row from the trigger, or a request row for a write that changed nothing
(refused, failed validation, a send). New routes and tools are covered
automatically: nothing here lists them.

TestAuditDiffsAndSecrets checks what a row holds: the actor (member, token,
OAuth client, admin), the address and user agent, a before/after diff of only
the fields that changed — and never a secret or a message body, anywhere in
the table. It also checks the role gate, RLS, that the API's own role cannot
write the log, and retention.
*/

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/audit"
	"github.com/fadymondy/moharrik/internal/db/migrate"
	"github.com/fadymondy/moharrik/internal/mcp"
	"github.com/fadymondy/moharrik/internal/testdb"
)

// bootAudit boots the whole stack on this package's own database.
func bootAudit(t *testing.T) (*app.App, string) {
	t.Helper()
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "audit-test-secret-audit-test-secret-0123456789")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")
	t.Setenv("SNOOZE_WAKER", "off")
	t.Setenv("EVOLUTION_API_URL", "http://127.0.0.1:9")
	t.Setenv("APP_URL", "http://127.0.0.1")
	a := Boot()
	t.Cleanup(a.Kernel.Close)
	srv := newTestServer(t, a)
	t.Setenv("APP_URL", srv)
	return a, srv
}

func newTestServer(t *testing.T, a *app.App) string {
	t.Helper()
	srv := httptest.NewServer(a.Kernel.Handler())
	t.Cleanup(srv.Close)
	return srv.URL
}

// doRID is client.do that also returns the request id the audit middleware set.
func (c *client) doRID(method, path string, body any) (int, []byte, string) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "audit-test/1.0")
	if c.ws != "" {
		req.Header.Set("X-Workspace-ID", c.ws)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if method != http.MethodGet && c.bearer == "" {
		if c.csrf == "" {
			r, err := c.http.Get(c.base + "/api/auth/csrf")
			if err != nil {
				c.t.Fatal(err)
			}
			var out struct {
				Token string `json:"csrf_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&out)
			r.Body.Close()
			c.csrf = out.Token
		}
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res.Header.Get("X-Request-Id")
}

func auditRows(t *testing.T, a *app.App, rid string) int {
	t.Helper()
	var n int
	if err := a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE request_id = $1`, rid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAuditEveryWrite(t *testing.T) {
	a, base := bootAudit(t)

	alice := newClient(t, base)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "audit-owner-" + rnd() + "@mail.example", "password": "correct-horse-1"}, 201)
	ws := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Audit " + rnd()}, 201)["workspace"].(map[string]any)
	alice.ws = ws["id"].(string)

	type route struct{ method, pattern string }
	var routes []route
	_ = chi.Walk(a.Kernel.Router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, route{method, pattern})
		return nil
	})
	param := regexp.MustCompile(`\{[^}]+\}`)
	swept := 0
	for _, rt := range routes {
		switch rt.method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			continue
		}
		if !strings.HasPrefix(rt.pattern, "/api/") {
			continue // /graphql is read-only (internal/graph/schema): no mutations to record
		}
		probe, _ := http.NewRequest(rt.method, base+rt.pattern, nil)
		probe.URL.Path = rt.pattern
		if classify(probe) != tierWorkspace {
			continue // public and account-level routes act in no workspace yet
		}
		// A fresh id per parameter: nothing that exists is aimed at, so the
		// sweep cannot delete the workspace out from under itself.
		path := param.ReplaceAllStringFunc(rt.pattern, func(string) string { return "00000000-0000-4000-8000-" + rnd() + "00" })
		code, b, rid := alice.doRID(rt.method, path, map[string]any{})
		if rid == "" {
			t.Errorf("%s %s: no X-Request-Id", rt.method, rt.pattern)
			continue
		}
		if auditRows(t, a, rid) == 0 {
			t.Errorf("%s %s (%d) left no audit row: %.200s", rt.method, rt.pattern, code, b)
		}
		swept++
	}
	if swept < 100 {
		t.Fatalf("swept only %d mutating routes — the walk is broken", swept)
	}
	t.Logf("every one of %d mutating workspace routes wrote an audit row", swept)

	// Every write MCP tool, called with empty arguments by a token holding
	// every scope: each call leaves a row, success or refusal.
	all := []string{}
	for _, sc := range mcp.UserScopes {
		all = append(all, sc.Scope)
	}
	tools := 0
	for _, tool := range mcp.UserTools(nil) {
		if !mcp.IsWriteTool(tool) {
			continue
		}
		tk := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "sweep " + tool.Name, "scopes": all}, 201)
		agent := newClient(t, base)
		agent.bearer = tk["plaintext"].(string)
		code, b, rid := agent.doRID(http.MethodPost, "/api/mcp", map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool.Name, "arguments": map[string]any{}},
		})
		if code != 200 || auditRows(t, a, rid) == 0 {
			t.Errorf("MCP %s (%d) left no audit row: %.200s", tool.Name, code, b)
		}
		var op string
		_ = a.SysDB.QueryRow(`SELECT operation FROM audit_log WHERE request_id = $1 LIMIT 1`, rid).Scan(&op)
		if op != "mcp:"+tool.Name {
			t.Errorf("MCP %s was recorded as operation %q", tool.Name, op)
		}
		tools++
	}
	if tools < 30 {
		t.Fatalf("swept only %d write tools — the registry walk is broken", tools)
	}
	t.Logf("every one of %d write MCP tools wrote an audit row", tools)
}

func TestAuditDiffsAndSecrets(t *testing.T) {
	a, base := bootAudit(t)

	alice := newClient(t, base)
	aliceEmail := "audit-alice-" + rnd() + "@mail.example"
	alice.must("POST", "/api/auth/register", map[string]string{"email": aliceEmail, "password": "correct-horse-1"}, 201)
	wsA := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Audit A " + rnd()}, 201)["workspace"].(map[string]any)
	alice.ws = wsA["id"].(string)

	// ---- a member's change: before/after of exactly the changed fields ------
	ct := alice.must("POST", "/api/w/contacts", map[string]any{"email": "diff-" + rnd() + "@mail.example", "name": "Before Name"}, 201)
	ctID := ct["contact"].(map[string]any)["id"].(string)
	code, b, rid := alice.doRID("PATCH", "/api/w/contacts/"+ctID, map[string]any{"name": "After Name"})
	if code != 200 {
		t.Fatalf("update contact: %d %s", code, b)
	}
	var kind, actorKind, actorName, via, op, action, entity, entityID, changes, ip, ua string
	if err := a.SysDB.QueryRow(`
		SELECT kind, actor_kind, actor_name, via, operation, action, entity_type, entity_id, changes::text, ip, user_agent
		FROM audit_log WHERE request_id = $1 AND entity_type = 'contacts'`, rid).
		Scan(&kind, &actorKind, &actorName, &via, &op, &action, &entity, &entityID, &changes, &ip, &ua); err != nil {
		t.Fatalf("the contact update left no change row: %v", err)
	}
	if kind != "change" || actorKind != "member" || actorName != aliceEmail || via != "web" || op != "w-update-contact" ||
		action != "update" || entityID != ctID || ip == "" || ua != "audit-test/1.0" {
		t.Errorf("change row: kind=%s actor=%s/%s via=%s op=%s action=%s id=%s ip=%q ua=%q", kind, actorKind, actorName, via, op, action, entityID, ip, ua)
	}
	var diff map[string]map[string]any
	_ = json.Unmarshal([]byte(changes), &diff)
	if diff["name"]["before"] != "Before Name" || diff["name"]["after"] != "After Name" {
		t.Errorf("the diff does not carry the name's before and after: %s", changes)
	}
	if _, ok := diff["email"]; ok {
		t.Errorf("an unchanged field is in the diff: %s", changes)
	}

	// ---- secrets never reach the log ----------------------------------------
	smtpPassword := "SMTP-PASSWORD-" + rnd()
	alice.must("PUT", "/api/w/mail/smtp", map[string]any{"host": "smtp.mail.example", "port": 587, "username": "u",
		"password": smtpPassword, "from_email": "news@mail.example", "tls_mode": "starttls"}, 200)
	wfSecret := "WORKFLOW-SECRET-" + rnd()
	wfSteps := []map[string]any{
		{"command": "goto", "args": map[string]any{"url": "https://site.example/"}},
		{"command": "type", "args": map[string]any{"selector": "#pw", "text": wfSecret}},
	}
	alice.must("POST", "/api/w/workflows", map[string]any{"name": "Secretive", "steps": wfSteps}, 201)
	// A token's plaintext and a message body.
	tok := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "audit agent", "scopes": []string{"contacts:write", "audit:read", "members:read"}}, 201)
	plaintext := tok["plaintext"].(string)

	// ---- an MCP token's change is recorded as the token ----------------------
	agent := newClient(t, base)
	agent.bearer = plaintext
	_, _, mrid := agent.doRID("POST", "/api/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "update_contact", "arguments": map[string]any{"id": ctID, "title": "Set by the agent"}}})
	if err := a.SysDB.QueryRow(`SELECT actor_kind, actor_name, via, operation FROM audit_log WHERE request_id = $1 AND entity_type = 'contacts'`, mrid).
		Scan(&actorKind, &actorName, &via, &op); err != nil || actorKind != "token" || actorName != "audit agent" || via != "mcp" || op != "mcp:update_contact" {
		t.Errorf("an MCP write: %v kind=%s name=%s via=%s op=%s", err, actorKind, actorName, via, op)
	}

	// A reply's body: that the member replied is recorded, never what they said.
	const origin = "https://shop.example"
	wgt := obj(alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "Site", "allowed_origins": []string{origin}}, 201)["widget"])
	visitorBody := "VISITOR-BODY-" + rnd()
	visitorWrites(t, base, wgt["public_key"].(string), origin, map[string]any{"body": visitorBody, "name": "Visitor"})
	convID := obj(arr(alice.must("GET", "/api/w/conversations", nil, 200)["conversations"])[0])["id"].(string)
	replyBody := "REPLY-BODY-" + rnd()
	_, _, replyRID := alice.doRID("POST", "/api/w/conversations/"+convID+"/reply", map[string]any{"body": replyBody})
	var replyRows int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE request_id = $1 AND entity_type = 'messages' AND action = 'create'`, replyRID).Scan(&replyRows)
	if replyRows == 0 {
		t.Error("the reply was not recorded as a created message")
	}

	var everything string
	if err := a.SysDB.QueryRow(`SELECT coalesce(string_agg(changes::text || ' ' || actor_name || ' ' || operation, ' '), '') FROM audit_log`).Scan(&everything); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{smtpPassword, wfSecret, plaintext, mcp.HashToken(plaintext), replyBody, visitorBody} {
		if strings.Contains(everything, secret) {
			t.Errorf("a secret reached the audit log: %.12s…", secret)
		}
	}
	if !strings.Contains(everything, `"[redacted]"`) || !strings.Contains(everything, `"[hidden]"`) {
		t.Errorf("expected redacted secrets and hidden bodies in the log")
	}

	// ---- the role gate --------------------------------------------------------
	mallory := newClient(t, base)
	malloryEmail := "audit-member-" + rnd() + "@mail.example"
	mallory.must("POST", "/api/auth/register", map[string]string{"email": malloryEmail, "password": "correct-horse-3"}, 201)
	var malloryID string
	_ = a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, malloryEmail).Scan(&malloryID)
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'member', 'active')`, alice.ws, malloryID); err != nil {
		t.Fatal(err)
	}
	mallory.ws = alice.ws
	if code, _ := mallory.do("GET", "/api/w/audit", nil); code != http.StatusForbidden {
		t.Errorf("a member reading the audit log: got %d, want 403", code)
	}
	if code, _ := mallory.do("PUT", "/api/w/audit/retention", map[string]any{"retention_days": 30}); code != http.StatusForbidden {
		t.Errorf("a member changing retention: got %d, want 403", code)
	}
	// The member's refused write is itself in the log.
	_, _, refusedRID := mallory.doRID("PUT", "/api/w/mail/smtp", map[string]any{"host": "h.example", "port": 25, "from_email": "a@mail.example", "tls_mode": "none"})
	var status int
	if err := a.SysDB.QueryRow(`SELECT status FROM audit_log WHERE request_id = $1 AND kind = 'request'`, refusedRID).Scan(&status); err != nil || status != http.StatusForbidden {
		t.Errorf("a refused write is not recorded as refused: %v %d", err, status)
	}

	// ---- the page's API: filters and paging -----------------------------------
	page := alice.must("GET", "/api/w/audit?entity_type=contacts&entity_id="+ctID, nil, 200)
	entries := page["entries"].([]any)
	if len(entries) < 3 { // create, update by alice, update by the token
		t.Errorf("filter by entity: %d entries", len(entries))
	}
	byToken := alice.must("GET", "/api/w/audit?actor_kind=token", nil, 200)["entries"].([]any)
	for _, e := range byToken {
		if e.(map[string]any)["actor_kind"] != "token" {
			t.Errorf("actor_kind filter let through %v", e)
		}
	}
	p1 := alice.must("GET", "/api/w/audit?limit=2", nil, 200)
	cursor, _ := p1["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("a page of 2 has no next_cursor")
	}
	p2 := alice.must("GET", "/api/w/audit?limit=2&cursor="+cursor, nil, 200)
	first := p1["entries"].([]any)
	second := p2["entries"].([]any)
	if len(second) == 0 || second[0].(map[string]any)["id"] == first[1].(map[string]any)["id"] {
		t.Error("the second page repeats the first")
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if n := len(alice.must("GET", "/api/w/audit?from="+future, nil, 200)["entries"].([]any)); n != 0 {
		t.Errorf("a date filter in the future returned %d entries", n)
	}
	facets := alice.must("GET", "/api/w/audit/facets", nil, 200)
	if facets["retention_days"].(float64) != 365 {
		t.Errorf("default retention: %v", facets["retention_days"])
	}
	// The audit_log MCP tool is admin-only: this token holds audit:read, but it
	// only takes effect for an admin.
	agentAudit := mcpJSON(t, agent, "audit_log", map[string]any{"entity_id": ctID})
	if len(agentAudit["entries"].([]any)) == 0 {
		t.Error("audit_log over MCP returned nothing")
	}
	mtok := mallory.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "member agent", "scopes": []string{"members:read"}}, 201)
	_ = mtok
	if code, _ := mallory.do("POST", "/api/w/mcp_tokens", map[string]any{"name": "member audit", "scopes": []string{"audit:read"}}); code != 422 {
		t.Errorf("a member minting audit:read: got %d, want 422", code)
	}

	// ---- RLS: another workspace's log is invisible ------------------------------
	bob := newClient(t, base)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "audit-bob-" + rnd() + "@mail.example", "password": "correct-horse-2"}, 201)
	bob.ws = bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Audit B " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	_, bb := bob.do("GET", "/api/w/audit?limit=200", nil)
	if strings.Contains(string(bb), ctID) || strings.Contains(string(bb), aliceEmail) {
		t.Error("bob's audit log shows alice's workspace")
	}
	if _, bb := bob.do("GET", "/api/w/audit?entity_id="+ctID, nil); strings.Contains(string(bb), ctID) {
		t.Error("a filter on another workspace's id leaked it")
	}

	// ---- the API's own role cannot write the log -------------------------------
	for _, stmt := range []string{
		`INSERT INTO audit_log (workspace_id, action) VALUES ('` + alice.ws + `', 'forged')`,
		`UPDATE audit_log SET actor_name = 'someone else'`,
		`DELETE FROM audit_log`,
	} {
		if _, err := a.SQLDB.Exec(stmt); err == nil || !strings.Contains(err.Error(), "permission denied") {
			t.Errorf("the app role ran %q: %v", stmt, err)
		}
	}

	// ---- retention ---------------------------------------------------------------
	alice.must("PUT", "/api/w/audit/retention", map[string]any{"retention_days": 30}, 200)
	if _, err := a.SysDB.Exec(`INSERT INTO audit_log (workspace_id, action, occurred_at) VALUES ($1, 'old', now() - interval '40 days'), ($1, 'recent', now() - interval '20 days')`, alice.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO audit_log (workspace_id, action, occurred_at) VALUES ($1, 'old-default', now() - interval '40 days')`, bob.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Sweep(t.Context(), a, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var old, recent, bobOld int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND action = 'old'`, alice.ws).Scan(&old)
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND action = 'recent'`, alice.ws).Scan(&recent)
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND action = 'old-default'`, bob.ws).Scan(&bobOld)
	if old != 0 || recent != 1 || bobOld != 1 {
		t.Errorf("retention: alice old=%d recent=%d (30 days); bob 40-day-old=%d (365 days)", old, recent, bobOld)
	}
	var retentionRow int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND entity_type = 'audit_settings'`, alice.ws).Scan(&retentionRow)
	if retentionRow == 0 {
		t.Error("changing the retention window was not itself recorded")
	}
}

// TestAuditedTablesAreTriggered checks the migration against its own lists:
// every audited tenant table carries the trigger, and no excluded one does.
func TestAuditedTablesAreTriggered(t *testing.T) {
	a, _ := bootAudit(t)
	rows, err := a.SysDB.Query(`SELECT c.relname FROM pg_trigger g JOIN pg_class c ON c.oid = g.tgrelid WHERE g.tgname = 'audit_row_change'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		_ = rows.Scan(&n)
		have[n] = true
	}
	for _, tbl := range migrate.AuditedTables() {
		if !have[tbl] {
			t.Errorf("tenant table %s has no audit trigger", tbl)
		}
	}
	for tbl := range migrate.AuditExcluded {
		if have[tbl] {
			t.Errorf("excluded table %s still has the audit trigger", tbl)
		}
	}
}
