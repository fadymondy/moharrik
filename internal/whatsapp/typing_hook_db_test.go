package whatsapp

// MH-660: a presence.update webhook delivery reaches TypingSink with the right
// (workspaceID, phone, state) — against a real Postgres database (RLS on),
// same pattern as the other *_db_test.go files in this package. Nothing here
// talks to a real Evolution server or a real phone number: the webhook is
// posted locally with a synthetic token, exactly as Evolution itself would.
//
//	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/whatsapp/ -run PresenceWebhook -v

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/tenant"
)

func TestPresenceWebhookReachesTypingSink(t *testing.T) {
	a := testApp(t)
	suffix := idSuffix(t)
	var wsID string
	if err := a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('WA Typing Test', $1, 'test') RETURNING id`,
		"wa-typ-"+suffix).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	ctx := tenant.WithWorkspace(context.Background(), wsID)
	l, err := Ensure(ctx, a, wsID, "test-user")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	var mu sync.Mutex
	var got []string
	SetTypingSink(func(_ context.Context, workspaceID, phone, state string) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, workspaceID+"|"+phone+"|"+state)
		return nil
	})
	t.Cleanup(func() { SetTypingSink(nil) })

	r := chi.NewRouter()
	RegisterHook(r, a)
	srv := httptest.NewServer(r)
	defer srv.Close()

	body := `{"event":"presence.update","instance":"` + l.Instance + `","data":` +
		`{"id":"201234567890@s.whatsapp.net","presences":{"201234567890@s.whatsapp.net":{"lastKnownPresence":"composing"}}}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/whatsapp/hook/"+l.HookID, strings.NewReader(body))
	req.Header.Set(TokenHeader, l.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("webhook returned %d", res.StatusCode)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != wsID+"|201234567890|composing" {
		t.Fatalf("got %v, want [%q]", got, wsID+"|201234567890|composing")
	}
}
