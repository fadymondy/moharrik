/*
Package google connects a workspace's own Google accounts (MH-439), the
foundation for GA4, Search Console and YouTube (MH-440..447).

Fady (2026-09-23): "every team can link all of this, as each team is a
product". So every workspace connects its own accounts with user OAuth. A
service account (what fadymondy.com uses for GA4 and Search Console) cannot
work multi-tenant: every tenant would have to add Moharrik's service account to
their property by hand.

The design is ported from fadymondy.com's internal/youtube (single owner there,
per workspace here).

# Connect

	GET /api/w/google/connect?product=…   integrations:manage → {url} + state cookie
	GET /api/google/oauth/callback        signed-in tier (see below)

The connect leg answers JSON rather than a 302: it is a workspace route, and a
browser navigation cannot carry X-Workspace-ID. The page sends the browser to
the returned URL.

Consent is incremental per product (include_granted_scopes=true), with
access_type=offline and prompt="consent select_account": a refresh token only
comes with a fresh consent, and the right Google account is often not the one
signed in to the browser.

	analytics        analytics.readonly
	search_console   webmasters (read + sitemap submit)
	youtube          youtube.readonly + yt-analytics.readonly,
	                 + yt-analytics-monetary.readonly only when asked for

plus openid and email, for the account's stable id and address.

The state is single use and bound three ways: a random value in an HttpOnly
cookie scoped to /api/google/oauth, its sha256 in google_oauth_states with the
sealed PKCE (S256) verifier, the workspace, the user, the product and a
10-minute expiry, and the value Google echoes. The callback carries no
X-Workspace-ID, so it re-derives the workspace from that row, and it must come
back to the same signed-in user, who must still be allowed to manage
integrations there.

The callback refuses the grant, and revokes it, when the refresh token is
missing or a scope this product needs was unticked. "Revokes" has one
exception: Google revokes the whole grant for (account, client), so a token is
not revoked while that Google account is still connected somewhere in Moharrik,
where revoking it would silently disconnect the existing connection.

# Tokens

The refresh token is sealed with internal/secret (moharrik/google-refresh/v1)
and never selected by a read path, returned or logged. Access tokens live in
memory only, per connection (token.go). A rotated refresh token is re-sealed;
invalid_grant marks the connection 'reauth'. Disconnect revokes at
oauth2.googleapis.com/revoke with the token in the POST body, then deletes.

# Env

	GOOGLE_INTEGRATIONS_CLIENT_ID / _SECRET   the OAuth web client. Deliberately
	    NOT OAUTH_GOOGLE_*: togo's auth-oauth plugin shows a "Sign in with Google"
	    button whenever those are set, with a callback that is not registered.
	GOOGLE_INTEGRATIONS_REDIRECT_URL          full callback URL; default
	    PUBLIC_URL (else APP_URL) + CallbackPath
	GOOGLE_API_BASE                           DEV/TEST ONLY: one origin standing
	    in for every Google endpoint (a fake server). Refused in production.
*/
package google

import (
	"errors"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/secret"
)

const (
	// CallbackPath is the redirect URI's path. The full URL must be registered
	// on the OAuth client in the Google Cloud console.
	CallbackPath = "/api/google/oauth/callback"

	cookieName = "moh_google_state"
	cookiePath = "/api/google/oauth"
	stateTTL   = 10 * time.Minute

	httpTimeout = 20 * time.Second
)

// Scopes, as Google spells them in a token response.
const (
	ScopeOpenID       = "openid"
	ScopeEmail        = "email"
	ScopeAnalytics    = "https://www.googleapis.com/auth/analytics.readonly"
	ScopeWebmasters   = "https://www.googleapis.com/auth/webmasters"
	ScopeWebmastersRO = "https://www.googleapis.com/auth/webmasters.readonly"
	ScopeYouTube      = "https://www.googleapis.com/auth/youtube.readonly"
	ScopeYTAnalytics  = "https://www.googleapis.com/auth/yt-analytics.readonly"
	ScopeYTMonetary   = "https://www.googleapis.com/auth/yt-analytics-monetary.readonly"
	// ScopeContacts / ScopeContactsOther (MH-585): the People API's own
	// contacts, and "Other contacts" (people Google inferred from mail/chat
	// history but the user never explicitly saved). Deliberately NOT
	// contacts (read/write) or contacts.readonly's write sibling: this is an
	// import feature, never a write-back to the person's Google account.
	ScopeContacts      = "https://www.googleapis.com/auth/contacts.readonly"
	ScopeContactsOther = "https://www.googleapis.com/auth/contacts.other.readonly"
)

// Product is what a connect is for.
type Product string

const (
	Analytics     Product = "analytics"
	SearchConsole Product = "search_console"
	YouTube       Product = "youtube"
	// Contacts (MH-585): import from Google Contacts (People API). Unlike
	// the other three, it has no "resource" to pick (no property/site/
	// channel) — one connection is the whole account's address book — so it
	// never appears in internal/google/resources.go's Kind list.
	Contacts Product = "contacts"
)

// Products lists every product, in the order the page shows them.
var Products = []Product{Analytics, SearchConsole, YouTube, Contacts}

// ParseProduct validates a product name.
func ParseProduct(s string) (Product, bool) {
	for _, p := range Products {
		if string(p) == s {
			return p, true
		}
	}
	return "", false
}

// Scopes is what a connect for p must end up granted. monetary adds YouTube
// revenue reporting and means nothing for the other products.
func (p Product) Scopes(monetary bool) []string {
	switch p {
	case Analytics:
		return []string{ScopeAnalytics}
	case SearchConsole:
		return []string{ScopeWebmasters}
	case YouTube:
		if monetary {
			return []string{ScopeYouTube, ScopeYTAnalytics, ScopeYTMonetary}
		}
		return []string{ScopeYouTube, ScopeYTAnalytics}
	case Contacts:
		// monetary has no meaning here; reused as "also request Other
		// contacts" so Contacts.Scopes keeps the same one-bool shape as
		// every other product instead of a bespoke signature.
		if monetary {
			return []string{ScopeContacts, ScopeContactsOther}
		}
		return []string{ScopeContacts}
	}
	return nil
}

// productScopes are the scopes a product can need: what a reconnect carries over.
var productScopes = map[string]bool{
	ScopeAnalytics: true, ScopeWebmasters: true, ScopeWebmastersRO: true,
	ScopeYouTube: true, ScopeYTAnalytics: true, ScopeYTMonetary: true,
	ScopeContacts: true, ScopeContactsOther: true,
}

// ProductsOf derives what a connection can serve from what Google granted.
// It is the only place that decides it: never from what was asked for.
func ProductsOf(granted []string) []Product {
	has := set(granted)
	out := []Product{}
	if has[ScopeAnalytics] {
		out = append(out, Analytics)
	}
	if has[ScopeWebmasters] || has[ScopeWebmastersRO] {
		out = append(out, SearchConsole)
	}
	if has[ScopeYouTube] && has[ScopeYTAnalytics] {
		out = append(out, YouTube)
	}
	if has[ScopeContacts] {
		out = append(out, Contacts)
	}
	return out
}

func set(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, s := range list {
		m[s] = true
	}
	return m
}

func hasAll(have []string, want ...string) bool {
	m := set(have)
	for _, w := range want {
		if !m[w] {
			return false
		}
	}
	return true
}

func sortedUnique(list []string) []string {
	m := set(list)
	out := make([]string, 0, len(m))
	for s := range m {
		if s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// Errors the HTTP layer and later cards map to answers.
var (
	ErrNotConfigured = errors.New("Google is not configured on this server (GOOGLE_INTEGRATIONS_CLIENT_ID / GOOGLE_INTEGRATIONS_CLIENT_SECRET)")
	ErrNotConnected  = errors.New("that Google connection does not exist in this workspace")
	ErrReauth        = errors.New("Google refused the stored grant — reconnect the account")
	ErrScope         = errors.New("this Google account has not granted the access this needs — reconnect it for that product")
)

// The two boxes this package seals with. Separate domains, so a sealed
// verifier can never stand in for a sealed refresh token or the reverse.
var (
	refreshBox  = secret.New(secret.DomainGoogleRefresh, "the Google grant", "reconnect the account")
	verifierBox = secret.New("moharrik/google-oauth-verifier/v1", "the sign-in attempt", "start the connect again")
)

// ---- endpoints ---------------------------------------------------------------

// Endpoints are Google's URLs; tests and the dev fake replace them.
type Endpoints struct {
	AuthURL     string
	TokenURL    string
	RevokeURL   string
	UserInfoURL string
}

// Real is Google's own set.
var Real = Endpoints{
	AuthURL:     "https://accounts.google.com/o/oauth2/v2/auth",
	TokenURL:    "https://oauth2.googleapis.com/token",
	RevokeURL:   "https://oauth2.googleapis.com/revoke",
	UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo",
}

// At is the endpoint set a fake Google at base serves (googletest).
func At(base string) Endpoints {
	base = strings.TrimRight(base, "/")
	return Endpoints{
		AuthURL:     base + "/o/oauth2/v2/auth",
		TokenURL:    base + "/token",
		RevokeURL:   base + "/revoke",
		UserInfoURL: base + "/v1/userinfo",
	}
}

var (
	endpointsMu sync.RWMutex
	endpoints   *Endpoints
)

// SetEndpoints replaces the endpoints (tests). nil restores the default.
func SetEndpoints(e *Endpoints) {
	endpointsMu.Lock()
	endpoints = e
	endpointsMu.Unlock()
	resetCache()
}

func current() Endpoints {
	endpointsMu.RLock()
	e := endpoints
	endpointsMu.RUnlock()
	if e != nil {
		return *e
	}
	if base := strings.TrimSpace(os.Getenv("GOOGLE_API_BASE")); base != "" && !Production() {
		return At(base)
	}
	return Real
}

// FakeRefused reports whether GOOGLE_API_BASE is set but ignored because this
// looks like production. Boot logs it, so a misconfiguration is visible.
func FakeRefused() bool {
	return strings.TrimSpace(os.Getenv("GOOGLE_API_BASE")) != "" && Production()
}

// Production is true unless this is plainly a local process: the fake-Google
// override is honoured only when no *_ENV says production and APP_URL is on
// localhost. Both, because either alone has been wrong somewhere.
func Production() bool {
	for _, k := range []string{"APP_ENV", "TOGO_ENV", "ENV"} {
		if v := strings.ToLower(strings.TrimSpace(os.Getenv(k))); v == "production" || v == "prod" {
			return true
		}
	}
	u, err := url.Parse(strings.TrimSpace(os.Getenv("APP_URL")))
	if err != nil {
		return true
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return false
	}
	return true
}

// ---- configuration -------------------------------------------------------------

// Config is the OAuth client.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// LoadConfig reads the environment. Nothing here has a default host.
func LoadConfig() Config {
	return Config{
		ClientID:     strings.TrimSpace(os.Getenv("GOOGLE_INTEGRATIONS_CLIENT_ID")),
		ClientSecret: strings.TrimSpace(os.Getenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET")),
		RedirectURL:  RedirectURL(),
	}
}

// RedirectURL is the callback URL Google sends the browser back to:
// GOOGLE_INTEGRATIONS_REDIRECT_URL, else PUBLIC_URL (else APP_URL) +
// CallbackPath. Empty when none is set — never a guessed localhost.
func RedirectURL() string {
	if v := strings.TrimSpace(os.Getenv("GOOGLE_INTEGRATIONS_REDIRECT_URL")); v != "" {
		return v
	}
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v + CallbackPath
		}
	}
	return ""
}

// Ready reports whether a connect can complete.
func (c Config) Ready() bool { return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != "" }
