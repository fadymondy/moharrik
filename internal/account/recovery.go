package account

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/crypto/bcrypt"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/platform"
)

// Forgot password, reset password, and confirm an address — each through a
// one-time link mailed to the address (auth_email_tokens).

// useEmailToken burns a link token and returns whose it was. A token for an
// address the account no longer has is refused: the link proved a mailbox,
// not the account.
func useEmailToken(ctx context.Context, tx *sql.Tx, purpose, token string) (string, string, error) {
	var userID, email, current string
	err := tx.QueryRowContext(ctx, `
		UPDATE auth_email_tokens t SET used_at = now()
		FROM users u
		WHERE t.token_hash = $1 AND t.purpose = $2 AND t.used_at IS NULL AND t.expires_at > now() AND u.id = t.user_id
		RETURNING t.user_id, t.email, u.email`,
		sha256Hex(token), purpose).Scan(&userID, &email, &current)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && normalizeEmail(current) != email) {
		return "", "", huma.Error400BadRequest("link_invalid")
	}
	return userID, email, err
}

func registerRecovery(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{OperationID: "auth-password-forgot", Method: http.MethodPost, Path: "/api/auth/password/forgot",
		DefaultStatus: http.StatusAccepted, Summary: "Mail a password reset link", Tags: tags,
		Description: "Always 202, whether or not the address has an account, so the answer reveals nothing. The link works " +
			"once, for an hour. 503 when this server cannot send mail. Rate-limited per client network and per address."},
		func(ctx context.Context, in *struct {
			Body struct {
				Email string `json:"email" maxLength:"254"`
			}
		}) (*struct{}, error) {
			s := get()
			if s == nil || !MailConfigured() {
				return nil, huma.Error503ServiceUnavailable("mail_unavailable")
			}
			email := normalizeEmail(in.Body.Email)
			if !allow("forgot:"+ctxBucket(ctx), 10, time.Hour) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			// Per address, silently: a 429 here would say the address exists.
			if !allow("forgot-email:"+email, 3, time.Hour) {
				return &struct{}{}, nil
			}
			var userID string
			err := s.a.SysDB.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = $1`, email).Scan(&userID)
			if err == nil {
				if err := sendReset(ctx, s.a.SysDB, userID, email); err != nil {
					s.a.Log.Error("account: could not send the reset email", "err", err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				s.a.Log.Error("account: forgot password", "err", err)
			}
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-password-reset", Method: http.MethodPost, Path: "/api/auth/password/reset",
		DefaultStatus: http.StatusNoContent, Summary: "Set a new password with a reset link", Tags: tags,
		Description: "Sets the password, confirms the address (the link reached it) and signs the account out everywhere: " +
			"every web session, app and device token. 400 link_invalid for a used, expired or unknown link."},
		func(ctx context.Context, in *struct {
			Body struct {
				Token    string `json:"token" minLength:"1" maxLength:"128"`
				Password string `json:"password" maxLength:"200"`
			}
		}) (*struct{}, error) {
			s := get()
			if s == nil {
				return nil, huma.Error503ServiceUnavailable("not configured")
			}
			if !allow("reset:"+ctxBucket(ctx), 30, 15*time.Minute) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			if err := checkPassword(in.Body.Password); err != nil {
				return nil, err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(in.Body.Password), bcrypt.DefaultCost)
			if err != nil {
				return nil, statusOr(a, err, "reset the password")
			}
			tx, err := s.a.SysDB.BeginTx(ctx, nil)
			if err != nil {
				return nil, statusOr(a, err, "reset the password")
			}
			defer func() { _ = tx.Rollback() }()
			userID, _, err := useEmailToken(ctx, tx, "reset", in.Body.Token)
			if err != nil {
				return nil, statusOr(a, err, "reset the password")
			}
			for _, q := range []string{
				`UPDATE users SET password_hash = $2 WHERE id = $1`,
				`DELETE FROM auth_email_tokens WHERE user_id = $1 AND purpose = 'reset'`,
			} {
				args := []any{userID}
				if q[0] == 'U' {
					args = append(args, string(hash))
				}
				if _, err := tx.ExecContext(ctx, q, args...); err != nil {
					return nil, statusOr(a, err, "reset the password")
				}
			}
			if err := signOutEverywhere(ctx, tx, userID); err != nil {
				return nil, statusOr(a, err, "reset the password")
			}
			if err := markVerified(ctx, tx, userID); err != nil {
				return nil, statusOr(a, err, "reset the password")
			}
			if err := tx.Commit(); err != nil {
				return nil, statusOr(a, err, "reset the password")
			}
			platform.Invalidate(userID)
			return &struct{}{}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-email-verify", Method: http.MethodPost, Path: "/api/auth/email/verify",
		Summary: "Confirm an address with the mailed link", Tags: tags,
		Description: "Public: the link's token is the credential. 400 link_invalid for a used, expired or unknown link."},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token" minLength:"1" maxLength:"128"`
			}
		}) (*body[struct {
			Email string `json:"email"`
		}], error) {
			s := get()
			if s == nil {
				return nil, huma.Error503ServiceUnavailable("not configured")
			}
			if !allow("verify:"+ctxBucket(ctx), 30, 15*time.Minute) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			tx, err := s.a.SysDB.BeginTx(ctx, nil)
			if err != nil {
				return nil, statusOr(a, err, "confirm the address")
			}
			defer func() { _ = tx.Rollback() }()
			userID, email, err := useEmailToken(ctx, tx, "verify", in.Body.Token)
			if err != nil {
				return nil, statusOr(a, err, "confirm the address")
			}
			if err := markVerified(ctx, tx, userID); err != nil {
				return nil, statusOr(a, err, "confirm the address")
			}
			if err := tx.Commit(); err != nil {
				return nil, statusOr(a, err, "confirm the address")
			}
			platform.Invalidate(userID)
			return out(struct {
				Email string `json:"email"`
			}{email}), nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-email-verification", Method: http.MethodPost, Path: "/api/me/email/verification",
		DefaultStatus: http.StatusAccepted, Summary: "Mail the link that confirms my address again", Tags: tags,
		Description: "409 already_verified; 503 mail_unavailable when this server cannot send mail."},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			s := get()
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			if s == nil || !MailConfigured() {
				return nil, huma.Error503ServiceUnavailable("mail_unavailable")
			}
			if !allow("resend:"+id.ID, 5, time.Hour) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			var email string
			var verified sql.NullTime
			if err := s.a.SysDB.QueryRowContext(ctx, `
				SELECT u.email, p.email_verified_at FROM users u LEFT JOIN platform_users p ON p.user_id = u.id WHERE u.id = $1`,
				id.ID).Scan(&email, &verified); err != nil {
				return nil, statusOr(a, err, "send the email")
			}
			if verified.Valid {
				return nil, huma.Error409Conflict("already_verified")
			}
			if err := sendVerify(ctx, s.a.SysDB, id.ID, normalizeEmail(email)); err != nil {
				return nil, statusOr(a, err, "send the email")
			}
			return &struct{}{}, nil
		})
}

// signOutEverywhere ends every web session (sessions_valid_after, which the
// guard checks), app and device token of the account.
func signOutEverywhere(ctx context.Context, tx *sql.Tx, userID string) error {
	for _, q := range []string{
		`DELETE FROM device_tokens WHERE user_id = $1`,
		`DELETE FROM app_refresh_tokens WHERE user_id = $1`,
		`DELETE FROM personal_access_tokens WHERE user_id = $1`,
		`INSERT INTO platform_users (user_id, sessions_valid_after) VALUES ($1, now())
		 ON CONFLICT (user_id) DO UPDATE SET sessions_valid_after = now(), updated_at = now()`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return nil
}
