/*
Package account is the part of the account cycle the auth plugin
(togo-framework/auth v0.8) does not have: the apps' own email and password
sign-in and sign-up, confirming an address, and resetting a forgotten password
by a mailed link. Sign in with Google and Apple, connected accounts and
deleting the account are internal/social.

The web keeps the auth plugin's cookie session; the apps get appsignin's
device token and refresh token.
*/
package account

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/secret"
)

// Service is the configured account cycle for one process.
type Service struct {
	a    *app.App
	auth *auth.Service
}

var (
	mu        sync.RWMutex
	current   *Service
	announced bool
)

func get() *Service {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// limiter bounds the public endpoints per client network and per address.
var limiter = inbox.NewMemoryLimiter()

func allow(key string, limit int, window time.Duration) bool {
	ok, _ := limiter.Take(time.Now(), inbox.Rule{Key: key, Limit: limit, Window: window})
	return ok
}

// Configure sets the service up for a booted app: the "confirm your email"
// mail for accounts made on the web (the auth plugin's register), and, when
// this server can send mail, "password_reset" on GET /api/auth/methods so the
// sign-in screens show "Forgot password?". Call before Register.
func Configure(a *app.App, svc *auth.Service) {
	mu.Lock()
	current = &Service{a: a, auth: svc}
	if MailConfigured() && !announced {
		// The plugin's list is process-global and append-only.
		announced = true
		auth.RegisterLoginMethod(auth.LoginMethod{Name: "password_reset", Label: "Forgot password?", Type: "recovery",
			URL: "/api/auth/password/forgot"})
	}
	mu.Unlock()

	if a.Hooks != nil {
		a.Hooks.On(auth.EventRegistered, 50, func(_ context.Context, payload any) error {
			u, ok := payload.(auth.User)
			if !ok || u.ID == "" {
				return nil
			}
			// Off the request: a slow mail server must not hold up signing up.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := sendVerify(ctx, a.SysDB, u.ID, normalizeEmail(u.Email)); err != nil {
					a.Log.Error("account: could not send the verification email", "err", err)
				}
			}()
			return nil
		})
	}
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func identity(ctx context.Context) (*auth.Identity, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id == nil {
		return nil, huma.Error401Unauthorized("unauthorized")
	}
	return id, nil
}

// statusOr passes a huma error through, and logs anything else as a 500 that
// says what failed.
func statusOr(a *app.App, err error, what string) error {
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	a.Log.Error("account: "+what, "err", err)
	return huma.Error500InternalServerError("could not " + what)
}

var tags = []string{"Account"}

// Register mounts the account endpoints. `a` may be nil (OpenAPI export), in
// which case nothing is ever served and Configure was never called.
func Register(api huma.API, a *app.App) {
	registerNative(api, a)
	registerRecovery(api, a)
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// markVerified records that the account's address is proven.
func markVerified(ctx context.Context, q execer, userID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO platform_users (user_id, email_verified_at) VALUES ($1, now())
		ON CONFLICT (user_id) DO UPDATE SET email_verified_at = COALESCE(platform_users.email_verified_at, now()), updated_at = now()`,
		userID)
	return err
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// randHex returns n random bytes, hex-encoded. crypto/rand does not fail on
// the platforms Moharrik runs on; if it ever did, nothing random is safe.
func randHex(n int) string {
	s, err := secret.RandomHex(n)
	if err != nil {
		panic(err)
	}
	return s
}
