package google

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFakeGoogleIsRefusedInProduction(t *testing.T) {
	t.Setenv("GOOGLE_API_BASE", "http://127.0.0.1:9")
	cases := []struct {
		env, appURL string
		fake        bool
	}{
		{"development", "http://localhost:8120", true},
		{"test", "http://127.0.0.1:5555", true},
		{"production", "http://localhost:8120", false},
		{"prod", "http://localhost:8120", false},
		{"development", "https://app.moharrik.com", false},
		{"development", "https://dev.moharrik.com", false},
		{"development", "", false},
	}
	for _, c := range cases {
		t.Setenv("APP_ENV", c.env)
		t.Setenv("APP_URL", c.appURL)
		got := current().TokenURL != Real.TokenURL
		if got != c.fake {
			t.Errorf("APP_ENV=%s APP_URL=%q: fake honoured = %v, want %v", c.env, c.appURL, got, c.fake)
		}
		if FakeRefused() == c.fake {
			t.Errorf("APP_ENV=%s APP_URL=%q: FakeRefused = %v", c.env, c.appURL, FakeRefused())
		}
	}
}

func TestRedirectURL(t *testing.T) {
	t.Setenv("GOOGLE_INTEGRATIONS_REDIRECT_URL", "")
	t.Setenv("PUBLIC_URL", "")
	t.Setenv("APP_URL", "")
	if got := RedirectURL(); got != "" {
		t.Fatalf("no base URL must mean no redirect URL, never a guessed localhost: %q", got)
	}
	if LoadConfig().Ready() {
		t.Fatal("not ready without a redirect URL")
	}
	t.Setenv("APP_URL", "https://app.moharrik.com/")
	if got := RedirectURL(); got != "https://app.moharrik.com/api/google/oauth/callback" {
		t.Fatalf("from APP_URL: %q", got)
	}
	t.Setenv("PUBLIC_URL", "https://dev.moharrik.com")
	if got := RedirectURL(); got != "https://dev.moharrik.com/api/google/oauth/callback" {
		t.Fatalf("PUBLIC_URL wins: %q", got)
	}
	t.Setenv("GOOGLE_INTEGRATIONS_REDIRECT_URL", "https://example.test/cb")
	if got := RedirectURL(); got != "https://example.test/cb" {
		t.Fatalf("override: %q", got)
	}
}

func TestOnlyTheIntegrationClientConfigures(t *testing.T) {
	t.Setenv("APP_URL", "https://app.moharrik.com")
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", "")
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", "")
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "signin-client")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "signin-secret")
	if LoadConfig().Ready() {
		t.Fatal("the sign-in client (OAUTH_GOOGLE_*) must never configure integrations")
	}
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", "id")
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", "secret")
	if !LoadConfig().Ready() {
		t.Fatal("ready with the integrations client")
	}
}

func TestProductsComeFromGrantedScopes(t *testing.T) {
	if p := ProductsOf([]string{"openid", "email", ScopeAnalytics}); len(p) != 1 || p[0] != Analytics {
		t.Errorf("analytics: %v", p)
	}
	if p := ProductsOf([]string{ScopeYouTube}); len(p) != 0 {
		t.Errorf("youtube needs both youtube scopes: %v", p)
	}
	if p := ProductsOf([]string{ScopeYouTube, ScopeYTAnalytics, ScopeWebmastersRO}); len(p) != 2 || p[0] != SearchConsole || p[1] != YouTube {
		t.Errorf("sc+yt: %v", p)
	}
	if got := YouTube.Scopes(true); len(got) != 3 {
		t.Errorf("youtube+monetary: %v", got)
	}
	if got := Analytics.Scopes(true); len(got) != 1 {
		t.Errorf("monetary means nothing for analytics: %v", got)
	}
	if _, ok := ParseProduct("drive"); ok {
		t.Error("unknown product accepted")
	}
}

func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func TestIDTokenClaimsAreChecked(t *testing.T) {
	exp := time.Now().Add(time.Hour).Unix()
	ok := map[string]any{"iss": "https://accounts.google.com", "aud": "cid", "sub": "123", "email": "a@x.test", "exp": exp}
	if sub, email, err := idTokenClaims(jwt(ok), "cid"); err != nil || sub != "123" || email != "a@x.test" {
		t.Fatalf("valid: %q %q %v", sub, email, err)
	}
	bad := []map[string]any{
		{"iss": "https://evil.test", "aud": "cid", "sub": "1", "exp": exp},
		{"iss": "accounts.google.com", "aud": "other", "sub": "1", "exp": exp},
		{"iss": "accounts.google.com", "aud": "cid", "sub": "", "exp": exp},
		{"iss": "accounts.google.com", "aud": "cid", "sub": "1", "exp": time.Now().Add(-time.Hour).Unix()},
	}
	for i, c := range bad {
		if _, _, err := idTokenClaims(jwt(c), "cid"); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if _, _, err := idTokenClaims("not-a-jwt", "cid"); err == nil {
		t.Error("garbage accepted")
	}
	if _, _, err := idTokenClaims(jwt(map[string]any{"iss": "accounts.google.com", "aud": []string{"x", "cid"}, "sub": "1"}), "cid"); err != nil {
		t.Errorf("audience list: %v", err)
	}
}

func TestTokenErrorCarriesNoRequest(t *testing.T) {
	e := &tokenError{Status: 400, Code: "invalid_grant"}
	if strings.Contains(e.Error(), "refresh") || !strings.Contains(e.Error(), "invalid_grant") {
		t.Fatal(e.Error())
	}
}
