package server

/*
Sign in with Google and Apple (internal/social) against the real stack and a fake Google/Apple
(internal/social/socialtest): the web flows (Google's GET callback, Apple's cross-site form_post
and its same-site finish hop), the linking rules (subject first, verified e-mail, no silent
attach to a password account — the pre-account hijack), ?next= surviving the round trip (the
apps' /app-signin), the native endpoints, connected accounts, and deleting an account.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run Social -v
*/

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/social/socialtest"
)

const (
	testServicesID = "com.fadymondy.moharrik.web"
	testBundleID   = "com.fadymondy.moharrik"
	testGoogleWeb  = "google-web-client.apps.googleusercontent.com"
	testGoogleIOS  = "google-ios-client.apps.googleusercontent.com"
)

type socialStack struct {
	t    *testing.T
	a    *app.App
	url  string
	fake *socialtest.Fake
}

func bootSocial(t *testing.T) *socialStack {
	t.Helper()
	f := socialtest.New(t)
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", testGoogleWeb)
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "google-web-secret")
	t.Setenv("OAUTH_GOOGLE_AUDIENCES", testGoogleIOS)
	t.Setenv("APPLE_SERVICES_ID", testServicesID)
	t.Setenv("APPLE_TEAM_ID", "JW9HJH86GC")
	t.Setenv("APPLE_KEY_ID", "TESTKEY123")
	t.Setenv("APPLE_PRIVATE_KEY", f.AppleP8)
	t.Setenv("APPLE_BUNDLE_IDS", testBundleID)
	t.Setenv("OAUTH_AFTER_LOGIN", "/workspaces")
	a, base := bootAudit(t)
	return &socialStack{t: t, a: a, url: base, fake: f}
}

// browser is a cookie jar that does not follow redirects, so each hop can be checked.
type browser struct {
	t    *testing.T
	base string
	http *http.Client
}

func (s *socialStack) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: s.t, base: s.url, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (b *browser) get(path string) (int, string) {
	b.t.Helper()
	res, err := b.http.Get(b.base + path)
	if err != nil {
		b.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, res.Header.Get("Location")
}

func (b *browser) postForm(path string, form url.Values) (int, string) {
	b.t.Helper()
	res, err := b.http.PostForm(b.base+path, form)
	if err != nil {
		b.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode, res.Header.Get("Location")
}

// me is the signed-in e-mail ("" when not signed in).
func (b *browser) me() string {
	b.t.Helper()
	res, err := b.http.Get(b.base + "/api/auth/me")
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return ""
	}
	var out struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out.Email
}

// asClient is a JSON client sharing this browser's cookies (for CSRF-checked calls).
func (b *browser) asClient() *client {
	c := newClient(b.t, b.base)
	c.http.Jar = b.http.Jar
	return c
}

// start begins a web sign-in and returns the provider redirect's state and nonce.
func (b *browser) start(provider, query string) (state, nonce string) {
	b.t.Helper()
	code, loc := b.get("/api/auth/oauth/" + provider + query)
	if code != http.StatusFound {
		b.t.Fatalf("start %s: %d %s", provider, code, loc)
	}
	u, _ := url.Parse(loc)
	q := u.Query()
	if q.Get("state") == "" || q.Get("nonce") == "" {
		b.t.Fatalf("start %s redirected to %s", provider, loc)
	}
	return q.Get("state"), q.Get("nonce")
}

func googleClaims(sub, email string, verified bool, nonce string) jwt.MapClaims {
	return jwt.MapClaims{"iss": "https://accounts.google.com", "sub": sub, "email": email, "email_verified": verified, "nonce": nonce, "name": "G " + sub}
}

func appleClaims(sub, email, nonce string) jwt.MapClaims {
	return jwt.MapClaims{"iss": "https://appleid.apple.com", "sub": sub, "email": email, "email_verified": "true", "is_private_email": "false", "nonce": nonce}
}

// google runs a whole Google web sign-in and returns where the browser landed.
func (s *socialStack) google(b *browser, query, sub, email string, verified bool) string {
	s.t.Helper()
	state, nonce := b.start("google", query)
	code := s.fake.Code("google", googleClaims(sub, email, verified, nonce))
	st, loc := b.get("/api/auth/oauth/google/callback?" + url.Values{"state": {state}, "code": {code}}.Encode())
	if st != http.StatusFound {
		s.t.Fatalf("google callback: %d", st)
	}
	return loc
}

// apple runs a whole Apple web sign-in: the callback is posted WITHOUT the browser's cookies (a
// cross-site POST), then the browser follows to finish.
func (s *socialStack) apple(b *browser, query, sub, email, userJSON string) string {
	s.t.Helper()
	state, nonce := b.start("apple", query)
	code := s.fake.Code("apple", appleClaims(sub, email, nonce))
	crossSite := s.browser()
	st, loc := crossSite.postForm("/api/auth/oauth/apple/callback", url.Values{"state": {state}, "code": {code}, "user": {userJSON}})
	if st != http.StatusSeeOther || !strings.HasPrefix(loc, "/api/auth/oauth/apple/finish?t=") {
		s.t.Fatalf("apple callback: %d %s", st, loc)
	}
	st, landed := b.get(loc)
	if st != http.StatusFound {
		s.t.Fatalf("apple finish: %d", st)
	}
	return landed
}

func errorCode(loc string) string {
	u, _ := url.Parse(loc)
	return u.Query().Get("social_error")
}

func (s *socialStack) userID(email string) string {
	var id string
	_ = s.a.SysDB.QueryRow(`SELECT id FROM users WHERE lower(email) = lower($1)`, email).Scan(&id)
	return id
}

func (s *socialStack) identities(userID string) map[string]string {
	rows, err := s.a.SysDB.Query(`SELECT provider, subject FROM user_identities WHERE user_id = $1`, userID)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, sub string
		_ = rows.Scan(&p, &sub)
		out[p] = sub
	}
	return out
}

func TestSocialGoogleWeb(t *testing.T) {
	s := bootSocial(t)

	t.Run("a new person gets a passwordless account, linked by subject", func(t *testing.T) {
		b := s.browser()
		email := "g-new-" + rnd() + "@gmail.test"
		sub := "g-" + rnd()
		if loc := s.google(b, "", sub, email, true); loc != "/workspaces" {
			t.Errorf("landed on %q, want OAUTH_AFTER_LOGIN", loc)
		}
		if b.me() != email {
			t.Fatal("not signed in")
		}
		uid := s.userID(email)
		if s.identities(uid)["google"] != sub {
			t.Errorf("identity: %v", s.identities(uid))
		}
		// Next time, found by subject even though the address changed at Google.
		b2 := s.browser()
		s.google(b2, "", sub, "renamed-"+rnd()+"@gmail.test", true)
		if b2.me() != email {
			t.Errorf("the subject must sign in as the same account, got %q", b2.me())
		}
	})

	t.Run("next survives the round trip (the apps' /app-signin)", func(t *testing.T) {
		b := s.browser()
		next := "/ar/app-signin?redirect_uri=moharrik%3A%2F%2Fauth%2Fcallback&code_challenge=abc&state=xyz"
		if loc := s.google(b, "?next="+url.QueryEscape(next), "g-"+rnd(), "g-next-"+rnd()+"@gmail.test", true); loc != next {
			t.Errorf("landed on %q, want %q", loc, next)
		}
		if loc := s.google(s.browser(), "?next="+url.QueryEscape("//evil.example/x"), "g-"+rnd(), "g-evil-"+rnd()+"@gmail.test", true); loc != "/workspaces" {
			t.Errorf("an off-site next must be dropped, landed on %q", loc)
		}
	})

	t.Run("pre-account hijack: a password account with the address is not attached", func(t *testing.T) {
		victim := "victim-" + rnd() + "@gmail.test"
		attacker := newClient(t, s.url)
		attacker.must("POST", "/api/auth/register", map[string]string{"email": victim, "password": "attacker-pass-1"}, 201)
		b := s.browser()
		loc := s.google(b, "", "g-victim-"+rnd(), victim, true)
		if errorCode(loc) != "account_conflict" || !strings.HasPrefix(loc, "/login") {
			t.Errorf("landed on %q, want account_conflict on the login page", loc)
		}
		if b.me() != "" {
			t.Error("the Google sign-in was handed the password account")
		}
		if len(s.identities(s.userID(victim))) != 0 {
			t.Error("an identity was attached to the password account")
		}
	})

	t.Run("the owner signed in in the same browser links their password account", func(t *testing.T) {
		email := "owner-" + rnd() + "@gmail.test"
		b := s.browser()
		b.asClient().must("POST", "/api/auth/register", map[string]string{"email": email, "password": "owner-pass-12"}, 201)
		sub := "g-owner-" + rnd()
		if loc := s.google(b, "", sub, email, true); errorCode(loc) != "" {
			t.Fatalf("landed on %q", loc)
		}
		if s.identities(s.userID(email))["google"] != sub {
			t.Error("not linked")
		}
	})

	t.Run("an existing passwordless account (an earlier Google sign-in) is linked by verified e-mail", func(t *testing.T) {
		email := "legacy-" + rnd() + "@gmail.test"
		if _, err := s.a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, '!sso', $3)`,
			"legacy-"+rnd(), email, time.Now().UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
		b := s.browser()
		s.google(b, "", "g-legacy-"+rnd(), email, true)
		if b.me() != email {
			t.Error("the legacy Google user could not sign in")
		}
	})

	t.Run("an unverified address is refused", func(t *testing.T) {
		b := s.browser()
		if loc := s.google(b, "", "g-"+rnd(), "unverified-"+rnd()+"@gmail.test", false); errorCode(loc) != "email_unverified" {
			t.Errorf("landed on %q", loc)
		}
	})

	t.Run("state: single use, bound to the browser, and a foreign one is refused", func(t *testing.T) {
		b := s.browser()
		state, nonce := b.start("google", "")
		code := s.fake.Code("google", googleClaims("g-"+rnd(), "st-"+rnd()+"@gmail.test", true, nonce))
		// Another browser presenting the callback: no binding cookie.
		if _, loc := s.browser().get("/api/auth/oauth/google/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()); errorCode(loc) != "failed" {
			t.Errorf("a callback in another browser: %q", loc)
		}
		// The state was consumed by that attempt; the real browser cannot reuse it either.
		if _, loc := b.get("/api/auth/oauth/google/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()); errorCode(loc) != "failed" {
			t.Errorf("a reused state: %q", loc)
		}
		if b.me() != "" {
			t.Error("signed in from a consumed state")
		}
		// A nonce that is not the one this sign-in made.
		state, _ = b.start("google", "")
		code = s.fake.Code("google", googleClaims("g-"+rnd(), "st2-"+rnd()+"@gmail.test", true, "some-other-nonce"))
		if _, loc := b.get("/api/auth/oauth/google/callback?" + url.Values{"state": {state}, "code": {code}}.Encode()); errorCode(loc) != "failed" {
			t.Errorf("a wrong nonce: %q", loc)
		}
	})

	t.Run("the providers are advertised", func(t *testing.T) {
		_, body := newClient(t, s.url).do("GET", "/api/auth/methods", nil)
		for _, want := range []string{`"/api/auth/oauth/google"`, `"/api/auth/oauth/apple"`, `"/api/auth/native/apple"`} {
			if !strings.Contains(string(body), want) {
				t.Errorf("methods lack %s: %s", want, body)
			}
		}
	})
}

func TestSocialAppleWeb(t *testing.T) {
	s := bootSocial(t)

	t.Run("form_post callback, then the same-site finish sets the session; the name is kept", func(t *testing.T) {
		b := s.browser()
		email := "a-" + rnd() + "@privaterelay.appleid.com"
		sub := "001." + rnd()
		loc := s.apple(b, "?next=/en/w/acme", sub, email, `{"name":{"firstName":"Fady","lastName":"Mondy"}}`)
		if loc != "/en/w/acme" {
			t.Errorf("landed on %q", loc)
		}
		if b.me() != email {
			t.Fatal("not signed in")
		}
		var name, client, sealed string
		_ = s.a.SysDB.QueryRow(`SELECT display_name, client_id, refresh_token FROM user_identities WHERE provider = 'apple' AND subject = $1`, sub).Scan(&name, &client, &sealed)
		if name != "Fady Mondy" || client != testServicesID || sealed == "" || strings.HasPrefix(sealed, "rt-") {
			t.Errorf("identity: name=%q client=%q sealed=%q (must be sealed)", name, client, sealed)
		}
		// Apple sends the name only once; a later sign-in without it keeps it.
		s.apple(s.browser(), "", sub, email, "")
		_ = s.a.SysDB.QueryRow(`SELECT display_name FROM user_identities WHERE provider = 'apple' AND subject = $1`, sub).Scan(&name)
		if name != "Fady Mondy" {
			t.Errorf("the name was lost: %q", name)
		}
	})

	t.Run("a finish link opened in another browser does nothing, and does not burn the real one", func(t *testing.T) {
		b := s.browser()
		state, nonce := b.start("apple", "")
		code := s.fake.Code("apple", appleClaims("001."+rnd(), "af-"+rnd()+"@icloud.test", nonce))
		_, finish := s.browser().postForm("/api/auth/oauth/apple/callback", url.Values{"state": {state}, "code": {code}})
		other := s.browser()
		if _, loc := other.get(finish); errorCode(loc) != "failed" || other.me() != "" {
			t.Errorf("another browser finished the sign-in: %q", loc)
		}
		if _, loc := b.get(finish); errorCode(loc) != "" || b.me() == "" {
			t.Errorf("the real browser could not finish: %q", loc)
		}
		if _, loc := b.get(finish); errorCode(loc) != "failed" {
			t.Errorf("a finish ticket used twice: %q", loc)
		}
	})

	t.Run("a cancelled consent and an unknown state", func(t *testing.T) {
		b := s.browser()
		state, _ := b.start("apple", "")
		_, finish := s.browser().postForm("/api/auth/oauth/apple/callback", url.Values{"state": {state}, "error": {"user_cancelled_authorize"}})
		if _, loc := b.get(finish); errorCode(loc) != "cancelled" {
			t.Errorf("cancelled: %q", loc)
		}
		if st, loc := s.browser().postForm("/api/auth/oauth/apple/callback", url.Values{"state": {"forged"}, "code": {"x"}}); st != http.StatusSeeOther || errorCode(loc) != "failed" {
			t.Errorf("a forged state: %d %q", st, loc)
		}
	})

	t.Run("a relay address only matches itself", func(t *testing.T) {
		real := "real-" + rnd() + "@gmail.test"
		s.google(s.browser(), "", "g-"+rnd(), real, true)
		relay := "x" + rnd() + "@privaterelay.appleid.com"
		b := s.browser()
		s.apple(b, "", "001."+rnd(), relay, "")
		if b.me() != relay || s.userID(relay) == s.userID(real) {
			t.Errorf("the relay sign-in became %q", b.me())
		}
	})
}

func TestSocialLinking(t *testing.T) {
	s := bootSocial(t)
	email := "link-" + rnd() + "@company.test"
	b := s.browser()
	web := b.asClient()
	web.must("POST", "/api/auth/register", map[string]string{"email": email, "password": "link-pass-123"}, 201)
	uid := s.userID(email)

	// Connect Apple (a different address: a relay one) from the account page.
	loc := s.apple(b, "?intent=link&next="+url.QueryEscape("/en/account"), "001.link"+rnd(), "rel"+rnd()+"@privaterelay.appleid.com", "")
	if loc != "/en/account?linked=apple" {
		t.Fatalf("link landed on %q", loc)
	}
	m := web.must("GET", "/api/me/identities", nil, 200)
	if m["has_password"] != true || len(m["identities"].([]any)) != 1 || m["apple_available"] != true {
		t.Errorf("methods: %v", m)
	}

	// That Apple account cannot be connected to a second Moharrik account.
	other := s.browser()
	other.asClient().must("POST", "/api/auth/register", map[string]string{"email": "other-" + rnd() + "@company.test", "password": "other-pass-123"}, 201)
	appleSub := s.identities(uid)["apple"]
	state, nonce := other.start("apple", "?intent=link&next=/en/account")
	code := s.fake.Code("apple", appleClaims(appleSub, "x@privaterelay.appleid.com", nonce))
	_, finish := s.browser().postForm("/api/auth/oauth/apple/callback", url.Values{"state": {state}, "code": {code}})
	if _, loc := other.get(finish); errorCode(loc) != "identity_taken" {
		t.Errorf("connecting someone else's Apple account: %q", loc)
	}

	// Linking needs a signed-in browser.
	if _, loc := s.browser().get("/api/auth/oauth/apple?intent=link&next=/en/account"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("an anonymous link: %q", loc)
	}

	// Unlinking Apple revokes it at Apple. With a password left, it is allowed.
	before := len(s.fake.Revocations())
	web.must("DELETE", "/api/me/identities/apple", nil, 204)
	if r := s.fake.Revocations(); len(r) != before+1 || r[len(r)-1].ClientID != testServicesID {
		t.Errorf("revocations: %v", r)
	}

	// A passwordless Google account cannot drop its only way in.
	g := s.browser()
	onlySub, onlyEmail := "g-only-"+rnd(), "only-"+rnd()+"@gmail.test"
	s.google(g, "", onlySub, onlyEmail, true)
	code409, body := g.asClient().do("DELETE", "/api/me/identities/google", nil)
	if code409 != http.StatusConflict || !strings.Contains(string(body), "last_method") {
		t.Errorf("unlinking the last method: %d %s", code409, body)
	}

	// Re-authenticating (before deleting the account) must be the same provider account.
	if loc := s.google(g, "?intent=reauth&next="+url.QueryEscape("/en/account"), "g-someone-else-"+rnd(), onlyEmail, true); errorCode(loc) != "reauth_mismatch" {
		t.Errorf("reauth with another Google account: %q", loc)
	}
	if loc := s.google(g, "?intent=reauth&next="+url.QueryEscape("/en/account"), onlySub, onlyEmail, true); loc != "/en/account?reauth=google" || g.me() != onlyEmail {
		t.Errorf("reauth: %q", loc)
	}
}

func TestSocialNative(t *testing.T) {
	s := bootSocial(t)
	post := func(path string, body map[string]any) (int, map[string]any) {
		code, b := newClient(t, s.url).do("POST", path, body)
		out := map[string]any{}
		_ = json.Unmarshal(b, &out)
		return code, out
	}
	appleBody := func(sub, email, nonce, aud string) map[string]any {
		c := appleClaims(sub, email, nonce)
		c["aud"] = aud
		codeClaims := appleClaims(sub, email, "")
		return map[string]any{"identity_token": s.fake.Sign(c, nil), "authorization_code": s.fake.Code("apple", codeClaims),
			"nonce": nonce, "given_name": "Sara", "family_name": "Ali", "device": "iPhone"}
	}

	sub := "001.native" + rnd()
	email := "n-" + rnd() + "@icloud.test"
	nonce := "nonce-" + rnd() + rnd()
	code, out := post("/api/auth/native/apple", appleBody(sub, email, nonce, testBundleID))
	if code != 200 || !strings.HasPrefix(out["token"].(string), "togo_pat_") || out["email"] != email {
		t.Fatalf("native apple: %d %v", code, out)
	}
	app := newClient(t, s.url)
	app.bearer = out["token"].(string)
	app.must("GET", "/api/me/workspaces", nil, 200)
	var name string
	_ = s.a.SysDB.QueryRow(`SELECT display_name FROM user_identities WHERE provider = 'apple' AND subject = $1`, sub).Scan(&name)
	if name != "Sara Ali" {
		t.Errorf("name: %q", name)
	}

	if code, _ := post("/api/auth/native/apple", appleBody(sub, email, nonce, testBundleID)); code != http.StatusUnauthorized {
		t.Errorf("a replayed nonce: %d", code)
	}
	if code, _ := post("/api/auth/native/apple", appleBody(sub, email, "nonce-"+rnd()+rnd(), "com.someone.else")); code != http.StatusUnauthorized {
		t.Errorf("another app's token: %d", code)
	}
	// A password account with the address: account_conflict, never attached.
	pw := "pw-" + rnd() + "@icloud.test"
	newClient(t, s.url).must("POST", "/api/auth/register", map[string]string{"email": pw, "password": "pw-pass-1234"}, 201)
	if code, out := post("/api/auth/native/apple", appleBody("001."+rnd(), pw, "nonce-"+rnd()+rnd(), testBundleID)); code != http.StatusConflict || out["code"] != "account_conflict" {
		t.Errorf("native apple onto a password account: %d %v", code, out)
	}

	// Google, native: an id_token for the iOS client.
	gc := googleClaims("g-native-"+rnd(), "gn-"+rnd()+"@gmail.test", true, "")
	gc["aud"] = testGoogleIOS
	if code, out := post("/api/auth/native/google", map[string]any{"id_token": s.fake.Sign(gc, nil), "device": "Pixel"}); code != 200 || out["token"] == nil {
		t.Errorf("native google: %d %v", code, out)
	}
	gc["aud"] = "someone-elses-client"
	if code, _ := post("/api/auth/native/google", map[string]any{"id_token": s.fake.Sign(gc, nil)}); code != http.StatusUnauthorized {
		t.Errorf("native google for another client: %d", code)
	}
}

func TestSocialAccountDeletion(t *testing.T) {
	s := bootSocial(t)

	// An Apple user on the phone: two workspaces, one alone, one shared with a member.
	sub := "001.del" + rnd()
	email := "del-" + rnd() + "@icloud.test"
	nonce := "nonce-" + rnd() + rnd()
	c := appleClaims(sub, email, nonce)
	c["aud"] = testBundleID
	authCode := s.fake.Code("apple", appleClaims(sub, email, ""))
	refresh := s.fake.RefreshFor(authCode)
	code, b := newClient(t, s.url).do("POST", "/api/auth/native/apple", map[string]any{
		"identity_token": s.fake.Sign(c, nil), "authorization_code": authCode, "nonce": nonce})
	if code != 200 {
		t.Fatalf("native apple: %d %s", code, b)
	}
	var tok map[string]any
	_ = json.Unmarshal(b, &tok)
	phone := newClient(t, s.url)
	phone.bearer = tok["token"].(string)
	uid := s.userID(email)

	solo := phone.must("POST", "/api/me/workspaces", map[string]string{"name": "Solo " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	shared := phone.must("POST", "/api/me/workspaces", map[string]string{"name": "Shared " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	mate := newClient(t, s.url)
	mateEmail := "mate-" + rnd() + "@company.test"
	mate.must("POST", "/api/auth/register", map[string]string{"email": mateEmail, "password": "mate-pass-123"}, 201)
	mateID := s.userID(mateEmail)
	if _, err := s.a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'member', 'active')`, shared, mateID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.SysDB.Exec(`INSERT INTO webhook_sources (workspace_id, name, created_by) VALUES ($1, 'deploys', $2)`, shared, uid); err != nil {
		t.Fatal(err)
	}

	preview := phone.must("GET", "/api/me/account/deletion", nil, 200)
	if preview["fresh"] != true || len(preview["delete_workspaces"].([]any)) != 1 || len(preview["blocking_workspaces"].([]any)) != 1 {
		t.Fatalf("preview: %v", preview)
	}

	if code, b := phone.do("POST", "/api/me/account/delete", map[string]any{"confirm": "someone@else.test"}); code != 422 {
		t.Errorf("a wrong confirmation: %d %s", code, b)
	}
	if code, b := phone.do("POST", "/api/me/account/delete", map[string]any{"confirm": email}); code != http.StatusConflict || !strings.Contains(string(b), "sole_owner") {
		t.Errorf("the only owner of a shared workspace: %d %s", code, b)
	}

	// A stale credential needs a re-authentication.
	stale := newClient(t, s.url)
	stale.bearer = "togo_pat_" + rnd() + rnd() + rnd() + rnd()
	if _, err := s.a.SysDB.Exec(`INSERT INTO personal_access_tokens (id, user_id, name, token_hash, abilities, created_at) VALUES ($1, $2, 'old', encode(sha256($3::bytea), 'hex'), '', $4)`,
		"old-"+rnd(), uid, stale.bearer, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if code, b := stale.do("POST", "/api/me/account/delete", map[string]any{"confirm": email}); code != http.StatusForbidden || !strings.Contains(string(b), "reauth_required") {
		t.Errorf("a stale token: %d %s", code, b)
	}

	// Make the mate an owner: now the shared workspace stays with them.
	if _, err := s.a.SysDB.Exec(`UPDATE workspace_members SET role = 'owner' WHERE workspace_id = $1 AND user_id = $2`, shared, mateID); err != nil {
		t.Fatal(err)
	}
	before := len(s.fake.Revocations())
	phone.must("POST", "/api/me/account/delete", map[string]any{"confirm": strings.ToUpper(email)}, 204)

	if s.userID(email) != "" {
		t.Error("the user row is still there")
	}
	var n int
	_ = s.a.SysDB.QueryRow(`SELECT count(*) FROM workspaces WHERE id = $1`, solo).Scan(&n)
	if n != 0 {
		t.Error("the workspace only they were in survived")
	}
	_ = s.a.SysDB.QueryRow(`SELECT count(*) FROM workspaces WHERE id = $1`, shared).Scan(&n)
	if n != 1 {
		t.Error("the shared workspace was deleted")
	}
	var creator string
	_ = s.a.SysDB.QueryRow(`SELECT created_by FROM webhook_sources WHERE workspace_id = $1`, shared).Scan(&creator)
	if creator != mateID {
		t.Errorf("the webhook source went to %q, want the remaining owner", creator)
	}
	_ = s.a.SysDB.QueryRow(`SELECT count(*) FROM personal_access_tokens WHERE user_id = $1`, uid).Scan(&n)
	if n != 0 {
		t.Error("app tokens survived")
	}
	if code, _ := phone.do("GET", "/api/me/workspaces", nil); code != http.StatusUnauthorized {
		t.Errorf("the deleted account's token still works: %d", code)
	}
	r := s.fake.Revocations()
	if len(r) != before+1 || r[len(r)-1].ClientID != testBundleID || r[len(r)-1].Token != refresh {
		t.Errorf("Apple was not revoked: %v (want token %s)", r, refresh)
	}

	// A password account deletes with its password (any session age).
	pw := s.browser()
	pwEmail := "pwdel-" + rnd() + "@company.test"
	web := pw.asClient()
	web.must("POST", "/api/auth/register", map[string]string{"email": pwEmail, "password": "pwdel-pass-123"}, 201)
	if code, b := web.do("POST", "/api/me/account/delete", map[string]any{"confirm": pwEmail, "password": "wrong-password"}); code != http.StatusForbidden || !strings.Contains(string(b), "wrong_password") {
		t.Errorf("a wrong password: %d %s", code, b)
	}
	web.must("POST", "/api/me/account/delete", map[string]any{"confirm": pwEmail, "password": "pwdel-pass-123"}, 204)
	if pw.me() != "" {
		t.Error("still signed in after deleting the account")
	}
}
