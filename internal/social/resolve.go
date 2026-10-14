package social

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/platform"
	"github.com/fadymondy/moharrik/internal/secret"
)

// Refusal codes the web and the apps translate (web/lang auth.socialError.<code>,
// apps/shared i18n signin.error.<code>).
const (
	ErrFailed       = "failed"           // the provider round trip failed
	ErrCancelled    = "cancelled"        // the person cancelled at the provider
	ErrNoEmail      = "no_email"         // the provider shared no address
	ErrUnverified   = "email_unverified" // the provider has not verified the address
	ErrConflict     = "account_conflict" // a password account has this address; sign in with it, then connect
	ErrTaken        = "identity_taken"   // this provider account is connected to another Moharrik account
	ErrAlready      = "already_linked"   // this Moharrik account already has another account of the provider
	ErrReauth       = "reauth_mismatch"  // re-authentication with a provider account that is not this person's
	ErrSuspended    = "suspended"        // the account is suspended
	ErrUnavailable  = "unavailable"      // the database or the configuration
	ErrNotAvailable = "not_configured"   // the provider is not configured on this server
)

// Refusal is a sign-in that did not complete, with the code the page shows.
type Refusal struct {
	Code string
	Err  error
}

func (r *Refusal) Error() string {
	if r.Err == nil {
		return r.Code
	}
	return r.Code + ": " + r.Err.Error()
}

func refuse(code, msg string) *Refusal { return &Refusal{code, errors.New(msg)} }

// Intent is why a provider round trip was started.
type Intent string

const (
	IntentLogin  Intent = "login"
	IntentLink   Intent = "link"   // attach the provider to the signed-in account
	IntentReauth Intent = "reauth" // prove it is the signed-in person (account deletion)
)

// Service holds what the handlers share.
type Service struct {
	DB   *sql.DB
	Auth *auth.Service
	Log  *slog.Logger
	// Env is the configuration source; FromEnv unless a test replaces it.
	Env func() Config
	// DeleteWorkspace deletes a workspace the way its owner would (its WhatsApp number released
	// first); wired by the server so this package does not import the workspaces package.
	DeleteWorkspace func(ctx context.Context, workspaceID string) error
}

var appleBox = secret.New(secret.DomainAppleRefresh, "the Sign in with Apple refresh token", "sign in with Apple again")

// Resolve turns a provider-verified person into the Moharrik user they sign in as, creating or
// linking per the package rules. sessionUser is the Moharrik user signed in in this browser ("" for
// none, and always "" for the native endpoints).
func (s *Service) Resolve(ctx context.Context, p Person, intent Intent, sessionUser string) (string, *Refusal) {
	if p.Subject == "" {
		return "", refuse(ErrFailed, "no subject")
	}
	uid, err := s.userBySubject(ctx, p.Provider, p.Subject)
	switch {
	case err == nil:
		if intent != IntentLogin && uid != sessionUser {
			if intent == IntentReauth {
				return "", refuse(ErrReauth, "the provider account belongs to another Moharrik account")
			}
			return "", refuse(ErrTaken, "the provider account is connected to another Moharrik account")
		}
		s.touch(ctx, p)
		return uid, s.usable(ctx, uid)
	case !errors.Is(err, sql.ErrNoRows):
		return "", &Refusal{ErrUnavailable, err}
	}

	switch intent {
	case IntentReauth:
		return "", refuse(ErrReauth, "that provider account is not connected to this Moharrik account")
	case IntentLink:
		if sessionUser == "" {
			return "", refuse(ErrFailed, "linking needs a signed-in account")
		}
		if r := s.link(ctx, p, sessionUser, "link"); r != nil {
			return "", r
		}
		return sessionUser, nil
	}

	if p.Email == "" {
		return "", refuse(ErrNoEmail, "the provider sent no e-mail address")
	}
	if !p.EmailVerified {
		return "", refuse(ErrUnverified, "the provider has not verified this address")
	}
	var id string
	var passwordless bool
	err = s.DB.QueryRowContext(ctx, `
		SELECT id, password_hash = '!sso' FROM users WHERE lower(email) = lower($1) ORDER BY created_at LIMIT 1`, p.Email).
		Scan(&id, &passwordless)
	switch {
	case err == nil:
		// Moharrik never verifies the address of a password account: linking one on the strength
		// of the address alone would hand it to whoever registered that address. Only its owner,
		// signed in in this browser, may connect it (or later, from the account page).
		if !passwordless && id != sessionUser {
			return "", refuse(ErrConflict, "an account with this address has a password and is not signed in here")
		}
		if r := s.usable(ctx, id); r != nil {
			return "", r
		}
		if r := s.link(ctx, p, id, "email"); r != nil {
			return "", r
		}
		return id, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", &Refusal{ErrUnavailable, err}
	}

	ident, err := s.Auth.FindOrCreateByEmail(ctx, p.Email)
	if err != nil {
		return "", &Refusal{ErrUnavailable, err}
	}
	if r := s.link(ctx, p, ident.ID, "created"); r != nil {
		return "", r
	}
	return ident.ID, nil
}

func (s *Service) usable(ctx context.Context, userID string) *Refusal {
	u, err := platform.Load(ctx, s.DB, userID)
	if err != nil {
		return &Refusal{ErrUnavailable, err}
	}
	if u.Suspended() {
		return refuse(ErrSuspended, u.SuspendedMessage())
	}
	return nil
}

func (s *Service) userBySubject(ctx context.Context, provider, subject string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM user_identities WHERE provider = $1 AND subject = $2`, provider, subject).Scan(&id)
	return id, err
}

// link records provider subject -> user. Both directions are unique, so a race or a second account
// of the same provider shows up as a different winner and is refused, never assumed.
func (s *Service) link(ctx context.Context, p Person, userID, via string) *Refusal {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO user_identities (provider, subject, user_id, email, email_verified, display_name, linked_via)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
		p.Provider, p.Subject, userID, p.Email, p.EmailVerified, clip(p.Name, 200), via)
	if err != nil {
		return &Refusal{ErrUnavailable, err}
	}
	got, err := s.userBySubject(ctx, p.Provider, p.Subject)
	if errors.Is(err, sql.ErrNoRows) {
		return refuse(ErrAlready, "this account already has another "+p.Provider+" account connected")
	}
	if err != nil {
		return &Refusal{ErrUnavailable, err}
	}
	if got != userID {
		return refuse(ErrTaken, "the provider account is connected to another Moharrik account")
	}
	return nil
}

// touch refreshes what the provider says now. The name is only ever filled, never blanked: Apple
// sends it on the first consent alone.
func (s *Service) touch(ctx context.Context, p Person) {
	_, _ = s.DB.ExecContext(ctx, `
		UPDATE user_identities SET last_login_at = now(),
		  email = CASE WHEN $3 <> '' THEN $3 ELSE email END,
		  email_verified = CASE WHEN $3 <> '' THEN $4 ELSE email_verified END,
		  display_name = CASE WHEN display_name = '' THEN $5 ELSE display_name END
		WHERE provider = $1 AND subject = $2`, p.Provider, p.Subject, p.Email, p.EmailVerified, clip(p.Name, 200))
}

// keepAppleToken stores (sealed) the refresh token of an Apple sign-in, for revocation later.
func (s *Service) keepAppleToken(ctx context.Context, subject, clientID, refresh string) {
	if refresh == "" {
		return
	}
	sealed, err := appleBox.Seal(refresh)
	if err != nil {
		s.Log.Error("social: sealing the Apple refresh token", "err", err)
		return
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE user_identities SET client_id = $2, refresh_token = $3 WHERE provider = 'apple' AND subject = $1`,
		subject, clientID, sealed); err != nil {
		s.Log.Error("social: keeping the Apple refresh token", "err", err)
	}
}

// identityOf is the auth plugin's identity for userID, to issue Moharrik's normal session.
func (s *Service) identityOf(ctx context.Context, userID string) (*auth.Identity, error) {
	var email string
	if err := s.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		return nil, err
	}
	// The plugin has no lookup by id; by e-mail it returns the same row (the address is unique).
	ident, err := s.Auth.FindOrCreateByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if ident.ID != userID {
		return nil, errors.New("the account changed during sign-in")
	}
	return ident, nil
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
