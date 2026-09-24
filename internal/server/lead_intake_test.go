package server

/*
TestLeadIntake — a website's inquiry form, end to end through the real stack
(MH-390): the guard's public tier, CORS, the client-address middleware, the
form's origin allow-list, the rate limits, RLS, the notification center, the
Leads API and the MCP tools.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run LeadIntake -v
*/

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/leads"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/testdb"
)

type inquiryReply struct {
	code       int
	retryAfter string
	acao       string
	raw        string
}

func postInquiry(t *testing.T, base, key, origin, ip string, body map[string]any) inquiryReply {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/inquiry/"+key, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return inquiryReply{res.StatusCode, res.Header.Get("Retry-After"), res.Header.Get("Access-Control-Allow-Origin"), string(raw)}
}

func TestLeadIntake(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "lead-intake-test-secret-lead-intake-test-secret")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1,::1")
	t.Setenv("CLIENT_IP_HEADER", "")
	t.Setenv("PUBLIC_URL", "https://app.moharrik.test")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()
	// Every sub-test starts from fresh limits, so one does not spend another's.
	fresh := func() func() { return leads.SetLimiter(inbox.NewMemoryLimiter(), leads.DefaultInquiryLimits()) }

	alice, bob := newClient(t, srv.URL), newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "lead-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "lead-b-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	aWS := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Leads A " + rnd()}, 201)["workspace"].(map[string]any)
	aID, aSlug := aWS["id"].(string), aWS["slug"].(string)
	bID := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Leads B " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws, bob.ws = aID, bID

	const site = "https://acme-site.test"
	newForm := func(c *client, origins []string) (id, key string, form map[string]any) {
		f := c.must("POST", "/api/w/subscribe-forms", map[string]any{"kind": "inquiry", "name": "Contact " + rnd(), "allowed_origins": origins}, 201)["form"].(map[string]any)
		return f["id"].(string), f["public_key"].(string), f
	}
	formID, key, form := newForm(alice, []string{site})
	if form["kind"] != "inquiry" || form["list_id"] != "" || !strings.Contains(form["embed"].(string), "/api/inquiry/"+key) ||
		form["endpoint"] != "https://app.moharrik.test/api/inquiry/"+key {
		t.Fatalf("the inquiry form came back wrong: %+v", form)
	}
	msg := func(email string) map[string]any {
		return map[string]any{"name": "Visitor", "email": email, "message": "We would like a quote.", "locale": "en",
			"utm_source": "newsletter", "utm_medium": "email", "utm_campaign": "sept", "gclid": "gc-1",
			"referrer": "https://news.test/", "landing_page": site + "/?utm_source=newsletter"}
	}
	leadCount := func(ws string) int {
		var n int
		if err := a.SysDB.QueryRow(`SELECT count(*) FROM leads WHERE workspace_id = $1`, ws).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("an inquiry form cannot take a list, and a subscribe form still needs one", func(t *testing.T) {
		if code, b := alice.do("POST", "/api/w/subscribe-forms", map[string]any{"kind": "inquiry", "name": "x", "list_id": "anything"}); code != 422 {
			t.Errorf("inquiry form with a list: %d %s", code, b)
		}
		if code, b := alice.do("PATCH", "/api/w/subscribe-forms/"+formID, map[string]any{"list_id": "anything"}); code != 422 {
			t.Errorf("giving an inquiry form a list: %d %s", code, b)
		}
		if code, b := alice.do("POST", "/api/w/subscribe-forms", map[string]any{"name": "no list"}); code != 404 {
			t.Errorf("subscribe form without a list: %d %s", code, b)
		}
	})

	t.Run("origins are denied by default", func(t *testing.T) {
		defer fresh()()
		_, closedKey, _ := newForm(alice, nil)
		before := leadCount(aID)
		if r := postInquiry(t, srv.URL, closedKey, site, "203.0.113.1", msg("x@acme.test")); r.code != 403 {
			t.Errorf("a form with no origins accepted a post: %d %s", r.code, r.raw)
		}
		if r := postInquiry(t, srv.URL, key, "", "203.0.113.1", msg("x@acme.test")); r.code != 403 {
			t.Errorf("a post with no Origin was accepted: %d %s", r.code, r.raw)
		}
		if r := postInquiry(t, srv.URL, key, "https://evil.test", "203.0.113.1", msg("x@acme.test")); r.code != 403 {
			t.Errorf("an origin off the list was accepted: %d %s", r.code, r.raw)
		}
		if r := postInquiry(t, srv.URL, "if_nope", site, "203.0.113.1", msg("x@acme.test")); r.code != 404 {
			t.Errorf("an unknown key: %d %s", r.code, r.raw)
		}
		if n := leadCount(aID); n != before {
			t.Fatalf("refused posts filed %d lead(s)", n-before)
		}
	})

	t.Run("the kinds are not interchangeable", func(t *testing.T) {
		defer fresh()()
		list := alice.must("POST", "/api/w/mail/lists", map[string]any{"name": "News " + rnd()}, 201)["list"].(map[string]any)["id"].(string)
		sub := alice.must("POST", "/api/w/subscribe-forms", map[string]any{"list_id": list, "name": "Newsletter", "allowed_origins": []string{site}}, 201)["form"].(map[string]any)
		if sub["kind"] != "subscribe" {
			t.Fatalf("a form with no kind must be a subscribe form: %+v", sub)
		}
		if r := postInquiry(t, srv.URL, sub["public_key"].(string), site, "203.0.113.2", msg("x@acme.test")); r.code != 404 {
			t.Errorf("a subscribe key filed an inquiry: %d %s", r.code, r.raw)
		}
		b, _ := json.Marshal(map[string]any{"email": "x@acme.test"})
		req, _ := http.NewRequest("POST", srv.URL+"/api/subscribe/"+key, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", site)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("an inquiry key subscribed somebody: %d", res.StatusCode)
		}
	})

	t.Run("CORS lets the customer's page read the answer", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/inquiry/"+key, nil)
		req.Header.Set("Origin", site)
		req.Header.Set("Access-Control-Request-Method", "POST")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 204 || res.Header.Get("Access-Control-Allow-Origin") != site {
			t.Errorf("preflight: %d ACAO=%q", res.StatusCode, res.Header.Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("the same answer for a known and a new address", func(t *testing.T) {
		defer fresh()()
		known := "known-" + rnd() + "@acme.test"
		alice.must("POST", "/api/w/contacts", map[string]any{"email": known, "name": "Already Here"}, 201)
		r1 := postInquiry(t, srv.URL, key, site, "203.0.113.10", msg(known))
		r2 := postInquiry(t, srv.URL, key, site, "203.0.113.11", msg("new-"+rnd()+"@acme.test"))
		if r1.code != 200 || r2.code != 200 {
			t.Fatalf("posts: %d %s / %d %s", r1.code, r1.raw, r2.code, r2.raw)
		}
		if r1.raw != r2.raw {
			t.Fatalf("the answer differs for a known address — that is an oracle:\nknown: %s\nnew:   %s", r1.raw, r2.raw)
		}
		if r1.acao != site {
			t.Errorf("the answer is not readable cross-origin: ACAO=%q", r1.acao)
		}
		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM leads WHERE workspace_id = $1 AND email = $2`, aID, known).Scan(&n)
		if n != 1 {
			t.Errorf("the known address filed %d leads, want 1", n)
		}
	})

	t.Run("the honeypot is thanked and dropped", func(t *testing.T) {
		defer fresh()()
		human := postInquiry(t, srv.URL, key, site, "203.0.113.20", msg("human-"+rnd()+"@acme.test"))
		before := leadCount(aID)
		var items int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1`, aID).Scan(&items)
		bot := msg("bot-" + rnd() + "@acme.test")
		bot["nickname"] = "I fill every field"
		r := postInquiry(t, srv.URL, key, site, "203.0.113.21", bot)
		if r.code != human.code || r.raw != human.raw {
			t.Fatalf("the bot was told something different: %d %s vs %d %s", r.code, r.raw, human.code, human.raw)
		}
		if n := leadCount(aID); n != before {
			t.Fatalf("the honeypot filed %d lead(s)", n-before)
		}
		var after int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM notification_items WHERE workspace_id = $1`, aID).Scan(&after)
		if after != items {
			t.Fatalf("the honeypot raised %d notification(s)", after-items)
		}
	})

	t.Run("validation", func(t *testing.T) {
		defer fresh()()
		bad := msg("not-an-email")
		if r := postInquiry(t, srv.URL, key, site, "203.0.113.30", bad); r.code != 422 {
			t.Errorf("a bad email: %d %s", r.code, r.raw)
		}
		empty := msg("ok@acme.test")
		empty["message"] = "   "
		if r := postInquiry(t, srv.URL, key, site, "203.0.113.30", empty); r.code != 422 {
			t.Errorf("an empty message: %d %s", r.code, r.raw)
		}
	})

	t.Run("rate limited per address and per form", func(t *testing.T) {
		defer leads.SetLimiter(inbox.NewMemoryLimiter(), leads.InquiryLimits{
			IPSubmits:   inbox.Rate{Limit: 2, Window: time.Minute},
			FormSubmits: inbox.Rate{Limit: 4, Window: time.Minute},
		})()
		for i := 0; i < 2; i++ {
			if r := postInquiry(t, srv.URL, key, site, "198.51.100.1", msg("rl@acme.test")); r.code != 200 {
				t.Fatalf("post %d: %d %s", i+1, r.code, r.raw)
			}
		}
		before := leadCount(aID)
		r := postInquiry(t, srv.URL, key, site, "198.51.100.1", msg("rl@acme.test"))
		if r.code != 429 || r.retryAfter == "" || r.acao != site {
			t.Fatalf("third from one address: %d Retry-After=%q ACAO=%q %s", r.code, r.retryAfter, r.acao, r.raw)
		}
		if n := leadCount(aID); n != before {
			t.Fatal("a refused post filed a lead")
		}
		// Another address is not held back by the first one's limit…
		if r := postInquiry(t, srv.URL, key, site, "198.51.100.2", msg("rl2@acme.test")); r.code != 200 {
			t.Fatalf("a second address: %d %s", r.code, r.raw)
		}
		if r := postInquiry(t, srv.URL, key, site, "198.51.100.3", msg("rl3@acme.test")); r.code != 200 {
			t.Fatalf("a third address: %d %s", r.code, r.raw)
		}
		// …until the whole form's ceiling is reached.
		if r := postInquiry(t, srv.URL, key, site, "198.51.100.4", msg("rl4@acme.test")); r.code != 429 {
			t.Fatalf("past the form's ceiling: %d %s", r.code, r.raw)
		}
		// The ceiling is per FORM: another workspace's form is unaffected.
		_, keyB, _ := newForm(bob, []string{site})
		if r := postInquiry(t, srv.URL, keyB, site, "198.51.100.1", msg("other@acme.test")); r.code != 200 {
			t.Fatalf("another form was limited by this one: %d %s", r.code, r.raw)
		}
	})

	t.Run("a lead is in the Leads list with its attribution, and in the notification center", func(t *testing.T) {
		defer fresh()()
		email := "attr-" + rnd() + "@acme.test"
		if r := postInquiry(t, srv.URL, key, site, "203.0.113.40", msg(email)); r.code != 200 {
			t.Fatalf("post: %d %s", r.code, r.raw)
		}
		list := alice.must("GET", "/api/w/leads?q="+email, nil, 200)
		ls := list["leads"].([]any)
		if len(ls) != 1 {
			t.Fatalf("found %d leads for %s", len(ls), email)
		}
		l := ls[0].(map[string]any)
		at := l["attribution"].(map[string]any)
		if l["status"] != "new" || at["utm_source"] != "newsletter" || at["utm_campaign"] != "sept" || at["gclid"] != "gc-1" ||
			at["referrer"] != "https://news.test/" || l["origin"] != site {
			t.Errorf("lead as listed: %+v", l)
		}
		if list["counts"].(map[string]any)["new"].(float64) < 1 {
			t.Errorf("counts: %+v", list["counts"])
		}
		items := alice.must("GET", "/api/w/notifications", nil, 200)["items"].([]any)
		found := false
		for _, it := range items {
			m := it.(map[string]any)
			if m["kind"] == "lead" && m["actor"] == email {
				found = true
				if want := "https://app.moharrik.test/en/w/" + aSlug + "/leads/" + l["id"].(string); m["url"] != want {
					t.Errorf("notification links to %q, want %q", m["url"], want)
				}
			}
		}
		if !found {
			t.Error("no notification-center item for the new lead")
		}
	})

	// MH-497: the Leads page listens for "lead", and the hub drops an event that names no
	// audience. A member who may read leads hears the new lead (and the bell's refresh); a
	// client-role member and another workspace hear nothing.
	t.Run("an open Leads page hears the new lead; a client and another workspace do not", func(t *testing.T) {
		defer fresh()()
		member, stopM := a.Events.Subscribe(aID, realtime.Viewer{UserID: "a-member", Role: "member"})
		defer stopM()
		clientTab, stopC := a.Events.Subscribe(aID, realtime.Viewer{UserID: "a-client", Role: "client"})
		defer stopC()
		otherWS, stopO := a.Events.Subscribe(bID, realtime.Viewer{UserID: "b-owner", Role: "owner"})
		defer stopO()
		kinds := func(ch <-chan []byte, wait time.Duration) map[string]int {
			out := map[string]int{}
			deadline := time.After(wait)
			for {
				select {
				case b := <-ch:
					var ev struct {
						Kind string `json:"kind"`
					}
					_ = json.Unmarshal(b, &ev)
					out[ev.Kind]++
				case <-deadline:
					return out
				}
			}
		}
		if r := postInquiry(t, srv.URL, key, site, "203.0.113.50", msg("live-"+rnd()+"@acme.test")); r.code != 200 {
			t.Fatalf("post: %d %s", r.code, r.raw)
		}
		if got := kinds(member, 500*time.Millisecond); got["lead"] != 1 || got["notifications"] != 1 {
			t.Errorf("a member's tab got %v, want one lead and one notifications refresh", got)
		}
		if got := kinds(clientTab, 150*time.Millisecond); len(got) != 0 {
			t.Errorf("a client-role member's tab got %v, want nothing", got)
		}
		if got := kinds(otherWS, 150*time.Millisecond); len(got) != 0 {
			t.Errorf("another workspace's tab got %v, want nothing", got)
		}
	})

	t.Run("convert in one call; free mail makes no company; not across workspaces", func(t *testing.T) {
		defer fresh()()
		biz := "buyer-" + rnd() + "@globex-" + rnd() + ".test"
		free := "someone" + rnd() + "@gmail.com"
		for i, e := range []string{biz, free} {
			if r := postInquiry(t, srv.URL, key, site, "203.0.113.5"+string(rune('0'+i)), msg(e)); r.code != 200 {
				t.Fatalf("post: %d %s", r.code, r.raw)
			}
		}
		idOf := func(email string) string {
			return alice.must("GET", "/api/w/leads?q="+email, nil, 200)["leads"].([]any)[0].(map[string]any)["id"].(string)
		}
		bizID, freeID := idOf(biz), idOf(free)

		// Bob, in his own workspace, cannot see or convert Alice's lead.
		for _, c := range []struct{ method, path string }{{"GET", "/api/w/leads/" + bizID}, {"POST", "/api/w/leads/" + bizID + "/convert"}} {
			if code, b := bob.do(c.method, c.path, nil); code != 404 || strings.Contains(string(b), biz) {
				t.Errorf("bob %s %s: %d %s", c.method, c.path, code, b)
			}
		}
		if code, b := bob.do("PATCH", "/api/w/leads/"+bizID, map[string]any{"status": "spam"}); code != 404 {
			t.Errorf("bob marking Alice's lead: %d %s", code, b)
		}

		c := alice.must("POST", "/api/w/leads/"+bizID+"/convert", nil, 200)
		if c["company_id"] == "" || c["deal_id"] == "" || c["contact_id"] == "" {
			t.Fatalf("business conversion: %+v", c)
		}
		deal := alice.must("GET", "/api/w/deals/"+c["deal_id"].(string), nil, 200)
		acts := deal["activities"].([]any)
		if len(acts) != 1 || !strings.Contains(acts[0].(map[string]any)["body"].(string), "We would like a quote.") {
			t.Errorf("the deal's timeline: %+v", acts)
		}
		if code, _ := alice.do("POST", "/api/w/leads/"+bizID+"/convert", nil); code != 409 {
			t.Errorf("converting twice: %d, want 409", code)
		}

		cf := alice.must("POST", "/api/w/leads/"+freeID+"/convert", nil, 200)
		if cf["company_id"] != "" || cf["free_mail"] != true || cf["deal_id"] == "" {
			t.Errorf("free-mail conversion: %+v", cf)
		}
		lead := alice.must("GET", "/api/w/leads/"+freeID, nil, 200)["lead"].(map[string]any)
		if lead["status"] != "converted" || lead["converted_company"] != nil || lead["converted_deal"] == nil {
			t.Errorf("free-mail lead after converting: %+v", lead)
		}
	})

	t.Run("the MCP tools, and convert_lead needing contacts:write", func(t *testing.T) {
		defer fresh()()
		email := "mcp-" + rnd() + "@initech.test"
		if r := postInquiry(t, srv.URL, key, site, "203.0.113.60", msg(email)); r.code != 200 {
			t.Fatalf("post: %d %s", r.code, r.raw)
		}
		mint := func(scopes ...string) *client {
			tok := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "leads " + rnd(), "scopes": scopes}, 201)
			c := newClient(t, srv.URL)
			c.bearer = tok["plaintext"].(string)
			return c
		}
		reader := mint("leads:read")
		_, text, isErr := mcpCall(reader, "/api/mcp", "list_leads", map[string]any{"q": email})
		if isErr || !strings.Contains(text, email) || !strings.Contains(text, "newsletter") {
			t.Fatalf("list_leads: %v %q", isErr, text)
		}
		id := alice.must("GET", "/api/w/leads?q="+email, nil, 200)["leads"].([]any)[0].(map[string]any)["id"].(string)
		if _, text, isErr := mcpCall(reader, "/api/mcp", "get_lead", map[string]any{"id": id}); isErr || !strings.Contains(text, "We would like a quote.") || !strings.Contains(text, "gc-1") {
			t.Errorf("get_lead: %v %q", isErr, text)
		}
		if _, _, isErr := mcpCall(reader, "/api/mcp", "update_lead", map[string]any{"id": id, "status": "contacted"}); !isErr {
			t.Error("leads:read changed a lead")
		}
		triage := mint("leads:read", "leads:write")
		if _, text, isErr := mcpCall(triage, "/api/mcp", "update_lead", map[string]any{"id": id, "status": "contacted"}); isErr || !strings.Contains(text, "contacted") {
			t.Errorf("update_lead: %v %q", isErr, text)
		}
		if _, text, isErr := mcpCall(triage, "/api/mcp", "convert_lead", map[string]any{"id": id}); !isErr || !strings.Contains(text, "contacts:write") {
			t.Errorf("convert_lead without contacts:write: %v %q", isErr, text)
		}
		full := mint("leads:write", "contacts:write")
		if _, text, isErr := mcpCall(full, "/api/mcp", "convert_lead", map[string]any{"id": id}); isErr || !strings.Contains(text, "company: Initech") {
			t.Errorf("convert_lead: %v %q", isErr, text)
		}
		// A token acts only in its own workspace.
		btok := bob.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "b", "scopes": []string{"leads:read", "leads:write", "contacts:write"}}, 201)
		bc := newClient(t, srv.URL)
		bc.bearer = btok["plaintext"].(string)
		if _, text, isErr := mcpCall(bc, "/api/mcp", "get_lead", map[string]any{"id": id}); !isErr || strings.Contains(text, email) {
			t.Errorf("bob's token read Alice's lead: %v %q", isErr, text)
		}
		if _, text, _ := mcpCall(bc, "/api/mcp", "list_leads", nil); strings.Contains(text, email) {
			t.Errorf("bob's list_leads shows Alice's lead: %q", text)
		}
	})
}
