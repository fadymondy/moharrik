package server

/*
TestAlertStreamAudience — who hears what on /api/w/events (MH-378).

Boots the real stack against a throwaway database, gives one workspace an
owner, a member and a client, opens each one's event stream, and then has a
visitor write through the public chat widget and a webhook deliver an item —
the two things that must reach a member the moment they happen.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run AlertStream -v
*/

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/testdb"
)

type streamEvent struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id"`
	ItemID         string `json:"item_id"`
	Direction      string `json:"direction"`
	Sender         string `json:"sender"`
	Snippet        string `json:"snippet"`
	Title          string `json:"title"`
	Alert          bool   `json:"alert"`
}

// openStream subscribes c to its workspace's events and returns them on a
// channel. It returns once the server has said ": open", i.e. once the
// subscription is registered — anything published after that is delivered.
func openStream(t *testing.T, c *client) <-chan streamEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/w/events", nil)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Workspace-ID", c.ws)
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("GET /api/w/events: %d", res.StatusCode)
	}
	out := make(chan streamEvent, 32)
	opened := make(chan struct{})
	go func() {
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		once := false
		for sc.Scan() {
			line := sc.Text()
			if !once && strings.HasPrefix(line, ": open") {
				once = true
				close(opened)
				continue
			}
			if strings.HasPrefix(line, "data:") {
				var ev streamEvent
				if json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &ev) == nil {
					out <- ev
				}
			}
		}
	}()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream never opened")
	}
	return out
}

// next waits for the first event of a kind, skipping others.
func next(t *testing.T, ch <-chan streamEvent, kind, who string) streamEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == kind {
				return ev
			}
		case <-deadline:
			t.Fatalf("%s: no %q event arrived", who, kind)
			return streamEvent{}
		}
	}
}

func nothing(t *testing.T, ch <-chan streamEvent, who string) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("%s received %+v — it must receive nothing", who, ev)
	case <-time.After(400 * time.Millisecond):
	}
}

func drain(ch <-chan streamEvent) {
	for {
		select {
		case <-ch:
		case <-time.After(250 * time.Millisecond):
			return
		}
	}
}

func TestAlertStreamAudience(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "alert-stream-secret-alert-stream-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	// Push is not what this test is about (internal/alerts tests it); with no
	// keys nothing leaves the process.
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")

	a := Boot()
	t.Cleanup(func() { a.Kernel.Close() })
	srv := httptest.NewServer(a.Kernel.Handler())
	// A Cleanup, not a defer: srv.Close waits for every open request, and the
	// streams are only cancelled by their own Cleanups — which run before this
	// one (LIFO), but after any defer. As a defer it hung the whole package.
	t.Cleanup(srv.Close)

	// ---- one workspace: an owner, a member, a client ------------------------
	alice, bob, carol := newClient(t, srv.URL), newClient(t, srv.URL), newClient(t, srv.URL)
	emails := map[*client]string{}
	for _, c := range []*client{alice, bob, carol} {
		emails[c] = "alert-" + rnd() + "@example.test"
		c.must("POST", "/api/auth/register", map[string]string{"email": emails[c], "password": "correct-horse-9"}, 201)
	}
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Alerts " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID
	join := func(c *client, role string) {
		inv := alice.must("POST", "/api/w/invitations", map[string]string{"email": emails[c], "role": role}, 201)
		token := strings.TrimPrefix(inv["accept_url"].(string), "/invite/")
		c.must("POST", "/api/invitations/accept", map[string]string{"token": token}, 200)
		c.ws = wsID
	}
	join(bob, "member")
	join(carol, "client")
	var bobID string
	if err := a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, emails[bob]).Scan(&bobID); err != nil {
		t.Fatal(err)
	}

	// A visitor has no session: the stream is not theirs to open at all.
	anon := newClient(t, srv.URL)
	anon.ws = wsID
	if code, _ := anon.do("GET", "/api/w/events", nil); code != http.StatusUnauthorized {
		t.Fatalf("a visitor opening the event stream: got %d, want 401", code)
	}

	aliceEv, bobEv, carolEv := openStream(t, alice), openStream(t, bob), openStream(t, carol)

	// ---- a visitor writes through the public widget --------------------------
	const origin = "https://shop.alerts.test"
	key := alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "Site", "allowed_origins": []string{origin}}, 201)["widget"].(map[string]any)["public_key"].(string)
	visit := func(body, token string) string {
		t.Helper()
		payload := map[string]any{"body": body, "name": "Rana", "email": "rana@shop.alerts.test"}
		if token != "" {
			payload["visitor_token"] = token
		}
		b, _ := json.Marshal(payload)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/chat/"+key+"/messages", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("visitor message: %d", res.StatusCode)
		}
		var out struct {
			VisitorToken string `json:"visitor_token"`
		}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return out.VisitorToken
	}
	token := visit("Is the blue one in stock?", "")

	for who, ch := range map[string]<-chan streamEvent{"the owner": aliceEv, "a member": bobEv} {
		ev := next(t, ch, "message", who)
		if ev.Direction != "in" || ev.Sender != "Rana" || ev.Snippet != "Is the blue one in stock?" || !ev.Alert || ev.ConversationID == "" {
			t.Errorf("%s should be alerted with the sender and snippet: %+v", who, ev)
		}
	}
	nothing(t, carolEv, "a client-role member")
	convID := ""
	if err := a.SysDB.QueryRow(`SELECT id FROM conversations WHERE workspace_id = $1 AND visitor_token = $2`, wsID, token).Scan(&convID); err != nil {
		t.Fatal(err)
	}

	// ---- assigned: only the assignee is alerted -------------------------------
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"assignee_user_id": bobID}, 200)
	drain(aliceEv)
	drain(bobEv)
	visit("Hello?", token)
	if ev := next(t, bobEv, "message", "the assignee"); !ev.Alert {
		t.Errorf("the assignee must be alerted: %+v", ev)
	}
	if ev := next(t, aliceEv, "message", "the owner"); ev.Alert || ev.ConversationID != convID {
		t.Errorf("the owner must see the badge move but not be alerted: %+v", ev)
	}
	nothing(t, carolEv, "a client-role member")

	// ---- a member's own reply never alerts them -------------------------------
	drain(aliceEv)
	drain(bobEv)
	bob.must("POST", "/api/w/conversations/"+convID+"/reply", map[string]any{"body": "Yes, it is."}, 201)
	if ev := next(t, bobEv, "message", "the author"); ev.Alert || ev.Direction != "out" {
		t.Errorf("a member's own reply must not alert them: %+v", ev)
	}
	if ev := next(t, aliceEv, "message", "the owner"); ev.Alert {
		t.Errorf("a reply alerts nobody: %+v", ev)
	}

	// ---- a webhook delivery reaches the notification readers live -------------
	drain(aliceEv)
	drain(bobEv)
	created := alice.must("POST", "/api/w/webhook_sources", map[string]any{"name": "CI"}, 201)
	srcID := created["source"].(map[string]any)["id"].(string)
	secret := created["secret"].(string)
	raw := []byte(`{"title":"Deploy failed on main"}`)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(stamp + "."))
	mac.Write(raw)
	hreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/hooks/"+srcID, bytes.NewReader(raw))
	hreq.Header.Set("X-Webhook-Timestamp", stamp)
	hreq.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	hres, err := http.DefaultClient.Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	hres.Body.Close()
	if hres.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook delivery: %d", hres.StatusCode)
	}
	for who, ch := range map[string]<-chan streamEvent{"the owner": aliceEv, "a member": bobEv} {
		ev := next(t, ch, "notification", who)
		if ev.ItemID == "" || ev.Title != "Deploy failed on main" || !ev.Alert {
			t.Errorf("%s should be alerted about the item: %+v", who, ev)
		}
	}
	nothing(t, carolEv, "a client-role member")

	// The bell agrees with the stream, and says nothing to the client.
	bell := bob.must("GET", "/api/w/alerts", nil, 200)
	if bell["unread_notifications"].(float64) < 1 || len(bell["items"].([]any)) < 2 {
		t.Errorf("the member's bell should list the conversation and the item: %v", bell)
	}
	cbell := carol.must("GET", "/api/w/alerts", nil, 200)
	if cbell["can_inbox"].(bool) || cbell["can_notifications"].(bool) || len(cbell["items"].([]any)) != 0 {
		t.Errorf("a client's bell must be empty: %v", cbell)
	}
	if code, _ := carol.do("GET", "/api/w/push", nil); code != http.StatusForbidden {
		t.Errorf("a client has no Delivery settings: got %d, want 403", code)
	}
}
