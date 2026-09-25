package inbox

/*
MH-588: email's photo source, Gravatar — against a FAKE Gravatar server only.
Covers the priority rule the task brief calls out explicitly: Gravatar is
"only when nothing else exists", so it must never overwrite a row that
already carries ANY other photo_source.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run Gravatar -v
*/

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeGravatar(t *testing.T, found bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/avatar/", func(w http.ResponseWriter, r *http.Request) {
		if !found {
			// d=404 is the real Gravatar behaviour this fetcher relies on to
			// tell "no gravatar" apart from a genuine failure.
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(photoRefreshTinyPNG) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestGravatarURLIsTheDocumentedMD5Scheme(t *testing.T) {
	defer SetGravatarBase("https://fake.example.test")()
	got := gravatarURL(" Fady@Example.com ")
	// md5("fady@example.com") — lowercased and trimmed, per Gravatar's own spec.
	want := "https://fake.example.test/avatar/25231a73218a1cbcd84862ca8db45792?s=256&d=404"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestQueueGravatarRefreshCachesAPhotoOnAnUnlinkedConversation(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	fake := fakeGravatar(t, true)
	defer SetGravatarBase(fake.URL)()

	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_email, subject, last_message_at)
		VALUES ($1, 'email', $2, 'hi', now()) RETURNING id`,
		f.wsID, "gr1-"+idSuffix()+"@example.test").Scan(&convID); err != nil {
		t.Fatal(err)
	}

	queueGravatarRefresh(a, f.wsID, "conversation", convID, "gr1-x@example.test")

	waitUntil(t, 5*time.Second, func() (bool, error) {
		var fetched bool
		err := a.SQLDB.QueryRowContext(f.ctx, `SELECT photo_fetched_at IS NOT NULL FROM conversations WHERE workspace_id = $1 AND id = $2`,
			f.wsID, convID).Scan(&fetched)
		return fetched, err
	})

	var url, source, status string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT coalesce(photo_url,''), coalesce(photo_source,''), coalesce(photo_fetch_status,'') FROM conversations WHERE workspace_id = $1 AND id = $2`,
		f.wsID, convID).Scan(&url, &source, &status); err != nil {
		t.Fatal(err)
	}
	if source != "gravatar" || status != "ok" {
		t.Fatalf("source=%q status=%q, want gravatar/ok", source, status)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("photo_url = %q, want a data: URI", url)
	}
}

func TestQueueGravatarRefreshA404IsCachedAsOkNotFailed(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	fake := fakeGravatar(t, false)
	defer SetGravatarBase(fake.URL)()

	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_email, subject, last_message_at)
		VALUES ($1, 'email', $2, 'hi', now()) RETURNING id`,
		f.wsID, "gr2-"+idSuffix()+"@example.test").Scan(&convID); err != nil {
		t.Fatal(err)
	}

	queueGravatarRefresh(a, f.wsID, "conversation", convID, "gr2-x@example.test")

	waitUntil(t, 5*time.Second, func() (bool, error) {
		var fetched bool
		err := a.SQLDB.QueryRowContext(f.ctx, `SELECT photo_fetched_at IS NOT NULL FROM conversations WHERE workspace_id = $1 AND id = $2`,
			f.wsID, convID).Scan(&fetched)
		return fetched, err
	})

	var url, status string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT coalesce(photo_url,''), coalesce(photo_fetch_status,'') FROM conversations WHERE workspace_id = $1 AND id = $2`,
		f.wsID, convID).Scan(&url, &status); err != nil {
		t.Fatal(err)
	}
	if status != "ok" {
		t.Fatalf("a confirmed 'no gravatar' (d=404) must cache status 'ok', got %q", status)
	}
	if url != "" {
		t.Fatalf("expected no photo, got %q", url)
	}
}

// TestQueueGravatarRefreshNeverOverwritesAnExistingSource is the priority
// rule from the task brief: Gravatar only ever fills a row that has no photo
// source yet — a row that already has one (whatsapp, telegram, an upload...)
// must never be touched, even if queueGravatarRefresh is asked to.
func TestQueueGravatarRefreshNeverOverwritesAnExistingSource(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)
	fake := fakeGravatar(t, true)
	defer SetGravatarBase(fake.URL)()

	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_email, subject, last_message_at, photo_url, photo_source, photo_fetched_at, photo_fetch_status)
		VALUES ($1, 'email', $2, 'hi', now(), 'data:image/png;base64,already', 'whatsapp', now() - interval '10 days', 'ok') RETURNING id`,
		f.wsID, "gr3-"+idSuffix()+"@example.test").Scan(&convID); err != nil {
		t.Fatal(err)
	}

	queueGravatarRefresh(a, f.wsID, "conversation", convID, "gr3-x@example.test")
	// queueGravatarRefresh is fire-and-forget; give the goroutine time to run
	// (or, correctly, decide not to) before asserting nothing changed.
	time.Sleep(300 * time.Millisecond)

	var url, source string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT coalesce(photo_url,''), coalesce(photo_source,'') FROM conversations WHERE workspace_id = $1 AND id = $2`,
		f.wsID, convID).Scan(&url, &source); err != nil {
		t.Fatal(err)
	}
	if source != "whatsapp" || url != "data:image/png;base64,already" {
		t.Fatalf("Gravatar must never overwrite an existing photo_source, got source=%q url=%q", source, url)
	}
}
