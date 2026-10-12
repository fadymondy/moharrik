package server

/*
TestThreadPagesToItsFirstMessage — MH-471, through the real stack.

A thread used to be read with LIMIT 500 in ascending order: past 500 messages
it showed the FIRST 500 and silently dropped the newest, including the one that
had just arrived, in the member's inbox and in the visitor's widget alike.

A 600-message thread must now open on its newest page and state its total.
Following the cursor must reach the first message, with every message exactly
once and in order. The visitor's widget must show the newest message.

MH-648 shrank the opening page from 100 to 30 (every channel loads older on
scroll-up now, so there is no reason to ship more than a screenful up front)
and capped GET .../messages at 100 (down from 500) since that endpoint is the
human-scroll path, not the internal "read the whole recent thread" one.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ThreadPages -v
*/

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestThreadPagesToItsFirstMessage(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "thread-paging-secret-thread-paging-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("SNOOZE_WAKER", "off")
	defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "thread-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Thread " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	const origin = "https://shop.test"
	key := obj(alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "Site", "allowed_origins": []string{origin}}, 201)["widget"])["public_key"].(string)
	tok := visitorWrites(t, srv.URL, key, origin, map[string]any{"body": "message 000", "name": "Long Talker", "email": "long@shop.test"})["visitor_token"].(string)
	convID := obj(arr(alice.must("GET", "/api/w/conversations", nil, 200)["conversations"])[0])["id"].(string)

	// 599 more, a second apart, alternating sides. Written directly: the public
	// route is rate-limited, and the order is what is under test. Two share a
	// timestamp on purpose, so the cursor's id tiebreak is exercised.
	var first time.Time
	if err := a.SysDB.QueryRow(`SELECT created_at FROM messages WHERE conversation_id = $1`, convID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 600; i++ {
		dir := "in"
		if i%2 == 1 {
			dir = "out"
		}
		at := first.Add(time.Duration(i) * time.Second)
		if i == 300 {
			at = first.Add(299 * time.Second)
		}
		if _, err := a.SysDB.Exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, $3, $4, $5)`,
			wsID, convID, dir, fmt.Sprintf("message %03d", i), at); err != nil {
			t.Fatal(err)
		}
	}

	type msg struct {
		ID, Body  string
		CreatedAt time.Time `json:"created_at"`
	}
	var detail struct {
		Messages           []msg  `json:"messages"`
		MessagesTotal      int64  `json:"messages_total"`
		MessagesNextCursor string `json:"messages_next_cursor"`
	}
	_, b := alice.do("GET", "/api/w/conversations/"+convID, nil)
	if err := json.Unmarshal(b, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.MessagesTotal != 600 || len(detail.Messages) != 30 || detail.MessagesNextCursor == "" {
		t.Fatalf("opening: total %d, %d messages, cursor %q", detail.MessagesTotal, len(detail.Messages), detail.MessagesNextCursor)
	}
	if detail.Messages[29].Body != "message 599" || detail.Messages[0].Body != "message 570" {
		t.Fatalf("opening shows %s .. %s, want the newest page, 570 .. 599", detail.Messages[0].Body, detail.Messages[29].Body)
	}

	// Walk back to the first message.
	all := append([]msg{}, detail.Messages...)
	cursor, pages := detail.MessagesNextCursor, 1
	for cursor != "" {
		var page struct {
			Messages   []msg  `json:"messages"`
			Total      int64  `json:"total"`
			NextCursor string `json:"next_cursor"`
		}
		code, b := alice.do("GET", "/api/w/conversations/"+convID+"/messages?limit=100&cursor="+url.QueryEscape(cursor), nil)
		if code != 200 {
			t.Fatalf("page %d: %d %s", pages+1, code, b)
		}
		_ = json.Unmarshal(b, &page)
		if page.Total != 600 {
			t.Fatalf("page %d says the thread has %d", pages+1, page.Total)
		}
		all = append(page.Messages, all...)
		cursor, pages = page.NextCursor, pages+1
		if pages > 20 {
			t.Fatal("the cursor never ran out")
		}
	}
	if len(all) != 600 || pages != 7 {
		t.Fatalf("walked %d messages in %d pages, want 600 in 7 (30 opening + 100 x 5 + 70)", len(all), pages)
	}
	seen := map[string]bool{}
	for i, m := range all {
		if seen[m.ID] {
			t.Fatalf("message %s came back twice", m.Body)
		}
		seen[m.ID] = true
		if i > 0 && m.CreatedAt.Before(all[i-1].CreatedAt) {
			t.Fatalf("%s is out of order after %s", m.Body, all[i-1].Body)
		}
	}
	if all[0].Body != "message 000" {
		t.Fatalf("the walk ends at %q, not the first message", all[0].Body)
	}

	// Refused, not guessed: a cursor this server never issued, a page too big.
	for _, q := range []string{"cursor=not-a-cursor", "limit=101", "limit=-1"} {
		if code, b := alice.do("GET", "/api/w/conversations/"+convID+"/messages?"+q, nil); code != http.StatusUnprocessableEntity {
			t.Errorf("?%s: %d %s, want 422", q, code, b)
		}
	}
	if code, _ := alice.do("GET", "/api/w/conversations/00000000-0000-0000-0000-000000000000/messages", nil); code != http.StatusNotFound {
		t.Errorf("an unknown conversation's messages: %d, want 404", code)
	}

	// The visitor's widget shows the newest message, not the first 500.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/chat/"+key+"/messages?visitor_token="+url.QueryEscape(tok), nil)
	req.Header.Set("Origin", origin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var poll struct{ Messages []msg }
	_ = json.Unmarshal(raw, &poll)
	if res.StatusCode != 200 || len(poll.Messages) == 0 || poll.Messages[len(poll.Messages)-1].Body != "message 599" {
		t.Fatalf("the widget's thread: %d, %d messages", res.StatusCode, len(poll.Messages))
	}
}
