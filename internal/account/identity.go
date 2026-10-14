package account

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/platform"
	"github.com/fadymondy/moharrik/internal/secret"
)

var refreshBox = secret.New(secret.DomainAppleRefresh, "the Apple sign-in token", "sign in with Apple again")

// Why a provider sign-in was refused. Each is a code the web and the apps
// show in the person's language.
var (
	ErrNoEmail    = errors.New("no_email")
	ErrUnverified = errors.New("email_unverified")
)

// login is one successful provider sign-in, before it is matched to an account.
type login struct {
	Provider, Subject, Email string
	Verified                 bool
	// Apple only: the refresh token and the client id it belongs to.
	RefreshToken, ClientID string
}

/*
resolve finds or makes the Moharrik account for a provider sign-in. This is
the rule that replaced the auth-oauth plugin's FindOrCreateByEmail, which
signed anyone whose Google address matched an account straight into it:

 1. The provider's stable subject is already linked: that account. The email
    is not consulted, so an address that changes hands at the provider does
    not move the account with it.
 2. Otherwise the provider must vouch for the email (email_verified). An
    unverified provider email never links and never creates.
 3. An account with that email exists: link it. If that account's own email
    was never verified, whoever made it never proved they own the mailbox —
    it may be a squatter waiting for the real owner to sign in with Google.
    The password is wiped, every session and app token ended, and the email
    marked verified: from now on only the mailbox's owner gets in.
 4. No account: create one, already verified, with no usable password.
*/
func resolve(ctx context.Context, db *sql.DB, l login) (userID string, created bool, err error) {
	sealed, err := refreshBox.Seal(l.RefreshToken)
	if err != nil {
		return "", false, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRowContext(ctx, `SELECT user_id FROM user_identities WHERE provider = $1 AND subject = $2 FOR UPDATE`,
		l.Provider, l.Subject).Scan(&userID)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_identities SET last_login_at = now(),
			  email = CASE WHEN $3 <> '' THEN $3 ELSE email END,
			  refresh_token = CASE WHEN $4 <> '' THEN $4 ELSE refresh_token END,
			  client_id = CASE WHEN $4 <> '' THEN $5 ELSE client_id END
			WHERE provider = $1 AND subject = $2`,
			l.Provider, l.Subject, normalizeEmail(l.Email), sealed, l.ClientID); err != nil {
			return "", false, err
		}
		return userID, false, tx.Commit()
	case !errors.Is(err, sql.ErrNoRows):
		return "", false, err
	}

	email := normalizeEmail(l.Email)
	if email == "" {
		return "", false, ErrNoEmail
	}
	if !l.Verified {
		return "", false, ErrUnverified
	}

	var verifiedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT u.id, p.email_verified_at FROM users u LEFT JOIN platform_users p ON p.user_id = u.id
		WHERE lower(u.email) = $1 FOR UPDATE OF u`, email).Scan(&userID, &verifiedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		userID = randHex(16)
		created = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, email, password_hash, roles, permissions, created_at) VALUES ($1, $2, $3, '', '', $4)`,
			userID, email, noPassword, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return "", false, err
		}
		if err := markVerified(ctx, tx, userID); err != nil {
			return "", false, err
		}
	case err != nil:
		return "", false, err
	case !verifiedAt.Valid:
		if err := takeOver(ctx, tx, userID); err != nil {
			return "", false, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_identities (provider, subject, user_id, email, refresh_token, client_id) VALUES ($1, $2, $3, $4, $5, $6)`,
		l.Provider, l.Subject, userID, email, sealed, l.ClientID); err != nil {
		return "", false, err
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	platform.Invalidate(userID)
	return userID, created, nil
}

// noPassword is a password hash no password matches (the auth plugin's own
// marker for an account made by single sign-on).
const noPassword = "!sso"

// takeOver hands an account whose email was never proven to the person who
// just proved it: no password, no sessions, no app tokens, email verified.
func takeOver(ctx context.Context, tx *sql.Tx, userID string) error {
	for _, q := range []string{
		`UPDATE users SET password_hash = '` + noPassword + `' WHERE id = $1`,
		`DELETE FROM device_tokens WHERE user_id = $1`,
		`DELETE FROM app_refresh_tokens WHERE user_id = $1`,
		`DELETE FROM personal_access_tokens WHERE user_id = $1`,
		`DELETE FROM auth_email_tokens WHERE user_id = $1`,
		`INSERT INTO platform_users (user_id, sessions_valid_after) VALUES ($1, now())
		 ON CONFLICT (user_id) DO UPDATE SET sessions_valid_after = now(), updated_at = now()`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return markVerified(ctx, tx, userID)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func markVerified(ctx context.Context, q execer, userID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO platform_users (user_id, email_verified_at) VALUES ($1, now())
		ON CONFLICT (user_id) DO UPDATE SET email_verified_at = COALESCE(platform_users.email_verified_at, now()), updated_at = now()`,
		userID)
	return err
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// randHex returns n random bytes, hex-encoded. crypto/rand does not fail on
// the platforms Moharrik runs on; if it ever did, nothing random is safe.
func randHex(n int) string {
	s, err := secret.RandomHex(n)
	if err != nil {
		panic(err)
	}
	return s
}
