// Package social is Sign in with Google and Sign in with Apple, on the web and in the apps.
//
// It replaces the togo auth-oauth plugin's Google routes (same paths, so the redirect URI already
// registered on the Google client keeps working) because the plugin had three problems:
//
//   - it linked a Google sign-in to ANY account with that address — including a password account
//     whose address Moharrik never verified, so anyone could register someone's address with a
//     password and inherit their Google sign-in later (pre-account hijack);
//   - it stored no provider subject, so the account was found by e-mail every time;
//   - it ignored ?next=, so a Google sign-in started from the apps' browser page (/app-signin)
//     never returned to the app.
//
// Resolution (resolve.go) follows the CircleXO rule (internal/circlexo/sso.go): a known provider
// subject signs in as its user; otherwise the provider must vouch for the address; an existing
// account with it is linked only when it has no password (it was created by a verified sign-in)
// or its owner is signed in in the same browser; otherwise account_conflict, and the person signs
// in with their password and connects the provider from the account page. Otherwise a
// passwordless account is created.
//
// Routes (all under /api/auth/, the guard's public tier, except /api/me/*):
//
//	GET  /api/auth/oauth/{google|apple}           start (?next=/local/path&intent=login|link|reauth)
//	GET  /api/auth/oauth/google/callback          Google's redirect
//	POST /api/auth/oauth/apple/callback           Apple's form_post
//	GET  /api/auth/oauth/apple/finish             the same-site hop that sets the session (see web.go)
//	POST /api/auth/native/apple                   the iOS app: identity token + code -> device token
//	POST /api/auth/native/google                  a native Google id_token -> device token
//	GET  /api/me/identities, DELETE /api/me/identities/{provider}
//	GET  /api/me/account/deletion, POST /api/me/account/delete
package social

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"strings"
)

// Endpoints are the providers' URLs. Variables so tests point them at fakes; nothing else
// changes them.
var Endpoints = struct {
	GoogleAuth, GoogleToken, GoogleJWKS string
	GoogleIssuers                       []string
	AppleAuth, AppleToken, AppleRevoke  string
	AppleJWKS, AppleIssuer              string
}{
	GoogleAuth:    "https://accounts.google.com/o/oauth2/v2/auth",
	GoogleToken:   "https://oauth2.googleapis.com/token",
	GoogleJWKS:    "https://www.googleapis.com/oauth2/v3/certs",
	GoogleIssuers: []string{"https://accounts.google.com", "accounts.google.com"},
	AppleAuth:     "https://appleid.apple.com/auth/authorize",
	AppleToken:    "https://appleid.apple.com/auth/token",
	AppleRevoke:   "https://appleid.apple.com/auth/revoke",
	AppleJWKS:     "https://appleid.apple.com/auth/keys",
	AppleIssuer:   "https://appleid.apple.com",
}

// Config is what the environment enables. Read per request (cheap), so a test can change it.
type Config struct {
	AppURL string

	// Sign in with Google on the web: the OAuth client the auth-oauth plugin used (OAUTH_GOOGLE_*),
	// deliberately separate from the Google data connections (GOOGLE_INTEGRATIONS_*).
	GoogleClientID, GoogleClientSecret string
	// Extra audiences a native Google id_token may carry (the iOS and Android client ids).
	GoogleAudiences []string

	// Sign in with Apple. ServicesID is the web client (a Services ID); BundleIDs are the apps.
	AppleServicesID string
	AppleTeamID     string
	AppleKeyID      string
	AppleKey        *ecdsa.PrivateKey
	AppleKeyErr     error
	AppleBundleIDs  []string
}

func list(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// FromEnv reads the configuration.
func FromEnv() Config {
	c := Config{
		AppURL:             strings.TrimRight(os.Getenv("APP_URL"), "/"),
		GoogleClientID:     strings.TrimSpace(os.Getenv("OAUTH_GOOGLE_CLIENT_ID")),
		GoogleClientSecret: strings.TrimSpace(os.Getenv("OAUTH_GOOGLE_CLIENT_SECRET")),
		GoogleAudiences:    list(os.Getenv("OAUTH_GOOGLE_AUDIENCES")),
		AppleServicesID:    strings.TrimSpace(os.Getenv("APPLE_SERVICES_ID")),
		AppleTeamID:        strings.TrimSpace(os.Getenv("APPLE_TEAM_ID")),
		AppleKeyID:         strings.TrimSpace(os.Getenv("APPLE_KEY_ID")),
		AppleBundleIDs:     list(os.Getenv("APPLE_BUNDLE_IDS")),
	}
	raw := os.Getenv("APPLE_PRIVATE_KEY")
	if raw == "" {
		if p := strings.TrimSpace(os.Getenv("APPLE_PRIVATE_KEY_PATH")); p != "" {
			b, err := os.ReadFile(p) //#nosec G304 -- an operator-provided path
			if err != nil {
				c.AppleKeyErr = err
			}
			raw = string(b)
		}
	}
	if strings.TrimSpace(raw) != "" {
		c.AppleKey, c.AppleKeyErr = ParseAppleKey(raw)
	}
	return c
}

// ParseAppleKey reads the .p8 file Apple hands out (PKCS #8 PEM, P-256). An env var often loses
// its newlines, so a literal "\n" is accepted too.
func ParseAppleKey(raw string) (*ecdsa.PrivateKey, error) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("APPLE_PRIVATE_KEY is not a PEM (.p8) key")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("APPLE_PRIVATE_KEY is not an EC (ES256) key")
	}
	return ec, nil
}

// GoogleWeb reports whether Sign in with Google is on for the web (and the apps' browser flow).
func (c Config) GoogleWeb() bool { return c.GoogleClientID != "" && c.GoogleClientSecret != "" }

// GoogleNative reports whether native Google id_tokens are accepted.
func (c Config) GoogleNative() bool { return len(c.GoogleAudiences) > 0 }

func (c Config) googleAudiences() []string {
	out := append([]string(nil), c.GoogleAudiences...)
	if c.GoogleClientID != "" {
		out = append(out, c.GoogleClientID)
	}
	return out
}

func (c Config) appleKeyed() bool {
	return c.AppleTeamID != "" && c.AppleKeyID != "" && c.AppleKey != nil
}

// AppleWeb reports whether Sign in with Apple is on for the web (and Android, through the browser).
func (c Config) AppleWeb() bool { return c.AppleServicesID != "" && c.appleKeyed() }

// AppleNative reports whether the iOS app's native Sign in with Apple is accepted.
func (c Config) AppleNative() bool { return len(c.AppleBundleIDs) > 0 && c.appleKeyed() }

// appleClient reports whether id is one of Moharrik's Apple clients (the Services ID or a bundle id).
func (c Config) appleClient(id string) bool {
	if id == "" {
		return false
	}
	if id == c.AppleServicesID {
		return true
	}
	for _, b := range c.AppleBundleIDs {
		if b == id {
			return true
		}
	}
	return false
}

func (c Config) redirectURI(provider string) string {
	return c.AppURL + "/api/auth/oauth/" + provider + "/callback"
}
