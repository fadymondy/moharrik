/*
Package account is the account cycle the auth plugin (togo-framework/auth
v0.8) does not have: Sign in with Google and Sign in with Apple — on the web and
in the apps — with accounts linked by the provider's stable subject, native
email and password sign-in for the apps, verifying an address, resetting a
forgotten password, and deleting the account (App Store Review Guideline
5.1.1(v), with Apple's tokens revoked).

It replaces the auth-oauth plugin, which matched a provider sign-in to an
account by email alone (FindOrCreateByEmail) and ignored ?next=, so an app
that started Google sign-in never got back to itself.

The web keeps the auth plugin's cookie session (auth.IssueSession); the apps
get appsignin's device token and refresh token.
*/
package account

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
)

// Service is the configured account cycle for one process.
type Service struct {
	a    *app.App
	auth *auth.Service
	p    Providers
}

var (
	mu      sync.RWMutex
	current *Service
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

// advertised remembers which sign-in buttons were announced on GET
// /api/auth/methods: the auth plugin's list is process-global and append-only.
var advertised = map[string]bool{}

// Configure reads the providers from the environment, mounts the browser
// routes (Google and Apple redirects), and sends the "confirm your email" mail
// for accounts made on the web. Call before Register.
func Configure(k chi.Router, a *app.App, svc *auth.Service) {
	p, problems := ProvidersFromEnv()
	for _, msg := range problems {
		a.Log.Warn("account: " + msg)
	}
	s := &Service{a: a, auth: svc, p: p}
	mu.Lock()
	current = s
	if p.Google != nil && !advertised["google"] {
		advertised["google"] = true
		auth.RegisterLoginMethod(auth.LoginMethod{Name: "google", Label: "Continue with Google", Type: "oauth", URL: "/api/auth/oauth/google"})
	}
	if p.AppleWeb() && !advertised["apple"] {
		advertised["apple"] = true
		auth.RegisterLoginMethod(auth.LoginMethod{Name: "apple", Label: "Continue with Apple", Type: "oauth", URL: "/api/auth/oauth/apple"})
	}
	mu.Unlock()

	k.Get("/api/auth/oauth/{provider}", s.start)
	k.Get("/api/auth/oauth/{provider}/callback", s.callback)
	// Apple answers with a form POST (response_mode=form_post): it sends the
	// name only that way, and only the first time.
	k.Post("/api/auth/oauth/{provider}/callback", s.callback)

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

// appURL is the public origin the providers send the browser back to.
func appURL() string {
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("APP_URL")), "/"); v != "" {
		return v
	}
	return "http://localhost:8080"
}

func callbackURL(provider string) string {
	return appURL() + "/api/auth/oauth/" + provider + "/callback"
}

// afterLogin is where a web sign-in lands when the page asked for nowhere in
// particular: OAUTH_AFTER_LOGIN (a path on this site), else the home page,
// which opens the last workspace.
func afterLogin() string {
	if v := strings.TrimSpace(os.Getenv("OAUTH_AFTER_LOGIN")); safePath(v) {
		return v
	}
	return "/"
}

// safePath accepts a path on this site only: never "//host", "/\host" or a URL.
func safePath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "/\\") &&
		!strings.ContainsAny(p, "\r\n") && len(p) <= 500
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
	if se, ok := err.(huma.StatusError); ok {
		return se
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
	registerAccount(api, a)
}
