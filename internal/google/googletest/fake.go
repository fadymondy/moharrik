/*
Package googletest is a fake Google for MH-439: OAuth consent, the token
endpoint (authorization_code and refresh_token), revoke and userinfo. Ported
from fadymondy.com's internal/youtube/youtubetest, and made multi-account.

It is used by the package tests and by the dev-stack browser check
(./fakegoogle), so no test or check ever calls Google. It is strict where the
real one is strict and where this app's safety depends on it: the PKCE
verifier must hash to the challenge sent at consent, codes are single use, the
redirect URI and client credentials must match, a token in a revoke URL is
refused, and revoking any token revokes the whole grant of that (account,
client), which is what Google does.

The app points at it with GOOGLE_API_BASE, which internal/google refuses in
production.
*/
package googletest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Account is a Google account the fake signs in as.
type Account struct {
	Sub   string
	Email string
}

type codeGrant struct {
	challenge, redirect, scope string
	account                    Account
}

type refreshGrant struct {
	account Account
	scope   string
	live    bool
}

type accessGrant struct {
	account Account
	expires time.Time
}

// Server is the fake.
type Server struct {
	*httptest.Server

	ClientID, ClientSecret string

	mu sync.Mutex
	// Account is who the next consent signs in as.
	Account Account
	// Interactive shows a consent page (account + one checkbox per scope)
	// instead of answering at once. The browser check uses it; tests do not.
	Interactive bool
	// Behaviour switches.
	Deny          bool     // consent answers error=access_denied
	OmitRefresh   bool     // the code exchange returns no refresh token
	OmitIDToken   bool     // the code exchange returns no id_token (userinfo answers instead)
	Untick        []string // scopes the person "unticks" at consent
	RefreshFails  bool     // every refresh answers 400 invalid_grant
	RotateRefresh bool     // a refresh returns a NEW refresh token and kills the old one
	TokenTTL      int      // seconds; default 3600

	codes   map[string]codeGrant
	access  map[string]accessGrant
	refresh map[string]*refreshGrant
	granted map[string]map[string]bool // sub -> scopes granted to this client (include_granted_scopes)
	revoked []string
	calls   map[string]int
	// The data APIs (data.go): what each account can see, injected failures.
	data          map[string]*AccountData
	failures      map[string]*failSpec
	AdminPageSize int // accountSummaries per page; default 2, to exercise paging
	// DataFor, when set, picks an account's data the first time it is seen
	// (the dev check: "many@…" sees several of each). nil or a nil answer: Demo.
	DataFor  func(Account) *AccountData
	lastAuth url.Values
	lastForm map[string]url.Values
}

// New starts a fake Google on a random local port.
func New() *Server {
	s, h := build()
	s.Server = httptest.NewServer(h)
	return s
}

// NewAt is New listening on a fixed address (the dev check).
func NewAt(addr string) (*Server, error) {
	s, h := build()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := httptest.NewUnstartedServer(h)
	_ = srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	s.Server = srv
	return s, nil
}

func build() (*Server, http.Handler) {
	s := &Server{
		ClientID: "fake-client.apps.googleusercontent.com", ClientSecret: "fake-secret",
		Account: Account{Sub: "100000000000000000001", Email: "brand@example.test"},
		codes:   map[string]codeGrant{}, access: map[string]accessGrant{}, refresh: map[string]*refreshGrant{},
		granted: map[string]map[string]bool{}, calls: map[string]int{}, lastForm: map[string]url.Values{},
		data: map[string]*AccountData{}, failures: map[string]*failSpec{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/o/oauth2/v2/auth", s.authorize)
	mux.HandleFunc("/token", s.token)
	mux.HandleFunc("/revoke", s.revoke)
	mux.HandleFunc("/v1/userinfo", s.userinfo)
	mux.HandleFunc("/", s.api) // every data API (data.go)
	return s, mux
}

func token(prefix string) string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func (s *Server) count(name string) {
	s.mu.Lock()
	s.calls[name]++
	s.mu.Unlock()
}

// Calls is how many times an endpoint was hit ("token:authorization_code",
// "token:refresh_token", "revoke", "userinfo", and "api:<name>" per data API:
// admin, runReport, sites, searchAnalytics, sitemaps, inspect, channels,
// playlistItems, videos, commentThreads, ytAnalytics).
func (s *Server) Calls(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

// Revoked lists every token revoked, in order.
func (s *Server) Revoked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.revoked...)
}

// LastAuthorize is the query of the last consent request.
func (s *Server) LastAuthorize() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastAuth
}

// LastTokenForm is the last form posted to /token for a grant type.
func (s *Server) LastTokenForm(grant string) url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastForm[grant]
}

// LiveRefreshTokens lists refresh tokens that still work, for an account.
func (s *Server) LiveRefreshTokens(sub string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for t, g := range s.refresh {
		if g.live && g.account.Sub == sub {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// AllTokens is every refresh and access token ever issued (live or not), for
// "no token appears anywhere" assertions.
func (s *Server) AllTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for t := range s.refresh {
		out = append(out, t)
	}
	for t := range s.access {
		out = append(out, t)
	}
	return out
}

// ExpireAccessTokens makes every issued access token stale.
func (s *Server) ExpireAccessTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, g := range s.access {
		g.expires = time.Now().Add(-time.Minute)
		s.access[k] = g
	}
}

// Set runs fn under the fake's lock (to flip switches).
func (s *Server) Set(fn func(s *Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// ---- consent ------------------------------------------------------------------

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		q = r.PostForm
	}
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" {
		http.Error(w, "redirect_uri_mismatch", http.StatusBadRequest)
		return
	}
	if q.Get("client_id") != s.ClientID {
		http.Error(w, "invalid_client", http.StatusUnauthorized)
		return
	}
	s.mu.Lock()
	interactive := s.Interactive
	if r.Method == http.MethodGet {
		s.lastAuth = q
	}
	s.mu.Unlock()

	if interactive && r.Method == http.MethodGet {
		s.consentPage(w, q)
		return
	}

	s.mu.Lock()
	deny, account, untick := s.Deny, s.Account, s.Untick
	s.mu.Unlock()
	scope := q.Get("scope")
	if r.Method == http.MethodPost {
		if q.Get("decision") != "allow" {
			deny = true
		}
		if e := strings.TrimSpace(q.Get("email")); e != "" {
			account = Account{Sub: "sub-" + hexOf(e)[:16], Email: e}
		}
		// Only the ticked boxes are granted.
		scope = strings.Join(q["grant"], " ")
		untick = nil
	}

	back := redirect.Query()
	back.Set("state", q.Get("state"))
	switch {
	case deny:
		back.Set("error", "access_denied")
	case q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		back.Set("error", "invalid_request")
	default:
		kept := []string{}
		drop := map[string]bool{}
		for _, u := range untick {
			drop[u] = true
		}
		for _, sc := range strings.Fields(scope) {
			if !drop[sc] {
				kept = append(kept, sc)
			}
		}
		code := token("code-")
		s.mu.Lock()
		s.codes[code] = codeGrant{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"),
			scope: strings.Join(kept, " "), account: account}
		s.mu.Unlock()
		back.Set("code", code)
	}
	redirect.RawQuery = back.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func hexOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// consentPage is a plain stand-in for Google's: pick the account, untick
// scopes, allow or cancel. Deliberately unstyled — it is not Moharrik's page.
func (s *Server) consentPage(w http.ResponseWriter, q url.Values) {
	s.mu.Lock()
	email := s.Account.Email
	s.mu.Unlock()
	var b strings.Builder
	b.WriteString(`<!doctype html><meta charset="utf-8"><title>Fake Google — consent</title>` +
		`<body style="font-family:sans-serif;max-width:40rem;margin:3rem auto">` +
		`<h1>Fake Google</h1><p>This is the test stand-in for Google's consent screen.</p><form method="post">`)
	for k, vs := range q {
		if k == "scope" {
			continue
		}
		for _, v := range vs {
			fmt.Fprintf(&b, `<input type="hidden" name="%s" value="%s">`, html.EscapeString(k), html.EscapeString(v))
		}
	}
	fmt.Fprintf(&b, `<p><label>Account <input name="email" value="%s" size="32"></label></p>`, html.EscapeString(email))
	for _, sc := range strings.Fields(q.Get("scope")) {
		fmt.Fprintf(&b, `<p><label><input type="checkbox" name="grant" value="%s" checked> %s</label></p>`,
			html.EscapeString(sc), html.EscapeString(sc))
	}
	b.WriteString(`<p><button name="decision" value="allow">Allow</button> <button name="decision" value="deny">Cancel</button></p></form>`)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// Consent performs the consent step for an authorization URL without a
// browser and returns the redirect Google would send the user to.
func (s *Server) Consent(authURL string) (string, error) {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(authURL)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		return "", fmt.Errorf("consent: %d", res.StatusCode)
	}
	return res.Header.Get("Location"), nil
}

// ---- tokens -------------------------------------------------------------------

func (s *Server) idToken(a Account) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	return enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		enc(map[string]any{"iss": "https://accounts.google.com", "aud": s.ClientID, "sub": a.Sub,
			"email": a.Email, "email_verified": true, "iat": now, "exp": now + 3600}) + ".fake-signature"
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	_ = r.ParseForm()
	grant := r.PostForm.Get("grant_type")
	s.count("token:" + grant)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastForm[grant] = r.PostForm
	if r.PostForm.Get("client_id") != s.ClientID || r.PostForm.Get("client_secret") != s.ClientSecret {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	ttl := s.TokenTTL
	if ttl == 0 {
		ttl = 3600
	}
	switch grant {
	case "authorization_code":
		g, ok := s.codes[r.PostForm.Get("code")]
		delete(s.codes, r.PostForm.Get("code")) // single use
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		switch {
		case !ok:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "code_verifier"})
		case r.PostForm.Get("redirect_uri") != g.redirect:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "redirect_uri_mismatch"})
		default:
			// include_granted_scopes: the answer carries everything this
			// account has granted this client, not just this consent.
			if s.granted[g.account.Sub] == nil {
				s.granted[g.account.Sub] = map[string]bool{}
			}
			for _, sc := range strings.Fields(g.scope) {
				s.granted[g.account.Sub][sc] = true
			}
			scope := s.scopeOf(g.account.Sub)
			at := token("ya29.fake-")
			s.access[at] = accessGrant{account: g.account, expires: time.Now().Add(time.Duration(ttl) * time.Second)}
			out := map[string]any{"access_token": at, "expires_in": ttl, "scope": scope, "token_type": "Bearer"}
			if !s.OmitRefresh {
				rt := token("1//fake-")
				s.refresh[rt] = &refreshGrant{account: g.account, scope: scope, live: true}
				out["refresh_token"] = rt
			}
			if !s.OmitIDToken {
				out["id_token"] = s.idToken(g.account)
			}
			writeJSON(w, http.StatusOK, out)
		}
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		g := s.refresh[rt]
		if s.RefreshFails || g == nil || !g.live {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
			return
		}
		at := token("ya29.fake-")
		s.access[at] = accessGrant{account: g.account, expires: time.Now().Add(time.Duration(ttl) * time.Second)}
		out := map[string]any{"access_token": at, "expires_in": ttl, "scope": g.scope, "token_type": "Bearer"}
		if s.RotateRefresh {
			g.live = false
			nrt := token("1//fake-rotated-")
			s.refresh[nrt] = &refreshGrant{account: g.account, scope: g.scope, live: true}
			out["refresh_token"] = nrt
		}
		writeJSON(w, http.StatusOK, out)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
	}
}

func (s *Server) scopeOf(sub string) string {
	var list []string
	for sc := range s.granted[sub] {
		list = append(list, sc)
	}
	sort.Strings(list)
	return strings.Join(list, " ")
}

// revoke kills the whole grant of the token's account, as Google does.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	s.count("revoke")
	if r.URL.Query().Get("token") != "" {
		// The app must never put a token in a URL.
		http.Error(w, "token in query", http.StatusBadRequest)
		return
	}
	_ = r.ParseForm()
	t := r.PostForm.Get("token")
	s.mu.Lock()
	defer s.mu.Unlock()
	var sub string
	if g, ok := s.refresh[t]; ok && g.live {
		sub = g.account.Sub
	} else if g, ok := s.access[t]; ok {
		sub = g.account.Sub
	}
	if sub == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_token"})
		return
	}
	s.revoked = append(s.revoked, t)
	for _, g := range s.refresh {
		if g.account.Sub == sub {
			g.live = false
		}
	}
	for k, g := range s.access {
		if g.account.Sub == sub {
			delete(s.access, k)
		}
	}
	delete(s.granted, sub)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) userinfo(w http.ResponseWriter, r *http.Request) {
	s.count("userinfo")
	at := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	g, ok := s.access[at]
	s.mu.Unlock()
	if !ok || time.Now().After(g.expires) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sub": g.account.Sub, "email": g.account.Email, "email_verified": true})
}
