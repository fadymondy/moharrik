package account

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"net/url"
	"os"
	"strings"
)

// Google is "Sign in with Google": the web client (OAUTH_GOOGLE_CLIENT_ID and
// _SECRET — the sign-in client, never the GOOGLE_INTEGRATIONS_* data client).
type Google struct {
	ClientID, Secret string
}

// Apple is "Sign in with Apple". One key (.p8) serves both the iOS app, whose
// client id is the bundle id, and the web, whose client id is the Services ID.
type Apple struct {
	ServicesID, BundleID, TeamID, KeyID string
	key                                 *ecdsa.PrivateKey
}

// Providers is what is configured on this server; nil = off, and every
// button for it is hidden (GET /api/auth/app/methods, GET /api/auth/methods).
type Providers struct {
	Google *Google
	Apple  *Apple
}

// AppleWeb reports whether the web sign-in (Services ID) is configured.
func (p Providers) AppleWeb() bool { return p.Apple != nil && p.Apple.ServicesID != "" }

// AppleNative reports whether the iOS app's native sign-in is configured.
func (p Providers) AppleNative() bool { return p.Apple != nil && p.Apple.BundleID != "" }

// ProvidersFromEnv reads the providers' configuration. A half-configured
// provider is off, with the reason returned for the boot log; nothing is
// fatal, because email and password sign-in works without either.
func ProvidersFromEnv() (Providers, []string) {
	var p Providers
	var problems []string
	env := func(k string) string { return strings.TrimSpace(os.Getenv(k)) }

	if id, sec := env("OAUTH_GOOGLE_CLIENT_ID"), env("OAUTH_GOOGLE_CLIENT_SECRET"); id != "" || sec != "" {
		if id == "" || sec == "" {
			problems = append(problems, "Google sign-in is off: set both OAUTH_GOOGLE_CLIENT_ID and OAUTH_GOOGLE_CLIENT_SECRET")
		} else {
			p.Google = &Google{ClientID: id, Secret: sec}
		}
	}

	team, kid, pk := env("OAUTH_APPLE_TEAM_ID"), env("OAUTH_APPLE_KEY_ID"), os.Getenv("OAUTH_APPLE_PRIVATE_KEY")
	services := env("OAUTH_APPLE_SERVICES_ID")
	if team != "" || kid != "" || strings.TrimSpace(pk) != "" || services != "" {
		key, err := parseP8(pk)
		switch {
		case team == "" || kid == "":
			problems = append(problems, "Apple sign-in is off: set OAUTH_APPLE_TEAM_ID, OAUTH_APPLE_KEY_ID and OAUTH_APPLE_PRIVATE_KEY")
		case err != nil:
			problems = append(problems, "Apple sign-in is off: "+err.Error())
		default:
			bundle := env("OAUTH_APPLE_BUNDLE_ID")
			if bundle == "" {
				bundle = "com.fadymondy.moharrik"
			}
			p.Apple = &Apple{ServicesID: services, BundleID: bundle, TeamID: team, KeyID: kid, key: key}
		}
	}
	return p, problems
}

// exchangeGoogle trades a web authorization code (with its PKCE verifier) for
// the person's verified identity.
func exchangeGoogle(ctx context.Context, g *Google, code, verifier, redirectURI string) (claims, error) {
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := postForm(ctx, Endpoints.GoogleToken, url.Values{
		"client_id": {g.ClientID}, "client_secret": {g.Secret}, "code": {code},
		"code_verifier": {verifier}, "grant_type": {"authorization_code"}, "redirect_uri": {redirectURI},
	}, &tok); err != nil {
		return claims{}, err
	}
	if tok.IDToken == "" {
		return claims{}, errors.New("Google returned no identity")
	}
	return verifyIDToken(ctx, googleKeys, tok.IDToken, googleIssuers, []string{g.ClientID})
}

// exchangeApple trades an authorization code for the person's identity and
// the refresh token that deleting the account revokes. clientID is the bundle
// id for a code from the iOS app (no redirect) or the Services ID for the web.
func exchangeApple(ctx context.Context, a *Apple, clientID, code, redirectURI string) (claims, string, error) {
	secret, err := a.clientSecret(clientID)
	if err != nil {
		return claims{}, "", err
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}}
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	var tok struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := postForm(ctx, Endpoints.AppleToken, form, &tok); err != nil {
		return claims{}, "", err
	}
	if tok.IDToken == "" {
		return claims{}, "", errors.New("Apple returned no identity")
	}
	c, err := verifyIDToken(ctx, appleKeys, tok.IDToken, appleIssuers, []string{clientID})
	return c, tok.RefreshToken, err
}

// revokeApple tells Apple the person deleted their account, so the app no
// longer appears under "Sign in with Apple" in their Apple ID settings.
func revokeApple(ctx context.Context, a *Apple, clientID, refreshToken string) error {
	secret, err := a.clientSecret(clientID)
	if err != nil {
		return err
	}
	return postForm(ctx, Endpoints.Revoke, url.Values{
		"client_id": {clientID}, "client_secret": {secret}, "token": {refreshToken}, "token_type_hint": {"refresh_token"},
	}, nil)
}
