package server

/*
TestChatWidgetParity — the widget's settings, config, presence, unread count
and live stream (MH-382), through the real stack: guard, chat CORS, RLS pool.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ChatWidgetParity -v
*/

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/presence"
	"github.com/fadymondy/moharrik/internal/testdb"
)

type rawReply struct {
	code int
	body map[string]any
	raw  string
	hdr  http.Header
}

func publicCall(t *testing.T, method, url, origin string, body any) rawReply {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, rd)
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return rawReply{code: res.StatusCode, body: out, raw: string(raw), hdr: res.Header}
}

func TestChatWidgetParity(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "chat-parity-test-secret-chat-parity-test-secret-0")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("NOTIFY_COLLECTOR", "off")
	defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice, bob := newClient(t, srv.URL), newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "par-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "par-b-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	aID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Parity A " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	bID := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Parity B " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws, bob.ws = aID, bID

	const originA, originB = "https://shop-a.test", "https://shop-b.test"

	// ---- settings -----------------------------------------------------------------

	if code, b := alice.do("POST", "/api/w/chat_widgets", map[string]any{"name": "Bad", "allowed_origins": []string{originA}, "accent": "red;}*{x:y"}); code != 422 {
		t.Fatalf("an accent that is not a colour: got %d want 422: %s", code, b)
	}
	if code, b := alice.do("POST", "/api/w/chat_widgets", map[string]any{"name": "Bad", "quick_actions": []map[string]any{{"label": "x", "message": "y", "path": "pricing"}}}); code != 422 {
		t.Fatalf("a quick action path without a leading slash: got %d want 422: %s", code, b)
	}
	w := alice.must("POST", "/api/w/chat_widgets", map[string]any{
		"name": "Shop", "allowed_origins": []string{originA},
		"greeting": `Hi "there" <b>`, "greeting_ar": "أهلًا", "accent": "#4B00B6", "theme": "dark", "position": "start",
		"invite": true, "invite_after_seconds": 10,
		"quick_actions": []map[string]any{{"label": "Pricing", "message": "How much is it?", "path": "/pricing"}},
	}, 201)["widget"].(map[string]any)
	keyA, widgetA := w["public_key"].(string), w["id"].(string)
	embed := w["embed"].(string)
	for _, want := range []string{`data-key="` + keyA + `"`, `data-greeting="Hi &#34;there&#34; &lt;b&gt;"`, `data-accent="#4B00B6"`, `data-theme="dark"`, `data-position="start"`, `data-greeting-ar="أهلًا"`} {
		if !strings.Contains(embed, want) {
			t.Errorf("the snippet lacks %s: %s", want, embed)
		}
	}
	if w["lead_event"] != true || w["invite"] != true || w["invite_after_seconds"].(float64) != 10 {
		t.Errorf("created settings: %+v", w)
	}
	// A patch changes one thing and keeps the rest.
	p := alice.must("PATCH", "/api/w/chat_widgets/"+widgetA, map[string]any{"lead_event": false, "theme": "auto"}, 200)["widget"].(map[string]any)
	if p["lead_event"] != false || p["greeting"] != `Hi "there" <b>` || p["accent"] != "#4B00B6" || strings.Contains(p["embed"].(string), "data-theme") {
		t.Errorf("patched settings: %+v", p)
	}
	keyB := bob.must("POST", "/api/w/chat_widgets", map[string]any{"name": "B", "allowed_origins": []string{originB}}, 201)["widget"].(map[string]any)["public_key"].(string)

	// ---- config and presence ----------------------------------------------------

	presence.Reset()
	cfgURL := srv.URL + "/api/chat/" + keyA + "/config"
	if r := publicCall(t, "GET", cfgURL, "https://evil.test", nil); r.code != 403 {
		t.Fatalf("config from an origin not on the list: got %d want 403: %s", r.code, r.raw)
	}

	// MH-687: an entry that could never match a browser Origin is refused on
	// create and on edit, and "*." covers the subdomains (not the apex).
	for _, bad := range []string{"shop-a.test", "https://shop-a.test/contact", "https://a.*.test"} {
		if code, b := alice.do("POST", "/api/w/chat_widgets", map[string]any{"name": "Bad", "allowed_origins": []string{bad}}); code != 422 {
			t.Errorf("create with origin %q: got %d want 422: %s", bad, code, b)
		}
		if code, b := alice.do("PATCH", "/api/w/chat_widgets/"+widgetA, map[string]any{"allowed_origins": []string{originA, bad}}); code != 422 {
			t.Errorf("edit to origin %q: got %d want 422: %s", bad, code, b)
		}
	}
	alice.must("PATCH", "/api/w/chat_widgets/"+widgetA, map[string]any{"allowed_origins": []string{originA, "https://*.shop-a.test"}}, 200)
	if r := publicCall(t, "GET", cfgURL, "https://www.shop-a.test", nil); r.code != 200 {
		t.Errorf("config from a subdomain under a *. entry: got %d want 200: %s", r.code, r.raw)
	}
	if r := publicCall(t, "GET", cfgURL, "https://shop-a.test.evil.test", nil); r.code != 403 {
		t.Errorf("config from a look-alike host: got %d want 403", r.code)
	}
	alice.must("PATCH", "/api/w/chat_widgets/"+widgetA, map[string]any{"allowed_origins": []string{originA}}, 200)
	presence.Reset() // the calls above counted as visits
	if r := publicCall(t, "GET", srv.URL+"/api/chat/cwk_nope/config", originA, nil); r.code != 404 {
		t.Fatalf("config for an unknown key: got %d want 404", r.code)
	}
	r := publicCall(t, "GET", cfgURL, originA, nil)
	if r.code != 200 || r.body["greeting"] != `Hi "there" <b>` || r.body["presence"] != "offline" {
		t.Fatalf("config: %d %s", r.code, r.raw)
	}
	if qa := r.body["quick_actions"].([]any); len(qa) != 1 || qa[0].(map[string]any)["path"] != "/pricing" {
		t.Errorf("quick actions: %+v", qa)
	}
	if r.hdr.Get("Access-Control-Allow-Origin") != originA {
		t.Errorf("the config is readable cross-origin only with CORS headers; got %q", r.hdr.Get("Access-Control-Allow-Origin"))
	}
	// Nothing about who, or when.
	for _, leak := range []string{"@example.test", "last_seen", "minutes"} {
		if strings.Contains(r.raw, leak) {
			t.Errorf("the public config says %q: %s", leak, r.raw)
		}
	}
	// A member who can answer is active: A is online, B (untouched since the reset) is not.
	alice.must("GET", "/api/w/conversations", nil, 200)
	if r := publicCall(t, "GET", cfgURL, originA, nil); r.body["presence"] != "online" {
		t.Errorf("after the owner's request, presence = %v, want online", r.body["presence"])
	}
	if r := publicCall(t, "GET", srv.URL+"/api/chat/"+keyB+"/config", originB, nil); r.body["presence"] != "offline" {
		t.Errorf("A's activity made B online: %v", r.body["presence"])
	}

	// ---- a visitor, a reply, the stream, unread -------------------------------------

	sent := publicCall(t, "POST", srv.URL+"/api/chat/"+keyA+"/messages", originA, map[string]any{"body": "hello", "page_url": originA + "/pricing"})
	if sent.code != 201 {
		t.Fatalf("visitor send: %d %s", sent.code, sent.raw)
	}
	tok := sent.body["visitor_token"].(string)
	thread := publicCall(t, "GET", srv.URL+"/api/chat/"+keyA+"/messages?visitor_token="+tok, originA, nil)
	if thread.code != 200 || thread.body["greeting"] != `Hi "there" <b>` || thread.body["unread"].(float64) != 0 || thread.body["presence"] != "online" {
		t.Fatalf("thread: %d %s", thread.code, thread.raw)
	}

	// The stream: refused before it opens for a wrong origin, a wrong token, or
	// A's token presented with B's key.
	evURL := srv.URL + "/api/chat/" + keyA + "/events?visitor_token=" + tok
	if r := publicCall(t, "GET", evURL, "https://evil.test", nil); r.code != 403 {
		t.Errorf("events from a foreign origin: got %d want 403", r.code)
	}
	if r := publicCall(t, "GET", srv.URL+"/api/chat/"+keyA+"/events?visitor_token=cvt_guess", originA, nil); r.code != 404 {
		t.Errorf("events with an unknown token: got %d want 404", r.code)
	}
	if r := publicCall(t, "GET", srv.URL+"/api/chat/"+keyB+"/events?visitor_token="+tok, originB, nil); r.code != 404 {
		t.Errorf("A's visitor token with B's key: got %d want 404", r.code)
	}
	if r := publicCall(t, "POST", srv.URL+"/api/chat/"+keyB+"/read", originB, map[string]any{"visitor_token": tok}); r.code != 404 {
		t.Errorf("marking A's thread read through B's key: got %d want 404", r.code)
	}

	req, _ := http.NewRequest("GET", evURL, nil)
	req.Header.Set("Origin", originA)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("events: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				lines <- strings.TrimPrefix(l, "data: ")
			}
		}
		close(lines)
	}()

	var convID string
	if err := a.SysDB.QueryRow(`SELECT id FROM conversations WHERE visitor_token = $1`, tok).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	// Bob replying into A's conversation reaches nobody.
	bob.do("POST", "/api/w/conversations/"+convID+"/reply", map[string]any{"body": "not yours"})
	alice.must("POST", "/api/w/conversations/"+convID+"/reply", map[string]any{"body": "We are here"}, 201)
	select {
	case l := <-lines:
		if !strings.Contains(l, `"kind":"message"`) || strings.Contains(l, "We are here") {
			t.Errorf("the stream event should say only that something changed: %s", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the visitor's stream heard nothing after the team replied")
	}
	thread = publicCall(t, "GET", srv.URL+"/api/chat/"+keyA+"/messages?visitor_token="+tok, originA, nil)
	if thread.body["unread"].(float64) != 1 || len(thread.body["messages"].([]any)) != 2 {
		t.Fatalf("after the reply: %s", thread.raw)
	}
	if r := publicCall(t, "POST", srv.URL+"/api/chat/"+keyA+"/read", originA, map[string]any{"visitor_token": "cvt_nope"}); r.code != 404 {
		t.Errorf("read with an unknown token: got %d want 404", r.code)
	}
	if r := publicCall(t, "POST", srv.URL+"/api/chat/"+keyA+"/read", originA, map[string]any{"visitor_token": tok}); r.code != 200 {
		t.Fatalf("read: %d %s", r.code, r.raw)
	}
	thread = publicCall(t, "GET", srv.URL+"/api/chat/"+keyA+"/messages?visitor_token="+tok, originA, nil)
	if thread.body["unread"].(float64) != 0 {
		t.Errorf("unread after read: %v", thread.body["unread"])
	}

	// Closing tells the stream too.
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"status": "closed"}, 200)
	select {
	case l := <-lines:
		if !strings.Contains(l, `"kind":"status"`) {
			t.Errorf("close event: %s", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the visitor's stream heard nothing when the conversation closed")
	}
}
