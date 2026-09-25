package server

/*
TestWhatsAppLinkRetry — MH-529/530: linking a number survives an Evolution
that is still starting the instance (the create fails, the first connect
answers 500) by asking once more, and a link that does fail answers 424 with
the reason rather than a bare 500. A stand-in Evolution; nothing is linked.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run WhatsAppLinkRetry -v
*/

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestWhatsAppLinkRetry(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "wa-link-retry-secret-wa-link-retry-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")

	// connectFails is how many connects answer 500 before one answers a QR;
	// garbled makes every connect answer something that is not JSON.
	var connects, connectFails atomic.Int32
	var garbled atomic.Bool
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/instance/create":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"response":{"message":"instance is starting"}}`))
		case strings.HasPrefix(r.URL.Path, "/instance/connect/"):
			n := connects.Add(1)
			if garbled.Load() {
				_, _ = w.Write([]byte(`<html>bad gateway</html>`))
				return
			}
			if n <= connectFails.Load() {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"response":{"message":"not ready"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":"2@QR","base64":"data:image/png;base64,iVBOR"}`))
		case strings.HasPrefix(r.URL.Path, "/webhook/set/"):
			_, _ = w.Write([]byte(`{"enabled":true}`))
		case strings.HasPrefix(r.URL.Path, "/instance/connectionState/"):
			_, _ = w.Write([]byte(`{"instance":{"state":"connecting"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer evo.Close()
	t.Setenv("EVOLUTION_API_URL", evo.URL)
	t.Setenv("EVOLUTION_API_KEY", "evo-test-key")
	t.Setenv("WHATSAPP_HOOK_BASE", "https://app.example.test")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "walink-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	alice.ws = alice.must("POST", "/api/me/workspaces", map[string]string{"name": "WA " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)

	// Create fails, the first connect fails: the second connect is the QR.
	connectFails.Store(1)
	got := alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)
	if got["qr_base64"] == nil && got["qr_code"] == nil {
		t.Fatalf("no QR after the retry: %v", got)
	}
	if n := connects.Load(); n != 2 {
		t.Fatalf("connects = %d, want 2 (one retry)", n)
	}

	// An answer that is not JSON is the WhatsApp server's failure: 424, named.
	garbled.Store(true)
	code, body := alice.do("POST", "/api/w/whatsapp/link", map[string]any{})
	if code != http.StatusFailedDependency || !strings.Contains(string(body), "not readable") {
		t.Fatalf("garbled answer: %d %s, want 424 naming it", code, body)
	}
}

// A 5xx report says why, with a phone number masked (MH-529/530).
func TestProblemDetail(t *testing.T) {
	got := problemDetail([]byte(`{"title":"Bad Gateway","status":502,"detail":"the WhatsApp server answered 400: 201012345678 is not\non WhatsApp"}`))
	if got != "\n\nDetail: the WhatsApp server answered 400: <number> is not on WhatsApp" {
		t.Fatalf("problemDetail = %q", got)
	}
	if problemDetail([]byte("not json")) != "" || problemDetail(nil) != "" {
		t.Fatal("no detail should read as nothing")
	}
}
