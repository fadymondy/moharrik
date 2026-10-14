package social

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// jwks caches a provider's signing keys by URL. Keys rotate rarely; an unknown kid refetches
// (at most once a minute), so a rotation is picked up on the first token signed with the new key.
type jwksCache struct {
	mu      sync.Mutex
	keys    map[string]map[string]*rsa.PublicKey
	fetched map[string]time.Time
}

var keys = &jwksCache{keys: map[string]map[string]*rsa.PublicKey{}, fetched: map[string]time.Time{}}

// ResetKeys forgets every cached key (tests point Endpoints at a new fake).
func ResetKeys() {
	keys.mu.Lock()
	keys.keys = map[string]map[string]*rsa.PublicKey{}
	keys.fetched = map[string]time.Time{}
	keys.mu.Unlock()
}

func (c *jwksCache) key(ctx context.Context, url, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if k := c.keys[url][kid]; k != nil && time.Since(c.fetched[url]) < 24*time.Hour {
		return k, nil
	}
	if time.Since(c.fetched[url]) < time.Minute && c.keys[url] != nil {
		if k := c.keys[url][kid]; k != nil {
			return k, nil
		}
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	set, err := fetchJWKS(ctx, url)
	if err != nil {
		return nil, err
	}
	c.keys[url], c.fetched[url] = set, time.Now()
	if k := set[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func fetchJWKS(ctx context.Context, url string) (map[string]*rsa.PublicKey, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, err
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	return out, nil
}

// Person is what a provider vouched for in a verified id_token.
type Person struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Audience      string
}

// verifyIDToken checks an OpenID Connect id_token: RS256 signature against the provider's keys,
// issuer, audience (one of audiences), expiry and — when nonce is not "" — the nonce. Apple's
// native flow commonly sends sha256(nonce) to Apple and the raw nonce to the server, so the claim
// may be either.
func verifyIDToken(ctx context.Context, raw, jwksURL string, issuers, audiences []string, nonce string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return keys.key(ctx, jwksURL, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithLeeway(2*time.Minute))
	if err != nil {
		return nil, err
	}
	iss, _ := claims["iss"].(string)
	if !slices.Contains(issuers, iss) {
		return nil, fmt.Errorf("unexpected issuer %q", iss)
	}
	aud, _ := claims.GetAudience()
	ok := false
	for _, a := range aud {
		if a != "" && slices.Contains(audiences, a) {
			ok = true
		}
	}
	if !ok {
		return nil, errors.New("the token is for another client")
	}
	if sub, _ := claims["sub"].(string); sub == "" {
		return nil, errors.New("no subject")
	}
	if nonce != "" {
		got, _ := claims["nonce"].(string)
		if got == "" || (got != nonce && got != sha256Hex(nonce)) {
			return nil, errors.New("nonce mismatch")
		}
	}
	return claims, nil
}

// truthy reads a claim that may be a JSON bool or the string "true" (Apple sends strings).
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return strings.EqualFold(x, "true")
	}
	return false
}

func personFrom(provider string, c jwt.MapClaims) Person {
	p := Person{Provider: provider}
	p.Subject, _ = c["sub"].(string)
	p.Email, _ = c["email"].(string)
	p.Email = strings.ToLower(strings.TrimSpace(p.Email))
	p.EmailVerified = truthy(c["email_verified"])
	p.Name, _ = c["name"].(string)
	if aud, _ := c.GetAudience(); len(aud) > 0 {
		p.Audience = aud[0]
	}
	return p
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// appleClientSecret is the ES256 JWT Apple takes as client_secret, for clientID (the Services ID
// on the web, the bundle id for the app's code). Valid five minutes: it is made per request.
func appleClientSecret(c Config, clientID string) (string, error) {
	if !c.appleKeyed() {
		return "", errors.New("Sign in with Apple is not configured (APPLE_TEAM_ID, APPLE_KEY_ID, APPLE_PRIVATE_KEY)")
	}
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": c.AppleTeamID,
		"iat": now.Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
		"aud": "https://appleid.apple.com",
		"sub": clientID,
	})
	t.Header["kid"] = c.AppleKeyID
	return t.SignedString((*ecdsa.PrivateKey)(c.AppleKey))
}
