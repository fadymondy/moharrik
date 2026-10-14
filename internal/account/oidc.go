package account

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Endpoints are the providers' URLs. Variables so a test can stand a fake in
// for Google and Apple; production never changes them.
var Endpoints = struct {
	GoogleAuth, GoogleToken, GoogleKeys      string
	AppleAuth, AppleToken, AppleKeys, Revoke string
}{
	GoogleAuth:  "https://accounts.google.com/o/oauth2/v2/auth",
	GoogleToken: "https://oauth2.googleapis.com/token",
	GoogleKeys:  "https://www.googleapis.com/oauth2/v3/certs",
	AppleAuth:   "https://appleid.apple.com/auth/authorize",
	AppleToken:  "https://appleid.apple.com/auth/token",
	AppleKeys:   "https://appleid.apple.com/auth/keys",
	Revoke:      "https://appleid.apple.com/auth/revoke",
}

var (
	googleIssuers = []string{"https://accounts.google.com", "accounts.google.com"}
	appleIssuers  = []string{"https://appleid.apple.com"}
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// claims is what Moharrik reads from a provider's ID token.
type claims struct {
	Subject  string
	Email    string
	Verified bool
	Nonce    string
}

// keySet caches a provider's signing keys (a JWKS), refetching when a token
// names a key it does not have — that is how a rotation shows up — but at most
// once a minute, so a stream of forged kids cannot hammer the provider.
type keySet struct {
	url     func() string
	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

var (
	googleKeys = &keySet{url: func() string { return Endpoints.GoogleKeys }}
	appleKeys  = &keySet{url: func() string { return Endpoints.AppleKeys }}
)

func (s *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}
	if time.Since(s.fetched) < time.Minute && s.keys != nil {
		return nil, errors.New("unknown signing key")
	}
	keys, err := fetchKeys(ctx, s.url())
	s.fetched = time.Now()
	if err != nil {
		return nil, err
	}
	s.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, errors.New("unknown signing key")
}

func fetchKeys(ctx context.Context, u string) (map[string]*rsa.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signing keys: %s", res.Status)
	}
	var doc struct {
		Keys []struct {
			Kty, Kid, N, E string
		} `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&doc); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) > 4 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return out, nil
}

// verifyIDToken checks an ID token's signature against the provider's keys,
// its issuer, its expiry and that it was issued to one of audiences.
func verifyIDToken(ctx context.Context, keys *keySet, raw string, issuers, audiences []string) (claims, error) {
	mc := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, mc, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return keys.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithLeeway(time.Minute))
	if err != nil {
		return claims{}, fmt.Errorf("the sign-in token is not valid: %w", err)
	}
	iss, _ := mc["iss"].(string)
	if !contains(issuers, iss) {
		return claims{}, errors.New("the sign-in token is from someone else")
	}
	aud, _ := mc.GetAudience()
	ok := false
	for _, a := range aud {
		if a != "" && contains(audiences, a) {
			ok = true
		}
	}
	if !ok {
		return claims{}, errors.New("the sign-in token was issued to another app")
	}
	c := claims{}
	c.Subject, _ = mc["sub"].(string)
	c.Email, _ = mc["email"].(string)
	c.Nonce, _ = mc["nonce"].(string)
	// Google sends a boolean; Apple has sent both a boolean and "true".
	switch v := mc["email_verified"].(type) {
	case bool:
		c.Verified = v
	case string:
		c.Verified = v == "true"
	}
	if c.Subject == "" {
		return claims{}, errors.New("the sign-in token names nobody")
	}
	return c, nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// postForm sends a form to a provider's token or revoke endpoint and decodes
// the JSON answer into out (nil: the body is ignored).
func postForm(ctx context.Context, u string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error == "" {
			e.Error = res.Status
		}
		return fmt.Errorf("the provider refused: %s", e.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}

// appleClientSecret is the short-lived ES256 JWT Apple takes in place of a
// client secret, signed with the Sign in with Apple key (.p8) and naming the
// client id the request is for (the bundle id or the Services ID).
func (c *Apple) clientSecret(clientID string) (string, error) {
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.TeamID,
		"iat": now.Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": clientID,
	})
	t.Header["kid"] = c.KeyID
	return t.SignedString(c.key)
}

// parseP8 reads the Sign in with Apple private key: the .p8 file's PEM text,
// with literal "\n" accepted for an environment variable that cannot hold
// newlines.
func parseP8(raw string) (*ecdsa.PrivateKey, error) {
	raw = strings.ReplaceAll(strings.TrimSpace(raw), `\n`, "\n")
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return nil, errors.New("OAUTH_APPLE_PRIVATE_KEY is not a PEM private key (paste the .p8 file's contents)")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("OAUTH_APPLE_PRIVATE_KEY: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("OAUTH_APPLE_PRIVATE_KEY is not an EC key")
	}
	return ec, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
