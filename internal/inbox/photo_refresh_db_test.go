package inbox

/*
MH-602 defect 1 (fetch on list/first-show, not only on inbound) and defect 2
(never inline a data: URI into JSON), against a real Postgres database with
RLS on. Evolution is always a fake httptest server here — never the real
thing, per the card's rule.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run PhotoRefresh -v
	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run ListConversationsJSON -v
*/

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/whatsapp"
)

var photoRefreshTinyPNG, _ = base64.StdEncoding.DecodeString(tinyPNGB64)

// fakeEvolutionCountingHits is fakeEvolutionWithPhoto's twin (internal/
// whatsapp/avatar_test.go), with a hit counter so the dedupe tests below can
// assert "exactly once" rather than just "it happened".
func fakeEvolutionCountingHits(t *testing.T) (srv *httptest.Server, hits *int32) {
	t.Helper()
	var n int32
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/fetchProfilePictureUrl/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"profilePictureUrl":"` + srv.URL + `/img/photo.png"}`)) //nolint:errcheck
	})
	mux.HandleFunc("/img/photo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(photoRefreshTinyPNG) //nolint:errcheck
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &n
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := cond()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the background photo fetch to land")
}

// TestListConversationsQueuesExactlyOneFetchPerRowAcrossTwoListCalls is
// defect 1's dedupe requirement: an unfetched WhatsApp row gets ONE
// background fetch queued for it, even when the same page is listed twice in
// a row (a slow-scrolling tab, or two tabs open on the same inbox) — the
// in-process dedupe map (photoRefreshQueued in whatsapp.go), not luck.
func TestListConversationsQueuesExactlyOneFetchPerRowAcrossTwoListCalls(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	srv, hits := fakeEvolutionCountingHits(t)
	t.Setenv("EVOLUTION_API_URL", srv.URL)
	t.Setenv("EVOLUTION_API_KEY", "test-key")
	if _, err := whatsapp.Ensure(context.Background(), a, f.wsID, f.userID); err != nil {
		t.Fatal(err)
	}

	convID := f.conversation(t, "whatsapp", "open", false)
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE conversations SET customer_phone = $1 WHERE workspace_id = $2 AND id = $3`,
		"201234567890", f.wsID, convID); err != nil {
		t.Fatal(err)
	}

	if _, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50}); err != nil {
		t.Fatal(err)
	}
	if _, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50}); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, 5*time.Second, func() (bool, error) {
		var fetched bool
		err := a.SQLDB.QueryRowContext(f.ctx, `
			SELECT photo_fetched_at IS NOT NULL FROM conversations WHERE workspace_id = $1 AND id = $2`,
			f.wsID, convID).Scan(&fetched)
		return fetched, err
	})
	// A little longer, so a second (wrongly queued) goroutine would have had
	// time to land too before the assertion below.
	time.Sleep(300 * time.Millisecond)
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("expected exactly one Evolution fetch across two back-to-back list calls, got %d", got)
	}
}

// TestGetConversationTriggersAFetchForAPermalinkOpen covers the other half of
// defect 1: a conversation opened directly (GetConversation), never having
// appeared in a list response, still gets its photo queued.
func TestGetConversationTriggersAFetchForAPermalinkOpen(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	srv, hits := fakeEvolutionCountingHits(t)
	t.Setenv("EVOLUTION_API_URL", srv.URL)
	t.Setenv("EVOLUTION_API_KEY", "test-key")
	if _, err := whatsapp.Ensure(context.Background(), a, f.wsID, f.userID); err != nil {
		t.Fatal(err)
	}

	convID := f.conversation(t, "whatsapp", "open", false)
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE conversations SET customer_phone = $1 WHERE workspace_id = $2 AND id = $3`,
		"201234567891", f.wsID, convID); err != nil {
		t.Fatal(err)
	}

	if _, err := GetConversation(f.ctx, a, convID); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, 5*time.Second, func() (bool, error) {
		var fetched bool
		err := a.SQLDB.QueryRowContext(f.ctx, `
			SELECT photo_fetched_at IS NOT NULL FROM conversations WHERE workspace_id = $1 AND id = $2`,
			f.wsID, convID).Scan(&fetched)
		return fetched, err
	})
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Fatalf("expected exactly one Evolution fetch, got %d", got)
	}
}

// TestListConversationsJSONNeverContainsADataURI is defect 2's core promise:
// whatever the list scans off contacts.photo_url / conversations.photo_url,
// the marshalled response body must never carry the raw data: URI — only a
// signed /api/provider-photos/ link.
func TestListConversationsJSONNeverContainsADataURI(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)

	var contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, name, phone, source, photo_url, photo_source, photo_fetched_at, photo_fetch_status)
		VALUES ($1, 'Photo Contact', $2, 'manual', 'data:image/png;base64,xx', 'whatsapp', now(), 'ok') RETURNING id`,
		f.wsID, "20199901"+idSuffix()).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, contact_id, subject, last_message_at)
		VALUES ($1, 'whatsapp', $2, $3, 'hi', now()) RETURNING id`,
		f.wsID, "20199901", contactID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SQLDB.ExecContext(f.ctx, `INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ($1, $2, 'in', 'hi')`,
		f.wsID, convID); err != nil {
		t.Fatal(err)
	}

	var convID2 string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, subject, last_message_at, photo_url, photo_source, photo_fetched_at, photo_fetch_status)
		VALUES ($1, 'whatsapp', $2, 'hi', now(), 'data:image/png;base64,yy', 'whatsapp', now(), 'ok') RETURNING id`,
		f.wsID, "20199902"+idSuffix()).Scan(&convID2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SQLDB.ExecContext(f.ctx, `INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ($1, $2, 'in', 'hi')`,
		f.wsID, convID2); err != nil {
		t.Fatal(err)
	}

	res, err := ListConversations(f.ctx, a, ConversationFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "data:") {
		t.Errorf("the list JSON must never contain a data: URI, got %s", body)
	}
	// Both rows must still resolve to a usable, signed link.
	var contactRowURL, ownRowURL string
	for _, c := range res.Conversations {
		switch c.ID {
		case convID:
			contactRowURL = c.AvatarURL
		case convID2:
			ownRowURL = c.AvatarURL
		}
	}
	if !strings.HasPrefix(contactRowURL, "/api/provider-photos/contact/") {
		t.Errorf("the linked contact's row should get a signed provider-photo URL, got %q", contactRowURL)
	}
	if !strings.HasPrefix(ownRowURL, "/api/provider-photos/conversation/") {
		t.Errorf("the unlinked conversation's own photo should get a signed provider-photo URL, got %q", ownRowURL)
	}
}
