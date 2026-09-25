package server

/*
TestProviderPhotoIsReachableWithoutASession is the guard-tier regression for
MH-602 defect 2's download endpoint. The endpoint's OWN handler
(internal/crm/avatar.go's RegisterProviderPhotos) verifies the signed
ws/exp/sig query params and needs no session — that is the whole point, since
a plain <img src> in the inbox carries no cookies or Authorization header of
its own, exactly like the pre-existing /api/contact-avatars/ endpoint.

But the request never reaches that handler unless guard.go's classify() also
puts the path in tierPublic: this app's guard is a single default-deny
middleware in front of EVERY /api/ route (see the doc comment atop guard.go),
so a route can have a perfectly correct signature check and still 401 before
its handler runs, simply because nobody told the guard the route is meant to
be public. That is exactly what shipped first here — caught by the manual
browser check in the MH-602 card, not by internal/crm's endpoint tests, which
call RegisterProviderPhotos directly on a bare humatest API and never go
through this guard at all.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ProviderPhoto -v
*/

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestProviderPhotoIsReachableWithoutASession(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "provider-photo-guard-secret-provider-photo-guard-0123")
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
	alice.must("POST", "/api/auth/register", map[string]string{"email": "ppg-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Provider Photo Guard " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	contact := alice.must("POST", "/api/w/contacts", map[string]any{"name": "Has A Provider Photo", "phone": "20155501" + rnd()[:6]}, 201)
	contactID, _ := contact["id"].(string)
	if contactID == "" {
		if c, ok := contact["contact"].(map[string]any); ok {
			contactID, _ = c["id"].(string)
		}
	}
	if contactID == "" {
		t.Fatalf("could not find the new contact's id in %v", contact)
	}
	// A data: URI written directly (WhatsApp-cached shape), same as the
	// inbound photo-cache path — the CREATE endpoint above never accepts one,
	// so this mirrors how a real one actually lands on the row.
	if _, err := a.SysDB.Exec(`UPDATE contacts SET photo_url = $1 WHERE workspace_id = $2 AND id = $3`,
		"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=",
		wsID, contactID); err != nil {
		t.Fatal(err)
	}

	body := alice.must("GET", "/api/w/contacts/"+contactID, nil, 200)
	rawBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	// json.Marshal HTML-escapes "&" to & by default (this is a re-marshal
	// of the already-decoded body, purely to search it with a regexp) — undo
	// that here, or the exp/sig query params after the first & get truncated
	// and the signature check below fails for a reason that has nothing to do
	// with the guard fix this test is actually proving.
	raw := strings.ReplaceAll(string(rawBytes), "\\u0026", "&")
	m := regexp.MustCompile(`/api/provider-photos/contact/[^"\\]+`).FindString(raw)
	if m == "" {
		t.Fatalf("expected a signed /api/provider-photos/contact/... URL in the contact response, got %s", raw)
	}

	// The point of this test: fetch that URL with NO cookies and NO
	// Authorization header at all — a bare <img src> request, the same as the
	// browser sends for /api/contact-avatars/.
	resp, err := http.Get(srv.URL + m)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("an unauthenticated request to a validly signed provider-photo URL must succeed (200), got %d: %s", resp.StatusCode, b)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected Content-Type image/png, got %q", ct)
	}
}
