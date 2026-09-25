package server

/*
TestInboxUnreadFirstPages — MH-592: the inbox list asks for ten at a time with
order=unread_first. Every unread conversation comes before every read one, each
group newest first, and following next_cursor reaches every conversation
exactly once — including across the page where unread gives way to read, and
across two conversations that share a timestamp.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run InboxUnreadFirst -v
*/

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestInboxUnreadFirstPages(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "unread-first-secret-unread-first-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("SNOOZE_WAKER", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "unreadfirst-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Unread " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	// 25 threads a minute apart; every third is unread, so unread ones are
	// scattered through time rather than already on top. Two share a time.
	base := time.Now().Add(-2 * time.Hour).UTC()
	unreadWant := 0
	for i := 0; i < 25; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		if i == 13 {
			at = base.Add(12 * time.Minute)
		}
		unread := i%3 == 0
		if unread {
			unreadWant++
		}
		if _, err := a.SysDB.Exec(`INSERT INTO conversations (workspace_id, channel, customer_name, last_message_at, unread) VALUES ($1, 'email', $2, $3, $4)`,
			wsID, fmt.Sprintf("c%02d", i), at, unread); err != nil {
			t.Fatal(err)
		}
	}

	type conv struct {
		ID            string    `json:"id"`
		Unread        bool      `json:"unread"`
		LastMessageAt time.Time `json:"last_message_at"`
	}
	var all []conv
	cursor, pages := "", 0
	for {
		path := "/api/w/conversations?limit=10&order=unread_first"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		code, b := alice.do("GET", path, nil)
		if code != 200 {
			t.Fatalf("page %d: %d %s", pages+1, code, b)
		}
		var page struct {
			Conversations []conv `json:"conversations"`
			NextCursor    string `json:"next_cursor"`
		}
		_ = json.Unmarshal(b, &page)
		if len(page.Conversations) > 10 {
			t.Fatalf("page %d has %d conversations, want at most 10", pages+1, len(page.Conversations))
		}
		all = append(all, page.Conversations...)
		cursor, pages = page.NextCursor, pages+1
		if cursor == "" || pages > 5 {
			break
		}
	}
	if pages != 3 || len(all) != 25 {
		t.Fatalf("%d pages, %d conversations; want 3 pages holding all 25", pages, len(all))
	}
	seen := map[string]bool{}
	for i, c := range all {
		if seen[c.ID] {
			t.Fatalf("%s listed twice", c.ID)
		}
		seen[c.ID] = true
		if (i < unreadWant) != c.Unread {
			t.Fatalf("position %d unread=%v; want all %d unread first", i, c.Unread, unreadWant)
		}
		if i > 0 && all[i-1].Unread == c.Unread && c.LastMessageAt.After(all[i-1].LastMessageAt) {
			t.Fatalf("position %d is newer than the one before it", i)
		}
	}

	// A recent-order cursor is not an unread_first cursor: refused, not misread.
	_, b := alice.do("GET", "/api/w/conversations?limit=10", nil)
	var recent struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(b, &recent)
	if code, _ := alice.do("GET", "/api/w/conversations?limit=10&order=unread_first&cursor="+url.QueryEscape(recent.NextCursor), nil); code != 422 {
		t.Fatalf("a recent cursor under unread_first answered %d, want 422", code)
	}
}
