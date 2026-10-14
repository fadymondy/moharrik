package social

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"
)

/*
The web flow.

	start     GET  /api/auth/oauth/{provider}  a state row (server-side), a browser-binding cookie,
	                                           and a redirect to the provider
	callback  GET  .../google/callback          Google: a top-level GET, so the binding cookie comes
	                                           along and the session is set right there
	          POST .../apple/callback           Apple (response_mode=form_post): a CROSS-SITE POST.
	                                           SameSite=Lax cookies are not sent on it, so the state
	                                           is checked against the server-side row, the code is
	                                           exchanged and the person resolved, and the browser is
	                                           sent (303) to …
	finish    GET  .../apple/finish?t=…         a top-level GET again: the binding cookie must match
	                                           the browser that started, and only then is the session
	                                           cookie set. A finish link sent to someone else's
	                                           browser does nothing.
*/

const (
	stateTTL      = 10 * time.Minute
	bindingCookie = "moharrik_social"
	maxNext       = 1024
)

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkce(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// SafeNext keeps a post-sign-in destination on this site: a local path only ("/x", never
// "//host", "/\host" or a scheme). Anything else is "".
func SafeNext(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxNext || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, "/\\") {
		return ""
	}
	if strings.ContainsAny(v, "\r\n\t") {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return v
}

// landing is where a sign-in with no (valid) next goes: OAUTH_AFTER_LOGIN, else the web's home.
func landing() string {
	if v := SafeNext(os.Getenv("OAUTH_AFTER_LOGIN")); v != "" {
		return v
	}
	return "/"
}

func secureCookie(c Config) bool {
	if v := os.Getenv("COOKIE_SECURE"); v != "" {
		return v == "1" || v == "true"
	}
	return strings.HasPrefix(c.AppURL, "https://")
}

// sessionUser is the Moharrik user whose valid session r carries, or "".
func (s *Service) sessionUser(r *http.Request) string {
	tok := ""
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		tok = c.Value
	}
	if tok == "" {
		return ""
	}
	if strings.Count(tok, ".") != 2 {
		// A server-side session driver keeps an id in the cookie.
		var jwt string
		if s.DB.QueryRowContext(r.Context(), `SELECT token FROM auth_sessions WHERE sid = $1`, tok).Scan(&jwt) != nil {
			return ""
		}
		tok = jwt
	}
	if id, err := s.Auth.Verify(tok); err == nil && id != nil {
		return id.ID
	}
	return ""
}

// Mount registers the web routes on the kernel router.
func (s *Service) Mount(r chi.Router) {
	r.Get("/api/auth/oauth/{provider}", s.start)
	r.Get("/api/auth/oauth/google/callback", s.googleCallback)
	r.Post("/api/auth/oauth/apple/callback", s.appleCallback)
	r.Get("/api/auth/oauth/apple/finish", s.appleFinish)
}

// errorPage is where a refusal lands: the login page for a sign-in, the page that asked for a
// link or a re-authentication otherwise, with ?social_error=<code>&provider=<p>.
func errorPage(intent Intent, next, provider, code string) string {
	base := "/login"
	if intent != IntentLogin && next != "" {
		base = next
	} else if next != "" {
		base = "/login?next=" + url.QueryEscape(next)
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "social_error=" + url.QueryEscape(code) + "&provider=" + url.QueryEscape(provider)
}

func (s *Service) start(w http.ResponseWriter, r *http.Request) {
	c := s.Env()
	provider := chi.URLParam(r, "provider")
	q := r.URL.Query()
	next := SafeNext(q.Get("next"))
	intent := Intent(q.Get("intent"))
	if intent != IntentLink && intent != IntentReauth {
		intent = IntentLogin
	}
	switch provider {
	case "google":
		if !c.GoogleWeb() {
			http.Redirect(w, r, errorPage(intent, next, provider, ErrNotAvailable), http.StatusFound)
			return
		}
	case "apple":
		if !c.AppleWeb() {
			http.Redirect(w, r, errorPage(intent, next, provider, ErrNotAvailable), http.StatusFound)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	user := s.sessionUser(r)
	if intent != IntentLogin && user == "" {
		http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusFound)
		return
	}

	state, binding, nonce, verifier := randToken(24), randToken(24), randToken(24), ""
	if provider == "google" {
		verifier = randToken(48)
	}
	if _, err := s.DB.ExecContext(r.Context(), `
		INSERT INTO social_login_states (state_hash, provider, browser_hash, nonce, code_verifier, next_path, intent, signed_in_user, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		sha256Hex(state), provider, sha256Hex(binding), nonce, verifier, next, string(intent), user, time.Now().Add(stateTTL)); err != nil {
		s.Log.Error("social: saving the sign-in state", "err", err)
		http.Redirect(w, r, errorPage(intent, next, provider, ErrUnavailable), http.StatusFound)
		return
	}
	_, _ = s.DB.ExecContext(r.Context(), `DELETE FROM social_login_states WHERE expires_at < now() - interval '1 hour'`)
	http.SetCookie(w, &http.Cookie{Name: bindingCookie, Value: binding, Path: "/api/auth/oauth", HttpOnly: true,
		Secure: secureCookie(c), SameSite: http.SameSiteLaxMode, MaxAge: int(stateTTL.Seconds())})

	var dest string
	switch provider {
	case "google":
		v := url.Values{
			"client_id": {c.GoogleClientID}, "redirect_uri": {c.redirectURI("google")}, "response_type": {"code"},
			"scope": {"openid email profile"}, "state": {state}, "nonce": {nonce},
			"code_challenge": {pkce(verifier)}, "code_challenge_method": {"S256"}, "prompt": {"select_account"},
		}
		dest = Endpoints.GoogleAuth + "?" + v.Encode()
	case "apple":
		v := url.Values{
			"client_id": {c.AppleServicesID}, "redirect_uri": {c.redirectURI("apple")}, "response_type": {"code"},
			"response_mode": {"form_post"}, "scope": {"name email"}, "state": {state}, "nonce": {nonce},
		}
		dest = Endpoints.AppleAuth + "?" + v.Encode()
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

type loginState struct {
	provider, browserHash, nonce, verifier, next, sessionUser, resultUser, resultError string
	intent                                                                             Intent
	expires                                                                            time.Time
}

const stateCols = `provider, browser_hash, nonce, code_verifier, next_path, intent, signed_in_user, result_user, result_error, expires_at`

func scanState(row *sql.Row) (*loginState, error) {
	var st loginState
	var intent string
	if err := row.Scan(&st.provider, &st.browserHash, &st.nonce, &st.verifier, &st.next, &intent, &st.sessionUser, &st.resultUser, &st.resultError, &st.expires); err != nil {
		return nil, err
	}
	st.intent = Intent(intent)
	if time.Now().After(st.expires) {
		return nil, sql.ErrNoRows
	}
	return &st, nil
}

// takeState consumes a state row by its state value (single use).
func (s *Service) takeState(ctx context.Context, provider, state string) (*loginState, error) {
	if state == "" {
		return nil, sql.ErrNoRows
	}
	return scanState(s.DB.QueryRowContext(ctx, `DELETE FROM social_login_states WHERE state_hash = $1 AND provider = $2 AND finish_hash IS NULL RETURNING `+stateCols,
		sha256Hex(state), provider))
}

func (s *Service) boundToThisBrowser(r *http.Request, st *loginState) bool {
	c, err := r.Cookie(bindingCookie)
	return err == nil && c.Value != "" && sha256Hex(c.Value) == st.browserHash
}

func clearBinding(w http.ResponseWriter, c Config) {
	http.SetCookie(w, &http.Cookie{Name: bindingCookie, Value: "", Path: "/api/auth/oauth", HttpOnly: true,
		Secure: secureCookie(c), SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// complete starts the session (a sign-in) and sends the browser on.
func (s *Service) complete(w http.ResponseWriter, r *http.Request, st *loginState, userID string) {
	c := s.Env()
	clearBinding(w, c)
	next := st.next
	switch st.intent {
	case IntentLogin:
		ident, err := s.identityOf(r.Context(), userID)
		if err == nil {
			_, err = s.Auth.IssueSession(w, *ident)
		}
		if err != nil {
			s.Log.Error("social: starting the session", "err", err)
			http.Redirect(w, r, errorPage(st.intent, next, st.provider, ErrUnavailable), http.StatusFound)
			return
		}
		if next == "" {
			next = landing()
		}
	case IntentReauth:
		// The proof is the fresh session: a new one is issued, so its issue time is now
		// (account.go: fresh()).
		ident, err := s.identityOf(r.Context(), userID)
		if err == nil {
			_, err = s.Auth.IssueSession(w, *ident)
		}
		if err != nil {
			http.Redirect(w, r, errorPage(st.intent, next, st.provider, ErrUnavailable), http.StatusFound)
			return
		}
		next = withParam(orDefault(next, "/"), "reauth", st.provider)
	case IntentLink:
		next = withParam(orDefault(next, "/"), "linked", st.provider)
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func withParam(path, k, v string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + k + "=" + url.QueryEscape(v)
}

func (s *Service) googleCallback(w http.ResponseWriter, r *http.Request) {
	c := s.Env()
	q := r.URL.Query()
	st, err := s.takeState(r.Context(), "google", q.Get("state"))
	if err != nil || !s.boundToThisBrowser(r, st) {
		http.Redirect(w, r, errorPage(IntentLogin, "", "google", ErrFailed), http.StatusFound)
		return
	}
	if e := q.Get("error"); e != "" {
		code := ErrFailed
		if e == "access_denied" {
			code = ErrCancelled
		}
		http.Redirect(w, r, errorPage(st.intent, st.next, "google", code), http.StatusFound)
		return
	}
	p, err := googleExchange(r.Context(), c, q.Get("code"), st.verifier, st.nonce)
	if err != nil {
		s.Log.Warn("social: google sign-in failed", "err", err)
		http.Redirect(w, r, errorPage(st.intent, st.next, "google", ErrFailed), http.StatusFound)
		return
	}
	uid, ref := s.Resolve(r.Context(), p, st.intent, st.sessionUser)
	if ref != nil {
		s.Log.Warn("social: google sign-in refused", "code", ref.Code, "err", ref.Err)
		http.Redirect(w, r, errorPage(st.intent, st.next, "google", ref.Code), http.StatusFound)
		return
	}
	s.complete(w, r, st, uid)
}

// appleUser is the `user` form field Apple posts on the first consent only.
type appleUser struct {
	Name struct {
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
	} `json:"name"`
}

func (u appleUser) full() string {
	return strings.TrimSpace(strings.TrimSpace(u.Name.FirstName) + " " + strings.TrimSpace(u.Name.LastName))
}

func (s *Service) appleCallback(w http.ResponseWriter, r *http.Request) {
	c := s.Env()
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// The state is looked up but not consumed yet: it becomes the finish ticket.
	state := r.PostForm.Get("state")
	var st *loginState
	var err error
	if state != "" {
		st, err = scanState(s.DB.QueryRowContext(r.Context(), `SELECT `+stateCols+` FROM social_login_states WHERE state_hash = $1 AND provider = 'apple' AND finish_hash IS NULL`, sha256Hex(state)))
	}
	if state == "" || err != nil {
		http.Redirect(w, r, errorPage(IntentLogin, "", "apple", ErrFailed), http.StatusSeeOther)
		return
	}
	result, failure := "", ""
	if e := r.PostForm.Get("error"); e != "" {
		failure = ErrFailed
		if e == "user_cancelled_authorize" {
			failure = ErrCancelled
		}
	} else {
		res, err := appleExchange(r.Context(), c, c.AppleServicesID, r.PostForm.Get("code"), c.redirectURI("apple"), st.nonce)
		if err != nil {
			s.Log.Warn("social: apple sign-in failed", "err", err)
			failure = ErrFailed
		} else {
			var u appleUser
			if raw := r.PostForm.Get("user"); raw != "" {
				_ = json.Unmarshal([]byte(raw), &u)
			}
			res.Person.Name = u.full()
			uid, ref := s.Resolve(r.Context(), res.Person, st.intent, st.sessionUser)
			if ref != nil {
				s.Log.Warn("social: apple sign-in refused", "code", ref.Code, "err", ref.Err)
				failure = ref.Code
			} else {
				result = uid
				s.keepAppleToken(r.Context(), res.Person.Subject, res.ClientID, res.RefreshToken)
			}
		}
	}
	ticket := randToken(24)
	if _, err := s.DB.ExecContext(r.Context(), `
		UPDATE social_login_states SET finish_hash = $2, result_user = $3, result_error = $4, expires_at = now() + interval '2 minutes'
		WHERE state_hash = $1 AND finish_hash IS NULL`, sha256Hex(state), sha256Hex(ticket), result, failure); err != nil {
		http.Redirect(w, r, errorPage(st.intent, st.next, "apple", ErrUnavailable), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/api/auth/oauth/apple/finish?t="+url.QueryEscape(ticket), http.StatusSeeOther)
}

func (s *Service) appleFinish(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	if t == "" {
		http.Redirect(w, r, errorPage(IntentLogin, "", "apple", ErrFailed), http.StatusFound)
		return
	}
	// Looked up first, consumed only by the browser that started: a ticket opened elsewhere must
	// not burn the real one.
	st, err := scanState(s.DB.QueryRowContext(r.Context(), `SELECT `+stateCols+` FROM social_login_states WHERE finish_hash = $1`, sha256Hex(t)))
	if err != nil || !s.boundToThisBrowser(r, st) {
		http.Redirect(w, r, errorPage(IntentLogin, "", "apple", ErrFailed), http.StatusFound)
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM social_login_states WHERE finish_hash = $1`, sha256Hex(t))
	if n, _ := res.RowsAffected(); err != nil || n != 1 {
		http.Redirect(w, r, errorPage(IntentLogin, "", "apple", ErrFailed), http.StatusFound)
		return
	}
	if st.resultError != "" || st.resultUser == "" {
		clearBinding(w, s.Env())
		http.Redirect(w, r, errorPage(st.intent, st.next, "apple", orDefault(st.resultError, ErrFailed)), http.StatusFound)
		return
	}
	s.complete(w, r, st, st.resultUser)
}

