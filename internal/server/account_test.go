package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/moharrik/internal/account"
)

/*
The account cycle (internal/account) end to end through the real guard:
signing up and in from an app, refreshing, confirming an address, resetting a
forgotten password, Sign in with Google (web and app) and Sign in with Apple
(the iOS sheet) against a fake provider, the linking rules, and deleting the
account.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run Account -v
*/

// fakeProvider stands in for Google and Apple: one RSA key signs every ID
// token, /token answers with whatever the test queued for a code, and /revoke
// records what it was asked to revoke.
type fakeProvider struct {
	url string
	key *rsa.PrivateKey

	mu      sync.Mutex
	codes   map[string]map[string]any // code -> id token claims (+ "refresh_token")
	revoked []string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProvider{key: key, codes: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		e := big.NewInt(int64(key.E)).Bytes()
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "test", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		c, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		if !ok || r.Form.Get("client_secret") == "" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		out := map[string]any{"id_token": f.sign(t, c)}
		if rt, ok := c["refresh_token"]; ok {
			out["refresh_token"] = rt
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		f.mu.Unlock()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeProvider) sign(t *testing.T, c map[string]any) string {
	mc := jwt.MapClaims{"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix()}
	for k, v := range c {
		if k != "refresh_token" {
			mc[k] = v
		}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, mc)
	tok.Header["kid"] = "test"
	s, err := tok.SignedString(f.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *fakeProvider) queue(code string, c map[string]any) {
	f.mu.Lock()
	f.codes[code] = c
	f.mu.Unlock()
}

// mailbox captures account mail.
type mailbox struct {
	mu   sync.Mutex
	sent []account.Message
}

func (m *mailbox) link(t *testing.T, to, path string) string {
	t.Helper()
	re := regexp.MustCompile(regexp.QuoteMeta(path) + `\?token=([0-9a-f]+)`)
	for i := 0; i < 50; i++ { // the web sign-up's mail goes out off the request
		m.mu.Lock()
		for j := len(m.sent) - 1; j >= 0; j-- {
			if m.sent[j].To == to {
				if g := re.FindStringSubmatch(m.sent[j].Text); g != nil {
					m.mu.Unlock()
					return g[1]
				}
			}
		}
		m.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no %s mail to %s", path, to)
	return ""
}

func (m *mailbox) count(to string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, s := range m.sent {
		if s.To == to {
			n++
		}
	}
	return n
}

func appleKeyPEM(t *testing.T) string {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func noFollow(c *client) *client {
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// get follows nothing and returns the status and Location.
func (c *client) location(path string) (int, *url.URL) {
	c.t.Helper()
	res, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	return res.StatusCode, loc
}

func TestAccount(t *testing.T) {
	fake := newFakeProvider(t)
	saved := account.Endpoints
	account.Endpoints.GoogleAuth, account.Endpoints.GoogleToken, account.Endpoints.GoogleKeys = fake.url+"/auth", fake.url+"/token", fake.url+"/keys"
	account.Endpoints.AppleAuth, account.Endpoints.AppleToken, account.Endpoints.AppleKeys = fake.url+"/auth", fake.url+"/token", fake.url+"/keys"
	account.Endpoints.Revoke = fake.url + "/revoke"
	box := &mailbox{}
	deliver, configured := account.Deliver, account.MailConfigured
	account.Deliver = func(_ context.Context, m account.Message) error {
		box.mu.Lock()
		box.sent = append(box.sent, m)
		box.mu.Unlock()
		return nil
	}
	account.MailConfigured = func() bool { return true }
	t.Cleanup(func() { account.Endpoints, account.Deliver, account.MailConfigured = saved, deliver, configured })

	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "google-web")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "google-secret")
	t.Setenv("OAUTH_APPLE_TEAM_ID", "TEAM123456")
	t.Setenv("OAUTH_APPLE_KEY_ID", "KEY1234567")
	t.Setenv("OAUTH_APPLE_PRIVATE_KEY", appleKeyPEM(t))
	t.Setenv("OAUTH_APPLE_SERVICES_ID", "com.moharrik.web")
	t.Setenv("OAUTH_APPLE_BUNDLE_ID", "com.fadymondy.moharrik")
	a, srv := bootAudit(t)
	anon := newClient(t, srv)

	userID := func(email string) string {
		var id string
		if err := a.SysDB.QueryRow(`SELECT id FROM users WHERE lower(email) = $1`, email).Scan(&id); err != nil {
			t.Fatalf("user %s: %v", email, err)
		}
		return id
	}

	t.Run("methods", func(t *testing.T) {
		m := anon.must("GET", "/api/auth/app/methods", nil, 200)
		for _, k := range []string{"password", "password_reset", "google", "apple", "apple_web"} {
			if m[k] != true {
				t.Errorf("%s: %v", k, m)
			}
		}
		_, b := anon.do("GET", "/api/auth/methods", nil)
		if !strings.Contains(string(b), "/api/auth/oauth/google") || !strings.Contains(string(b), "/api/auth/oauth/apple") {
			t.Errorf("web methods: %s", b)
		}
	})

	email := "native-" + rnd() + "@example.test"
	var sess map[string]any
	t.Run("sign up, sign in and refresh from an app", func(t *testing.T) {
		if code, _ := anon.do("POST", "/api/auth/app/signup", map[string]string{"email": email, "password": "short"}); code != 422 {
			t.Errorf("a short password: %d, want 422", code)
		}
		sess = anon.must("POST", "/api/auth/app/signup", map[string]string{"email": email, "password": "correct-horse-1", "device": "iPhone"}, 201)
		if sess["token"] == "" || sess["refresh_token"] == "" {
			t.Fatalf("session: %v", sess)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signup", map[string]string{"email": strings.ToUpper(email), "password": "correct-horse-1"}); code != 409 {
			t.Errorf("the same address again: %d, want 409", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": "wrong-horse-11"}); code != 401 {
			t.Errorf("a wrong password: %d, want 401", code)
		}
		in := anon.must("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": "correct-horse-1"}, 200)
		app := newClient(t, srv)
		app.bearer = in["token"].(string)
		app.must("GET", "/api/me/workspaces", nil, 200)

		next := anon.must("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": in["refresh_token"].(string)}, 200)
		if next["token"] == in["token"] {
			t.Error("refresh returned the same token")
		}
		if code, _ := anon.do("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": in["refresh_token"].(string)}); code == 200 {
			t.Error("a refresh token worked twice")
		}
	})

	t.Run("confirm the address", func(t *testing.T) {
		app := newClient(t, srv)
		app.bearer = sess["token"].(string)
		if acc := app.must("GET", "/api/me/account", nil, 200); acc["email_verified"] != false || acc["has_password"] != true {
			t.Errorf("before: %v", acc)
		}
		tok := box.link(t, email, "/verify-email")
		anon.must("POST", "/api/auth/email/verify", map[string]string{"token": tok}, 200)
		if code, _ := anon.do("POST", "/api/auth/email/verify", map[string]string{"token": tok}); code != 400 {
			t.Errorf("a used link: %d, want 400", code)
		}
		if acc := app.must("GET", "/api/me/account", nil, 200); acc["email_verified"] != true {
			t.Errorf("after: %v", acc)
		}
		if code, _ := app.do("POST", "/api/me/email/verification", nil); code != 409 {
			t.Errorf("resend when verified: %d, want 409", code)
		}
	})

	t.Run("forgot and reset the password", func(t *testing.T) {
		ghost := "nobody-" + rnd() + "@example.test"
		anon.must("POST", "/api/auth/password/forgot", map[string]string{"email": ghost}, 202)
		if box.count(ghost) != 0 {
			t.Error("mail went to an address with no account")
		}
		anon.must("POST", "/api/auth/password/forgot", map[string]string{"email": email}, 202)
		tok := box.link(t, email, "/reset-password")
		if code, _ := anon.do("POST", "/api/auth/password/reset", map[string]string{"token": tok, "password": "x"}); code != 422 {
			t.Errorf("a short new password: %d, want 422", code)
		}
		anon.must("POST", "/api/auth/password/reset", map[string]string{"token": tok, "password": "new-horse-2222"}, 204)
		if code, _ := anon.do("POST", "/api/auth/password/reset", map[string]string{"token": tok, "password": "new-horse-3333"}); code != 400 {
			t.Errorf("a used link: %d, want 400", code)
		}
		old := newClient(t, srv)
		old.bearer = sess["token"].(string)
		if code, _ := old.do("GET", "/api/me/workspaces", nil); code != 401 {
			t.Errorf("an app token after a reset: %d, want 401", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": "correct-horse-1"}); code != 401 {
			t.Errorf("the old password: %d, want 401", code)
		}
		sess = anon.must("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": "new-horse-2222"}, 200)
	})

	// googleWeb runs the browser half of Sign in with Google for c.
	googleWeb := func(c *client, query string, idClaims map[string]any) (int, *url.URL) {
		t.Helper()
		code, loc := c.location("/api/auth/oauth/google?" + query)
		if code != http.StatusFound || !strings.HasPrefix(loc.String(), fake.url+"/auth") {
			t.Fatalf("start: %d %v", code, loc)
		}
		q := loc.Query()
		idClaims["iss"], idClaims["aud"], idClaims["nonce"] = "https://accounts.google.com", "google-web", q.Get("nonce")
		gc := "gcode-" + rnd()
		fake.queue(gc, idClaims)
		return c.location("/api/auth/oauth/google/callback?" + url.Values{"code": {gc}, "state": {q.Get("state")}}.Encode())
	}

	t.Run("Google on the web: new account, lands on next", func(t *testing.T) {
		web := noFollow(newClient(t, srv))
		gEmail := "g-" + rnd() + "@example.test"
		code, loc := googleWeb(web, "next=/en/w/acme", map[string]any{"sub": "g-" + rnd(), "email": gEmail, "email_verified": true})
		if code != http.StatusSeeOther || loc.String() != "/en/w/acme" {
			t.Fatalf("callback: %d %v", code, loc)
		}
		acc := web.must("GET", "/api/me/account", nil, 200)
		if acc["email"] != gEmail || acc["email_verified"] != true || acc["has_password"] != false {
			t.Errorf("account: %v", acc)
		}
	})

	t.Run("Google: a replayed callback and an unverified email are refused", func(t *testing.T) {
		web := noFollow(newClient(t, srv))
		code, loc := googleWeb(web, "", map[string]any{"sub": "g-" + rnd(), "email": "u-" + rnd() + "@example.test", "email_verified": false})
		if code != http.StatusSeeOther || loc.Query().Get("oauth_error") != "email_unverified" {
			t.Errorf("unverified: %d %v", code, loc)
		}
		// A callback in a browser that did not start the sign-in.
		other := noFollow(newClient(t, srv))
		_, start := web.location("/api/auth/oauth/google")
		code, loc = other.location("/api/auth/oauth/google/callback?state=" + start.Query().Get("state") + "&code=x")
		if code != http.StatusSeeOther || loc.Query().Get("oauth_error") != "expired" {
			t.Errorf("another browser: %d %v", code, loc)
		}
	})

	t.Run("Google takes over an unverified squatter's account", func(t *testing.T) {
		victim := "victim-" + rnd() + "@example.test"
		squat := newClient(t, srv)
		squat.must("POST", "/api/auth/register", map[string]string{"email": victim, "password": "squatter-pass-1"}, 201)
		// Revocation is to the second (platform.SessionRevoked); a real squatter's session is older.
		time.Sleep(1100 * time.Millisecond)
		web := noFollow(newClient(t, srv))
		if code, loc := googleWeb(web, "", map[string]any{"sub": "g-" + rnd(), "email": victim, "email_verified": true}); code != http.StatusSeeOther || loc.Query().Get("oauth_error") != "" {
			t.Fatalf("callback: %d %v", code, loc)
		}
		if acc := web.must("GET", "/api/me/account", nil, 200); acc["has_password"] != false || len(acc["identities"].([]any)) != 1 {
			t.Errorf("account: %v", acc)
		}
		if code, _ := squat.do("GET", "/api/me/workspaces", nil); code != 401 {
			t.Errorf("the squatter's session: %d, want 401", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signin", map[string]string{"email": victim, "password": "squatter-pass-1"}); code != 401 {
			t.Errorf("the squatter's password: %d, want 401", code)
		}
	})

	t.Run("Google in an app: back to the app with a one-time code", func(t *testing.T) {
		verifier := "v-" + rnd() + rnd() + rnd() + rnd() + rnd()
		sum := sha256.Sum256([]byte(verifier))
		state := "state-" + rnd() + rnd()
		q := url.Values{"redirect_uri": {"moharrik://auth/callback"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
			"state": {state}, "device": {"Pixel"}}
		browser := noFollow(newClient(t, srv))
		code, loc := googleWeb(browser, q.Encode(), map[string]any{"sub": "g-" + rnd(), "email": "ga-" + rnd() + "@example.test", "email_verified": true})
		if code != http.StatusFound || loc.Scheme != "moharrik" || loc.Query().Get("state") != state || loc.Query().Get("code") == "" {
			t.Fatalf("callback: %d %v", code, loc)
		}
		tok := anon.must("POST", "/api/auth/app/token", map[string]string{"code": loc.Query().Get("code"), "code_verifier": verifier,
			"redirect_uri": "moharrik://auth/callback"}, 200)
		if tok["token"] == "" {
			t.Errorf("token: %v", tok)
		}
		// Not configured as an app redirect: refused before Google is asked.
		bad := q
		bad.Set("redirect_uri", "https://evil.example/cb")
		if code, _ := browser.location("/api/auth/oauth/google?" + bad.Encode()); code != http.StatusBadRequest {
			t.Errorf("a foreign redirect: %d, want 400", code)
		}
	})

	appleSub := "a-" + rnd()
	appleEmail := "relay-" + rnd() + "@privaterelay.appleid.com"
	var appleSess map[string]any
	t.Run("Sign in with Apple on iOS", func(t *testing.T) {
		raw := "nonce-" + rnd() + rnd()
		sum := sha256.Sum256([]byte(raw))
		hashed := hex.EncodeToString(sum[:])
		idc := map[string]any{"iss": "https://appleid.apple.com", "aud": "com.fadymondy.moharrik", "sub": appleSub,
			"email": appleEmail, "email_verified": "true", "nonce": hashed}
		identity := fake.sign(t, idc)
		fake.queue("acode-1", map[string]any{"iss": "https://appleid.apple.com", "aud": "com.fadymondy.moharrik", "sub": appleSub,
			"email": appleEmail, "email_verified": "true", "refresh_token": "apple-refresh-1"})
		if code, _ := anon.do("POST", "/api/auth/app/apple", map[string]string{"identity_token": identity, "authorization_code": "acode-1",
			"nonce": "another-nonce-0000"}); code != 401 {
			t.Errorf("a wrong nonce: %d, want 401", code)
		}
		fake.queue("acode-2", map[string]any{"iss": "https://appleid.apple.com", "aud": "com.fadymondy.moharrik", "sub": appleSub,
			"email": appleEmail, "email_verified": "true", "refresh_token": "apple-refresh-1"})
		appleSess = anon.must("POST", "/api/auth/app/apple", map[string]string{"identity_token": identity, "authorization_code": "acode-2",
			"nonce": raw, "device": "iPhone"}, 200)
		// Apple sends the email only the first time; the subject alone finds the account again.
		fake.queue("acode-3", map[string]any{"iss": "https://appleid.apple.com", "aud": "com.fadymondy.moharrik", "sub": appleSub})
		again := anon.must("POST", "/api/auth/app/apple", map[string]string{"identity_token": fake.sign(t, map[string]any{
			"iss": "https://appleid.apple.com", "aud": "com.fadymondy.moharrik", "sub": appleSub, "nonce": hashed}),
			"authorization_code": "acode-3", "nonce": raw}, 200)
		if again["email"] != appleEmail {
			t.Errorf("second sign-in: %v", again)
		}
	})

	t.Run("delete the account", func(t *testing.T) {
		app := newClient(t, srv)
		app.bearer = appleSess["token"].(string)
		me := userID(appleEmail)
		mine := app.must("POST", "/api/me/workspaces", map[string]string{"name": "Solo " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
		shared := app.must("POST", "/api/me/workspaces", map[string]string{"name": "Shared " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
		other := userID(email)
		if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'member')`, shared, other); err != nil {
			t.Fatal(err)
		}

		acc := app.must("GET", "/api/me/account", nil, 200)
		if b := acc["blocking_workspaces"].([]any); len(b) != 1 || b[0].(map[string]any)["id"] != shared {
			t.Fatalf("blocking: %v", acc)
		}
		if code, _ := app.do("POST", "/api/me/account/delete", map[string]string{"confirm": "delete"}); code != 409 {
			t.Errorf("sole owner: %d, want 409", code)
		}
		if code, _ := app.do("POST", "/api/me/account/delete", map[string]string{"confirm": "yes"}); code != 422 {
			t.Errorf("no confirmation: %d, want 422", code)
		}
		if _, err := a.SysDB.Exec(`UPDATE workspace_members SET role = 'owner' WHERE workspace_id = $1 AND user_id = $2`, shared, other); err != nil {
			t.Fatal(err)
		}
		app.must("POST", "/api/me/account/delete", map[string]string{"confirm": "delete"}, 204)

		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, me).Scan(&n)
		if n != 0 {
			t.Error("the users row survived")
		}
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM user_identities WHERE user_id = $1`, me).Scan(&n)
		if n != 0 {
			t.Error("the Apple identity survived")
		}
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM workspaces WHERE id = $1`, mine).Scan(&n)
		if n != 0 {
			t.Error("the workspace only they were in survived")
		}
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM workspaces WHERE id = $1`, shared).Scan(&n)
		if n != 1 {
			t.Error("the shared workspace went with them")
		}
		fake.mu.Lock()
		revoked := strings.Join(fake.revoked, ",")
		fake.mu.Unlock()
		if revoked != "apple-refresh-1" {
			t.Errorf("Apple revocations: %q", revoked)
		}
		if code, _ := app.do("GET", "/api/me/workspaces", nil); code != 401 {
			t.Errorf("the app after deletion: %d, want 401", code)
		}
		// The address is free again.
		anon.must("POST", "/api/auth/app/signup", map[string]string{"email": "again-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	})
}
