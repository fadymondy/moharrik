package account

import (
	"context"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/crypto/bcrypt"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/appsignin"
	"github.com/fadymondy/moharrik/internal/clientip"
)

// The apps' own email and password screens (Google and Apple are
// internal/social). Each ends in appsignin.IssueSession: a device token and a
// refresh token, as after the browser sign-in.

func ctxBucket(ctx context.Context) string { return clientip.Bucket(clientip.From(ctx)) }

func minPassword() int {
	if n, err := strconv.Atoi(os.Getenv("AUTH_MIN_PASSWORD")); err == nil && n > 0 {
		return n
	}
	return 8
}

// checkPassword applies the auth plugin's policy: AUTH_MIN_PASSWORD (8)
// characters at least, and no more than bcrypt reads (72 bytes).
func checkPassword(p string) error {
	if len(p) < minPassword() {
		return huma.Error422UnprocessableEntity("password_too_short")
	}
	if len(p) > 72 {
		return huma.Error422UnprocessableEntity("password_too_long")
	}
	return nil
}

func validEmail(e string) bool {
	if len(e) > 254 || !strings.Contains(e, "@") {
		return false
	}
	a, err := mail.ParseAddress(e)
	return err == nil && a.Address == e && strings.Contains(e[strings.LastIndex(e, "@"):], ".")
}

type credentials struct {
	Email    string `json:"email" maxLength:"254"`
	Password string `json:"password" maxLength:"200"`
	Device   string `json:"device,omitempty" maxLength:"200" doc:"Shown in the person's device list, e.g. \"Fady's iPhone\"."`
}

func registerNative(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{OperationID: "auth-app-signup", Method: http.MethodPost, Path: "/api/auth/app/signup",
		DefaultStatus: http.StatusCreated, Summary: "Create an account from an app", Tags: tags,
		Description: "Creates the account, mails the link that confirms the address, and signs this device in. 409 email_taken " +
			"when the address already has an account. Rate-limited per client network."},
		func(ctx context.Context, in *struct{ Body credentials }) (*body[appsignin.Session], error) {
			s := get()
			if s == nil {
				return nil, huma.Error503ServiceUnavailable("not configured")
			}
			if !allow("signup:"+ctxBucket(ctx), 10, time.Hour) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			email := normalizeEmail(in.Body.Email)
			if !validEmail(email) {
				return nil, huma.Error422UnprocessableEntity("invalid_email")
			}
			if err := checkPassword(in.Body.Password); err != nil {
				return nil, err
			}
			hash, err := bcrypt.GenerateFromPassword([]byte(in.Body.Password), bcrypt.DefaultCost)
			if err != nil {
				return nil, statusOr(a, err, "create the account")
			}
			userID := randHex(16)
			res, err := s.a.SysDB.ExecContext(ctx, `
				INSERT INTO users (id, email, password_hash, roles, permissions, created_at)
				SELECT $1, $2, $3, '', '', $4 WHERE NOT EXISTS (SELECT 1 FROM users WHERE lower(email) = $2)`,
				userID, email, string(hash), time.Now().UTC().Format(time.RFC3339))
			if err != nil {
				return nil, huma.Error409Conflict("email_taken")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error409Conflict("email_taken")
			}
			if err := sendVerify(ctx, s.a.SysDB, userID, email); err != nil {
				s.a.Log.Error("account: could not send the verification email", "err", err)
			}
			sess, err := appsignin.IssueSession(ctx, s.a.SysDB, userID, in.Body.Device)
			if err != nil {
				return nil, statusOr(a, err, "sign the app in")
			}
			return out(sess), nil
		})

	huma.Register(api, huma.Operation{OperationID: "auth-app-signin", Method: http.MethodPost, Path: "/api/auth/app/signin",
		Summary: "Sign an app in with email and password", Tags: tags,
		Description: "401 for a wrong email or password (which one is not said), 403 account_suspended. Rate-limited per " +
			"client network and per address."},
		func(ctx context.Context, in *struct{ Body credentials }) (*body[appsignin.Session], error) {
			s := get()
			if s == nil {
				return nil, huma.Error503ServiceUnavailable("not configured")
			}
			email := normalizeEmail(in.Body.Email)
			if !allow("signin:"+ctxBucket(ctx), 30, 15*time.Minute) || !allow("signin-email:"+email, 10, 15*time.Minute) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			id, err := s.auth.Guard("").Auth.Attempt(ctx, email, in.Body.Password)
			if err != nil || id == nil {
				return nil, huma.Error401Unauthorized("invalid_credentials")
			}
			sess, err := appsignin.IssueSession(ctx, s.a.SysDB, id.ID, in.Body.Device)
			if err != nil {
				return nil, statusOr(a, err, "sign the app in")
			}
			return out(sess), nil
		})
}
