package server

/*
TestPublicChatLimits — the public chat endpoint can no longer be flooded (MH-377).

Boots the real stack (guard, CORS, the client-address middleware, RLS pool)
against a throwaway database and drives POST /api/chat/{key}/messages the way a
browser — or a script — would. Addresses are supplied as X-Forwarded-For from
the loopback peer with TRUSTED_PROXIES=127.0.0.1, i.e. exactly what a trusted
proxy in front would send; that an UNtrusted peer's header is ignored is
internal/clientip's own test.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run PublicChat -v
*/

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/testdb"
)

// fakeClock runs the real in-memory limiter on a clock the test moves, so
// "a minute later" does not take a minute.
type fakeClock struct {
	inner inbox.Limiter
	mu    sync.Mutex
	at    time.Time
}

func (f *fakeClock) Take(_ time.Time, rules ...inbox.Rule) (bool, time.Duration) {
	f.mu.Lock()
	at := f.at
	f.mu.Unlock()
	return f.inner.Take(at, rules...)
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	f.at = f.at.Add(d)
	f.mu.Unlock()
}

type chatReply struct {
	code       int
	retryAfter string
	token      string
	raw        string
}

type chatPost struct {
	token, ip, pageURL, locale string
	headers                    map[string]string
}

func postChat(t *testing.T, base, key, origin string, p chatPost) chatReply {
	t.Helper()
	body := map[string]any{"body": "hello " + rnd()}
	if p.token != "" {
		body["visitor_token"] = p.token
	} else {
		body["name"], body["email"] = "Visitor", "visitor@chat-limit.test"
	}
	if p.pageURL != "" {
		body["page_url"] = p.pageURL
	}
	if p.locale != "" {
		body["locale"] = p.locale
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/chat/"+key+"/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if p.ip != "" {
		req.Header.Set("X-Forwarded-For", p.ip)
	}
	for k, v := range p.headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out struct {
		VisitorToken string `json:"visitor_token"`
	}
	_ = json.Unmarshal(raw, &out)
	return chatReply{code: res.StatusCode, retryAfter: res.Header.Get("Retry-After"), token: out.VisitorToken, raw: string(raw)}
}

func expect(t *testing.T, r chatReply, code int, what string) chatReply {
	t.Helper()
	if r.code != code {
		t.Fatalf("%s: got %d want %d: %s", what, r.code, code, r.raw)
	}
	return r
}

// retryWithin asserts a 429 carries a Retry-After of whole seconds in (0, max].
func retryWithin(t *testing.T, r chatReply, max int) int {
	t.Helper()
	n, err := strconv.Atoi(r.retryAfter)
	if err != nil || n < 1 || n > max {
		t.Fatalf("Retry-After = %q, want whole seconds in [1,%d]", r.retryAfter, max)
	}
	return n
}

func TestPublicChatLimits(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "chat-limit-test-secret-chat-limit-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	// The test client is the loopback "proxy"; the addresses it forwards are
	// the visitors.
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1,::1")
	t.Setenv("CLIENT_IP_HEADER", "")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	// Two tenants, a widget each.
	alice, bob := newClient(t, srv.URL), newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "chat-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "chat-b-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	aID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Chat A " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	bID := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Chat B " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws, bob.ws = aID, bID

	const originA, originB = "https://shop-a.test", "https://shop-b.test"
	newWidget := func(c *client, origin string) string {
		w := c.must("POST", "/api/w/chat_widgets", map[string]any{"name": "W " + rnd(), "allowed_origins": []string{origin}}, 201)
		return w["widget"].(map[string]any)["public_key"].(string)
	}
	keyB := newWidget(bob, originB)

	messagesIn := func(token string) int {
		t.Helper()
		var n int
		if err := a.SysDB.QueryRow(`
			SELECT count(*) FROM messages m JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
			WHERE c.visitor_token = $1`, token).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	t.Run("a burst from one visitor is refused with 429 and Retry-After", func(t *testing.T) {
		defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()
		key := newWidget(alice, originA)

		tok := expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.1"}), 201, "open").token
		for i := 1; i <= 12; i++ {
			expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.1", token: tok}), 201, fmt.Sprintf("message %d", i))
		}
		r := expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.1", token: tok}), 429, "the 13th message in a minute")
		retryWithin(t, r, 60)

		// Refused means refused: nothing was written for it.
		if n := messagesIn(tok); n != 13 {
			t.Fatalf("%d messages stored, want 13 (the open + 12); the refused one must not be written", n)
		}
	})

	t.Run("rotating visitor tokens is still capped by address", func(t *testing.T) {
		defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()
		key := newWidget(alice, originA)
		const ip = "198.51.100.2"

		// Dropping the token starts a new conversation each time: five, then no.
		var tokens []string
		for i := 1; i <= 5; i++ {
			tokens = append(tokens, expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: ip}), 201, fmt.Sprintf("open %d", i)).token)
		}
		r := expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: ip}), 429, "a 6th new conversation from one address")
		retryWithin(t, r, 600)

		// Spreading messages over those five tokens keeps each far under its own
		// limit (4 apiece), but the address has 20 a minute in total.
		for i := 0; i < 15; i++ {
			expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: ip, token: tokens[i%5]}), 201, fmt.Sprintf("rotated message %d", i+1))
		}
		r = expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: ip, token: tokens[0]}), 429, "the 21st message from one address")
		retryWithin(t, r, 60)

		// Per address, not per widget: someone else on the same site is fine.
		expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.3"}), 201, "a different visitor")
	})

	t.Run("rotating addresses is capped by the widget, and only that widget", func(t *testing.T) {
		defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()
		key := newWidget(alice, originA)

		for i := 1; i <= 30; i++ {
			expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: fmt.Sprintf("203.0.113.%d", i)}), 201, fmt.Sprintf("open from address %d", i))
		}
		r := expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "203.0.113.200"}), 429, "a 31st new conversation on the widget from a fresh address")
		retryWithin(t, r, 600)

		// IPv6 rotation inside one /64 is one address.
		expect(t, postChat(t, srv.URL, keyB, originB, chatPost{ip: "2001:db8:1:2::1"}), 201, "v6 open 1")
		for i := 2; i <= 5; i++ {
			expect(t, postChat(t, srv.URL, keyB, originB, chatPost{ip: fmt.Sprintf("2001:db8:1:2::%x", i)}), 201, fmt.Sprintf("v6 open %d", i))
		}
		expect(t, postChat(t, srv.URL, keyB, originB, chatPost{ip: "2001:db8:1:2:dead::1"}), 429, "a 6th open from the same /64")

		// Workspace B's widget has its own ceiling: A's flood does not touch it.
		expect(t, postChat(t, srv.URL, keyB, originB, chatPost{ip: "203.0.113.201"}), 201, "the other workspace's widget")
	})

	t.Run("the widget message ceiling holds across addresses and tokens", func(t *testing.T) {
		limits := inbox.DefaultChatLimits()
		limits.WidgetMessages = inbox.Rate{Limit: 10, Window: time.Minute}
		defer inbox.SetLimiter(inbox.NewMemoryLimiter(), limits)()
		key := newWidget(alice, originA)

		for i := 1; i <= 10; i++ {
			expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: fmt.Sprintf("192.0.2.%d", i)}), 201, fmt.Sprintf("message %d", i))
		}
		retryWithin(t, expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "192.0.2.99"}), 429, "past the widget ceiling"), 60)
		expect(t, postChat(t, srv.URL, keyB, originB, chatPost{ip: "192.0.2.99"}), 201, "the other workspace's widget")
	})

	t.Run("a visitor at a normal pace is never refused, and a refused one recovers", func(t *testing.T) {
		clock := &fakeClock{inner: inbox.NewMemoryLimiter(), at: time.Now()}
		defer inbox.SetLimiter(clock, inbox.DefaultChatLimits())()
		key := newWidget(alice, originA)
		const office = "198.51.100.50" // two people behind one NAT

		one := expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: office}), 201, "visitor one opens").token
		two := expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: office}), 201, "visitor two opens").token
		// Ten minutes of conversation: each writes every 8 seconds, interleaved.
		for i := 0; i < 75; i++ {
			clock.advance(4 * time.Second)
			tok := one
			if i%2 == 1 {
				tok = two
			}
			expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: office, token: tok}), 201, fmt.Sprintf("paced message %d", i+1))
		}

		// Now a burst, then waiting out exactly what Retry-After said.
		var refused chatReply
		for i := 0; i < 20; i++ {
			if refused = postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.51", token: one}); refused.code == 429 {
				break
			}
		}
		if refused.code != 429 {
			t.Fatal("a burst was never refused")
		}
		clock.advance(time.Duration(retryWithin(t, refused, 60)) * time.Second)
		expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.51", token: one}), 201, "after waiting Retry-After")
	})

	t.Run("the page, browser and language are recorded when the conversation opens", func(t *testing.T) {
		defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()
		key := newWidget(alice, originA)
		const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 Safari/605.1.15"

		tok := expect(t, postChat(t, srv.URL, key, originA, chatPost{
			ip: "198.51.100.60", pageURL: originA + "/pricing?plan=pro#faq",
			headers: map[string]string{"User-Agent": ua, "Accept-Language": "ar-EG,ar;q=0.9,en;q=0.5"},
		}), 201, "open with context").token

		conv := func(token string) map[string]any {
			t.Helper()
			var id string
			if err := a.SysDB.QueryRow(`SELECT id FROM conversations WHERE visitor_token = $1`, token).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return alice.must("GET", "/api/w/conversations/"+id, nil, 200)["conversation"].(map[string]any)
		}
		c := conv(tok)
		if c["page_url"] != originA+"/pricing?plan=pro" || c["user_agent"] != ua || c["locale"] != "ar-EG" {
			t.Fatalf("context not stored as sent: page_url=%v user_agent=%v locale=%v", c["page_url"], c["user_agent"], c["locale"])
		}

		// A page on some OTHER site is not believed — it would be a link an
		// operator clicks — and the Referer on the allowed origin is used.
		tok2 := expect(t, postChat(t, srv.URL, key, originA, chatPost{
			ip: "198.51.100.61", pageURL: "https://evil.test/login", locale: "en",
			headers: map[string]string{"Referer": originA + "/", "User-Agent": ua},
		}), 201, "open with a foreign page").token
		c2 := conv(tok2)
		if c2["page_url"] != originA+"/" || c2["locale"] != "en" {
			t.Fatalf("foreign page_url handling: page_url=%v locale=%v", c2["page_url"], c2["locale"])
		}

		// Continuing the thread does not rewrite where it started.
		expect(t, postChat(t, srv.URL, key, originA, chatPost{ip: "198.51.100.60", token: tok, pageURL: originA + "/checkout"}), 201, "continue")
		if c := conv(tok); c["page_url"] != originA+"/pricing?plan=pro" {
			t.Fatalf("a later message moved page_url to %v", c["page_url"])
		}

		// The context is workspace data like the rest: bob cannot read it.
		var id string
		_ = a.SysDB.QueryRow(`SELECT id FROM conversations WHERE visitor_token = $1`, tok).Scan(&id)
		bob.ws = aID
		if code, b := bob.do("GET", "/api/w/conversations/"+id, nil); code != 404 {
			t.Fatalf("bob read A's conversation: %d %s", code, b)
		}
		bob.ws = bID
		if code, b := bob.do("GET", "/api/w/conversations/"+id, nil); code != 404 || bytes.Contains(b, []byte("pricing")) {
			t.Fatalf("bob read A's conversation through B: %d %s", code, b)
		}
	})
}
