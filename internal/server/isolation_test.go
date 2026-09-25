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
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/mcp"
	"github.com/fadymondy/moharrik/internal/notifications"
	_ "github.com/fadymondy/moharrik/internal/plugins"
	"github.com/fadymondy/moharrik/internal/tenant"
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
	// The approval page's browser key (MH-478), sent as X-Connect-Key.
	connectKey string
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
	if c.connectKey != "" {
		req.Header.Set("X-Connect-Key", c.connectKey)
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
	// The Google data collector's ticker is off; the connect's own background
	// discovery and first sync run (against the fake), as in production.
	t.Setenv("GOOGLE_COLLECTOR", "off")
	// The notification collector's ticker is off; a collected source in A is
	// read by hand, from a fake Bluesky (MH-384).
	t.Setenv("NOTIFY_COLLECTOR", "off")
	// The GA4 sender's ticker is off: nothing here is meant to reach even the fake (MH-449).
	t.Setenv("GA4_SENDER", "off")
	// No real gateway socket, and Telegram's connect-time setWebhook call goes
	// to a local fake, never api.telegram.org (MH-563: never a real bot).
	t.Setenv("DISCORD_GATEWAY", "off")
	fakeTelegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"username":"isobot"}}`))
	}))
	defer fakeTelegramAPI.Close()
	defer channels.SetTelegramAPIBase(fakeTelegramAPI.URL)()
	fakePub := newFakePublic()
	defer fakePub.Close()
	defer notifications.SetEndpoints(notifications.Endpoints{Bluesky: fakePub.URL, YouTube: fakePub.URL})()
	// Google connections (MH-439) against the fake Google, never the real one.
	fakeGoogle := googletest.New()
	defer fakeGoogle.Close()
	t.Setenv("APP_URL", "http://127.0.0.1")
	t.Setenv("GOOGLE_API_BASE", fakeGoogle.URL)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", fakeGoogle.ClientID)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", fakeGoogle.ClientSecret)
	// Web push on, so a device row can exist in A for the sweep to aim at
	// (MH-378). Its endpoint is loopback and never answers: nothing is sent.
	t.Setenv("VAPID_PUBLIC_KEY", "BGfVQrK7aMmywg6zriWaZFVr4hcZH0muGY9nU73cHiVeIviM_0PGx9lZiYFgkjsWnH0QSKKz6mYN76K5Ql_hhSM")
	t.Setenv("VAPID_PRIVATE_KEY", "isolation-test-key-never-used-to-sign")
	t.Setenv("VAPID_SUBJECT", "mailto:iso@example.test")

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
	var visitor struct {
		Token string `json:"visitor_token"`
	}
	_ = json.NewDecoder(cres.Body).Decode(&visitor)
	cres.Body.Close()
	// MH-382: the widget's public routes (config, read, the live stream) are not
	// in the path sweep (public tier), so: B's own widget key never reaches A's
	// visitor thread, and A's widget from an origin not on A's list says nothing.
	{
		bob.ws = bID
		bWidget := bob.must("POST", "/api/w/chat_widgets", map[string]any{"name": "B widget", "allowed_origins": []string{"https://bob.test"}}, 201)["widget"].(map[string]any)
		bKey := bWidget["public_key"].(string)
		// Removed again below: later checks expect B to have set nothing up.
		for _, try := range []struct{ method, path, origin, body string }{
			{"GET", "/api/chat/" + bKey + "/messages?visitor_token=" + visitor.Token, "https://bob.test", ""},
			{"GET", "/api/chat/" + bKey + "/events?visitor_token=" + visitor.Token, "https://bob.test", ""},
			{"POST", "/api/chat/" + bKey + "/read", "https://bob.test", `{"visitor_token":"` + visitor.Token + `"}`},
			{"GET", "/api/chat/" + widgetKey + "/config", "https://bob.test", ""},
			{"GET", "/api/chat/" + widgetKey + "/events?visitor_token=" + visitor.Token, "https://bob.test", ""},
		} {
			req, _ := http.NewRequest(try.method, srv.URL+try.path, strings.NewReader(try.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", try.origin)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode/100 == 2 || strings.Contains(string(b), marker) {
				t.Errorf("chat widget %s %s from %s: got %d: %.200s", try.method, try.path, try.origin, res.StatusCode, b)
			}
		}
		bob.must("DELETE", "/api/w/chat_widgets/"+bWidget["id"].(string), nil, 204)
	}
	convs := alice.must("GET", "/api/w/conversations", nil, 200)["conversations"].([]any)
	if len(convs) == 0 {
		t.Fatal("the visitor message did not produce a conversation")
	}
	convID := convs[0].(map[string]any)["id"].(string)
	// MH-379: the conversation linked to A's contact and turned into a deal,
	// so the contact, company and deal pages all carry it; MH-383: a canned
	// reply carrying the marker.
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"contact_id": ctID}, 200)
	convDealID := alice.must("POST", "/api/w/conversations/"+convID+"/deal", map[string]any{"title": marker + " chat deal"}, 201)["deal_id"].(string)
	cannedID := alice.must("POST", "/api/w/canned_replies", map[string]any{"title": marker + " canned", "body_en": marker, "body_ar": marker}, 201)["reply"].(map[string]any)["id"].(string)
	// MH-384: a collected source in A, run once so it has items.
	notifSrcID := alice.must("POST", "/api/w/notification_sources", map[string]any{"kind": "bluesky", "target": "shop.bsky.social", "name": marker + " Bluesky"}, 201)["source"].(map[string]any)["id"].(string)
	alice.must("POST", "/api/w/notification_sources/"+notifSrcID+"/collect", nil, 200)

	// MH-449: GA4 conversions in A, switched on before the lead below arrives, so
	// its generate_lead sits in A's outbox under the lead's id. The API secret
	// carries the marker: no response may ever hand it back, to A or to anyone.
	ga4Secret := marker + "-ga4"
	ga4Page := alice.must("PATCH", "/api/w/ga4-conversions", map[string]any{
		"measurement_id": "G-ISO12345", "api_secret": ga4Secret, "events_enabled": true, "tag_in_embeds": true,
	}, 200)
	if b, _ := json.Marshal(ga4Page); strings.Contains(string(b), ga4Secret) {
		t.Fatalf("the GA4 settings handed back the API secret: %s", b)
	}

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
	// The connect discovered A's GA4 property in the background (MH-440).
	google.WaitBackground()
	gres2 := alice.must("GET", "/api/w/google/resources", nil, 200)["resources"].([]any)
	if len(gres2) != 1 {
		t.Fatalf("A's google resources: %v", gres2)
	}
	googleResID := gres2[0].(map[string]any)["id"].(string)
	googleResExt := gres2[0].(map[string]any)["external_id"].(string)

	// Alerts: one of alice's devices. The endpoint carries the marker, so a
	// response that leaked it would be caught like any other of A's data.
	pushEndpoint := "http://127.0.0.1:9/" + marker
	dev := alice.must("POST", "/api/w/push/subscriptions", map[string]any{
		"endpoint": pushEndpoint, "keys": map[string]string{"p256dh": "BIso" + rnd(), "auth": "iso" + rnd()},
	}, 201)
	pushID := dev["device"].(map[string]any)["id"].(string)

	// MH-412: an SMTP service in A, named with the marker, and a campaign that
	// names it. Its password carries the marker too, so a response that leaked
	// the credential would be caught by the same check as any other A data.
	smtpID := alice.must("POST", "/api/w/smtp-services", map[string]any{
		"name": marker + " SMTP", "host": "127.0.0.1", "port": 9, "username": "iso-user", "password": marker + "-pw",
		"from_email": "iso@example.test", "tls_mode": "none",
	}, 201)["service"].(map[string]any)["id"].(string)
	if _, b := alice.do("GET", "/api/w/smtp-services", nil); strings.Contains(string(b), marker+"-pw") {
		t.Fatalf("the SMTP service list handed back its password: %s", b)
	}
	alice.must("POST", "/api/w/campaigns", map[string]any{"list_id": listID, "subject": marker + " campaign", "body": "iso", "smtp_service_id": smtpID}, 201)

	// MH-555: a mailbox in A, named with the marker, and its password carries
	// the marker too — a leaked credential is caught the same way the SMTP
	// service's is above.
	mailboxID := alice.must("POST", "/api/w/mailboxes", map[string]any{
		"name": marker + " Mailbox", "host": "127.0.0.1", "port": 9, "username": "iso-user", "password": marker + "-mbpw", "tls_mode": "none",
	}, 201)["mailbox"].(map[string]any)["id"].(string)
	if _, b := alice.do("GET", "/api/w/mailboxes", nil); strings.Contains(string(b), marker+"-mbpw") {
		t.Fatalf("the mailbox list handed back its password: %s", b)
	}

	// MH-563/MH-564: a Telegram, a Slack, a Discord and a workflow channel in
	// A, each named with the marker. Bot tokens and secrets carry the marker
	// too, so a leak is caught the same way the SMTP password above is.
	tgID := alice.must("POST", "/api/w/telegram-channels", map[string]any{
		"name": marker + " Telegram", "bot_token": marker + "-tg-token",
	}, 201)["channel"].(map[string]any)["id"].(string)
	if _, b := alice.do("GET", "/api/w/telegram-channels", nil); strings.Contains(string(b), marker+"-tg-token") {
		t.Fatalf("the Telegram channel list handed back the bot token: %s", b)
	}
	slackID := alice.must("POST", "/api/w/slack-channels", map[string]any{
		"name": marker + " Slack", "bot_token": marker + "-slack-token", "signing_secret": marker + "-slack-signing",
	}, 201)["channel"].(map[string]any)["id"].(string)
	if _, b := alice.do("GET", "/api/w/slack-channels", nil); strings.Contains(string(b), marker+"-slack-token") || strings.Contains(string(b), marker+"-slack-signing") {
		t.Fatalf("the Slack channel list handed back a secret: %s", b)
	}
	discordID := alice.must("POST", "/api/w/discord-channels", map[string]any{
		"name": marker + " Discord", "bot_token": marker + "-discord-token",
	}, 201)["channel"].(map[string]any)["id"].(string)
	if _, b := alice.do("GET", "/api/w/discord-channels", nil); strings.Contains(string(b), marker+"-discord-token") {
		t.Fatalf("the Discord channel list handed back the bot token: %s", b)
	}
	workflowChannelID := alice.must("POST", "/api/w/workflow-channels", map[string]any{
		"name": marker + " LinkedIn", "network": "linkedin",
	}, 201)["channel"].(map[string]any)["id"].(string)

	// MH-411: alice's WhatsApp notifications in A, with a pending event and a
	// logged message that carry the marker. Written directly: verifying needs
	// a linked number. B's own /api/w/notify/whatsapp must show none of it.
	var aliceUserID string
	if err := a.SysDB.QueryRow(`SELECT user_id FROM workspace_members WHERE workspace_id = $1`, aID).Scan(&aliceUserID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO member_whatsapp (workspace_id, user_id, phone, verified_at) VALUES ($1, $2, '201699990000', now())`,
		`INSERT INTO member_whatsapp_events (workspace_id, user_id, kind, title, dedupe_key) VALUES ($1, $2, 'lead', $3, 'iso')`,
		`INSERT INTO member_whatsapp_messages (workspace_id, user_id, ok, error) VALUES ($1, $2, false, $3)`,
		`INSERT INTO member_whatsapp_marks (workspace_id, source, mark) VALUES ($1, 'iso-' || $2 || $3, now())`,
	} {
		args := []any{aID, aliceUserID}
		if strings.Contains(q, "$3") {
			args = append(args, marker)
		}
		if _, err := a.SysDB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, b := alice.do("GET", "/api/w/notify/whatsapp", nil); !strings.Contains(string(b), "201699990000") || !strings.Contains(string(b), marker) {
		t.Fatalf("alice should see her own WhatsApp notifications: %s", b)
	}
	// MH-389: alice's trends digest in A, its last error carrying the marker.
	if _, err := a.SysDB.Exec(`INSERT INTO member_trend_digest (workspace_id, user_id, frequency, top, last_status, last_error)
		VALUES ($1, $2, 'weekly', 7, 'failed', $3)`, aID, aliceUserID, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO trend_digest_sends (workspace_id, user_id, kind, status, error) VALUES ($1, $2, 'digest', 'failed', $3)`, aID, aliceUserID, marker); err != nil {
		t.Fatal(err)
	}

	// Trends (MH-385, MH-387): a keyword topic through the API, and a source, an
	// item and a trending topic (story) written directly: a real source would
	// fetch the internet on create. The marker is in each, so a leak is caught.
	kwID := alice.must("POST", "/api/w/topics", map[string]any{"name": marker + " kw", "keywords": []string{"iso"}}, 201)["topic"].(map[string]any)["id"].(string)
	var feedID, newsID, trendID string
	if err := a.SysDB.QueryRow(`INSERT INTO feed_sources (workspace_id, name, url, created_by) VALUES ($1, $2, $3, 'iso') RETURNING id`,
		aID, marker+" Feed", "https://iso-"+rnd()+".test/feed.xml").Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO trend_clusters (workspace_id, title, summary, outlets, source_ids) VALUES ($1, $2, $2, $3, $4) RETURNING id`,
		aID, marker+" story", "{"+marker+"}", "{"+feedID+"}").Scan(&trendID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO news_items (workspace_id, source_id, source_name, external_id, title, url, canonical_url, cluster_id)
		VALUES ($1, $2, $3, $4, $3, $5, $6, $7) RETURNING id`, aID, feedID, marker+" item", "iso-"+rnd(), "https://iso.test/a", "iso.test/a", trendID).Scan(&newsID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO trend_settings (workspace_id, fetch_budget_per_day) VALUES ($1, 7)`, aID); err != nil {
		t.Fatal(err)
	}

	// Automation (MH-399/400/403): a paired browser, a workflow edited once so
	// it has two versions, and a schedule that runs it with a secret value.
	// The browser is inserted directly — pairing needs the extension — and
	// everything after it goes through the routes.
	var agentID string
	if err := a.SysDB.QueryRow(`
		INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts)
		VALUES ($1, $2, $3, '', '{iso.test}') RETURNING id`, aID, marker+" Browser", "iso-token-"+rnd()).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	wfSteps := []map[string]any{
		{"command": "goto", "args": map[string]any{"url": "https://iso.test/{{page}}"}},
		{"command": "type", "args": map[string]any{"selector": "#pw", "text": "{{password}}"}},
	}
	wfParams := []map[string]any{{"name": "page", "required": true}, {"name": "password", "required": true, "secret": true}}
	wfID := alice.must("POST", "/api/w/workflows", map[string]any{"name": marker + " Workflow", "steps": wfSteps, "params": wfParams}, 201)["workflow"].(map[string]any)["id"].(string)
	alice.must("PUT", "/api/w/workflows/"+wfID, map[string]any{"name": marker + " Workflow v2", "steps": wfSteps, "params": wfParams, "base_version": 1}, 200)
	scheduleSecret := "ISO-A-PASSWORD-" + rnd()
	schedID := alice.must("POST", "/api/w/schedules", map[string]any{
		"name": marker + " Schedule", "agent_id": agentID, "cron": "0 7 * * *", "timezone": "Africa/Cairo",
		"workflow_id": wfID, "values": map[string]string{"page": "inbox", "password": scheduleSecret},
	}, 201)["schedule"].(map[string]any)["id"].(string)
	// The secret value is never read back, not even by the workspace it belongs to.
	if _, b := alice.do("GET", "/api/w/schedules", nil); strings.Contains(string(b), scheduleSecret) {
		t.Fatalf("the schedule list handed back a secret value: %s", b)
	}

	// An automation rule in A (MH-427), named with the marker, and a test run
	// of it in its log. Its webhook secret is the rule's own and carries no
	// marker, so it is listed by value: B must never read it.
	ruleID := alice.must("POST", "/api/w/rules", map[string]any{
		"name": marker + " Rule", "trigger": "lead.created",
		"actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": marker + " {{name}}"}}},
	}, 201)["rule"].(map[string]any)["id"].(string)
	ruleRunID := alice.must("POST", "/api/w/rules/"+ruleID+"/test", map[string]any{}, 200)["run"].(map[string]any)["id"].(string)
	ruleSecret := alice.must("GET", "/api/w/rules/"+ruleID+"/secret", nil, 200)["secret"].(string)

	// A connected app in A (MH-391): an OAuth client and a grant bound to A,
	// inserted directly — approving one needs the consent flow, which
	// oauth_test.go drives. Its routes (/api/w/oauth_grants) are swept below
	// like every other, and A's audit log (MH-392, /api/w/audit) — which by now
	// holds rows naming A's marker — is too.
	oauthClientID := "mhc_iso" + rnd()
	if _, err := a.SysDB.Exec(`INSERT INTO oauth_clients (id, client_name, redirect_uris) VALUES ($1, $2, '["https://iso.test/cb"]')`, oauthClientID, marker+" Connector"); err != nil {
		t.Fatal(err)
	}
	var grantID string
	if err := a.SysDB.QueryRow(`INSERT INTO oauth_grants (workspace_id, user_id, client_id, client_name, scopes, resource, activated_at)
		SELECT $1, user_id, $2, $3, 'members:read', 'x', now() FROM workspace_members WHERE workspace_id = $1 LIMIT 1 RETURNING id`,
		aID, oauthClientID, marker+" Connector").Scan(&grantID); err != nil {
		t.Fatal(err)
	}
	var auditRowsA int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1`, aID).Scan(&auditRowsA)
	if auditRowsA == 0 {
		t.Fatal("A's setup left no audit rows: the audit sweep below would prove nothing")
	}

	// Research (MH-388): a run with a source, a piece of evidence naming a
	// company, that company's suggestion, and a research schedule — written
	// directly, because a real run searches the web. The marker is in each.
	var runID, rsrcID, evID, suggID, rschedID string
	if err := a.SysDB.QueryRow(`INSERT INTO research_runs (workspace_id, topic, query, status, summary) VALUES ($1, $2, $2, 'ok', $2) RETURNING id`,
		aID, marker+" research").Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO research_sources (workspace_id, run_id, position, url, title, content, fetched) VALUES ($1, $2, 0, $3, $4, $4, true) RETURNING id`,
		aID, runID, "https://iso.test/"+marker, marker+" page").Scan(&rsrcID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO research_evidence (workspace_id, run_id, source_id, quote, source_url, company_name, company_key, signal_type, confidence, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'funding', 0.9, $8) RETURNING id`,
		aID, runID, rsrcID, marker+" raised money", "https://iso.test/"+marker, marker+" Corp", "isocorp-"+marker, "h-"+rnd()).Scan(&evID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO research_companies (workspace_id, company_key, name, evidence_count, first_run_id, last_run_id) VALUES ($1, $2, $3, 1, $4, $4) RETURNING id`,
		aID, "isocorp-"+marker, marker+" Corp", runID).Scan(&suggID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO research_schedules (workspace_id, name, topic, enabled) VALUES ($1, $2, $2, false) RETURNING id`,
		aID, marker+" schedule").Scan(&rschedID); err != nil {
		t.Fatal(err)
	}

	// Prospects (MH-397): A's profile, a prospect on A's suggestion, a verdict
	// on it and a draft — written directly, like the research rows above.
	var prospectID, pfbID, pdraftID string
	if _, err := a.SysDB.Exec(`INSERT INTO prospect_profiles (workspace_id, offer, keywords) VALUES ($1, $2, '{iso}')`, aID, marker+" offer"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO prospects (workspace_id, suggestion_id, score, contact_name) VALUES ($1, $2, 50, $3) RETURNING id`,
		aID, suggID, marker+" Person").Scan(&prospectID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO prospect_feedback (workspace_id, prospect_id, verdict, reason) VALUES ($1, $2, 'rejected', $3) RETURNING id`,
		aID, prospectID, marker+" reason").Scan(&pfbID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO prospect_drafts (workspace_id, prospect_id, body) VALUES ($1, $2, $3) RETURNING id`,
		aID, prospectID, marker+" draft").Scan(&pdraftID); err != nil {
		t.Fatal(err)
	}

	// A's workspace profile, which presets fill their defaults from (MH-478).
	alice.must("PUT", "/api/w/automation/profile", map[string]any{"profile": map[string]string{"first_name": marker, "email": marker + "@a.test"}}, 200)

	// A finished run in A with extracted data and a screenshot row (MH-478):
	// its data export and screenshot are read by id from the path.
	var autoRunID string
	if err := a.SysDB.QueryRow(`INSERT INTO automation_runs (workspace_id, agent_id, status, workflow_id, schedule_id) VALUES ($1, $2, 'ok', $3, $4) RETURNING id`,
		aID, agentID, wfID, schedID).Scan(&autoRunID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO automation_run_steps (workspace_id, run_id, position, command, status, result, screenshot_path)
		VALUES ($1, $2, 0, 'extract', 'ok', $3, $4)`, aID, autoRunID, `{"rows":[{"secret":"`+marker+`"}]}`, "automation/"+aID+"/"+autoRunID+"/step-000.png"); err != nil {
		t.Fatal(err)
	}

	// An import job and a group conversation's participant (MH-562), written
	// directly like the rows above — the job is read by id, the participant by
	// the conversation it belongs to.
	var importJobID, convPartID string
	if err := a.SysDB.QueryRow(`INSERT INTO import_jobs (workspace_id, source, status, created_by) VALUES ($1, 'whatsapp', 'done', $2) RETURNING id`,
		aID, marker).Scan(&importJobID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`INSERT INTO conversation_participants (workspace_id, conversation_id, identity, name) VALUES ($1, $2, $3, $4) RETURNING id`,
		aID, convID, "+201015550"+marker[:3], marker+" participant").Scan(&convPartID); err != nil {
		t.Fatal(err)
	}

	aIDs := []string{autoRunID, prospectID, pfbID, pdraftID, runID, rsrcID, evID, suggID, rschedID, aID, grantID, invID, tokID, memberID, coID, ctID, stageID, dealID, actID, srcID, itemID, widgetID, convID, formID, leadID, googleID, googleEmail, listID, pushID, kwID, feedID, newsID, trendID, googleResID, googleResExt, agentID, wfID, schedID, scheduleSecret, convDealID, cannedID, smtpID, mailboxID, notifSrcID, ruleID, ruleRunID, ruleSecret, tgID, slackID, discordID, workflowChannelID, importJobID, convPartID}
	idFor := map[string]string{
		"workspaces": aID, "workspace_members": memberID, "invitations": invID, "mcp_tokens": tokID,
		"companies": coID, "contacts": ctID, "deals": dealID, "activities": actID, "pipeline": stageID,
		"webhook_sources": srcID, "notifications": itemID,
		"import":       importJobID,
		"chat_widgets": widgetID, "conversations": convID,
		"subscribe-forms": formID, "leads": leadID,
		"google": googleID,
		"mail":   listID,
		"push":   pushID,
		"topics": kwID, "feed_sources": feedID, "news": newsID, "trends": trendID,
		"browsers": agentID, "workflows": wfID, "schedules": schedID, "runs": autoRunID,
		"canned_replies":       cannedID,
		"oauth_grants":         grantID,
		"smtp-services":        smtpID,
		"mailboxes":            mailboxID,
		"notification_sources": notifSrcID,
		"telegram-channels":    tgID,
		"slack-channels":       slackID,
		"discord-channels":     discordID,
		"workflow-channels":    workflowChannelID,
		"research":             runID, "company-suggestions": suggID, "research-schedules": rschedID,
		"prospects": prospectID,
		"rules":     ruleID,
		"rule-runs": ruleRunID,
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
	var grantRevoked any
	_ = a.SysDB.QueryRow(`SELECT revoked_at FROM oauth_grants WHERE id = $1`, grantID).Scan(&grantRevoked)
	if grantRevoked != nil {
		t.Error("B's sweep revoked A's connected app")
	}
	alice.ws = aID
	if code, b := alice.do("GET", "/api/w/invitations", nil); code != 200 || !strings.Contains(string(b), invID) {
		t.Errorf("A's invitation was removed from B: %d %s", code, b)
	}
	if code, b := alice.do("GET", "/api/w/notify/whatsapp", nil); code != 200 || !strings.Contains(string(b), "201699990000") {
		t.Errorf("alice's WhatsApp notifications were changed or removed from B: %d %s", code, b)
	}
	// MH-389: A's digest survived, and B's own shows none of it.
	if code, b := alice.do("GET", "/api/w/notify/digest", nil); code != 200 || !strings.Contains(string(b), `"frequency":"weekly"`) || !strings.Contains(string(b), marker) {
		t.Errorf("alice's trends digest was changed or removed from B: %d %s", code, b)
	}
	bob.ws = bID
	if _, b := bob.do("GET", "/api/w/notify/digest", nil); strings.Contains(string(b), marker) || strings.Contains(string(b), `"weekly"`) {
		t.Errorf("bob's trends digest showed alice's: %s", b)
	}
	// MH-427: A's rule, its log and its webhook secret survived B's sweep.
	if code, b := alice.do("GET", "/api/w/rules/"+ruleID+"/secret", nil); code != 200 || !strings.Contains(string(b), ruleSecret) {
		t.Errorf("A's rule or its secret was changed or removed from B: %d %s", code, b)
	}
	if code, b := alice.do("GET", "/api/w/rule-runs/"+ruleRunID, nil); code != 200 || !strings.Contains(string(b), marker) {
		t.Errorf("A's rule run was removed from B: %d %s", code, b)
	}
	bob.ws = bID
	if _, b := bob.do("GET", "/api/w/notify/whatsapp", nil); strings.Contains(string(b), "201699990000") || strings.Contains(string(b), marker) {
		t.Errorf("bob's WhatsApp notifications showed alice's: %s", b)
	}
	bob.ws = aID
	if code, _ := bob.do("GET", "/api/w/notify/whatsapp", nil); code != 404 {
		t.Errorf("bob naming A for WhatsApp notifications: %d", code)
	}
	if code, b := alice.do("GET", "/api/w/smtp-services", nil); code != 200 || !strings.Contains(string(b), smtpID) || !strings.Contains(string(b), `"is_default":true`) {
		t.Errorf("A's SMTP service was changed or removed from B: %d %s", code, b)
	}
	// MH-449: A's GA4 setup and outbox survived B's PATCH and test, and still hold A's lead.
	if code, b := alice.do("GET", "/api/w/ga4-conversions", nil); code != 200 || !strings.Contains(string(b), `"measurement_id":"G-ISO12345"`) ||
		!strings.Contains(string(b), `"events_enabled":true`) || !strings.Contains(string(b), `"api_secret_set":true`) || !strings.Contains(string(b), leadID) ||
		strings.Contains(string(b), ga4Secret) {
		t.Errorf("A's GA4 conversions were changed from B, or leaked the secret: %d %s", code, b)
	}

	// The sweep's empty body is refused by validation before the handler runs,
	// so try the webhook secret and signing writes with VALID bodies too: from
	// B they must not reach A's source. A secret overwritten from B would let B
	// forge A's notifications (MH-381).
	bob.ws = bID
	// The paged lists (MH-467) take A's ids in the QUERY string, which the path
	// sweep above never fills: a timeline of A's deal, contact or company, and
	// the next page of A's pipeline stage, must show bob nothing of A's.
	for _, p := range []string{
		"/api/w/activities?deal_id=" + dealID,
		"/api/w/activities?contact_id=" + ctID,
		"/api/w/activities?company_id=" + coID,
		"/api/w/deals?stage_id=" + stageID,
		"/api/w/deals?view=list",
		"/api/w/companies?q=" + url.QueryEscape(marker),
		"/api/w/campaigns",
	} {
		code, b := bob.do("GET", p, nil)
		if what := leaks(b); what != "" || (code != http.StatusOK && code != http.StatusNotFound) {
			t.Errorf("bob in B: GET %s -> %d, leaked %q: %.200s", p, code, what, b)
		}
		if strings.Contains(p, "/activities") && !strings.Contains(string(b), `"total":0`) {
			t.Errorf("bob in B: GET %s counted A's activities: %.200s", p, b)
		}
	}
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

	// The workflow canvas (MH-480): a layout needs its nodes and signature, so
	// the sweep's empty body never reached the handler. From B with valid
	// bodies: A's layout is neither read nor moved, and A's test runs are not
	// adopted into A's workflow by B (nor could B name them to).
	alice.must("PUT", "/api/w/workflows/"+wfID+"/layout", map[string]any{"nodes": []map[string]any{{"x": 1, "y": 2}, {"x": 3, "y": 4}}, "signature": "navigate|type"}, 200)
	bob.ws = bID
	if code, b := bob.do("PUT", "/api/w/workflows/"+wfID+"/layout", map[string]any{"nodes": []map[string]any{{"x": 999, "y": 999}}, "signature": "bob"}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B moving A's canvas nodes: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("POST", "/api/w/workflows/"+wfID+"/executions/adopt", map[string]any{"run_ids": []string{aID}}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B adopting runs into A's workflow: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("GET", "/api/w/workflow-executions", nil); code != 200 || leaks(b) != "" {
		t.Errorf("B's workflow executions: %d, leaked %q", code, leaks(b))
	}
	alice.ws = aID
	if l := alice.must("GET", "/api/w/workflows/"+wfID+"/layout", nil, 200); l["signature"] != "navigate|type" {
		t.Errorf("A's canvas layout changed during the sweep: %v", l)
	}

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

	// MH-563/MH-564: with VALID bodies from B, A's channels cannot be renamed,
	// re-keyed, tested, pushed into or deleted.
	bob.ws = bID
	if code, b := bob.do("PUT", "/api/w/telegram-channels/"+tgID, map[string]any{"name": "bob owns this", "bot_token": "bob-token"}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B editing A's Telegram channel: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("POST", "/api/w/telegram-channels/"+tgID+"/test", map[string]any{}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B testing A's Telegram channel: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("DELETE", "/api/w/slack-channels/"+slackID, nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B deleting A's Slack channel: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("PUT", "/api/w/discord-channels/"+discordID, map[string]any{"name": "bob", "bot_token": "bob-token"}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B editing A's Discord channel: got %d, want 404: %s", code, b)
	}
	if code, b := bob.do("POST", "/api/w/workflow-channels/"+workflowChannelID+"/push", map[string]any{
		"items": []map[string]any{{"thread_id": "bob-t", "message_id": "bob-m", "text": "bob's message"}},
	}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B pushing into A's workflow channel: got %d, want 404: %s", code, b)
	}
	alice.ws = aID
	if l := alice.must("GET", "/api/w/telegram-channels", nil, 200)["channels"].([]any); len(l) != 1 || l[0].(map[string]any)["name"] != marker+" Telegram" {
		t.Errorf("A's Telegram channel was changed or removed from B: %v", l)
	}
	if l := alice.must("GET", "/api/w/slack-channels", nil, 200)["channels"].([]any); len(l) != 1 {
		t.Errorf("A's Slack channel was deleted from B: %v", l)
	}

	// MH-571: the One Channels page aggregates mailboxes, chat widgets,
	// Telegram/Slack/Discord and workflow channels into one list — it must not
	// re-open the leak each per-type endpoint already closed. A's own answer
	// carries every fixture created above (marker-named); B's answer, scoped to
	// its own workspace, must carry none of A's names, ids or secrets, and the
	// general route sweep above already proved B naming A gets a 404.
	alice.ws = aID
	if l := alice.must("GET", "/api/w/channels", nil, 200)["channels"].([]any); len(l) < 6 {
		t.Errorf("A's aggregate channel list is missing fixtures: got %d rows: %v", len(l), l)
	} else {
		var sawMailbox, sawTelegram, sawSlack, sawDiscord, sawWorkflow, sawChat bool
		for _, row := range l {
			m := row.(map[string]any)
			switch m["id"] {
			case mailboxID:
				sawMailbox = true
			case tgID:
				sawTelegram = true
			case slackID:
				sawSlack = true
			case discordID:
				sawDiscord = true
			case workflowChannelID:
				sawWorkflow = true
			case widgetID:
				sawChat = true
			}
		}
		if !sawMailbox || !sawTelegram || !sawSlack || !sawDiscord || !sawWorkflow || !sawChat {
			t.Errorf("A's aggregate channel list is missing a fixture: mailbox=%v telegram=%v slack=%v discord=%v workflow=%v chat=%v (%v)",
				sawMailbox, sawTelegram, sawSlack, sawDiscord, sawWorkflow, sawChat, l)
		}
	}
	bob.ws = bID
	if code, b := bob.do("GET", "/api/w/channels", nil); code != 200 || leaks(b) != "" {
		t.Errorf("B's aggregate channel list leaked A's %s (status %d): %s", leaks(b), code, b)
	}
	bob.ws = aID
	if code, b := bob.do("GET", "/api/w/channels", nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B naming A's workspace for /api/w/channels: got %d, want 404: %s", code, b)
	}

	// Inbox → CRM and canned replies (MH-379, MH-383), with VALID bodies from B:
	// B cannot hang its contact on A's conversation, open a deal from it, snooze
	// it, or change or delete A's canned reply.
	bob.ws = bID
	bobContact := bob.must("POST", "/api/w/contacts", map[string]any{"email": "bob-ct-" + rnd() + "@example.test"}, 201)["contact"].(map[string]any)["id"].(string)
	for _, w := range []struct {
		method, path string
		body         map[string]any
	}{
		{"PATCH", "/api/w/conversations/" + convID, map[string]any{"contact_id": bobContact}},
		{"PATCH", "/api/w/conversations/" + convID, map[string]any{"snoozed_until": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}},
		{"POST", "/api/w/conversations/" + convID + "/contact", map[string]any{"email": "bob-steals-" + rnd() + "@example.test"}},
		{"POST", "/api/w/conversations/" + convID + "/deal", map[string]any{"title": "steal"}},
		// MH-583: B cannot link a new identity onto A's contact either.
		{"POST", "/api/w/contacts/" + ctID + "/identities", map[string]any{"channel": "telegram", "external_id": "@bob-steals-" + rnd()}},
		{"PATCH", "/api/w/canned_replies/" + cannedID, map[string]any{"title": "bob owns this"}},
		{"DELETE", "/api/w/canned_replies/" + cannedID, nil},
		// Editing a schedule (MH-471): B can neither re-time A's schedule nor
		// replace the secret it runs with.
		{"PATCH", "/api/w/schedules/" + schedID, map[string]any{"cron": "* * * * *", "name": "bob owns this"}},
		{"PATCH", "/api/w/schedules/" + schedID, map[string]any{"values": map[string]string{"page": "bob", "password": "bob-password"}}},
	} {
		if code, b := bob.do(w.method, w.path, w.body); code != http.StatusNotFound || leaks(b) != "" {
			t.Errorf("B %s %s: got %d, want 404: %s", w.method, w.path, code, b)
		}
	}
	// MH-583: B cannot unlink an identity off A's contact either — not even
	// A's own primary email identity, fetched from A's side just for its id.
	alice.ws = aID
	ctIdentities := alice.must("GET", "/api/w/contacts/"+ctID, nil, 200)["contact"].(map[string]any)["identities"].([]any)
	if len(ctIdentities) == 0 {
		t.Fatalf("A's contact should have at least its primary email identity: %v", ctIdentities)
	}
	ctIdentityID := ctIdentities[0].(map[string]any)["id"].(string)
	bob.ws = bID
	if code, b := bob.do("DELETE", "/api/w/contacts/"+ctID+"/identities/"+ctIdentityID, nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B unlinking A's contact identity: got %d, want 404: %s", code, b)
	}
	alice.ws = aID
	if ct := alice.must("GET", "/api/w/contacts/"+ctID, nil, 200)["contact"].(map[string]any); len(ct["identities"].([]any)) != len(ctIdentities) {
		t.Errorf("A's contact identity was removed from B: %+v", ct["identities"])
	}
	// And A's own side of it cannot point outside A: linking B's contact fails.
	alice.ws = aID
	if code, b := alice.do("PATCH", "/api/w/conversations/"+convID, map[string]any{"contact_id": bobContact}); code != http.StatusUnprocessableEntity {
		t.Errorf("A linking B's contact: got %d, want 422: %s", code, b)
	}
	if cv := alice.must("GET", "/api/w/conversations/"+convID, nil, 200)["conversation"].(map[string]any); cv["contact_id"] != ctID || cv["status"] != "open" ||
		cv["deal"].(map[string]any)["id"] != convDealID {
		t.Errorf("A's conversation was changed from B: %+v", cv)
	}
	if sc := alice.must("GET", "/api/w/schedules", nil, 200)["schedules"].([]any); len(sc) != 1 ||
		sc[0].(map[string]any)["cron"] != "0 7 * * *" || sc[0].(map[string]any)["values"].(map[string]any)["page"] != "inbox" {
		t.Errorf("A's schedule was changed from B: %+v", sc)
	}
	if r := alice.must("GET", "/api/w/canned_replies", nil, 200)["replies"].([]any); len(r) != 1 || r[0].(map[string]any)["title"] != marker+" canned" {
		t.Errorf("A's canned reply was changed from B: %+v", r)
	}
	if st := alice.must("GET", "/api/w/inbox/stats", nil, 200)["total"].(map[string]any); st["conversations"].(float64) != 1 {
		t.Errorf("A's stats: %+v", st)
	}
	bob.ws = bID
	if st := bob.must("GET", "/api/w/inbox/stats", nil, 200)["total"].(map[string]any); st["conversations"].(float64) != 0 {
		t.Errorf("B's stats counted A's conversations: %+v", st)
	}
	alice.ws = aID

	// Google (MH-439): B cannot list, disconnect or draw tokens from A's
	// connection, and A's grant survives every attempt — not revoked at Google.
	bob.ws = bID
	if code, b := bob.do("DELETE", "/api/w/google/connections/"+googleID, nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B disconnecting A's Google account: got %d, want 404: %s", code, b)
	}
	if _, b := bob.do("GET", "/api/w/google/connections", nil); leaks(b) != "" {
		t.Errorf("B's Google list leaked A: %s", b)
	}
	// MH-440: B cannot switch, sync or read A's property.
	for _, try := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/api/w/google/resources/" + googleResID, map[string]any{"enabled": false}},
		{"POST", "/api/w/google/resources/" + googleResID + "/sync", nil},
	} {
		if code, b := bob.do(try.method, try.path, try.body); code != http.StatusNotFound || leaks(b) != "" {
			t.Errorf("B %s %s: got %d, want 404: %s", try.method, try.path, code, b)
		}
	}
	// MH-444: B cannot read A's GA4 report by naming A's property.
	if code, b := bob.do("GET", "/api/w/analytics/report?resource="+googleResID, nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B reading A's GA4 report: got %d, want 404: %s", code, b)
	}
	if _, b := bob.do("GET", "/api/w/overview", nil); strings.Contains(string(b), googleResID) {
		t.Errorf("B's overview shows A's property: %s", b)
	}
	if _, b := bob.do("GET", "/api/w/google/resources", nil); leaks(b) != "" {
		t.Errorf("B's resource list leaked A: %s", b)
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
	if code, b := alice.do("GET", "/api/w/push", nil); code != 200 || !strings.Contains(string(b), pushID) {
		t.Errorf("A's push device was removed from B: %d %s", code, b)
	}

	// The overview (MH-462) aggregates every area, so it is the one route most
	// able to leak by accident. A's own overview shows A's lead, conversation
	// and notification; B's shows none of A's rows or figures —
	// and A's id in the header from B is the same 404 as everywhere else.
	alice.ws = aID
	if code, b := alice.do("GET", "/api/w/overview", nil); code != 200 ||
		!strings.Contains(string(b), leadID) || !strings.Contains(string(b), convID) || !strings.Contains(string(b), itemID) {
		t.Errorf("A's overview does not show A's own rows: %d %s", code, b)
	}
	bob.ws = bID
	if code, b := bob.do("GET", "/api/w/overview", nil); code != 200 || leaks(b) != "" {
		t.Errorf("B's overview leaked A's %s: %d %s", leaks(b), code, b)
	} else {
		var ov struct {
			Today struct {
				Leads               map[string]int `json:"leads"`
				UnreadConversations int            `json:"unread_conversations"`
				UnreadNotifications int            `json:"unread_notifications"`
			} `json:"today"`
			Setup []struct {
				Key  string `json:"key"`
				Done bool   `json:"done"`
			} `json:"setup"`
		}
		_ = json.Unmarshal(b, &ov)
		if ov.Today.Leads["new"] != 0 || ov.Today.UnreadConversations != 0 || ov.Today.UnreadNotifications != 0 {
			t.Errorf("B's overview counts A's rows: %s", b)
		}
		for _, it := range ov.Setup {
			if it.Done {
				t.Errorf("B's setup reads A's configuration (%s): %s", it.Key, b)
			}
		}
	}
	bob.ws = aID
	if code, b := bob.do("GET", "/api/w/overview", nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B asking for A's overview: got %d, want 404: %s", code, b)
	}

	// The CRM report (MH-394) aggregates deals, stage history, leads and the
	// inbox. A's lead carries the marker as its utm_source, so A's report names
	// it; B's report must show none of A's figures, and A's id from B is a 404.
	alice.ws = aID
	if code, b := alice.do("GET", "/api/w/reports/crm", nil); code != 200 || !strings.Contains(strings.ToLower(string(b)), strings.ToLower(marker)) ||
		!strings.Contains(string(b), `"opened":2`) {
		t.Errorf("A's report does not show A's own deals and lead source: %d %s", code, b)
	}
	bob.ws = bID
	if code, b := bob.do("GET", "/api/w/reports/crm", nil); code != 200 || leaks(b) != "" ||
		strings.Contains(strings.ToLower(string(b)), strings.ToLower(marker)) || !strings.Contains(string(b), `"opened":0`) ||
		!strings.Contains(string(b), `"open_deals":0`) || !strings.Contains(string(b), `"leads":0`) {
		t.Errorf("B's report leaked or counted A's rows: %d %s", code, b)
	}
	var bobEvents int
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(context.Background(), bID), `SELECT count(*) FROM deal_stage_events`).Scan(&bobEvents); err != nil || bobEvents != 0 {
		t.Errorf("B's workspace reads %d stage events through RLS (%v)", bobEvents, err)
	}
	bob.ws = aID
	if code, b := bob.do("GET", "/api/w/reports/crm", nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B asking for A's report: got %d, want 404: %s", code, b)
	}

	// The ideal-customer profile and lifecycle (MH-395): A's profile names the
	// marker as an industry, and A's contact has a hand-set stage. B's profile
	// is B's own; B cannot read or write A's, and B's contact lists never carry
	// A's scores or stages.
	alice.ws = aID
	alice.must("PUT", "/api/w/icp", map[string]any{"industries": map[string]any{"values": []string{marker}, "weight": 4},
		"sizes": map[string]any{"values": []string{}, "weight": 0}, "regions": map[string]any{"values": []string{}, "weight": 0},
		"keywords": map[string]any{"values": []string{}, "weight": 0}, "signals": map[string]any{"open_deal": 2}}, 200)
	alice.must("PATCH", "/api/w/contacts/"+ctID, map[string]any{"lifecycle": "customer"}, 200)
	if code, b := alice.do("GET", "/api/w/contacts/"+ctID, nil); code != 200 || !strings.Contains(string(b), `"lifecycle":"customer"`) || !strings.Contains(string(b), `"source":"manual"`) {
		t.Errorf("A's contact does not carry its lifecycle and history: %d %s", code, b)
	}
	bob.ws = bID
	if code, b := bob.do("GET", "/api/w/icp", nil); code != 200 || leaks(b) != "" || !strings.Contains(string(b), `"configured":false`) {
		t.Errorf("B's profile shows A's: %d %s", code, b)
	}
	if code, b := bob.do("GET", "/api/w/contacts?sort=score&lifecycle=customer", nil); code != 200 || leaks(b) != "" || !strings.Contains(string(b), `"total":0`) {
		t.Errorf("B's scored contacts carry A's: %d %s", code, b)
	}
	bob.ws = aID
	if code, b := bob.do("GET", "/api/w/icp", nil); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B reading A's profile: got %d, want 404: %s", code, b)
	}
	if code, _ := bob.do("PUT", "/api/w/icp", map[string]any{"signals": map[string]any{"open_deal": 9}}); code != http.StatusNotFound {
		t.Errorf("B writing A's profile: got %d, want 404", code)
	}
	if code, b := alice.do("GET", "/api/w/icp", nil); code != 200 || !strings.Contains(string(b), marker) || !strings.Contains(string(b), `"open_deal":2`) {
		t.Errorf("A's profile was changed from B: %d %s", code, b)
	}
	alice.ws = aID

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

	// ---- connecting a browser by approval (MH-478) ----------------------------
	// The request belongs to no workspace until it is approved, and the member
	// picks the workspace in the body — so the tenant check is the handler's,
	// not the guard's. Bob holds the page's key and still cannot approve into A;
	// alice approves into A, and nothing of it reaches B.
	cs := map[string]string{}
	{
		anonC := newClient(t, srv.URL)
		code, b := anonC.do("POST", "/api/agents/connect/start", map[string]any{"name": marker + " connecting", "fingerprint": "iso-fp-" + rnd()})
		if code != http.StatusCreated {
			t.Fatalf("connect/start: %d %s", code, b)
		}
		_ = json.Unmarshal(b, &cs)
	}
	bob.ws = bID
	if code, b := bob.do("POST", "/api/agents/connect/approve", map[string]any{"code": cs["code"], "key": cs["browser_key"], "workspace": aID, "name": "bob's"}); code != http.StatusNotFound || leaks(b) != "" {
		t.Errorf("B approving a browser into A: got %d, want 404: %s", code, b)
	}
	alice.ws = aID
	alice.must("POST", "/api/agents/connect/approve", map[string]any{"code": cs["code"], "key": cs["browser_key"], "workspace": aID, "name": marker + " connected"}, 200)
	bob.ws = bID
	for _, p := range []string{"/api/w/browsers", "/api/agents/connect/" + cs["code"]} {
		if _, b := bob.do("GET", p, nil); leaks(b) != "" || strings.Contains(string(b), aID) {
			t.Errorf("B: GET %s leaked A's connected browser: %s", p, b)
		}
	}
	alice.ws = aID

	// Runs (MH-478): B cannot read A's run, its data export or its screenshot,
	// in any format, and B's run list filtered by A's workflow is empty.
	bob.ws = bID
	for _, p := range []string{"/api/w/runs/" + autoRunID, "/api/w/runs/" + autoRunID + "/data", "/api/w/runs/" + autoRunID + "/data?format=csv",
		"/api/w/runs/" + autoRunID + "/steps/0/screenshot", "/api/w/runs?workflow=" + wfID, "/api/w/runs?schedule=" + schedID} {
		code, b := bob.do("GET", p, nil)
		if leaks(b) != "" || (code == 200 && !strings.Contains(string(b), `"runs":[]`)) {
			t.Errorf("B: GET %s -> %d, leaked %q: %.200s", p, code, leaks(b), b)
		}
	}
	alice.ws = aID
	if code, b := alice.do("GET", "/api/w/runs/"+autoRunID+"/data?format=csv", nil); code != 200 || !strings.Contains(string(b), marker) {
		t.Errorf("A's own run data: %d %s", code, b)
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

	// ---- every user MCP tool, aimed at A from B -------------------------------
	//
	// The REST sweep above walks the router; MCP tools are not routes, so they
	// get their own sweep (MH-380/386/393/409 added dozens). Bob's token holds
	// every scope, and each tool is called with A's ids in every id argument
	// it takes and A's marker as its search text. Nothing of A's may come back,
	// and A's rows must be unchanged afterwards. New tools are swept
	// automatically: nothing here lists them by hand.
	//
	// A's source, item and trending topic come from the setup above (feedID,
	// newsID, trendID). A's side gets rows in the two tables only these tools
	// reach: a WhatsApp send and a pending WhatsApp confirmation.
	if _, err := a.SysDB.Exec(`INSERT INTO whatsapp_sends (workspace_id, kind, phone, ok, error) VALUES ($1, 'direct', '201000000009', false, $2)`, aID, marker); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO whatsapp_confirmations (workspace_id, code_hash, contact_id, text_hash, requested_by, expires_at)
		VALUES ($1, $2, $3, 'x', 'iso', now() + interval '10 minutes')`, aID, "iso-"+rnd(), ctID); err != nil {
		t.Fatal(err)
	}
	bob.ws = bID
	allScopes := []string{}
	for _, sc := range mcp.UserScopes {
		allScopes = append(allScopes, sc.Scope)
	}
	// The tool's id argument, by what the tool is about. A tool whose subject
	// is not matched here is tried with every one of A's ids.
	subjects := []struct{ word, id string }{
		{"conversation", convID}, {"notification", itemID}, {"webhook_source", srcID},
		{"trending_topic", trendID}, {"trend_item", newsID}, {"trend_source", feedID}, {"contact", ctID},
		{"company_suggestion", suggID}, {"research_schedule", rschedID}, {"opportunit", prospectID}, {"compan", coID},
		{"deal", dealID}, {"lead", leadID}, {"whatsapp", ctID},
	}
	toolsSwept := 0
	for _, tool := range mcp.UserTools(nil) {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		ids := []string{}
		for _, sj := range subjects {
			if strings.Contains(tool.Name, sj.word) {
				ids = []string{sj.id}
				break
			}
		}
		if _, takesID := props["id"]; takesID && len(ids) == 0 {
			ids = aIDs
		}
		if len(ids) == 0 {
			ids = []string{""}
		}
		// A token per tool: the endpoint is rate limited per token.
		tk := bob.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "sweep " + tool.Name, "scopes": allScopes}, 201)
		agent := newClient(t, srv.URL)
		agent.bearer = tk["plaintext"].(string)
		for _, id := range ids {
			args := map[string]any{}
			for k := range props {
				switch k {
				case "id":
					args[k] = id
				case "contact_id":
					args[k] = ctID
				case "company_id":
					args[k] = coID
				case "deal_id":
					args[k] = dealID
				case "source_id":
					if strings.Contains(tool.Name, "trend") {
						args[k] = feedID
					} else {
						args[k] = srcID
					}
				case "trending_topic_id":
					args[k] = trendID
				case "run_id":
					args[k] = runID
				case "keyword_topic_id":
					args[k] = kwID
				case "q":
					args[k] = marker
				// The browser tools (MH-478): A's browser and workflow by id,
				// and a site to widen A's list with.
				case "agent":
					args[k] = agentID
				case "plugin":
					args[k] = wfID
				case "run":
					args[k] = autoRunID
				case "add", "replace":
					args[k] = []string{"evil.test"}
				}
			}
			code, text, _ := mcpCall(agent, "/api/mcp", tool.Name, args)
			if code != http.StatusOK {
				t.Errorf("MCP %s from B: status %d", tool.Name, code)
			}
			if what := leaks([]byte(text)); what != "" {
				t.Errorf("MCP %s(%v) from B leaked A's %s: %s", tool.Name, args, what, text)
			}
		}
		toolsSwept++
	}
	if toolsSwept < 60 {
		t.Fatalf("swept only %d MCP tools — the registry walk is broken", toolsSwept)
	}
	t.Logf("swept %d user MCP tools from B with A's ids", toolsSwept)

	// A's browser still allows exactly what it did: no tool of B's widened it.
	var aHosts string
	_ = a.SysDB.QueryRow(`SELECT array_to_string(allowed_hosts, ',') FROM browser_agents WHERE id = $1`, agentID).Scan(&aHosts)
	if aHosts != "iso.test" {
		t.Errorf("A's browser's allowed sites changed under B's MCP sweep: %q", aHosts)
	}

	// A's rows the new tools could write are exactly as they were.
	var convStatus, ctTitle string
	var convAssignee, itemRead, itemSaved, newsSaved any
	var srcEnabled, feedEnabled bool
	_ = a.SysDB.QueryRow(`SELECT status, assignee_user_id FROM conversations WHERE id = $1`, convID).Scan(&convStatus, &convAssignee)
	_ = a.SysDB.QueryRow(`SELECT read_at, saved_at FROM notification_items WHERE id = $1`, itemID).Scan(&itemRead, &itemSaved)
	_ = a.SysDB.QueryRow(`SELECT enabled FROM webhook_sources WHERE id = $1`, srcID).Scan(&srcEnabled)
	_ = a.SysDB.QueryRow(`SELECT saved_at FROM news_items WHERE id = $1`, newsID).Scan(&newsSaved)
	_ = a.SysDB.QueryRow(`SELECT enabled FROM feed_sources WHERE id = $1`, feedID).Scan(&feedEnabled)
	_ = a.SysDB.QueryRow(`SELECT title FROM contacts WHERE id = $1`, ctID).Scan(&ctTitle)
	var trendReviewed, trendDismissed, trendSaved any
	_ = a.SysDB.QueryRow(`SELECT reviewed_at, dismissed_at, saved_at FROM trend_clusters WHERE id = $1`, trendID).Scan(&trendReviewed, &trendDismissed, &trendSaved)
	if trendReviewed != nil || trendDismissed != nil || trendSaved != nil {
		t.Errorf("A's trending topic was reviewed from B's MCP sweep: %v %v %v", trendReviewed, trendDismissed, trendSaved)
	}
	// A's research: the suggestion is still waiting, the schedule unchanged and
	// still there, the evidence still there, and no CRM company came of it.
	var suggStatus, rschedName string
	var rschedEnabled bool
	var evLeft, aCompanies int
	_ = a.SysDB.QueryRow(`SELECT status FROM research_companies WHERE id = $1`, suggID).Scan(&suggStatus)
	_ = a.SysDB.QueryRow(`SELECT name, enabled FROM research_schedules WHERE id = $1`, rschedID).Scan(&rschedName, &rschedEnabled)
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM research_evidence WHERE id = $1`, evID).Scan(&evLeft)
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM companies WHERE workspace_id = $1 AND name = $2`, aID, marker+" Corp").Scan(&aCompanies)
	if suggStatus != "suggested" || rschedName != marker+" schedule" || rschedEnabled || evLeft != 1 || aCompanies != 0 {
		t.Errorf("A's research changed under B's MCP sweep: suggestion=%q schedule=%q/%v evidence=%d crm=%d",
			suggStatus, rschedName, rschedEnabled, evLeft, aCompanies)
	}
	// A's prospect is still waiting, with only its own draft, and A's profile
	// is A's.
	var pStatus, pOffer string
	var pDrafts int
	_ = a.SysDB.QueryRow(`SELECT status FROM prospects WHERE id = $1`, prospectID).Scan(&pStatus)
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM prospect_drafts WHERE prospect_id = $1`, prospectID).Scan(&pDrafts)
	_ = a.SysDB.QueryRow(`SELECT offer FROM prospect_profiles WHERE workspace_id = $1`, aID).Scan(&pOffer)
	if pStatus != "new" || pDrafts != 1 || pOffer != marker+" offer" {
		t.Errorf("A's prospects changed under B's MCP sweep: status=%q drafts=%d offer=%q", pStatus, pDrafts, pOffer)
	}
	if convStatus != "open" || convAssignee != nil || itemSaved != nil || !srcEnabled || newsSaved != nil || !feedEnabled || ctTitle != "" {
		t.Errorf("A's rows changed under B's MCP sweep: conv=%s/%v item saved=%v source enabled=%v news saved=%v feed enabled=%v contact title=%q",
			convStatus, convAssignee, itemSaved, srcEnabled, newsSaved, feedEnabled, ctTitle)
	}
	// A's conversation keeps the deal alice opened from it in the setup
	// (convDealID) and is not snoozed.
	var convDeal *string
	var convSnooze any
	_ = a.SysDB.QueryRow(`SELECT deal_id, snoozed_until FROM conversations WHERE id = $1`, convID).Scan(&convDeal, &convSnooze)
	if convDeal == nil || *convDeal != convDealID || convSnooze != nil {
		t.Errorf("B's MCP sweep changed A's conversation's deal or snoozed it: %v %v", convDeal, convSnooze)
	}
	var messages int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM messages WHERE conversation_id = $1 AND direction = 'out'`, convID).Scan(&messages)
	if messages != 0 {
		t.Errorf("B's MCP sweep wrote %d replies into A's conversation", messages)
	}
	// B's WhatsApp count is B's: A's send today is not in it.
	statusTok := bob.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "sweep status", "scopes": []string{"inbox:read"}}, 201)
	statusAgent := newClient(t, srv.URL)
	statusAgent.bearer = statusTok["plaintext"].(string)
	if _, text, isErr := mcpCall(statusAgent, "/api/mcp", "whatsapp_status", nil); isErr || !strings.Contains(text, `"sent_today":0`) || !strings.Contains(text, `"failed_today":0`) {
		t.Errorf("B's whatsapp_status counted A's sends: %s", text)
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
