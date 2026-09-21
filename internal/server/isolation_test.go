package server

/*
TestCrossWorkspaceIsolation — the cross-workspace sweep (plan: tenancy step 5).

It boots the real stack (guard, generated CRUD, RLS pool, MCP) against a
throwaway database, gives two users a workspace each, fills workspace A with
one row of every tenant table, then walks EVERY route on the router
(chi.Walk — the same list `list_routes` shows) and asserts, per route:

  1. anonymous            -> 401 (or the route is on the public allowlist)
  2. bob + X-Workspace-ID=A -> 404 (not a member; existence not revealed)
  3. bob + X-Workspace-ID=B, A's ids in the path -> never 2xx with A's data,
     never A's marker in any body, and A's rows survive every write attempt

New routes are swept automatically: nothing here lists them by hand.

Run it with a dedicated database (it creates users and workspaces):

	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55470/moharrik_test?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app_test:test@localhost:55470/moharrik_test?sslmode=disable \
	go test -count=1 ./internal/server/ -run Isolation -v
*/

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/db/migrate"
	"github.com/fadymondy/moharrik/internal/mcp"
	_ "github.com/fadymondy/moharrik/internal/plugins"
)

func rnd() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type client struct {
	t      *testing.T
	base   string
	http   *http.Client
	csrf   string
	ws     string
	bearer string
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.ws != "" {
		req.Header.Set("X-Workspace-ID", c.ws)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	if method != http.MethodGet && method != http.MethodHead && c.bearer == "" {
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

	// /api/w/events is the one route whose success is an OPEN CONNECTION: it
	// streams until the caller goes away, so reading it to EOF never returns
	// and the whole sweep hangs (it did, for ten minutes, before this).
	// Reading the headers is exactly what the sweep needs anyway — the tier
	// checks are about the status code, and a refusal arrives in the status.
	if strings.Contains(req.URL.Path, "/api/w/events") {
		return res.StatusCode, nil
	}

	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func (c *client) must(method, path string, body any, want int) map[string]any {
	c.t.Helper()
	code, b := c.do(method, path, body)
	if code != want {
		c.t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, b)
	}
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func mcpCall(c *client, endpoint, tool string, args map[string]any) (int, string, bool) {
	c.t.Helper()
	code, b := c.do(http.MethodPost, endpoint, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	var res struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	_ = json.Unmarshal(b, &res)
	text := ""
	if len(res.Result.Content) > 0 {
		text = res.Result.Content[0].Text
	}
	return code, text, res.Result.IsError
}

func TestCrossWorkspaceIsolation(t *testing.T) {
	owner, appURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_APP_DATABASE_URL")
	if owner == "" || appURL == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_APP_DATABASE_URL (a throwaway database) to run the isolation sweep")
	}
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "isolation-test-secret-isolation-test-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())

	// Migrate the test database exactly as `togo migrate` would.
	k0 := togo.New()
	sys, err := k0.SQL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(context.Background(), sys, appURL, "../..", t.Logf); err != nil {
		t.Fatal(err)
	}
	k0.Close()

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	// ---- two tenants -------------------------------------------------------
	marker := "ISO-A-SECRET-" + rnd()
	alice, bob := newClient(t, srv.URL), newClient(t, srv.URL)
	aliceEmail, bobEmail := "alice-"+rnd()+"@example.test", "bob-"+rnd()+"@example.test"
	alice.must("POST", "/api/auth/register", map[string]string{"email": aliceEmail, "password": "correct-horse-1"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-2"}, 201)

	wsA := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Iso A " + rnd()}, 201)["workspace"].(map[string]any)
	wsB := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Iso B " + rnd()}, 201)["workspace"].(map[string]any)
	aID, bID := wsA["id"].(string), wsB["id"].(string)

	// ---- one row of every tenant table in A --------------------------------
	alice.ws = aID
	inv := alice.must("POST", "/api/w/invitations", map[string]string{"email": "carol-" + rnd() + "@example.test", "role": "member"}, 201)
	invID := inv["invitation"].(map[string]any)["id"].(string)
	tok := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": marker, "scopes": []string{"members:read"}}, 201)
	tokID := tok["token"].(map[string]any)["id"].(string)
	aliceToken := tok["plaintext"].(string)
	var memberID string
	if err := a.SysDB.QueryRow(`SELECT id FROM workspace_members WHERE workspace_id = $1`, aID).Scan(&memberID); err != nil {
		t.Fatal(err)
	}
	// CRM rows: a company, a contact at it, a deal on the pipeline, an activity.
	coID := alice.must("POST", "/api/w/companies", map[string]any{"name": marker + " Co", "domain": "iso-" + rnd() + ".test"}, 201)["company"].(map[string]any)["id"].(string)
	ctID := alice.must("POST", "/api/w/contacts", map[string]any{"email": "iso-" + rnd() + "@example.test", "name": marker}, 201)["contact"].(map[string]any)["id"].(string)
	alice.must("PATCH", "/api/w/contacts/"+ctID, map[string]any{"company_id": coID}, 200)
	stages := alice.must("GET", "/api/w/pipeline/stages", nil, 200)["stages"].([]any)
	stageID := stages[0].(map[string]any)["id"].(string)
	dealID := alice.must("POST", "/api/w/deals", map[string]any{"title": marker + " deal", "value_minor": 1000, "contact_id": ctID, "company_id": coID}, 201)["deal"].(map[string]any)["id"].(string)
	actID := alice.must("POST", "/api/w/activities", map[string]any{"kind": "task", "body": marker + " task", "deal_id": dealID}, 201)["activity"].(map[string]any)["id"].(string)

	// Notifications: a webhook source, then a real signed delivery through the
	// PUBLIC /api/hooks/{id} route — proving the whole path (signature check,
	// workspace resolution, insert scoped by that workspace) rather than just
	// the authenticated CRUD around it.
	hookCreate := alice.must("POST", "/api/w/webhook_sources", map[string]any{"name": marker + " Hook"}, 201)
	srcID := hookCreate["source"].(map[string]any)["id"].(string)
	secret := hookCreate["secret"].(string)
	rawBody := []byte(`{"title":"` + marker + ` alert"}`)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	sig := hex.EncodeToString(mac.Sum(nil))
	hreq, _ := http.NewRequest("POST", srv.URL+"/api/hooks/"+srcID, bytes.NewReader(rawBody))
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("X-Webhook-Signature", "sha256="+sig)
	hres, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	if hres.StatusCode != 202 {
		b, _ := io.ReadAll(hres.Body)
		t.Fatalf("POST /api/hooks/%s: got %d want 202: %s", srcID, hres.StatusCode, b)
	}
	hres.Body.Close()
	items := alice.must("GET", "/api/w/notifications", nil, 200)["items"].([]any)
	if len(items) == 0 {
		t.Fatal("the signed delivery did not produce a notification item")
	}
	itemID := items[0].(map[string]any)["id"].(string)

	// Inbox: a chat widget, then a real visitor message through the PUBLIC route,
	// so the conversation and its message exist the way a visitor would make them.
	wgt := alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": marker + " Widget", "allowed_origins": []string{"https://iso.test"}}, 201)
	widgetID := wgt["widget"].(map[string]any)["id"].(string)
	widgetKey := wgt["widget"].(map[string]any)["public_key"].(string)
	creq, _ := http.NewRequest("POST", srv.URL+"/api/chat/"+widgetKey+"/messages", bytes.NewReader([]byte(`{"body":"`+marker+` hello","name":"Visitor"}`)))
	creq.Header.Set("Content-Type", "application/json")
	creq.Header.Set("Origin", "https://iso.test")
	cres, err := http.DefaultClient.Do(creq)
	if err != nil {
		t.Fatal(err)
	}
	if cres.StatusCode != 201 {
		b, _ := io.ReadAll(cres.Body)
		t.Fatalf("POST /api/chat/{key}/messages: got %d want 201: %s", cres.StatusCode, b)
	}
	cres.Body.Close()
	convs := alice.must("GET", "/api/w/conversations", nil, 200)["conversations"].([]any)
	if len(convs) == 0 {
		t.Fatal("the visitor message did not produce a conversation")
	}
	convID := convs[0].(map[string]any)["id"].(string)

	aIDs := []string{aID, invID, tokID, memberID, coID, ctID, stageID, dealID, actID, srcID, itemID, widgetID, convID}
	idFor := map[string]string{
		"workspaces": aID, "workspace_members": memberID, "invitations": invID, "mcp_tokens": tokID,
		"companies": coID, "contacts": ctID, "deals": dealID, "activities": actID, "pipeline": stageID,
		"webhook_sources": srcID, "notifications": itemID,
		"chat_widgets": widgetID, "conversations": convID,
	}

	leaks := func(b []byte) string {
		s := string(b)
		if strings.Contains(s, marker) {
			return "marker"
		}
		for _, id := range aIDs {
			if strings.Contains(s, id) {
				return "id " + id
			}
		}
		return ""
	}

	// ---- sweep every route -------------------------------------------------
	type route struct{ method, pattern string }
	var routes []route
	_ = chi.Walk(a.Kernel.Router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, route{method, pattern})
		return nil
	})

	param := regexp.MustCompile(`\{[^}]+\}`)
	swept := 0
	anon := newClient(t, srv.URL)
	for _, rt := range routes {
		if !strings.HasPrefix(rt.pattern, "/api/") && rt.pattern != "/graphql" {
			continue // pages, docs, playground
		}
		probe, _ := http.NewRequest(rt.method, srv.URL+rt.pattern, nil)
		probe.URL.Path = rt.pattern
		if classify(probe) == tierPublic || classify(probe) == tierBlocked {
			continue // the allowlist is reviewed in guard.go; MCP is swept below
		}
		segs := strings.Split(strings.TrimPrefix(rt.pattern, "/api/"), "/")
		table := segs[0]
		if table == "w" && len(segs) > 1 {
			table = segs[1]
		}
		path := param.ReplaceAllStringFunc(rt.pattern, func(string) string {
			if id, ok := idFor[table]; ok {
				return id
			}
			return aID
		})
		if rt.pattern == "/graphql" {
			path = "/graphql"
		}
		var body any = map[string]any{}
		if rt.pattern == "/graphql" {
			body = map[string]any{"query": "{ workspaces { id name } invitations { id email } }"}
		}
		swept++
		name := rt.method + " " + rt.pattern

		// 1. anonymous
		if code, b := anon.do(rt.method, path, body); code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: got %d, want 401: %s", name, code, b)
		}

		tier := classify(probe)
		if tier == tierWorkspace {
			// 2. bob naming A
			bob.ws = aID
			if code, b := bob.do(rt.method, path, body); code != http.StatusNotFound {
				t.Errorf("%s as a non-member of A: got %d, want 404: %s", name, code, b)
			}
		}

		// 3. bob in his own workspace, A's ids in the path
		bob.ws = bID
		code, b := bob.do(rt.method, path, body)
		if what := leaks(b); what != "" {
			t.Errorf("%s from B leaked A's %s (status %d): %s", name, what, code, b)
		}
		if strings.Contains(rt.pattern, "{") && rt.method == http.MethodGet && code/100 == 2 {
			t.Errorf("%s from B with A's id answered %d", name, code)
		}
	}
	if swept < 20 {
		t.Fatalf("swept only %d routes — the walk is broken", swept)
	}
	t.Logf("swept %d guarded routes x 3 callers", swept)

	// ---- A survived every write attempt -------------------------------------
	alice.ws = aID
	if code, b := alice.do("GET", "/api/w/invitations", nil); code != 200 || !strings.Contains(string(b), invID) {
		t.Errorf("A's invitation was removed from B: %d %s", code, b)
	}

	if code, b := alice.do("GET", "/api/w/deals/"+dealID, nil); code != 200 || !strings.Contains(string(b), actID) || !strings.Contains(string(b), coID) {
		t.Errorf("A's deal or its timeline was changed from B: %d %s", code, b)
	}

	// ---- CRM links cannot cross workspaces (composite FKs) -------------------
	bob.ws = bID
	if code, b := bob.do("POST", "/api/w/deals", map[string]any{"title": "steal", "company_id": coID}); code != http.StatusUnprocessableEntity || leaks(b) != "" {
		t.Errorf("B linking a deal to A's company: got %d, want 422: %s", code, b)
	}
	if code, b := bob.do("POST", "/api/w/activities", map[string]any{"kind": "note", "body": "x", "contact_id": ctID}); code != http.StatusUnprocessableEntity || leaks(b) != "" {
		t.Errorf("B logging an activity on A's contact: got %d, want 422: %s", code, b)
	}
	if code, b := bob.do("POST", "/api/w/deals", map[string]any{"title": "steal", "stage_id": stageID}); code != http.StatusUnprocessableEntity || leaks(b) != "" {
		t.Errorf("B putting a deal on A's stage: got %d, want 422: %s", code, b)
	}

	// ---- account level: bob's list never shows A ----------------------------
	bob.ws = ""
	if _, b := bob.do("GET", "/api/me/workspaces", nil); leaks(b) != "" {
		t.Errorf("/api/me/workspaces leaked A to bob: %s", b)
	}

	// ---- MCP: a token acts only in its own workspace -------------------------
	bob.ws = bID
	btok := bob.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "bob agent", "scopes": []string{"members:read"}}, 201)
	bnarrow := bob.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "bob narrow", "scopes": []string{"contacts:read"}}, 201)
	bobNarrow := newClient(t, srv.URL)
	bobNarrow.bearer = bnarrow["plaintext"].(string)
	bobMCP := newClient(t, srv.URL)
	bobMCP.bearer = btok["plaintext"].(string)
	if code, text, isErr := mcpCall(bobMCP, "/api/mcp", "whoami", nil); code != 200 || isErr || !strings.Contains(text, bID) || strings.Contains(text, aID) {
		t.Errorf("bob's whoami: %d %v %q", code, isErr, text)
	}
	if _, text, isErr := mcpCall(bobNarrow, "/api/mcp", "whoami", nil); !isErr || !strings.Contains(text, "members:read") {
		t.Errorf("an out-of-scope call must fail in-band naming the scope, got isErr=%v %q", isErr, text)
	}
	aliceMCP := newClient(t, srv.URL)
	aliceMCP.bearer = aliceToken
	if _, text, isErr := mcpCall(aliceMCP, "/api/mcp", "list_members", nil); isErr || !strings.Contains(text, aliceEmail) || strings.Contains(text, bobEmail) {
		t.Errorf("alice's token should list only A's members: %v %q", isErr, text)
	}
	if code, _, _ := mcpCall(aliceMCP, "/api/admin/mcp", "list_workspaces", nil); code != http.StatusUnauthorized {
		t.Errorf("a user token must not open the admin MCP, got %d", code)
	}

	// ---- admin MCP: only a platform admin's admin token ----------------------
	plain, visible := mcp.NewToken(mcp.AdminPrefix)
	var aliceID string
	_ = a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, aliceEmail).Scan(&aliceID)
	if _, err := a.SysDB.Exec(`INSERT INTO admin_mcp_tokens (user_id, name, token_hash, prefix, scopes) VALUES ($1, 't', $2, $3, '["admin:workspaces:read"]')`,
		aliceID, mcp.HashToken(plain), visible); err != nil {
		t.Fatal(err)
	}
	adminMCP := newClient(t, srv.URL)
	adminMCP.bearer = plain
	if code, _, _ := mcpCall(adminMCP, "/api/admin/mcp", "list_workspaces", nil); code != http.StatusUnauthorized {
		t.Errorf("an admin token of a NON-admin user must be refused, got %d", code)
	}
	_, _ = a.SysDB.Exec(`UPDATE users SET roles = 'admin' WHERE id = $1`, aliceID)
	if code, text, isErr := mcpCall(adminMCP, "/api/admin/mcp", "list_workspaces", nil); code != 200 || isErr {
		t.Errorf("admin list_workspaces: %d %v %q", code, isErr, text)
	}
	if _, text, isErr := mcpCall(adminMCP, "/api/admin/mcp", "list_users", nil); !isErr || !strings.Contains(text, "admin:users:read") {
		t.Errorf("admin out-of-scope call must fail in-band: %v %q", isErr, text)
	}
	if code, _, _ := mcpCall(adminMCP, "/api/mcp", "whoami", nil); code != http.StatusUnauthorized {
		t.Errorf("an admin token must not open the user MCP, got %d", code)
	}
	fmt.Fprintln(io.Discard)
}
