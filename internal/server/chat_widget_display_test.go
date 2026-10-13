package server

/*
TestChatWidgetDisplayName — who the visitor is talking to, per widget: the
public config names the workspace until a widget sets its own display name and
picture, the REST PATCH sets and resets them (an empty string falls back to the
workspace again), a picture that is not an https:// link is refused, and the
list returns what was saved.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ChatWidgetDisplayName -v
*/

import (
	"net/http/httptest"
	"testing"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestChatWidgetDisplayName(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "chat-display-test-secret-chat-display-test-secret-00")
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

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "disp-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsName := "3x1 " + rnd()
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": wsName}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	const origin = "https://me.display.test"
	wgt := obj(alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "CMS", "allowed_origins": []string{origin}}, 201)["widget"])
	if wgt["display_name"] != "" || wgt["display_avatar_url"] != "" {
		t.Fatalf("a new widget has no display name or picture: %v", wgt)
	}
	id, key := wgt["id"].(string), wgt["public_key"].(string)
	cfgURL := srv.URL + "/api/chat/" + key + "/config"
	patchURL := "/api/w/chat_widgets/" + id

	// Unset: the workspace's name.
	if cfg := publicCall(t, "GET", cfgURL, origin, nil); cfg.code != 200 || cfg.body["name"] != wsName {
		t.Fatalf("config before: %d %s", cfg.code, cfg.raw)
	}

	// Set: the widget's own name and picture.
	const pic = "https://me.display.test/photo.jpg"
	got := obj(alice.must("PATCH", patchURL, map[string]any{"display_name": "  Fady Mondy ", "display_avatar_url": pic}, 200)["widget"])
	if got["display_name"] != "Fady Mondy" || got["display_avatar_url"] != pic {
		t.Fatalf("patch: %v", got)
	}
	cfg := publicCall(t, "GET", cfgURL, origin, nil)
	if cfg.code != 200 || cfg.body["name"] != "Fady Mondy" || cfg.body["avatar_url"] != pic {
		t.Fatalf("config after: %d %s", cfg.code, cfg.raw)
	}

	// A PATCH that leaves them out keeps them.
	alice.must("PATCH", patchURL, map[string]any{"title": "Chat with me"}, 200)
	if cfg := publicCall(t, "GET", cfgURL, origin, nil); cfg.body["name"] != "Fady Mondy" || cfg.body["title"] != "Chat with me" {
		t.Fatalf("config after an unrelated patch: %s", cfg.raw)
	}

	// Only an https:// link is a picture.
	for _, bad := range []string{"http://me.display.test/p.jpg", "javascript:alert(1)", "https://", "https://a b.test/p.jpg", "https://u:p@me.display.test/p.jpg", "/photo.jpg"} {
		if code, body := alice.do("PATCH", patchURL, map[string]any{"display_avatar_url": bad}); code != 422 {
			t.Errorf("avatar %q: got %d want 422: %s", bad, code, body)
		}
	}
	long := make([]rune, 81)
	for i := range long {
		long[i] = 'a'
	}
	if code, body := alice.do("PATCH", patchURL, map[string]any{"display_name": string(long)}); code != 422 {
		t.Errorf("an 81-character display name: got %d want 422: %s", code, body)
	}

	// The list carries them.
	list := alice.must("GET", "/api/w/chat_widgets", nil, 200)
	widgets, _ := list["widgets"].([]any)
	if len(widgets) != 1 || obj(widgets[0])["display_name"] != "Fady Mondy" {
		t.Fatalf("list: %v", list)
	}

	// Empty resets to the workspace.
	alice.must("PATCH", patchURL, map[string]any{"display_name": "", "display_avatar_url": ""}, 200)
	cfg = publicCall(t, "GET", cfgURL, origin, nil)
	if cfg.body["name"] != wsName {
		t.Fatalf("config after reset: %s", cfg.raw)
	}
	if _, ok := cfg.body["avatar_url"]; ok {
		t.Errorf("reset, yet the config names a picture: %s", cfg.raw)
	}
}
