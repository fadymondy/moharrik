package circlexo

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/oidc"
)

/*
Sign in with CircleXO, next to (not instead of) Moharrik's own login.

Callback rules, in order:
 1. a hub subject already linked to a Moharrik user signs in as that user;
 2. otherwise the hub must vouch for the e-mail (email_verified) — an unverified address is refused;
 3. a Moharrik account with that address is linked — but only one that is "usable" (see
    userByEmail: it has no password), or one whose owner is signed in to Moharrik in this very
    browser with the same address (proof that they hold the account). Moharrik never verifies the
    address of a password account, so anything else is refused (account_conflict): nobody can
    register someone's address with a password and inherit their hub sign-in;
 4. otherwise a passwordless Moharrik account is created and linked.

Moharrik has no user suspension (a suspended workspace membership is handled by tenant.Resolve),
so there is no "suspended" check here. The session is Moharrik's normal one
(auth.IssueSession), so every existing login method keeps working alongside. Moharrik's 2FA is not
asked for: the hub is the identity provider and owns second factors.
*/

// Errors the login page can show, by code (web/lang: auth.circlexo_error.<code>).
const (
	errFailed      = "failed"
	errUnverified  = "email_unverified"
	errUnlinkable  = "account_conflict"
	errNoEmail     = "no_email"
	errNotMember   = "not_member"
	errUnavailable = "unavailable"
)

// loginErr is a refusal with a code for the page.
type loginErr struct {
	code string
	err  error
}

func (e *loginErr) Error() string { return e.code + ": " + e.err.Error() }

func refuse(code, msg string) *loginErr { return &loginErr{code, errors.New(msg)} }

// launchHint is the pseudo local path /sso/circlexo hands the OIDC round trip so the org named by
// the hub's launch link survives it; the callback reads the org slug off it.
const launchHint = "/__cxo"

// Login starts the sign-in (GET /auth/circlexo/login[?return_to=/local/path]).
func (s *Service) Login(w http.ResponseWriter, r *http.Request) { s.RP.Login(w, r) }

// Launch is the hub's app launch URL (GET /sso/circlexo?org=<slug>): the hub sends people here
// from its dashboard. They are signed in at the hub already, so the round trip is silent.
func (s *Service) Launch(w http.ResponseWriter, r *http.Request) {
	q := url.Values{}
	if org := strings.TrimSpace(r.URL.Query().Get("org")); org != "" {
		q.Set("return_to", launchHint+"?org="+url.QueryEscape(org))
	}
	http.Redirect(w, r, LoginPath+"?"+q.Encode(), http.StatusFound)
}

// Callback completes the sign-in (GET /auth/circlexo/callback).
func (s *Service) Callback(w http.ResponseWriter, r *http.Request) {
	login, err := s.RP.Exchange(w, r)
	if err != nil {
		s.Log.Warn("circlexo: sign-in failed", "err", err)
		s.fail(w, r, errFailed)
		return
	}
	to, lerr := s.FinishLoginAs(r.Context(), w, login, s.sessionUser(r))
	if lerr != nil {
		s.Log.Warn("circlexo: sign-in refused", "code", lerr.code, "err", lerr.err)
		s.fail(w, r, lerr.code)
		return
	}
	http.Redirect(w, r, to, http.StatusFound)
}

func (s *Service) fail(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/login?circlexo_error="+url.QueryEscape(code), http.StatusFound)
}

func rawString(c *circlexo.Claims, key string) string {
	if c == nil {
		return ""
	}
	v, _ := c.Raw[key].(string)
	return v
}

func rawBool(c *circlexo.Claims, key string) bool {
	if c == nil {
		return false
	}
	switch v := c.Raw[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// FinishLogin turns a verified hub sign-in into a Moharrik session and says where to land.
func (s *Service) FinishLogin(ctx context.Context, w http.ResponseWriter, l *oidc.Login) (string, *loginErr) {
	return s.FinishLoginAs(ctx, w, l, "")
}

// FinishLoginAs is FinishLogin for a browser that already holds a valid Moharrik session of
// currentUser ("" when it holds none). Only that lets an existing password account be linked.
func (s *Service) FinishLoginAs(ctx context.Context, w http.ResponseWriter, l *oidc.Login, currentUser string) (string, *loginErr) {
	id := l.IDClaims
	if id == nil || id.Subject == "" {
		return "", refuse(errFailed, "no subject")
	}
	userID, lerr := s.linkedUser(ctx, l, currentUser)
	if lerr != nil {
		return "", lerr
	}

	var email string
	if err := s.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		return "", refuse(errFailed, "the linked account no longer exists")
	}

	to, lerr := s.landing(ctx, l, userID)
	if lerr != nil {
		return "", lerr
	}

	ident, err := s.Auth.FindOrCreateByEmail(ctx, email)
	if err != nil || ident.ID != userID {
		return "", refuse(errFailed, "could not start the session")
	}
	token, err := s.Auth.IssueSession(w, *ident)
	if err != nil {
		return "", refuse(errFailed, "could not start the session")
	}
	sid := l.AccessClaims.SessionID
	if sid == "" {
		sid = id.SessionID
	}
	s.recordSession(ctx, sid, userID, token)
	return to, nil
}

// linkedUser is rules 1-4 of the header comment.
func (s *Service) linkedUser(ctx context.Context, l *oidc.Login, currentUser string) (string, *loginErr) {
	id := l.IDClaims
	if uid, err := s.UserBySubject(ctx, id.Subject); err == nil {
		return uid, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", &loginErr{errUnavailable, err}
	}

	email := norm(firstNonEmpty(rawString(id, "email"), rawString(l.AccessClaims, "email")))
	if email == "" {
		return "", refuse(errNoEmail, "the hub sent no e-mail address")
	}
	verified := rawBool(id, "email_verified") || rawBool(l.AccessClaims, "email_verified")
	if !verified {
		return "", refuse(errUnverified, "the hub has not verified this address")
	}

	uid, _, usable, err := s.userByEmail(ctx, email)
	switch {
	case err == nil:
		if !usable && uid != currentUser {
			return "", refuse(errUnlinkable, "an account with this address exists, has a password and is not signed in here")
		}
		got, err := s.linkUser(ctx, id.Subject, uid, email, "email")
		if err != nil {
			return "", &loginErr{errUnavailable, err}
		}
		if got != uid {
			return "", refuse(errUnlinkable, "the account is already linked to another CircleXO user")
		}
		return uid, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", &loginErr{errUnavailable, err}
	}

	ident, err := s.Auth.FindOrCreateByEmail(ctx, email)
	if err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	got, err := s.linkUser(ctx, id.Subject, ident.ID, email, "created")
	if err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	if got != ident.ID {
		return "", refuse(errUnlinkable, "the account is already linked to another CircleXO user")
	}
	return got, nil
}

// landing puts the user into the workspace mapped to the token's org (adding them with the hub
// role) and returns the page to open. With no org in play, the web app's own home decides.
func (s *Service) landing(ctx context.Context, l *oidc.Login, userID string) (string, *loginErr) {
	ac := l.AccessClaims
	orgID := ac.OrgID
	slugHint := ""
	if strings.HasPrefix(l.ReturnTo, launchHint) {
		v, _ := url.ParseQuery(strings.TrimPrefix(l.ReturnTo, launchHint+"?"))
		slugHint = v.Get("org")
	} else if l.ReturnTo != "" && orgID == "" {
		return l.ReturnTo, nil
	}
	if orgID == "" && slugHint != "" {
		_ = s.DB.QueryRowContext(ctx, `SELECT org_id FROM circlexo_org_links WHERE org_slug = $1 AND status = 'active'`, slugHint).Scan(&orgID)
	}
	if orgID == "" {
		return "/", nil
	}
	link, err := s.OrgByID(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		// The hub installed the app but the webhook has not (or did not) arrive: provision now if
		// the hub confirms the org has the app.
		if _, terr := s.Client.TenantByOrg(ctx, orgID); terr != nil {
			return "/", nil
		}
		if link, err = s.Provision(ctx, ProvisionInput{OrgID: orgID, Slug: slugHint}); err != nil {
			s.Log.Error("circlexo: provisioning at sign-in", "org", orgID, "err", err)
			return "/", nil
		}
	} else if err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	if !link.Active {
		return "/", nil
	}
	if err := s.setMember(ctx, link.WorkspaceID, userID, ac.OrgRole); err != nil {
		return "", &loginErr{errUnavailable, err}
	}
	locale := link.Locale
	if locale != "ar" {
		locale = "en"
	}
	return "/" + locale + "/w/" + link.WorkspaceSlug, nil
}

// Logout clears Moharrik's session and returns to the login page. Moharrik's session is a stateless
// token, so this ends it in this browser only; a copy of the token stays valid until it expires
// or the user is signed out everywhere (the same as Moharrik's own sign-out).
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	secure := strings.HasPrefix(baseURL(), "https://") || os.Getenv("COOKIE_SECURE") == "true"
	http.SetCookie(w, &http.Cookie{Name: "togo_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
	post := baseURL() + "/login"
	s.RP.Logout(post)(w, r)
}

// LoginError is a refused sign-in, exported so callers and tests can read its code.
type LoginError = loginErr

// Code is the short refusal code the login page translates.
func (e *loginErr) Code() string { return e.code }

// sessionUser is the Moharrik user whose valid session r carries, or "".
func (s *Service) sessionUser(r *http.Request) string {
	tok := requestToken(r)
	if tok == "" {
		return ""
	}
	if ident, err := s.Auth.Verify(tok); err == nil && ident != nil {
		return ident.ID
	}
	return ""
}
