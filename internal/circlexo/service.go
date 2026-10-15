// Package circlexo connects Moharrik to the CircleXO hub (docs/circlexo.md): sign in with the hub
// (with account linking), hub orgs mapped to workspaces, entitlements and billing from the hub,
// and hub-issued MCP tokens. Everything is off unless CIRCLEXO_ISSUER is set; with it unset the
// process behaves exactly as before, and Current() is nil.
//
// The hub's Go SDK (github.com/circlexo/circlexo-go) does the protocol work — OIDC, JWT
// verification, Standard Webhooks, the entitlements cache. This package is Moharrik's side of it:
// which user, which workspace, which role.
package circlexo

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/entitlements"
	"github.com/circlexo/circlexo-go/oidc"
	"github.com/circlexo/circlexo-go/service"
	"github.com/togo-framework/auth"
)

// Paths, matching manifests/mahaam/circlexo.app.yaml in the hub repository.
const (
	LoginPath       = "/auth/circlexo/login"
	CallbackPath    = "/auth/circlexo/callback"
	LogoutPath      = "/auth/circlexo/logout"
	LaunchPath      = "/sso/circlexo"
	EventsPath      = "/api/circlexo/events"
	ProvisionPath   = "/api/circlexo/provision"
	DeprovisionPath = "/api/circlexo/deprovision"
	ConfigPath      = "/api/circlexo/config"
)

// Service is the integration, built once at boot from the environment.
type Service struct {
	DB   *sql.DB // the owner pool: every table here is platform data
	Log  *slog.Logger
	Auth *auth.Service

	Cfg      circlexo.Config
	Client   *circlexo.Client
	Verifier *circlexo.Verifier
	Ents     *entitlements.Cache
	RP       *oidc.RP

	// WebhookSecrets verify the hub's Standard Webhooks signatures (the provision endpoint too).
	WebhookSecrets []string
	// BillingBase, when set, replaces <issuer>/orgs/<slug>/billing as the hub billing page.
	BillingBase string
	// OnChange is called with a workspace id whenever its link or membership changed, so caches
	// derived from it are dropped. Nil unless something registers interest.
	OnChange func(workspaceID string)
	// Now is the clock the webhook checks use; nil is time.Now. Tests set it.
	Now func() time.Time
}

var current atomic.Pointer[Service]

// Current returns the running integration, or nil when it is off.
func Current() *Service { return current.Load() }

// Enabled reports whether the integration is on.
func Enabled() bool { return Current() != nil }

// FromEnv builds the service from CIRCLEXO_* (names in .env.example). It returns nil, nil when
// CIRCLEXO_ISSUER is unset: the integration is off. A partial or invalid configuration is an
// error, so the caller can say so loudly rather than run half-connected.
func FromEnv(db *sql.DB, log *slog.Logger, authSvc *auth.Service) (*Service, error) {
	if strings.TrimSpace(os.Getenv("CIRCLEXO_ISSUER")) == "" {
		return nil, nil
	}
	cfg, err := circlexo.FromEnv()
	if err != nil {
		return nil, err
	}
	return New(db, log, authSvc, cfg, redirectURL(), Secrets{
		Webhook: os.Getenv("CIRCLEXO_WEBHOOK_SECRET"),
		Session: os.Getenv("CIRCLEXO_SESSION_KEY"),
	}, strings.TrimRight(os.Getenv("CIRCLEXO_BILLING_URL"), "/"))
}

// Secrets are the two values that are not part of the hub client configuration.
type Secrets struct {
	Webhook string // whsec_<base64>, the app's webhook signing secret
	Session string // seals the sign-in state cookie; any string of 16+ characters
}

// New builds the service from an explicit configuration (tests point it at circlexotest).
func New(db *sql.DB, log *slog.Logger, authSvc *auth.Service, cfg circlexo.Config, redirect string, sec Secrets, billingBase string) (*Service, error) {
	if authSvc == nil {
		return nil, errors.New("circlexo: the auth service is not available")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.ClientSecret == "" && cfg.PrivateKeyPEM == "" {
		return nil, errors.New("circlexo: CIRCLEXO_CLIENT_SECRET or CIRCLEXO_PRIVATE_KEY is required (the sign-in code exchange authenticates with it)")
	}
	if strings.TrimSpace(sec.Webhook) == "" {
		return nil, errors.New("circlexo: CIRCLEXO_WEBHOOK_SECRET is required")
	}
	key, err := sessionKey(sec.Session)
	if err != nil {
		return nil, err
	}
	rp, err := oidc.New(cfg, redirect, key)
	if err != nil {
		return nil, err
	}
	tokens, err := tokenSource(cfg)
	if err != nil {
		return nil, err
	}
	client := circlexo.NewClient(cfg, tokens)
	return &Service{
		DB: db, Log: log, Auth: authSvc, Cfg: cfg, Client: client, Verifier: circlexo.NewVerifier(cfg),
		Ents: entitlements.New(client), RP: rp, WebhookSecrets: []string{sec.Webhook}, BillingBase: billingBase,
	}, nil
}

// tokenSource is how Moharrik calls the hub's app APIs: the app key when there is one, else a
// service token from the client secret or private key (the scopes the calls below need).
func tokenSource(cfg circlexo.Config) (circlexo.TokenSource, error) {
	if cfg.AppKey != "" {
		return nil, nil // NewClient's default: the app key
	}
	c, err := service.New(cfg)
	if err != nil {
		return nil, err
	}
	return service.NewSource(c, "billing.entitlements", "orgs.read", "tenants.read", "tenants.write"), nil
}

// sessionKey turns CIRCLEXO_SESSION_KEY into the 32 bytes the OIDC state cookie is sealed with.
// Without it, one is derived from AUTH_SECRET (already required in production), so a
// deployment does not need a third secret.
func sessionKey(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		if mk := strings.TrimSpace(os.Getenv("AUTH_SECRET")); mk != "" {
			v = "circlexo-oidc-state:" + mk
		}
	}
	if len(v) < 16 {
		return nil, errors.New("circlexo: set CIRCLEXO_SESSION_KEY (16+ characters) or AUTH_SECRET")
	}
	sum := sha256.Sum256([]byte(v))
	return sum[:], nil
}

// baseURL is the public origin of the web app, which is also what the hub redirects back to: the
// callback is on the web host (proxied to the API), like every page the browser opens.
func baseURL() string {
	for _, k := range []string{"WEB_URL", "APP_URL"} {
		if v := strings.TrimRight(os.Getenv(k), "/"); v != "" {
			return v
		}
	}
	return ""
}

func redirectURL() string {
	if v := strings.TrimSpace(os.Getenv("CIRCLEXO_REDIRECT_URL")); v != "" {
		return v
	}
	return baseURL() + CallbackPath
}

// Install makes s the running integration. Passing nil (the integration is off) clears it, which
// matters to a test process that boots more than once.
func Install(s *Service) {
	current.Store(s)
}

// billingURL is where a linked workspace manages its subscription: the hub's own billing page.
func (s *Service) billingURL(orgSlug string) string {
	if s.BillingBase != "" {
		return strings.ReplaceAll(s.BillingBase, "{slug}", orgSlug)
	}
	return fmt.Sprintf("%s/orgs/%s/billing", s.Cfg.Issuer, orgSlug)
}

// ConfigHandler answers GET /api/circlexo/config (PUBLIC, no secrets): whether the sign-in button
// should show. The web reads it; with the integration off it answers {"enabled": false}.
func ConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if Enabled() {
		_, _ = w.Write([]byte(`{"enabled":true,"login_url":"` + LoginPath + `"}`))
		return
	}
	_, _ = w.Write([]byte(`{"enabled":false}`))
}

// BillingURL is the hub billing page for a workspace governed by the hub (an ACTIVE org link), so
// the plan screen and the billing API can send its owners there. ok is false for every other
// workspace, and whenever the integration is off: Moharrik has no billing of its own.
func BillingURL(ctx context.Context, workspaceID string) (string, bool) {
	s := Current()
	if s == nil {
		return "", false
	}
	link, err := s.OrgByWorkspace(ctx, workspaceID)
	if err != nil || !link.Active {
		return "", false
	}
	return s.billingURL(link.OrgSlug), true
}
