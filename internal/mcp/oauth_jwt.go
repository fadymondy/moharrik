package mcp

/*
private_key_jwt client authentication (RFC 7523 section 2.2, OIDC Core 9).

ChatGPT's Client ID Metadata Document (https://chatgpt.com/oauth/client.json,
checked 2026-09-17) declares:

	token_endpoint_auth_method   private_key_jwt
	token_endpoint_auth_signing_alg RS256
	jwks_uri                     https://chatgpt.com/oauth/jwks.json

so at the token endpoint it sends a JWT signed with its private key instead of
a secret. This file verifies that assertion:

  - asymmetric algorithms only (RS*, PS*, ES*); `none` and HMAC are refused —
    an HMAC "signature" keyed with a public key is the classic confusion;
  - the key comes from the client's own JWKS (inline, or jwks_uri fetched with
    the same SSRF guard as the metadata document), cached, and refetched once
    when an unknown kid appears (key rotation);
  - iss == sub == client_id; aud contains the token endpoint URL or the issuer;
  - exp in the future and at most assertionMaxLifetime away; iat/nbf not in
    the future beyond a small skew;
  - jti single-use, remembered until exp.

Any failure is invalid_client, and nothing is stored before the assertion is
fully verified — the jti row is the last write, and it is what makes the
assertion spent.
*/

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	clientAssertionType  = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	assertionMaxLifetime = 10 * time.Minute
	assertionSkew        = time.Minute
	jwksCacheTTL         = 10 * time.Minute
	jwksMinRefetch       = 30 * time.Second
)

// assertionAlgs is the allow-list, advertised in the metadata too.
var assertionAlgs = []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384"}

func allowedAlg(alg string) bool {
	for _, a := range assertionAlgs {
		if a == alg {
			return true
		}
	}
	return false
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

func parseJWKS(raw []byte) (*jwkSet, error) {
	var set jwkSet
	if err := json.Unmarshal(raw, &set); err != nil || len(set.Keys) == 0 {
		return nil, errors.New("JWKS is not a JSON key set")
	}
	return &set, nil
}

func b64int(s string) (*big.Int, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(b) == 0 {
		return nil, errors.New("bad key component")
	}
	return new(big.Int).SetBytes(b), nil
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	switch k.Kty {
	case "RSA":
		n, err := b64int(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64int(k.E)
		if err != nil || !e.IsInt64() || e.Int64() < 3 {
			return nil, errors.New("bad RSA exponent")
		}
		if n.BitLen() < 2048 {
			return nil, errors.New("RSA key shorter than 2048 bits")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		default:
			return nil, errors.New("unsupported EC curve")
		}
		x, err := b64int(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64int(k.Y)
		if err != nil {
			return nil, err
		}
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("EC point not on curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	}
	return nil, errors.New("unsupported key type")
}

func verifySignature(alg string, key crypto.PublicKey, signingInput, sig []byte) error {
	var h hash.Hash
	var ch crypto.Hash
	switch alg[2:] {
	case "256":
		h, ch = sha256.New(), crypto.SHA256
	case "384":
		h, ch = sha512.New384(), crypto.SHA384
	case "512":
		h, ch = sha512.New(), crypto.SHA512
	default:
		return errors.New("unsupported alg")
	}
	h.Write(signingInput)
	digest := h.Sum(nil)

	switch alg[:2] {
	case "RS":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("key type does not match alg")
		}
		return rsa.VerifyPKCS1v15(pub, ch, digest, sig)
	case "PS":
		pub, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("key type does not match alg")
		}
		return rsa.VerifyPSS(pub, ch, digest, sig, &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash})
	case "ES":
		pub, ok := key.(*ecdsa.PublicKey)
		if !ok {
			return errors.New("key type does not match alg")
		}
		size := (pub.Curve.Params().BitSize + 7) / 8
		if (alg == "ES256" && pub.Curve != elliptic.P256()) || (alg == "ES384" && pub.Curve != elliptic.P384()) {
			return errors.New("curve does not match alg")
		}
		if len(sig) != 2*size {
			return errors.New("bad ECDSA signature length")
		}
		r := new(big.Int).SetBytes(sig[:size])
		s := new(big.Int).SetBytes(sig[size:])
		if !ecdsa.Verify(pub, digest, r, s) {
			return errors.New("bad signature")
		}
		return nil
	}
	return errors.New("unsupported alg")
}

// jwksCache holds fetched key sets per URI.
type jwksCache struct {
	mu      sync.Mutex
	entries map[string]*jwksEntry
}

type jwksEntry struct {
	set     *jwkSet
	fetched time.Time
}

func newJWKSCache() *jwksCache { return &jwksCache{entries: map[string]*jwksEntry{}} }

// keysFor returns the client's key set. refresh forces a refetch (rate-limited)
// for an unknown kid.
func (s *oauthServer) keysFor(ctx context.Context, c *oauthClient, refresh bool) (*jwkSet, error) {
	if c.JWKS != "" {
		return parseJWKS([]byte(c.JWKS))
	}
	if c.JWKSURI == "" {
		return nil, errors.New("client has no keys")
	}
	now := s.now()
	s.jwks.mu.Lock()
	entry := s.jwks.entries[c.JWKSURI]
	s.jwks.mu.Unlock()
	if entry != nil {
		age := now.Sub(entry.fetched)
		if age < jwksCacheTTL && (!refresh || age < jwksMinRefetch) {
			return entry.set, nil
		}
	}
	raw, err := s.fetchJWKS(ctx, c.JWKSURI)
	if err != nil {
		return nil, fmt.Errorf("could not fetch the client's keys: %w", err)
	}
	set, err := parseJWKS(raw)
	if err != nil {
		return nil, err
	}
	s.jwks.mu.Lock()
	s.jwks.entries[c.JWKSURI] = &jwksEntry{set: set, fetched: now}
	s.jwks.mu.Unlock()
	return set, nil
}

func validJWKSURI(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Fragment == "" && len(raw) <= 2000
}

func pickKey(set *jwkSet, kid, alg string) (crypto.PublicKey, bool) {
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		if kid != "" && k.Kid != kid {
			continue
		}
		if k.Alg != "" && k.Alg != alg {
			continue
		}
		if pub, err := k.publicKey(); err == nil {
			return pub, true
		}
	}
	return nil, false
}

type assertionClaims struct {
	Iss string          `json:"iss"`
	Sub string          `json:"sub"`
	Aud json.RawMessage `json:"aud"`
	Exp *float64        `json:"exp"`
	Iat *float64        `json:"iat"`
	Nbf *float64        `json:"nbf"`
	Jti string          `json:"jti"`
}

func (c assertionClaims) audiences() []string {
	var one string
	if json.Unmarshal(c.Aud, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(c.Aud, &many)
	return many
}

func unix(f float64) time.Time { return time.Unix(int64(f), 0).UTC() }

// verifyClientAssertion checks a private_key_jwt assertion for client c and
// spends its jti. It stores nothing unless every check has passed.
func (s *oauthServer) verifyClientAssertion(ctx context.Context, c *oauthClient, assertion string) error {
	parts := strings.Split(assertion, ".")
	if len(parts) != 3 || len(assertion) > 16<<10 {
		return errors.New("malformed assertion")
	}
	headerRaw, err1 := base64.RawURLEncoding.DecodeString(parts[0])
	payloadRaw, err2 := base64.RawURLEncoding.DecodeString(parts[1])
	sig, err3 := base64.RawURLEncoding.DecodeString(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return errors.New("malformed assertion")
	}
	var header struct {
		Alg  string `json:"alg"`
		Kid  string `json:"kid"`
		Crit any    `json:"crit"`
	}
	if json.Unmarshal(headerRaw, &header) != nil || header.Crit != nil {
		return errors.New("malformed assertion header")
	}
	if !allowedAlg(header.Alg) {
		return fmt.Errorf("alg %q is not allowed", header.Alg)
	}

	set, err := s.keysFor(ctx, c, false)
	if err != nil {
		return err
	}
	key, ok := pickKey(set, header.Kid, header.Alg)
	if !ok {
		// Rotation: the client may have published a new key since we cached.
		if set, err = s.keysFor(ctx, c, true); err != nil {
			return err
		}
		if key, ok = pickKey(set, header.Kid, header.Alg); !ok {
			return errors.New("no matching key for the assertion")
		}
	}
	if err := verifySignature(header.Alg, key, []byte(parts[0]+"."+parts[1]), sig); err != nil {
		return errors.New("assertion signature is invalid")
	}

	var claims assertionClaims
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return errors.New("malformed assertion claims")
	}
	if claims.Iss != c.ID || claims.Sub != c.ID {
		return errors.New("assertion iss and sub must be the client_id")
	}
	tokenEndpoint := OAuthIssuer() + "/api/oauth/token"
	audOK := false
	for _, a := range claims.audiences() {
		if a == tokenEndpoint || a == OAuthIssuer() {
			audOK = true
		}
	}
	if !audOK {
		return errors.New("assertion audience is not this server")
	}
	now := s.now()
	if claims.Exp == nil {
		return errors.New("assertion has no exp")
	}
	exp := unix(*claims.Exp)
	if !now.Before(exp) {
		return errors.New("assertion has expired")
	}
	if exp.Sub(now) > assertionMaxLifetime {
		return errors.New("assertion lifetime is too long")
	}
	if claims.Iat != nil {
		iat := unix(*claims.Iat)
		if iat.After(now.Add(assertionSkew)) || exp.Sub(iat) > assertionMaxLifetime+assertionSkew {
			return errors.New("assertion iat is not acceptable")
		}
	}
	if claims.Nbf != nil && unix(*claims.Nbf).After(now.Add(assertionSkew)) {
		return errors.New("assertion is not valid yet")
	}
	if claims.Jti == "" || len(claims.Jti) > 512 {
		return errors.New("assertion has no jti")
	}

	// Spend the jti. The insert is the replay check: a second presentation
	// conflicts and is refused.
	_, _ = s.a.SysDB.ExecContext(ctx, `DELETE FROM oauth_client_assertions WHERE expires_at < $1`, now)
	res, err := s.a.SysDB.ExecContext(ctx, `
		INSERT INTO oauth_client_assertions (client_id, jti, expires_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, c.ID, claims.Jti, exp.Add(assertionSkew))
	if err != nil {
		return errors.New("could not record the assertion")
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("assertion has already been used")
	}
	return nil
}
