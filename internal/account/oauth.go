package account

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/appsignin"
	"github.com/fadymondy/moharrik/internal/platform"
)

/*
The browser half of Sign in with Google and Sign in with Apple, for the web
and for the apps:

	GET /api/auth/oauth/{google|apple}?next=/en/w/acme          the web
	GET /api/auth/oauth/{google|apple}?redirect_uri=moharrik://auth/callback
	    &code_challenge=…&state=…&device=…                      an app

The start remembers the attempt (auth_oauth_states, by the hash of the state it
sends the provider) and puts the state in an HttpOnly cookie, so the callback
only completes in the browser that began it. The callback checks the cookie,
trades the code at the provider, checks the ID token's nonce, links the account
(resolve) and then either starts the web session and goes to `next`, or hands
the app a one-time code on its own redirect (appsignin.IssueCode) — the same
code the app trades after a password sign-in in the browser.
*/

const (
	stateCookie = "mh_oauth"
	stateTTL    = 10 * time.Minute
)

// Why a provider sign-in failed, as codes the login page and the apps show in
// the person's language (web: auth.oauthError.<code>).
const (
	codeFailed    = "failed"
	codeCancelled = "cancelled"
	codeExpired   = "expired"
	codeSuspended = "account_suspended"
	codeOff       = "unavailable"
)

type attempt struct {
	provider, nonce, verifier, next  string
	appRedirect, appChallenge, appSt string
	device                           string
}

func (s *Service) configured(provider string) bool {
	switch provider {
	case "google":
		return s.p.Google != nil
	case "apple":
		return s.p.AppleWeb()
	}
	return false
}

func secure() bool { return strings.HasPrefix(appURL(), "https://") }

func (s *Service) setStateCookie(w http.ResponseWriter, value string, maxAge int) {
	c := &http.Cookie{Name: stateCookie, Value: value, Path: "/api/auth/oauth/", HttpOnly: true, MaxAge: maxAge}
	// Apple's callback is a cross-site form POST, which a Lax cookie does not
	// ride on. None requires Secure, so plain-http development stays Lax
	// (Apple will not redirect to http anyway).
	if secure() {
		c.Secure, c.SameSite = true, http.SameSiteNoneMode
	} else {
		c.SameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, c)
}

func (s *Service) start(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	q := r.URL.Query()
	at := attempt{provider: provider}
	if ru := q.Get("redirect_uri"); ru != "" {
		if !appsignin.ValidRedirect(ru) || len(q.Get("code_challenge")) < 43 || len(q.Get("code_challenge")) > 128 ||
			len(q.Get("state")) < 16 || len(q.Get("state")) > 128 {
			http.Error(w, "that is not a Moharrik app's sign-in", http.StatusBadRequest)
			return
		}
		at.appRedirect, at.appChallenge, at.appSt = ru, q.Get("code_challenge"), q.Get("state")
		at.device = q.Get("device")
		if len(at.device) > 200 {
			at.device = at.device[:200]
		}
	} else if n := q.Get("next"); safePath(n) {
		at.next = n
	}
	if !s.configured(provider) {
		s.fail(w, r, at, codeOff)
		return
	}
	if !allow("oauth-start:"+clientBucket(r), 60, 15*time.Minute) {
		http.Error(w, "too many sign-in attempts — wait a few minutes", http.StatusTooManyRequests)
		return
	}

	state, nonce := randHex(24), randHex(16)
	if provider == "google" {
		at.verifier = randHex(32)
	}
	ctx := r.Context()
	db := s.a.SysDB
	_, _ = db.ExecContext(ctx, `DELETE FROM auth_oauth_states WHERE expires_at < now()`)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO auth_oauth_states (state_hash, provider, nonce, verifier, next, app_redirect, app_challenge, app_state, device, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		sha256Hex(state), provider, nonce, at.verifier, at.next, at.appRedirect, at.appChallenge, at.appSt, at.device,
		time.Now().Add(stateTTL)); err != nil {
		s.a.Log.Error("account: oauth start", "err", err)
		s.fail(w, r, at, codeFailed)
		return
	}
	s.setStateCookie(w, state, int(stateTTL.Seconds()))

	v := url.Values{
		"response_type": {"code"},
		"redirect_uri":  {callbackURL(provider)},
		"state":         {state},
		"nonce":         {nonce},
	}
	var dest string
	switch provider {
	case "google":
		sum := sha256.Sum256([]byte(at.verifier))
		v.Set("client_id", s.p.Google.ClientID)
		v.Set("scope", "openid email profile")
		v.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
		v.Set("code_challenge_method", "S256")
		v.Set("prompt", "select_account")
		dest = Endpoints.GoogleAuth
	case "apple":
		v.Set("client_id", s.p.Apple.ServicesID)
		v.Set("scope", "name email")
		v.Set("response_mode", "form_post")
		dest = Endpoints.AppleAuth
	}
	http.Redirect(w, r, dest+"?"+v.Encode(), http.StatusFound)
}

func (s *Service) callback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	_ = r.ParseForm() // query (Google) and form body (Apple) alike
	state := r.Form.Get("state")
	c, _ := r.Cookie(stateCookie)
	s.setStateCookie(w, "", -1) // single use, whatever happens next

	ctx := r.Context()
	at, err := s.takeAttempt(ctx, provider, state)
	if err != nil {
		s.fail(w, r, attempt{}, codeExpired)
		return
	}
	if c == nil || c.Value == "" || c.Value != state {
		// Not the browser that started it: a forged or replayed callback.
		s.fail(w, r, at, codeExpired)
		return
	}
	if e := r.Form.Get("error"); e != "" {
		code := codeFailed
		if e == "access_denied" || e == "user_cancelled_authorize" {
			code = codeCancelled
		}
		s.fail(w, r, at, code)
		return
	}
	if !s.configured(provider) {
		s.fail(w, r, at, codeOff)
		return
	}

	l := login{Provider: provider}
	var cl claims
	switch provider {
	case "google":
		cl, err = exchangeGoogle(ctx, s.p.Google, r.Form.Get("code"), at.verifier, callbackURL(provider))
	case "apple":
		cl, l.RefreshToken, err = exchangeApple(ctx, s.p.Apple, s.p.Apple.ServicesID, r.Form.Get("code"), callbackURL(provider))
		l.ClientID = s.p.Apple.ServicesID
	}
	if err == nil && cl.Nonce != at.nonce {
		err = errors.New("the sign-in token is for another attempt")
	}
	if err != nil {
		s.a.Log.Warn("account: provider sign-in refused", "provider", provider, "err", err)
		s.fail(w, r, at, codeFailed)
		return
	}
	l.Subject, l.Email, l.Verified = cl.Subject, cl.Email, cl.Verified

	userID, _, err := resolve(ctx, s.a.SysDB, l)
	if errors.Is(err, ErrNoEmail) || errors.Is(err, ErrUnverified) {
		s.fail(w, r, at, err.Error())
		return
	}
	if err != nil {
		s.a.Log.Error("account: link provider sign-in", "provider", provider, "err", err)
		s.fail(w, r, at, codeFailed)
		return
	}
	platform.Invalidate(userID)
	if u, err := platform.Load(ctx, s.a.SysDB, userID); err != nil {
		s.fail(w, r, at, codeFailed)
		return
	} else if u.Suspended() {
		s.fail(w, r, at, codeSuspended)
		return
	}

	if at.appRedirect != "" {
		dest, err := appsignin.IssueCode(ctx, s.a.SysDB, userID, at.appRedirect, at.appChallenge, at.appSt, at.device)
		if err != nil {
			s.a.Log.Error("account: app code after provider sign-in", "err", err)
			s.fail(w, r, at, codeFailed)
			return
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}

	ident, err := s.auth.Guard("").Auth.ByID(ctx, userID)
	if err == nil {
		_, err = s.auth.IssueSession(w, *ident)
	}
	if err != nil {
		s.a.Log.Error("account: web session after provider sign-in", "err", err)
		s.fail(w, r, at, codeFailed)
		return
	}
	next := at.next
	if next == "" {
		next = afterLogin()
	}
	// 303: Apple's callback is a POST, and the landing page must be a GET.
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// takeAttempt loads and deletes the attempt the state names.
func (s *Service) takeAttempt(ctx context.Context, provider, state string) (attempt, error) {
	if state == "" {
		return attempt{}, sql.ErrNoRows
	}
	at := attempt{provider: provider}
	err := s.a.SysDB.QueryRowContext(ctx, `
		DELETE FROM auth_oauth_states WHERE state_hash = $1 AND provider = $2 AND expires_at > now()
		RETURNING nonce, verifier, next, app_redirect, app_challenge, app_state, device`,
		sha256Hex(state), provider).Scan(&at.nonce, &at.verifier, &at.next, &at.appRedirect, &at.appChallenge, &at.appSt, &at.device)
	return at, err
}

// fail sends the browser back with a code: to the app's redirect when an app
// started this, else to the login page.
func (s *Service) fail(w http.ResponseWriter, r *http.Request, at attempt, code string) {
	if at.appRedirect != "" && appsignin.ValidRedirect(at.appRedirect) {
		dest, _ := url.Parse(at.appRedirect)
		dest.RawQuery = url.Values{"error": {code}, "state": {at.appSt}}.Encode()
		http.Redirect(w, r, dest.String(), http.StatusSeeOther)
		return
	}
	q := url.Values{"oauth_error": {code}}
	if at.next != "" {
		q.Set("next", at.next)
	}
	http.Redirect(w, r, "/login?"+q.Encode(), http.StatusSeeOther)
}
