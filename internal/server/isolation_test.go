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
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/mcp"
	_ "github.com/fadymondy/moharrik/internal/plugins"
	"github.com/fadymondy/moharrik/internal/testdb"
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
	// Its own database (internal/testdb). The sweep boots the whole app and
	// walks every route; another package migrating the same database mid-sweep
	// invalidates pgx's cached statement descriptions and this fails with
	// something that has nothing to do with isolation.
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "isolation-test-secret-isolation-test-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	// No background collector reaches out to the internet or to a mail server
	// from a test. Without these two the sweep still passes, but it opens
	// sockets nobody asked for and its timing depends on them.
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	// Google connections (MH-439) against the fake Google, never the real one.
	fakeGoogle := googletest.New()
	defer fakeGoogle.Close()
	t.Setenv("APP_URL", "http://127.0.0.1")
	t.Setenv("GOOGLE_API_BASE", fakeGoogle.URL)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", fakeGoogle.ClientID)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", fakeGoogle.ClientSecret)

	// testdb.Setup already migrated this package's own database, under a lock
	// held on the shared base database — the app ROLE is cluster-global, so
	// two packages creating it at once collide on the same catalogue row.

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()
	t.Setenv("APP_URL", srv.URL)

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
	ctEmail := "iso-" + rnd() + "@example.test"
	ctID := alice.must("POST", "/api/w/contacts", map[string]any{"email": ctEmail, "name": marker}, 201)["contact"].(map[string]any)["id"].(string)
	// A mailing list with that contact on it (MH-408: unsubscribe-from-one-list,
	// list edit and the CSV export all name a list and a contact in the path).
	listID := alice.must("POST", "/api/w/mail/lists", map[string]any{"name": marker + " List", "double_opt_in": false}, 201)["list"].(map[string]any)["id"].(string)
	alice.must("POST", "/api/w/contacts", map[string]any{"email": ctEmail, "list_id": listID}, 201)
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
	// A new generic source takes only the timestamped scheme (MH-381):
	// HMAC(secret, timestamp + "." + body), timestamp within 5 minutes.
	rawBody := []byte(`{"title":"` + marker + ` alert"}`)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(rawBody)
	sig := hex.EncodeToString(mac.Sum(nil))
	hreq, _ := http.NewRequest("POST", srv.URL+"/api/hooks/"+srcID, bytes.NewReader(rawBody))
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("X-Webhook-Timestamp", stamp)
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

	// Leads (MH-390): an inquiry form, then a real submission through the
	// PUBLIC route, so the lead exists the way a visitor's website makes it.
	inq := alice.must("POST", "/api/w/subscribe-forms", map[string]any{"kind": "inquiry", "name": marker + " Inquiry", "allowed_origins": []string{"https://iso.test"}}, 201)
	formID := inq["form"].(map[string]any)["id"].(string)
	inqKey := inq["form"].(map[string]any)["public_key"].(string)
	lreq, _ := http.NewRequest("POST", srv.URL+"/api/inquiry/"+inqKey, bytes.NewReader([]byte(`{"email":"lead-`+rnd()+`@iso.test","name":"`+marker+`","message":"`+marker+` inquiry","utm_source":"`+marker+`"}`)))
	lreq.Header.Set("Content-Type", "application/json")
	lreq.Header.Set("Origin", "https://iso.test")
	lres, err := http.DefaultClient.Do(lreq)
	if err != nil {
		t.Fatal(err)
	}
	if lres.StatusCode != 200 {
		b, _ := io.ReadAll(lres.Body)
		t.Fatalf("POST /api/inquiry/{key}: got %d want 200: %s", lres.StatusCode, b)
	}
	lres.Body.Close()
	leadList := alice.must("GET", "/api/w/leads", nil, 200)["leads"].([]any)
	if len(leadList) == 0 {
		t.Fatal("the inquiry did not produce a lead")
	}
	leadID := leadList[0].(map[string]any)["id"].(string)

	// A Google account connected to A through the real flow: consent at the
	// fake Google, then the callback with alice's session and state cookie.
	googleEmail := "iso-a-" + rnd() + "@example.test"
	fakeGoogle.Set(func(s *googletest.Server) {
		s.Account = googletest.Account{Sub: "iso-sub-" + rnd(), Email: googleEmail}
	})
	consentURL := alice.must("GET", "/api/w/google/connect?product=analytics", nil, 200)["url"].(string)
	back, err := fakeGoogle.Consent(consentURL)
	if err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{Jar: alice.http.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	gres, err := noFollow.Get(back)
	if err != nil {
		t.Fatal(err)
	}
	gres.Body.Close()
	if loc := gres.Header.Get("Location"); gres.StatusCode != http.StatusFound || !strings.Contains(loc, "google=connected") {
		t.Fatalf("google connect in A: %d %s", gres.StatusCode, loc)
	}
	gconns := alice.must("GET", "/api/w/google/connections", nil, 200)["connections"].([]any)
	if len(gconns) != 1 {
		t.Fatalf("A's google connections: %v", gconns)
	}
	googleID := gconns[0].(map[string]any)["id"].(string)

	aIDs := []string{aID, invID, tokID, memberID, coID, ctID, stageID, dealID, actID, srcID, itemID, widgetID, convID, formID, leadID, googleID, googleEmail, listID}
	idFor := map[string]string{
		"workspaces": aID, "workspace_members": memberID, "invitations": invID, "mcp_tokens": tokID,
		"companies": coID, "contacts": ctID, "deals": dealID, "activities": actID, "pipeline": stageID,
		"webhook_sources": srcID, "notifications": itemID,
		"chat_widgets": widgetID, "conversations": convID,
		"subscribe-forms": formID, "leads": leadID,
		"google": googleID,
		"mail": listID,
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

	// The sweep's empty body is refused by validation before the handler runs,
	// so try the webhook secret and signing writes with VALID bodies too: from
	// B they must not reach A's source. A secret overwritten from B would let B
	// forge A's notifications (MH-381).
	bob.ws = bID
	if code, b := bob.do("PUT", "/api/w/webhook_sources/"+srcID+"/secret", map[string]any{"secret": "bob-controls-this-secret-now"}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B setting A's webhook secret: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("PATCH", "/api/w/webhook_sources/"+srcID, map[string]any{"legacy_signing": false, "enabled": false}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B patching A's webhook source: got %d, want 404: %s", code, b)
	}
	// A's source still verifies with A's secret, and is still enabled.
	stamp2 := strconv.FormatInt(time.Now().Unix(), 10)
	body2 := []byte(`{"title":"` + marker + ` after the sweep"}`)
	mac2 := hmac.New(sha256.New, []byte(secret))
	mac2.Write([]byte(stamp2 + "."))
	mac2.Write(body2)
	hreq2, _ := http.NewRequest("POST", srv.URL+"/api/hooks/"+srcID, bytes.NewReader(body2))
	hreq2.Header.Set("X-Webhook-Timestamp", stamp2)
	hreq2.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac2.Sum(nil)))
	if hres2, err := http.DefaultClient.Do(hreq2); err != nil || hres2.StatusCode != 202 {
		code := 0
		if hres2 != nil {
			code = hres2.StatusCode
			hres2.Body.Close()
		}
		t.Errorf("A's webhook stopped verifying with A's secret after the sweep: %v %d", err, code)
	} else {
		hres2.Body.Close()
	}
	alice.ws = aID

	// Leads: the sweep's empty bodies are refused by validation, so try the
	// writes with VALID bodies from B too. A's lead must stay new, unconverted.
	bob.ws = bID
	if code, b := bob.do("PATCH", "/api/w/leads/"+leadID, map[string]any{"status": "spam"}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B changing A's lead: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("POST", "/api/w/leads/"+leadID+"/convert", nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B converting A's lead: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("PATCH", "/api/w/subscribe-forms/"+formID, map[string]any{"allowed_origins": []string{"https://bob.test"}}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B opening A's inquiry form to its own site: got %d, want 404: %s", code, b)
	}
	alice.ws = aID
	if l := alice.must("GET", "/api/w/leads/"+leadID, nil, 200)["lead"].(map[string]any); l["status"] != "new" || l["converted_deal"] != nil {
		t.Errorf("A's lead was changed from B: %+v", l)
	}

	// Google (MH-439): B cannot list, disconnect or draw tokens from A's
	// connection, and A's grant survives every attempt — not revoked at Google.
	bob.ws = bID
	if code, b := bob.do("DELETE", "/api/w/google/connections/"+googleID, nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B disconnecting A's Google account: got %d, want 404: %s", code, b)
	}
	if _, b := bob.do("GET", "/api/w/google/connections", nil); leaks(b) != "" {
		t.Errorf("B's Google list leaked A: %s", b)
	}
	if _, err := google.TokenSource(context.Background(), a, bID, googleID); err == nil {
		t.Error("a token source in B must not open A's connection")
	}
	if n := fakeGoogle.Calls("revoke"); n != 0 {
		t.Errorf("A's Google grant was revoked %d times during the sweep", n)
	}
	alice.ws = aID
	if code, b := alice.do("GET", "/api/w/google/connections", nil); code != 200 || !strings.Contains(string(b), googleID) {
		t.Errorf("A's Google connection did not survive the sweep: %d %s", code, b)
	}
	if src, err := google.TokenSource(context.Background(), a, aID, googleID); err != nil {
		t.Errorf("A's own token source: %v", err)
	} else if _, err := src.Token(context.Background(), google.ScopeAnalytics); err != nil {
		t.Errorf("A's grant stopped working after the sweep: %v", err)
	}
	// Subscriber admin (MH-408), with VALID bodies from B: B cannot take A's
	// contact off A's list, rename A's list or turn its opt-in off, download it,
	// or find A's contacts through the new search, filter or cursor.
	bob.ws = bID
	if code, b := bob.do("POST", "/api/w/contacts/"+ctID+"/lists/"+listID+"/unsubscribe", nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B unsubscribing A's contact from A's list: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("PATCH", "/api/w/mail/lists/"+listID, map[string]any{"name": "bob owns this", "double_opt_in": false}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B editing A's list: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("GET", "/api/w/mail/lists/"+listID+"/export.csv", nil); code != http.StatusNotFound || leaks(b) != "" || strings.Contains(string(b), ctEmail) {
		t.Errorf("B exporting A's list: got %d, want 404: %s", code, b)
	}
	for _, q := range []string{"?list_id=" + listID, "?list_id=" + listID + "&status=subscribed", "?q=" + marker, "?q=" + ctEmail} {
		code, b := bob.do("GET", "/api/w/contacts"+q, nil)
		if leaks(b) != "" || strings.Contains(string(b), ctEmail) || (code == 200 && !strings.Contains(string(b), `"total":0`)) {
			t.Errorf("B's contacts%s found A's rows (status %d): %s", q, code, b)
		}
	}
	alice.ws = aID
	var subStatus, listName string
	var subBy *string
	if err := a.SysDB.QueryRow(`SELECT s.status, s.unsubscribed_by, l.name FROM list_subscriptions s JOIN mail_lists l ON l.id = s.list_id
		WHERE s.workspace_id = $1 AND s.list_id = $2 AND s.contact_id = $3`, aID, listID, ctID).Scan(&subStatus, &subBy, &listName); err != nil {
		t.Errorf("A's subscription or list is gone after the sweep: %v", err)
	} else if subStatus != "subscribed" || subBy != nil || listName != marker+" List" {
		t.Errorf("A's subscription or list was changed from B: status %q by %v name %q", subStatus, subBy, listName)
	}
	// Alice herself can still do all three — the 404s above are about B.
	if code, b := alice.do("GET", "/api/w/contacts?list_id="+listID+"&q="+ctEmail, nil); code != 200 || !strings.Contains(string(b), `"total":1`) {
		t.Errorf("A searching her own list: %d %s", code, b)
	}
	if code, b := alice.do("GET", "/api/w/mail/lists/"+listID+"/export.csv", nil); code != 200 || !strings.Contains(string(b), ctEmail) {
		t.Errorf("A exporting her own list: %d %s", code, b)
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
