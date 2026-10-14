package server

/*
The apps' account cycle (internal/account, internal/appsignin) end to end through the real guard:
signing up and in with email and password from an app, refreshing the device token, confirming
an address and resetting a forgotten password by the mailed links, and the 401 that sends an app
back to sign-in.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run AuthCycle -v
*/

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/account"
)

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

func TestAuthCycle(t *testing.T) {
	box := &mailbox{}
	deliver, configured := account.Deliver, account.MailConfigured
	account.Deliver = func(_ context.Context, m account.Message) error {
		box.mu.Lock()
		box.sent = append(box.sent, m)
		box.mu.Unlock()
		return nil
	}
	account.MailConfigured = func() bool { return true }
	t.Cleanup(func() { account.Deliver, account.MailConfigured = deliver, configured })

	a, srv := bootAudit(t)
	anon := newClient(t, srv)
	verified := func(email string) bool {
		var v bool
		if err := a.SysDB.QueryRow(`
			SELECT p.email_verified_at IS NOT NULL FROM users u LEFT JOIN platform_users p ON p.user_id = u.id
			WHERE lower(u.email) = $1`, email).Scan(&v); err != nil {
			t.Fatalf("user %s: %v", email, err)
		}
		return v
	}
	// Passwords are made per run: never a literal credential in the source.
	pass, newPass := "ch-"+rnd()+"-1", "nh-"+rnd()+"-2"

	t.Run("password_reset is advertised when mail works", func(t *testing.T) {
		_, b := anon.do("GET", "/api/auth/methods", nil)
		if !strings.Contains(string(b), `"password_reset"`) {
			t.Errorf("methods: %s", b)
		}
	})

	email := "native-" + rnd() + "@example.test"
	var sess map[string]any
	t.Run("sign up, sign in and refresh from an app", func(t *testing.T) {
		if code, _ := anon.do("POST", "/api/auth/app/signup", map[string]string{"email": email, "password": "short"}); code != 422 {
			t.Errorf("a short password: %d, want 422", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signup", map[string]string{"email": "not-an-address", "password": pass}); code != 422 {
			t.Errorf("a bad address: %d, want 422", code)
		}
		sess = anon.must("POST", "/api/auth/app/signup", map[string]string{"email": email, "password": pass, "device": "iPhone"}, 201)
		if sess["token"] == "" || !strings.HasPrefix(sess["refresh_token"].(string), "mrt_") || sess["expires_at"] == nil {
			t.Fatalf("session: %v", sess)
		}
		if verified(email) {
			t.Error("a new address is verified before its link is opened")
		}
		if code, _ := anon.do("POST", "/api/auth/app/signup", map[string]string{"email": strings.ToUpper(email), "password": pass}); code != 409 {
			t.Errorf("the same address again: %d, want 409", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": pass + "x"}); code != 401 {
			t.Errorf("a wrong password: %d, want 401", code)
		}
		in := anon.must("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": pass}, 200)
		app := newClient(t, srv)
		app.bearer = in["token"].(string)
		app.must("GET", "/api/me/workspaces", nil, 200)

		next := anon.must("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": in["refresh_token"].(string)}, 200)
		if next["token"] == in["token"] || next["refresh_token"] == in["refresh_token"] {
			t.Error("refresh returned the same pair")
		}
		renewed := newClient(t, srv)
		renewed.bearer = next["token"].(string)
		renewed.must("GET", "/api/me/workspaces", nil, 200)
		if code, _ := anon.do("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": in["refresh_token"].(string)}); code != 401 {
			t.Errorf("a refresh token used twice: %d, want 401", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": "mrt_" + rnd()}); code != 401 {
			t.Errorf("an unknown refresh token: %d, want 401", code)
		}

		// A device token revoked by a path that knows nothing of refresh tokens (the auth
		// plugin's token list) takes its refresh token down with it.
		other := anon.must("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": pass}, 200)
		if _, err := a.SysDB.Exec(`DELETE FROM personal_access_tokens WHERE token_hash = encode(sha256(convert_to($1, 'UTF8')), 'hex')`,
			other["token"].(string)); err != nil {
			t.Fatal(err)
		}
		if code, _ := anon.do("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": other["refresh_token"].(string)}); code != 401 {
			t.Errorf("a refresh token whose device token was revoked: %d, want 401", code)
		}
	})

	t.Run("confirm the address", func(t *testing.T) {
		app := newClient(t, srv)
		app.bearer = sess["token"].(string)
		tok := box.link(t, email, "/verify-email")
		anon.must("POST", "/api/auth/email/verify", map[string]string{"token": tok}, 200)
		if code, _ := anon.do("POST", "/api/auth/email/verify", map[string]string{"token": tok}); code != 400 {
			t.Errorf("a used link: %d, want 400", code)
		}
		if !verified(email) {
			t.Error("not verified after the link")
		}
		if code, _ := app.do("POST", "/api/me/email/verification", nil); code != 409 {
			t.Errorf("resend when verified: %d, want 409", code)
		}
	})

	t.Run("the web's register mails the link too", func(t *testing.T) {
		web := "web-" + rnd() + "@example.test"
		newClient(t, srv).must("POST", "/api/auth/register", map[string]string{"email": web, "password": pass}, 201)
		box.link(t, web, "/verify-email")
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
		refresh := sess["refresh_token"].(string)
		anon.must("POST", "/api/auth/password/reset", map[string]string{"token": tok, "password": newPass}, 204)
		if code, _ := anon.do("POST", "/api/auth/password/reset", map[string]string{"token": tok, "password": newPass + "3"}); code != 400 {
			t.Errorf("a used link: %d, want 400", code)
		}
		// Signed out everywhere: the app's token and its refresh token are both dead (the 401 the
		// apps answer by going back to sign-in).
		old := newClient(t, srv)
		old.bearer = sess["token"].(string)
		if code, _ := old.do("GET", "/api/me/workspaces", nil); code != 401 {
			t.Errorf("an app token after a reset: %d, want 401", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/refresh", map[string]string{"refresh_token": refresh}); code != 401 {
			t.Errorf("a refresh token after a reset: %d, want 401", code)
		}
		if code, _ := anon.do("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": pass}); code != 401 {
			t.Errorf("the old password: %d, want 401", code)
		}
		anon.must("POST", "/api/auth/app/signin", map[string]string{"email": email, "password": newPass}, 200)
	})
}
