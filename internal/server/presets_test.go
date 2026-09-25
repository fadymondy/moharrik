package server

/*
TestPresetsAndBrowserTools — MH-478 phase 3a, through the real stack.

The preset gallery adds fadymondy.com's plugins as a workspace's own workflows,
personalised (the workspace's WhatsApp number, its profile), and can allow a
preset's sites on a browser in the same step — an admin's decision. Over MCP,
fadymondy.com's browser tools answer the same way: browser_set_allowed_sites
is admin-only, pushed and audited; browser_plugin_save records sites without
allowing them; browser_plugin_run will not run a publishing workflow without
confirm.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run Presets -v
*/

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestPresetsAndBrowserTools(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "presets-test-secret-presets-test-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "presets-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Presets " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	bobEmail := "presets-bob-" + rnd() + "@example.test"
	bob := newClient(t, srv.URL)
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-1"}, 201)
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) SELECT $1, id, 'manager', 'active' FROM users WHERE email = $2`, wsID, bobEmail); err != nil {
		t.Fatal(err)
	}
	bob.ws = wsID

	var agentID string
	if err := a.SysDB.QueryRow(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts)
		VALUES ($1, 'Office Chrome', $2, '', '{}') RETURNING id`, wsID, "presets-token-"+rnd()).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	hostsOf := func() string {
		var h string
		_ = a.SysDB.QueryRow(`SELECT array_to_string(allowed_hosts, ',') FROM browser_agents WHERE id = $1`, agentID).Scan(&h)
		return h
	}

	// ---- the gallery ----
	lib := alice.must("GET", "/api/w/automation/presets", nil, 200)
	presets := lib["presets"].([]any)
	if len(presets) < 100 {
		t.Fatalf("the gallery lists %d presets", len(presets))
	}

	// ---- the profile, then a preset that uses it ----
	alice.must("PUT", "/api/w/automation/profile", map[string]any{"profile": map[string]string{"first_name": "Mona", "email": "mona@x.test", "shoe_size": "42"}}, 200)
	prof := alice.must("GET", "/api/w/automation/profile", nil, 200)["profile"].(map[string]any)
	if prof["first_name"] != "Mona" || prof["shoe_size"] != nil {
		t.Fatalf("profile: %v", prof)
	}
	if code, _ := bob.do("PUT", "/api/w/automation/profile", map[string]any{"profile": map[string]string{"first_name": "X"}}); code != 403 {
		t.Fatalf("a manager changed the workspace profile: %d", code)
	}

	// ---- add with the browser: an admin, the sites allowed and pushed ----
	added := alice.must("POST", "/api/w/automation/presets/google-news-search/add", map[string]any{"agent_id": agentID}, 201)
	wf := added["workflow"].(map[string]any)
	if wf["preset"] != "google-news-search" || wf["slug"] != "google-news-search" || strings.Join(toStrings(wf["hosts"]), ",") != "news.google.com" {
		t.Fatalf("added workflow: %v", wf)
	}
	if strings.Join(toStrings(added["hosts_added"]), ",") != "news.google.com" || hostsOf() != "news.google.com" {
		t.Fatalf("hosts added %v, browser now %q", added["hosts_added"], hostsOf())
	}
	if code, _ := alice.do("POST", "/api/w/automation/presets/google-news-search/add", map[string]any{}); code != 409 {
		t.Fatalf("added twice: %d", code)
	}
	for _, p := range alice.must("GET", "/api/w/automation/presets", nil, 200)["presets"].([]any) {
		if pm := p.(map[string]any); pm["slug"] == "google-news-search" && pm["workflow_id"] != wf["id"] {
			t.Fatalf("the gallery does not know it was added: %v", pm["workflow_id"])
		}
	}

	// A manager may add a preset, but not widen a browser's sites with it.
	if code, _ := bob.do("POST", "/api/w/automation/presets/google-trends-explore/add", map[string]any{"agent_id": agentID}); code != 403 {
		t.Fatalf("a manager allowed sites on a browser: %d", code)
	}
	var n int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM automation_workflows WHERE workspace_id = $1 AND preset = 'google-trends-explore'`, wsID).Scan(&n)
	if n != 0 || hostsOf() != "news.google.com" {
		t.Fatalf("a refused add left a workflow (%d) or sites (%q)", n, hostsOf())
	}
	bob.must("POST", "/api/w/automation/presets/google-trends-explore/add", map[string]any{}, 201)

	// The WhatsApp-to-yourself preset needs the workspace's number.
	if code, _ := alice.do("POST", "/api/w/automation/presets/whatsapp-send-self/add", map[string]any{}); code != 422 {
		t.Fatalf("added without a number: %d", code)
	}
	self := alice.must("POST", "/api/w/automation/presets/whatsapp-send-self/add", map[string]any{"whatsapp_number": "+20 100 000 0001"}, 201)["workflow"].(map[string]any)
	if self["writes"] != true || !strings.Contains(mustJSONText(self["steps"]), "phone=201000000001") {
		t.Fatalf("whatsapp-send-self: %v", self)
	}

	// ---- MCP ----
	token := func(c *client, name string, scopes ...string) *client {
		tk := c.must("POST", "/api/w/mcp_tokens", map[string]any{"name": name, "scopes": scopes}, 201)
		m := newClient(t, srv.URL)
		m.bearer = tk["plaintext"].(string)
		return m
	}
	all := []string{"automation:read", "automation:run", "automation:manage"}
	aliceMCP := token(alice, "alice agent", all...)

	decode := func(t *testing.T, text string) map[string]any {
		t.Helper()
		var v map[string]any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			t.Fatalf("not JSON: %s: %v", text, err)
		}
		return v
	}

	_, text, isErr := mcpCall(aliceMCP, "/api/mcp", "browser_allowed_sites", map[string]any{})
	allowed := decode(t, text)
	if isErr || allowed["count"] != float64(1) || !strings.Contains(mustJSONText(allowed["hosts"]), "news.google.com") {
		t.Fatalf("browser_allowed_sites: %v %v", isErr, allowed)
	}
	_, text, isErr = mcpCall(aliceMCP, "/api/mcp", "browser_set_allowed_sites", map[string]any{"agent": "office chrome", "add": []string{"https://Example.com/x", "not a host!"}})
	setSites := decode(t, text)
	if isErr || !strings.Contains(mustJSONText(setSites["added"]), "example.com") || !strings.Contains(mustJSONText(setSites["rejected"]), "not a host!") ||
		hostsOf() != "example.com,news.google.com" {
		t.Fatalf("browser_set_allowed_sites add: %v %v (browser %q)", isErr, setSites, hostsOf())
	}
	var audited int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND entity_type = 'browser_agents' AND entity_id = $2 AND via = 'mcp' AND operation = 'mcp:browser_set_allowed_sites'`, wsID, agentID).Scan(&audited)
	if audited == 0 {
		t.Error("the allowed-sites change is not in the audit log")
	}
	_, text, _ = mcpCall(aliceMCP, "/api/mcp", "browser_set_allowed_sites", map[string]any{"replace": []string{"news.google.com"}})
	replaced := decode(t, text)
	if !strings.Contains(mustJSONText(replaced["removed"]), "example.com") || hostsOf() != "news.google.com" {
		t.Fatalf("replace: %v (browser %q)", replaced, hostsOf())
	}

	// A manager's token cannot carry automation:manage, so the tool is refused.
	bobMCP := token(bob, "bob agent", "automation:read", "automation:run")
	if _, text, isErr := mcpCall(bobMCP, "/api/mcp", "browser_set_allowed_sites", map[string]any{"add": []string{"evil.test"}}); !isErr || hostsOf() != "news.google.com" {
		t.Fatalf("a manager widened the list over MCP: %v %s (browser %q)", isErr, text, hostsOf())
	}

	_, text, _ = mcpCall(bobMCP, "/api/mcp", "browser_plugins", map[string]any{})
	plugins := decode(t, text)
	pluginsText := mustJSONText(plugins)
	var anyWrites bool
	for _, it := range plugins["items"].([]any) {
		if it.(map[string]any)["writes"] == true {
			anyWrites = true
		}
	}
	if !strings.Contains(pluginsText, `"slug":"google-news-search"`) || !strings.Contains(pluginsText, `"hosts":["news.google.com"]`) ||
		!anyWrites || len(plugins["not_added"].([]any)) == 0 {
		t.Fatalf("browser_plugins: %s", pluginsText)
	}

	// Saving records sites but allows none of them.
	_, text, _ = mcpCall(bobMCP, "/api/mcp", "browser_plugin_save", map[string]any{
		"slug": "evil-read", "name": "Evil read", "hosts": []string{"evil.test"},
		"steps": []map[string]any{{"command": "navigate", "args": map[string]any{"url": "https://evil.test/"}}},
	})
	saved := decode(t, text)
	if saved["ok"] != true || saved["created"] != true || saved["version"] != float64(1) || hostsOf() != "news.google.com" {
		t.Fatalf("browser_plugin_save: %v (browser %q)", saved, hostsOf())
	}
	_, text, _ = mcpCall(bobMCP, "/api/mcp", "browser_plugin_save", map[string]any{
		"slug": "evil-read", "name": "Evil read, renamed", "hosts": []string{"evil.test"},
		"steps": []map[string]any{{"command": "navigate", "args": map[string]any{"url": "https://evil.test/"}}},
	})
	renamed := decode(t, text)
	if renamed["changed"] == true || renamed["version"] != float64(1) {
		t.Fatalf("a rename bumped the version: %v", renamed)
	}
	_, text, _ = mcpCall(bobMCP, "/api/mcp", "browser_plugin_save", map[string]any{
		"slug": "evil-read", "name": "Evil read, renamed",
		"steps": []map[string]any{{"command": "navigate", "args": map[string]any{"url": "https://evil.test/{{p}}"}}},
	})
	stepChanged := decode(t, text)
	if stepChanged["changed"] != true || stepChanged["version"] != float64(2) {
		t.Fatalf("a step change did not keep a version: %v", stepChanged)
	}
	// And running it is refused by the browser's list, not allowed by the save.
	_, text, _ = mcpCall(bobMCP, "/api/mcp", "browser_plugin_run", map[string]any{"plugin": "evil-read", "inputs": map[string]any{"p": "x"}})
	refusedRun := decode(t, text)
	if refusedRun["ok"] == true {
		t.Fatalf("a run to a site the browser does not allow started: %v", refusedRun)
	}

	// A publishing workflow does not run without confirm.
	_, text, _ = mcpCall(bobMCP, "/api/mcp", "browser_plugin_run", map[string]any{"plugin": "whatsapp-send-self", "inputs": map[string]any{"text": "hi"}})
	notConfirmed := decode(t, text)
	var runs int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM automation_runs WHERE workspace_id = $1`, wsID).Scan(&runs)
	if notConfirmed["needs_confirm"] != true || runs != 0 {
		t.Fatalf("a publishing workflow ran without confirm: %v (%d runs)", notConfirmed, runs)
	}
}

func toStrings(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, i := range items {
		out = append(out, i.(string))
	}
	return out
}

func mustJSONText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
