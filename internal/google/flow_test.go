package google_test

/*
The Google connect flow end to end (MH-439): the real stack (guard, session,
RLS pool) against a throwaway database and the fake Google in googletest.

	TEST_DATABASE_URL=postgres://postgres:…@localhost:55432/moharrik_mh439_test?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app_mh439t:…@localhost:55432/moharrik_mh439_test?sslmode=disable \
	go test -count=1 ./internal/google/ -v
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/secret"
	"github.com/fadymondy/moharrik/internal/server"
	"github.com/fadymondy/moharrik/internal/testdb"
)

// ---- a log sink and a response recorder, for the no-leak assertion -----------

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type fixture struct {
	t    *testing.T
	a    *app.App
	srv  *httptest.Server
	fake *googletest.Server
	logs *syncBuf
	// Every response body and header any client in this test received.
	seenMu sync.Mutex
	seen   []string
}

func (f *fixture) record(res *http.Response, body []byte) {
	f.seenMu.Lock()
	defer f.seenMu.Unlock()
	var h strings.Builder
	for k, vs := range res.Header {
		h.WriteString(k + ": " + strings.Join(vs, ",") + "\n")
	}
	f.seen = append(f.seen, h.String(), string(body))
}

type client struct {
	f     *fixture
	http  *http.Client
	jar   *cookiejar.Jar
	csrf  string
	ws    string
	email string
}

func (f *fixture) newUser(t *testing.T) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &client{f: f, jar: jar, http: &http.Client{Jar: jar,
		// The callback's 302 is the answer under test; never follow it.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	c.email = "u-" + rnd() + "@example.test"
	c.must(t, "POST", "/api/auth/register", map[string]string{"email": c.email, "password": "correct-horse-9"}, 201)
	return c
}

func rnd() string { return strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "") }

func (c *client) do(t *testing.T, method, path string, body any, hdr ...string) (int, []byte, *http.Response) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.f.srv.URL + path
	}
	req, _ := http.NewRequest(method, target, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.ws != "" {
		req.Header.Set("X-Workspace-ID", c.ws)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Add(hdr[i], hdr[i+1])
	}
	if method != http.MethodGet {
		if c.csrf == "" {
			r, err := c.http.Get(c.f.srv.URL + "/api/auth/csrf")
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Token string `json:"csrf_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&out)
			r.Body.Close()
			c.csrf = out.Token
		}
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	c.f.record(res, b)
	return res.StatusCode, b, res
}

func (c *client) must(t *testing.T, method, path string, body any, want int) map[string]any {
	t.Helper()
	code, b, _ := c.do(t, method, path, body)
	if code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, b)
	}
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func (c *client) workspace(t *testing.T) (id, slug string) {
	t.Helper()
	ws := c.must(t, "POST", "/api/me/workspaces", map[string]string{"name": "G " + rnd()}, 201)["workspace"].(map[string]any)
	c.ws = ws["id"].(string)
	return c.ws, ws["slug"].(string)
}

// start asks the API for the consent URL (setting the state cookie in c's jar).
func (c *client) start(t *testing.T, query string) string {
	t.Helper()
	out := c.must(t, "GET", "/api/w/google/connect?"+query, nil, 200)
	return out["url"].(string)
}

// callback follows Google's redirect back and returns the result and the page.
func (c *client) callback(t *testing.T, location string, hdr ...string) (result string, page *url.URL) {
	t.Helper()
	code, b, res := c.do(t, "GET", location, nil, hdr...)
	if code != http.StatusFound {
		t.Fatalf("callback: got %d want 302: %s", code, b)
	}
	u, err := url.Parse(res.Header.Get("Location"))
	if err != nil || u.Host != "" || !strings.HasPrefix(u.Path, "/") {
		t.Fatalf("callback must redirect to a local page, got %q", res.Header.Get("Location"))
	}
	return u.Query().Get("google"), u
}

// connect runs the whole flow for c and returns the callback result.
func (c *client) connect(t *testing.T, query string) (string, *url.URL) {
	t.Helper()
	loc, err := c.f.fake.Consent(c.start(t, query))
	if err != nil {
		t.Fatal(err)
	}
	return c.callback(t, loc)
}

type listOut struct {
	Configured  bool                `json:"configured"`
	RedirectURL string              `json:"redirect_url"`
	Connections []google.Connection `json:"connections"`
}

func (c *client) list(t *testing.T) listOut {
	t.Helper()
	code, b, _ := c.do(t, "GET", "/api/w/google/connections", nil)
	if code != 200 {
		t.Fatalf("list: %d %s", code, b)
	}
	var out listOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	owner, appURL := testdb.Setup(t, "google", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "google-test-secret-google-test-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	// The Google data collector never runs in a test; the connect flow's own
	// background discovery is off here and switched on by the tests that
	// exercise it (resources_test.go), so these counts stay exact.
	t.Setenv("GOOGLE_COLLECTOR", "off")
	t.Setenv("GOOGLE_SYNC_INLINE", "off")
	google.RetryBase = 5 * time.Millisecond
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_URL", "http://127.0.0.1") // local, so GOOGLE_API_BASE is honoured; the port is set below

	fake := googletest.New()
	t.Cleanup(fake.Close)
	t.Setenv("GOOGLE_API_BASE", fake.URL)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", fake.ClientID)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", fake.ClientSecret)
	// The sign-in client must play no part in this flow.
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "")

	a := server.Boot()
	t.Cleanup(func() { a.Kernel.Close() })
	logs := &syncBuf{}
	lg := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	a.Kernel.Log, a.Log = lg, lg
	prev := slog.Default()
	slog.SetDefault(lg)
	t.Cleanup(func() { slog.SetDefault(prev) })

	srv := httptest.NewServer(a.Kernel.Handler())
	t.Cleanup(srv.Close)
	// The redirect URI is built from APP_URL at request time.
	t.Setenv("APP_URL", srv.URL)
	t.Setenv("PUBLIC_URL", "")
	return &fixture{t: t, a: a, srv: srv, fake: fake, logs: logs}
}

func (f *fixture) setAccount(sub, email string) {
	f.fake.Set(func(s *googletest.Server) {
		s.Account = googletest.Account{Sub: sub, Email: email}
		s.Deny, s.OmitRefresh, s.OmitIDToken, s.Untick = false, false, false, nil
		s.RefreshFails, s.RotateRefresh, s.TokenTTL = false, false, 0
	})
}

func (f *fixture) connectionCount(t *testing.T, workspaceID string) int {
	t.Helper()
	var n int
	if err := f.a.SysDB.QueryRow(`SELECT count(*) FROM google_connections WHERE workspace_id = $1`, workspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) storedRefresh(t *testing.T, id string) string {
	t.Helper()
	var sealed string
	if err := f.a.SysDB.QueryRow(`SELECT refresh_token_sealed FROM google_connections WHERE id = $1`, id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := secret.New(secret.DomainGoogleRefresh, "x", "y").Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestGoogleConnect(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	alice := f.newUser(t)
	aWS, aSlug := alice.workspace(t)

	t.Run("configured and the redirect URI comes from APP_URL", func(t *testing.T) {
		l := alice.list(t)
		if !l.Configured || l.RedirectURL != f.srv.URL+"/api/google/oauth/callback" || len(l.Connections) != 0 {
			t.Fatalf("%+v", l)
		}
	})

	var connID string
	t.Run("the full connect flow", func(t *testing.T) {
		f.setAccount("sub-brand", "brand@example.test")
		authURL := alice.start(t, "product=analytics&locale=ar")
		q, _ := url.Parse(authURL)
		p := q.Query()
		for k, want := range map[string]string{
			"access_type": "offline", "prompt": "consent select_account", "include_granted_scopes": "true",
			"code_challenge_method": "S256", "response_type": "code", "client_id": f.fake.ClientID,
			"redirect_uri": f.srv.URL + "/api/google/oauth/callback",
		} {
			if p.Get(k) != want {
				t.Errorf("consent %s = %q, want %q", k, p.Get(k), want)
			}
		}
		scopes := strings.Fields(p.Get("scope"))
		if !contains(scopes, "openid") || !contains(scopes, "email") || !contains(scopes, google.ScopeAnalytics) || len(scopes) != 3 {
			t.Errorf("analytics must ask for openid, email and analytics.readonly only: %v", scopes)
		}
		if p.Get("state") == "" || p.Get("code_challenge") == "" {
			t.Fatal("no state or PKCE challenge")
		}
		loc, err := f.fake.Consent(authURL)
		if err != nil {
			t.Fatal(err)
		}
		result, page := alice.callback(t, loc)
		if result != google.ResultConnected {
			t.Fatalf("result %q", result)
		}
		if page.Path != "/ar/w/"+aSlug+"/settings/integrations" || page.Query().Get("product") != "analytics" {
			t.Errorf("came back to %s", page)
		}
		if f.fake.LastTokenForm("authorization_code").Get("code_verifier") == "" {
			t.Error("the code exchange must carry the PKCE verifier")
		}
		l := alice.list(t)
		if len(l.Connections) != 1 {
			t.Fatalf("connections: %+v", l.Connections)
		}
		c := l.Connections[0]
		connID = c.ID
		if c.Email != "brand@example.test" || len(c.Products) != 1 || c.Products[0] != google.Analytics ||
			c.Status != "" || c.ConnectedByEmail != alice.email || c.Revenue {
			t.Errorf("connection: %+v", c)
		}
	})

	t.Run("an incremental connect adds YouTube to the same row", func(t *testing.T) {
		result, _ := alice.connect(t, "product=youtube&monetary=true")
		if result != google.ResultConnected {
			t.Fatalf("result %q", result)
		}
		ask := strings.Fields(f.fake.LastAuthorize().Get("scope"))
		if !contains(ask, google.ScopeYTMonetary) || !contains(ask, google.ScopeYouTube) || !contains(ask, google.ScopeYTAnalytics) {
			t.Errorf("youtube+monetary asked for %v", ask)
		}
		l := alice.list(t)
		if len(l.Connections) != 1 || l.Connections[0].ID != connID {
			t.Fatalf("the same account must stay one row: %+v", l.Connections)
		}
		c := l.Connections[0]
		if len(c.Products) != 2 || c.Products[0] != google.Analytics || c.Products[1] != google.YouTube || !c.Revenue {
			t.Errorf("products after YouTube: %+v", c)
		}
	})

	t.Run("youtube without monetary does not ask for revenue", func(t *testing.T) {
		if strings.Contains(mustQuery(t, alice.start(t, "product=youtube"), "scope"), "monetary") {
			t.Error("monetary was not asked for")
		}
		sc := strings.Fields(mustQuery(t, alice.start(t, "product=search_console&monetary=true"), "scope"))
		if !contains(sc, google.ScopeWebmasters) || strings.Contains(strings.Join(sc, " "), "monetary") {
			t.Errorf("search console asked for %v", sc)
		}
	})

	t.Run("state replay is refused", func(t *testing.T) {
		f.setAccount("sub-replay", "replay@example.test")
		authURL := alice.start(t, "product=analytics")
		state := mustQuery(t, authURL, "state")
		loc, _ := f.fake.Consent(authURL)
		if r, _ := alice.callback(t, loc); r != google.ResultConnected {
			t.Fatalf("first use: %q", r)
		}
		// The cookie is spent; put it back, as an attacker holding both would.
		if r, _ := alice.callback(t, loc, "Cookie", "moh_google_state="+state); r != google.ResultState {
			t.Fatalf("replay: %q", r)
		}
	})

	t.Run("a callback without the state cookie is refused", func(t *testing.T) {
		f.setAccount("sub-nocookie", "nocookie@example.test")
		authURL := alice.start(t, "product=analytics")
		loc, _ := f.fake.Consent(authURL)
		alice.jar.SetCookies(mustURL(t, f.srv.URL+"/api/google/oauth/"), []*http.Cookie{{Name: "moh_google_state", Value: "", Path: "/api/google/oauth", MaxAge: -1}})
		if r, _ := alice.callback(t, loc); r != google.ResultState {
			t.Fatalf("no cookie: %q", r)
		}
		if f.connectionCount(t, aWS) != 2 {
			t.Error("a refused callback stored a connection")
		}
	})

	t.Run("a state started by another user is refused", func(t *testing.T) {
		f.setAccount("sub-other", "other@example.test")
		mallory := f.newUser(t)
		// Even a member of the same workspace, with the admin role.
		addMember(t, f, aWS, mallory.email, "admin")
		authURL := alice.start(t, "product=analytics")
		state := mustQuery(t, authURL, "state")
		loc, _ := f.fake.Consent(authURL)
		if r, _ := mallory.callback(t, loc, "Cookie", "moh_google_state="+state); r != google.ResultState {
			t.Fatalf("another user's state: %q", r)
		}
		// And it is spent: alice cannot use it afterwards either.
		if r, _ := alice.callback(t, loc); r != google.ResultState {
			t.Fatalf("after the other user's attempt: %q", r)
		}
		if contains(emails(alice.list(t)), "other@example.test") {
			t.Error("a refused callback stored a connection")
		}
	})

	t.Run("an expired state is refused", func(t *testing.T) {
		f.setAccount("sub-expired", "expired@example.test")
		authURL := alice.start(t, "product=analytics")
		if _, err := f.a.SysDB.Exec(`UPDATE google_oauth_states SET expires_at = now() - interval '1 minute' WHERE workspace_id = $1`, aWS); err != nil {
			t.Fatal(err)
		}
		loc, _ := f.fake.Consent(authURL)
		if r, _ := alice.callback(t, loc); r != google.ResultState {
			t.Fatalf("expired: %q", r)
		}
	})

	t.Run("an unauthenticated callback is refused before anything runs", func(t *testing.T) {
		anon := &client{f: f, http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		if code, _, _ := anon.do(t, "GET", "/api/google/oauth/callback?state=x&code=y", nil); code != http.StatusUnauthorized {
			t.Fatalf("anonymous callback: %d", code)
		}
	})

	t.Run("a missing refresh token is revoked and refused", func(t *testing.T) {
		f.setAccount("sub-norefresh", "norefresh@example.test")
		f.fake.Set(func(s *googletest.Server) { s.OmitRefresh = true })
		before := len(f.fake.Revoked())
		if r, _ := alice.connect(t, "product=analytics"); r != google.ResultNoRefresh {
			t.Fatalf("result %q", r)
		}
		if len(f.fake.Revoked()) != before+1 {
			t.Error("the access token of a refused grant must be revoked")
		}
		if contains(emails(alice.list(t)), "norefresh@example.test") {
			t.Error("stored without a refresh token")
		}
	})

	t.Run("an unticked scope is revoked and refused", func(t *testing.T) {
		f.setAccount("sub-untick", "untick@example.test")
		f.fake.Set(func(s *googletest.Server) { s.Untick = []string{google.ScopeYTAnalytics} })
		before := len(f.fake.Revoked())
		if r, _ := alice.connect(t, "product=youtube"); r != google.ResultScope {
			t.Fatalf("result %q", r)
		}
		if len(f.fake.Revoked()) != before+1 || len(f.fake.LiveRefreshTokens("sub-untick")) != 0 {
			t.Error("a grant missing a scope must be revoked at Google")
		}
		if contains(emails(alice.list(t)), "untick@example.test") {
			t.Error("stored without the scope")
		}
	})

	t.Run("an unticked scope on an account already connected refuses WITHOUT revoking the working grant", func(t *testing.T) {
		f.setAccount("sub-brand", "brand@example.test")
		f.fake.Set(func(s *googletest.Server) { s.Untick = []string{google.ScopeWebmasters} })
		before := len(f.fake.Revoked())
		if r, _ := alice.connect(t, "product=search_console"); r != google.ResultScope {
			t.Fatalf("result %q", r)
		}
		if len(f.fake.Revoked()) != before {
			t.Error("revoking here would have disconnected the Analytics/YouTube grant that is in use")
		}
		src, err := google.TokenSource(ctx, f.a, aWS, connID)
		if err != nil {
			t.Fatal(err)
		}
		google.ForgetAccessTokens()
		if _, err := src.Token(ctx, google.ScopeAnalytics); err != nil {
			t.Fatalf("the existing grant must still work: %v", err)
		}
	})

	t.Run("consent denied", func(t *testing.T) {
		f.setAccount("sub-deny", "deny@example.test")
		f.fake.Set(func(s *googletest.Server) { s.Deny = true })
		if r, _ := alice.connect(t, "product=analytics"); r != google.ResultAccessDenied {
			t.Fatalf("result %q", r)
		}
	})

	t.Run("no id_token: userinfo names the account", func(t *testing.T) {
		f.setAccount("sub-userinfo", "userinfo@example.test")
		f.fake.Set(func(s *googletest.Server) { s.OmitIDToken = true })
		if r, _ := alice.connect(t, "product=analytics"); r != google.ResultConnected {
			t.Fatalf("result %q", r)
		}
		if !contains(emails(alice.list(t)), "userinfo@example.test") || f.fake.Calls("userinfo") == 0 {
			t.Error("userinfo fallback")
		}
	})

	t.Run("token source: cache, scope check, rotation re-sealed", func(t *testing.T) {
		f.setAccount("sub-brand", "brand@example.test")
		src, err := google.TokenSource(ctx, f.a, aWS, connID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.Token(ctx, google.ScopeWebmasters); !errors.Is(err, google.ErrScope) {
			t.Fatalf("a scope never granted must be refused, got %v", err)
		}
		// Cached: a long-lived access token is not refreshed twice.
		f.fake.Set(func(s *googletest.Server) { s.TokenTTL = 3600 })
		t1, err := src.Token(ctx, google.ScopeAnalytics)
		if err != nil {
			t.Fatal(err)
		}
		n := f.fake.Calls("token:refresh_token")
		if t2, _ := src.Token(ctx, google.ScopeYouTube); t2 != t1 || f.fake.Calls("token:refresh_token") != n {
			t.Error("a live access token must come from the cache")
		}

		old := f.storedRefresh(t, connID)
		f.fake.Set(func(s *googletest.Server) { s.RotateRefresh = true })
		google.ForgetAccessTokens()
		if _, err := src.Token(ctx, google.ScopeAnalytics); err != nil {
			t.Fatal(err)
		}
		rotated := f.storedRefresh(t, connID)
		if rotated == old || !strings.HasPrefix(rotated, "1//fake-rotated-") {
			t.Fatalf("the rotated refresh token was not re-sealed")
		}
		f.fake.Set(func(s *googletest.Server) { s.RotateRefresh = false })
		google.ForgetAccessTokens()
		if _, err := src.Token(ctx, google.ScopeAnalytics); err != nil {
			t.Fatalf("the re-sealed token must work: %v", err)
		}
	})

	t.Run("invalid_grant marks the connection reauth; reconnect clears it", func(t *testing.T) {
		src, _ := google.TokenSource(ctx, f.a, aWS, connID)
		f.fake.Set(func(s *googletest.Server) { s.RefreshFails = true })
		google.ForgetAccessTokens()
		if _, err := src.Token(ctx, google.ScopeAnalytics); !errors.Is(err, google.ErrReauth) {
			t.Fatalf("got %v", err)
		}
		c := byID(alice.list(t), connID)
		if c.Status != "reauth" || !strings.Contains(c.AuthError, "invalid_grant") {
			t.Fatalf("%+v", c)
		}
		n := f.fake.Calls("token:refresh_token")
		if _, err := src.Token(ctx, google.ScopeAnalytics); !errors.Is(err, google.ErrReauth) || f.fake.Calls("token:refresh_token") != n {
			t.Error("a reauth connection must not keep asking Google")
		}
		f.setAccount("sub-brand", "brand@example.test")
		// Google dropped the whole grant: a reconnect must ask for every
		// product this connection had, or YouTube would silently vanish.
		authURL := alice.start(t, "product=analytics&connection="+connID)
		ask := strings.Fields(mustQuery(t, authURL, "scope"))
		if !contains(ask, google.ScopeYouTube) || !contains(ask, google.ScopeYTMonetary) || mustQuery(t, authURL, "login_hint") != "brand@example.test" {
			t.Fatalf("reconnect asked for %v (hint %q)", ask, mustQuery(t, authURL, "login_hint"))
		}
		loc, _ := f.fake.Consent(authURL)
		if r, _ := alice.callback(t, loc); r != google.ResultConnected {
			t.Fatalf("reconnect: %q", r)
		}
		c = byID(alice.list(t), connID)
		if c.Status != "" || c.AuthError != "" || len(c.Products) != 2 || !c.Revenue {
			t.Fatalf("reconnect must clear reauth and keep both products: %+v", c)
		}
		other := f.newUser(t)
		other.workspace(t)
		if code, _, _ := other.do(t, "GET", "/api/w/google/connect?product=analytics&connection="+connID, nil); code != 404 {
			t.Errorf("reconnecting another workspace's connection: %d", code)
		}
	})

	t.Run("roles: a manager cannot connect or list", func(t *testing.T) {
		mgr := f.newUser(t)
		addMember(t, f, aWS, mgr.email, "manager")
		mgr.ws = aWS
		if code, _, _ := mgr.do(t, "GET", "/api/w/google/connect?product=analytics", nil); code != 403 {
			t.Errorf("manager connect: %d", code)
		}
		if code, _, _ := mgr.do(t, "GET", "/api/w/google/connections", nil); code != 403 {
			t.Errorf("manager list: %d", code)
		}
		if code, _, _ := mgr.do(t, "DELETE", "/api/w/google/connections/"+connID, nil); code != 403 {
			t.Errorf("manager disconnect: %d", code)
		}
	})

	t.Run("an admin demoted mid-connect is refused at the callback", func(t *testing.T) {
		carol := f.newUser(t)
		addMember(t, f, aWS, carol.email, "admin")
		carol.ws = aWS
		f.setAccount("sub-demoted", "demoted@example.test")
		authURL := carol.start(t, "product=analytics")
		setRole(t, f, aWS, carol.email, "member")
		loc, _ := f.fake.Consent(authURL)
		if r, _ := carol.callback(t, loc); r != google.ResultForbidden {
			t.Fatalf("result %q", r)
		}
	})

	t.Run("the same account in two workspaces: disconnecting one does not revoke the other", func(t *testing.T) {
		bob := f.newUser(t)
		bWS, _ := bob.workspace(t)
		f.setAccount("sub-brand", "brand@example.test")
		if r, _ := bob.connect(t, "product=analytics"); r != google.ResultConnected {
			t.Fatalf("bob connect: %q", r)
		}
		bConn := bob.list(t).Connections[0].ID
		before := len(f.fake.Revoked())
		code, b, _ := alice.do(t, "DELETE", "/api/w/google/connections/"+connID, nil)
		var d google.Disconnected
		_ = json.Unmarshal(b, &d)
		if code != 200 || !d.Shared || d.Revoked || len(f.fake.Revoked()) != before {
			t.Fatalf("shared disconnect: %d %s", code, b)
		}
		src, err := google.TokenSource(ctx, f.a, bWS, bConn)
		if err != nil {
			t.Fatal(err)
		}
		google.ForgetAccessTokens()
		if _, err := src.Token(ctx, google.ScopeAnalytics); err != nil {
			t.Fatalf("bob's connection must keep working: %v", err)
		}

		t.Run("disconnect revokes at Google", func(t *testing.T) {
			stored := f.storedRefresh(t, bConn)
			code, b, _ := bob.do(t, "DELETE", "/api/w/google/connections/"+bConn, nil)
			var d google.Disconnected
			_ = json.Unmarshal(b, &d)
			if code != 200 || !d.Revoked || d.Shared {
				t.Fatalf("disconnect: %d %s", code, b)
			}
			rev := f.fake.Revoked()
			if len(rev) == 0 || rev[len(rev)-1] != stored {
				t.Error("the stored refresh token was not the one revoked")
			}
			if len(f.fake.LiveRefreshTokens("sub-brand")) != 0 {
				t.Error("the grant is still live at Google")
			}
			if len(bob.list(t).Connections) != 0 {
				t.Error("the connection is still listed")
			}
			if _, err := src.Token(ctx, google.ScopeAnalytics); !errors.Is(err, google.ErrNotConnected) {
				t.Errorf("a disconnected source must refuse, got %v", err)
			}
		})
	})

	t.Run("not configured: said so, and connect answers 503", func(t *testing.T) {
		t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", "")
		if alice.list(t).Configured {
			t.Error("configured without a client id")
		}
		if code, _, _ := alice.do(t, "GET", "/api/w/google/connect?product=analytics", nil); code != 503 {
			t.Errorf("connect without a client: %d", code)
		}
		// OAUTH_GOOGLE_* (the sign-in client) must not stand in for it.
		t.Setenv("OAUTH_GOOGLE_CLIENT_ID", f.fake.ClientID)
		t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", f.fake.ClientSecret)
		if alice.list(t).Configured {
			t.Error("the sign-in client's variables must not configure integrations")
		}
	})

	// Last: everything above went through the fixture's clients and logger.
	t.Run("no response body, header or log line carries a token", func(t *testing.T) {
		tokens := f.fake.AllTokens()
		if len(tokens) < 10 {
			t.Fatalf("only %d tokens issued — the flows above did not run", len(tokens))
		}
		var sealed []string
		rows, _ := f.a.SysDB.Query(`SELECT refresh_token_sealed FROM google_connections`)
		for rows.Next() {
			var s string
			_ = rows.Scan(&s)
			sealed = append(sealed, s)
		}
		rows.Close()
		logs := f.logs.String()
		f.seenMu.Lock()
		seen := append([]string{logs}, f.seen...)
		f.seenMu.Unlock()
		for _, s := range seen {
			for _, tok := range append(append(tokens, sealed...), f.fake.ClientSecret) {
				if tok != "" && strings.Contains(s, tok) {
					t.Fatalf("a token leaked into a response or log line: …%s…", around(s, tok))
				}
			}
			if strings.Contains(s, "refresh_token") {
				t.Fatalf("a response or log line mentions refresh_token: %s", around(s, "refresh_token"))
			}
		}
		if !strings.Contains(logs, "/api/google/oauth/callback") {
			t.Fatal("the log capture saw no callback — it is not capturing")
		}
	})
}

func around(s, tok string) string {
	i := strings.Index(s, tok)
	lo, hi := i-40, i+len(tok)+10
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return strings.ReplaceAll(s[lo:hi], tok, "<TOKEN>")
}

func emails(l listOut) []string {
	var out []string
	for _, c := range l.Connections {
		out = append(out, c.Email)
	}
	return out
}

func byID(l listOut, id string) google.Connection {
	for _, c := range l.Connections {
		if c.ID == id {
			return c
		}
	}
	return google.Connection{}
}

func mustQuery(t *testing.T, raw, key string) string {
	t.Helper()
	return mustURL(t, raw).Query().Get(key)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func addMember(t *testing.T, f *fixture, ws, email, role string) {
	t.Helper()
	if _, err := f.a.SysDB.Exec(`
		INSERT INTO workspace_members (workspace_id, user_id, role)
		SELECT $1, id, $3 FROM users WHERE email = $2`, ws, email, role); err != nil {
		t.Fatal(err)
	}
}

func setRole(t *testing.T, f *fixture, ws, email, role string) {
	t.Helper()
	if _, err := f.a.SysDB.Exec(`
		UPDATE workspace_members SET role = $3
		WHERE workspace_id = $1 AND user_id = (SELECT id FROM users WHERE email = $2)`, ws, email, role); err != nil {
		t.Fatal(err)
	}
}
