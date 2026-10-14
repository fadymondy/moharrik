package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func postToken(ctx context.Context, endpoint string, form url.Values) (*tokenResponse, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var out tokenResponse
	_ = json.Unmarshal(body, &out)
	if res.StatusCode != http.StatusOK || out.IDToken == "" {
		msg := out.Error
		if msg == "" {
			msg = res.Status
		}
		return nil, fmt.Errorf("token endpoint: %s", msg)
	}
	return &out, nil
}

// googleExchange trades the web flow's code (with its PKCE verifier) and verifies the id_token.
func googleExchange(ctx context.Context, c Config, code, verifier, nonce string) (Person, error) {
	tok, err := postToken(ctx, Endpoints.GoogleToken, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {c.redirectURI("google")},
		"client_id": {c.GoogleClientID}, "client_secret": {c.GoogleClientSecret}, "code_verifier": {verifier},
	})
	if err != nil {
		return Person{}, err
	}
	claims, err := verifyIDToken(ctx, tok.IDToken, Endpoints.GoogleJWKS, Endpoints.GoogleIssuers, []string{c.GoogleClientID}, nonce)
	if err != nil {
		return Person{}, err
	}
	return personFrom("google", claims), nil
}

// googleNative verifies an id_token a native Google SDK obtained (any configured audience).
func googleNative(ctx context.Context, c Config, idToken string) (Person, error) {
	claims, err := verifyIDToken(ctx, idToken, Endpoints.GoogleJWKS, Endpoints.GoogleIssuers, c.googleAudiences(), "")
	if err != nil {
		return Person{}, err
	}
	return personFrom("google", claims), nil
}

// appleResult is a verified Apple sign-in and the refresh token to keep for revocation.
type appleResult struct {
	Person       Person
	ClientID     string
	RefreshToken string
}

// appleExchange trades an authorization code for clientID (the Services ID on the web, the bundle
// id in the app), verifies the id_token it returns and, when idToken is given (the app's
// identityToken), that both name the same person. The code exchange is what yields the refresh
// token that revocation needs.
func appleExchange(ctx context.Context, c Config, clientID, code, redirectURI, nonce string) (*appleResult, error) {
	secret, err := appleClientSecret(c, clientID)
	if err != nil {
		return nil, err
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "client_secret": {secret}}
	if redirectURI != "" {
		form.Set("redirect_uri", redirectURI)
	}
	tok, err := postToken(ctx, Endpoints.AppleToken, form)
	if err != nil {
		return nil, err
	}
	claims, err := verifyIDToken(ctx, tok.IDToken, Endpoints.AppleJWKS, []string{Endpoints.AppleIssuer}, []string{clientID}, nonce)
	if err != nil {
		return nil, err
	}
	return &appleResult{Person: personFrom("apple", claims), ClientID: clientID, RefreshToken: tok.RefreshToken}, nil
}

// appleRevoke ends the person's Sign in with Apple authorization for Moharrik (the account is
// deleted, or they unlinked Apple). Apple answers 200 for a token it no longer knows.
func appleRevoke(ctx context.Context, c Config, clientID, refreshToken string) error {
	if refreshToken == "" {
		return nil
	}
	if !c.appleClient(clientID) {
		return fmt.Errorf("unknown Apple client %q", clientID)
	}
	secret, err := appleClientSecret(c, clientID)
	if err != nil {
		return err
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {secret}, "token": {refreshToken}, "token_type_hint": {"refresh_token"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, Endpoints.AppleRevoke, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return errors.New("apple revoke: " + res.Status)
	}
	return nil
}
