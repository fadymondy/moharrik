package account

import (
	"context"
	"errors"
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

// The apps' own sign-in screens: email and password, and Sign in with Apple
// with the system sheet (expo-apple-authentication). Google in the apps goes
// through the browser (GET /api/auth/oauth/google with the app's redirect).
// Each ends in appsignin.IssueSession: a device token and a refresh token.

func clientBucket(r *http.Request) string  { return clientip.Bucket(clientip.From(r.Context())) }
func ctxBucket(ctx context.Context) string { return clientip.Bucket(clientip.From(ctx)) }

// Methods is what the sign-in screens may show: a button only for what works.
type Methods struct {
	Password      bool `json:"password" doc:"Email and password sign-in and sign-up."`
	PasswordReset bool `json:"password_reset" doc:"\"Forgot password?\": the server can send mail."`
	Google        bool `json:"google" doc:"Sign in with Google (in the browser)."`
	Apple         bool `json:"apple" doc:"Sign in with Apple on iOS (the system sheet)."`
	AppleWeb      bool `json:"apple_web" doc:"Sign in with Apple in the browser."`
}

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
	huma.Register(api, huma.Operation{OperationID: "auth-app-methods", Method: http.MethodGet, Path: "/api/auth/app/methods",
		Summary: "Which sign-in methods this server offers", Tags: tags,
		Description: "The apps show a button only for what is configured here."},
		func(ctx context.Context, _ *struct{}) (*body[Methods], error) {
			s := get()
			m := Methods{Password: true, PasswordReset: MailConfigured()}
			if s != nil {
				m.Google, m.Apple, m.AppleWeb = s.p.Google != nil, s.p.AppleNative(), s.p.AppleWeb()
			}
			return out(m), nil
		})

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

	huma.Register(api, huma.Operation{OperationID: "auth-app-apple", Method: http.MethodPost, Path: "/api/auth/app/apple",
		Summary: "Sign an app in with Apple", Tags: tags,
		Description: "The iOS app's Sign in with Apple sheet returns an identity token and an authorization code. The app " +
			"made a random nonce and gave Apple its SHA-256 (hex); it sends the raw nonce here. The code is traded at Apple, " +
			"which makes the sign-in single-use and gives the token that deleting the account revokes."},
		func(ctx context.Context, in *struct {
			Body struct {
				IdentityToken     string `json:"identity_token" minLength:"1" maxLength:"8192"`
				AuthorizationCode string `json:"authorization_code" minLength:"1" maxLength:"1024"`
				Nonce             string `json:"nonce" minLength:"16" maxLength:"128"`
				Device            string `json:"device,omitempty" maxLength:"200"`
			}
		}) (*body[appsignin.Session], error) {
			s := get()
			if s == nil || !s.p.AppleNative() {
				return nil, huma.Error404NotFound("Sign in with Apple is not configured on this server")
			}
			if !allow("apple:"+ctxBucket(ctx), 30, 15*time.Minute) {
				return nil, huma.Error429TooManyRequests("too many attempts — wait a few minutes")
			}
			ap := s.p.Apple
			cl, err := verifyIDToken(ctx, appleKeys, in.Body.IdentityToken, appleIssuers, []string{ap.BundleID})
			if err == nil && cl.Nonce != sha256Hex(in.Body.Nonce) {
				err = errors.New("the sign-in token is for another attempt")
			}
			var refresh string
			if err == nil {
				var traded claims
				traded, refresh, err = exchangeApple(ctx, ap, ap.BundleID, in.Body.AuthorizationCode, "")
				if err == nil && traded.Subject != cl.Subject {
					err = errors.New("the code and the token name different people")
				}
			}
			if err != nil {
				s.a.Log.Warn("account: Apple sign-in refused", "err", err)
				return nil, huma.Error401Unauthorized("apple_failed")
			}
			return s.finishApp(ctx, a, login{Provider: "apple", Subject: cl.Subject, Email: cl.Email, Verified: cl.Verified,
				RefreshToken: refresh, ClientID: ap.BundleID}, in.Body.Device)
		})
}

func (s *Service) finishApp(ctx context.Context, a *app.App, l login, device string) (*body[appsignin.Session], error) {
	userID, _, err := resolve(ctx, s.a.SysDB, l)
	if errors.Is(err, ErrNoEmail) || errors.Is(err, ErrUnverified) {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if err != nil {
		return nil, statusOr(a, err, "link the sign-in")
	}
	sess, err := appsignin.IssueSession(ctx, s.a.SysDB, userID, device)
	if err != nil {
		return nil, statusOr(a, err, "sign the app in")
	}
	return out(sess), nil
}
