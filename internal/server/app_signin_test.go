package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/platform"
)

/*
Signing a Moharrik app in through the browser (docs/apps-plan.md §2), end to
end through the real guard: the signed-in web page asks for a one-time code
bound to the app's PKCE challenge, the app trades code + verifier for its
device token, and that token is an ordinary personal access token — it signs
in, registers the phone for push, survives an older "sign out everywhere",
dies with a newer one, and signs itself out.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run AppSignin -v
*/

func TestAppSignin(t *testing.T) {
	a, srv := bootAudit(t)
	web := newClient(t, srv)
	email := "app-" + rnd() + "@example.test"
	web.must("POST", "/api/auth/register", map[string]string{"email": email, "password": "correct-horse-9"}, 201)
	var userID string
	if err := a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	verifier := "v-" + rnd() + rnd() + rnd() + rnd() + rnd() // 52 chars
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := "http://127.0.0.1:53111/callback"
	start := func(redirect string) (int, map[string]any) {
		code, b := web.do("POST", "/api/me/app-signin", map[string]any{
			"redirect_uri": redirect, "code_challenge": challenge, "code_challenge_method": "S256",
			"state": "state-" + rnd() + rnd(), "device": "Fady's Pixel",
		})
		out := map[string]any{}
		_ = json.Unmarshal(b, &out)
		return code, out
	}

	for _, bad := range []string{"https://evil.example/callback", "http://127.0.0.1/callback", "moharrik://auth/other", "mahaam://auth/callback"} {
		if code, _ := start(bad); code != http.StatusBadRequest {
			t.Errorf("redirect %q: %d, want 400", bad, code)
		}
	}
	if code, _ := newClient(t, srv).do("POST", "/api/me/app-signin", map[string]any{}); code != http.StatusUnauthorized {
		t.Errorf("anonymous app-signin: %d, want 401", code)
	}

	code, out := start(redirect)
	if code != http.StatusOK {
		t.Fatalf("app-signin: %d %v", code, out)
	}
	dest, err := url.Parse(out["redirect_to"].(string))
	if err != nil || dest.Host != "127.0.0.1:53111" || dest.Query().Get("code") == "" {
		t.Fatalf("redirect_to: %v", out["redirect_to"])
	}
	oneTime := dest.Query().Get("code")

	app := newClient(t, srv)
	trade := func(code, verifier string) (int, map[string]any) {
		c := newClient(t, srv)
		st, b := c.do("POST", "/api/auth/app/token", map[string]any{"code": code, "code_verifier": verifier, "redirect_uri": redirect})
		out := map[string]any{}
		_ = json.Unmarshal(b, &out)
		return st, out
	}
	if st, _ := trade(oneTime, verifier+"-wrong"); st != http.StatusBadRequest {
		t.Errorf("a wrong verifier: %d, want 400", st)
	}
	st, tok := trade(oneTime, verifier)
	if st != http.StatusOK || tok["email"] != email {
		t.Fatalf("token: %d %v", st, tok)
	}
	if st, _ := trade(oneTime, verifier); st != http.StatusBadRequest {
		t.Errorf("a used code traded again: %d, want 400", st)
	}
	app.bearer = tok["token"].(string)

	app.must("GET", "/api/me/workspaces", nil, 200)

	t.Run("an older sign-out-everywhere leaves the app signed in", func(t *testing.T) {
		if _, err := a.SysDB.Exec(`INSERT INTO platform_users (user_id, sessions_valid_after) VALUES ($1, now() - interval '1 hour')
			ON CONFLICT (user_id) DO UPDATE SET sessions_valid_after = EXCLUDED.sessions_valid_after`, userID); err != nil {
			t.Fatal(err)
		}
		platform.Invalidate(userID)
		app.must("GET", "/api/me/workspaces", nil, 200)
	})

	devID := ""
	t.Run("the phone registers for push, and knows itself", func(t *testing.T) {
		res := app.must("POST", "/api/me/devices", map[string]any{
			"platform": "android", "token": "fcm-" + rnd() + rnd() + rnd(), "name": "Pixel 9", "app_version": "1.0.0", "locale": "ar",
		}, 200)
		d := res["device"].(map[string]any)
		devID = d["id"].(string)
		if d["current"] != true || d["locale"] != "ar" {
			t.Errorf("device: %v", d)
		}
		list := app.must("GET", "/api/me/devices", nil, 200)["devices"].([]any)
		if len(list) != 1 {
			t.Errorf("devices: %v", list)
		}
		// Someone else's device id is not found, not removed.
		other := newClient(t, srv)
		other.must("POST", "/api/auth/register", map[string]string{"email": "other-" + rnd() + "@example.test", "password": "correct-horse-8"}, 201)
		if code, _ := other.do("DELETE", "/api/me/devices/"+devID, nil); code != http.StatusNotFound {
			t.Errorf("another user removing my device: %d, want 404", code)
		}
	})

	t.Run("a second install with the same name leaves the first signed in", func(t *testing.T) {
		// Same device name ("Fady's Pixel"): iOS names every phone "iPhone".
		_, out := start(redirect)
		dest, _ := url.Parse(out["redirect_to"].(string))
		st, tok := trade(dest.Query().Get("code"), verifier)
		if st != http.StatusOK {
			t.Fatalf("second token: %d %v", st, tok)
		}
		second := newClient(t, srv)
		second.bearer = tok["token"].(string)
		second.must("GET", "/api/me/workspaces", nil, 200)
		app.must("GET", "/api/me/workspaces", nil, 200)
		if _, b := second.do("POST", "/api/me/app-signout", nil); b == nil {
			t.Fatal("second signout")
		}
		app.must("GET", "/api/me/workspaces", nil, 200)
	})

	t.Run("signing the app out revokes its token and its push", func(t *testing.T) {
		if code, b := app.do("POST", "/api/me/app-signout", nil); code != http.StatusNoContent {
			t.Fatalf("signout: %d %s", code, b)
		}
		if code, _ := app.do("GET", "/api/me/workspaces", nil); code != http.StatusUnauthorized {
			t.Errorf("after signout: %d, want 401", code)
		}
		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM device_tokens WHERE id = $1`, devID).Scan(&n)
		if n != 0 {
			t.Error("the phone's push registration outlived the app's sign-out")
		}
	})

	t.Run("a newer sign-out-everywhere ends a fresh app token", func(t *testing.T) {
		_, out := start(redirect)
		dest, _ := url.Parse(out["redirect_to"].(string))
		_, tok := trade(dest.Query().Get("code"), verifier)
		fresh := newClient(t, srv)
		fresh.bearer = tok["token"].(string)
		fresh.must("GET", "/api/me/workspaces", nil, 200)
		// Second resolution: the PAT's created_at is RFC 3339 seconds.
		time.Sleep(1100 * time.Millisecond)
		if _, err := a.SysDB.Exec(`UPDATE platform_users SET sessions_valid_after = now() WHERE user_id = $1`, userID); err != nil {
			t.Fatal(err)
		}
		platform.Invalidate(userID)
		if code, _ := fresh.do("GET", "/api/me/workspaces", nil); code == http.StatusOK {
			t.Error("an app token issued before sign-out-everywhere still works")
		}
	})
}
