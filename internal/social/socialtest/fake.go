// Package socialtest is a fake Google and Apple for the sign-in tests: a JWKS, token endpoints
// that trade a code for an id_token the test chose, and Apple's revoke endpoint. Point
// social.Endpoints at it with Use.
package socialtest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/moharrik/internal/social"
)

// Fake is the fake providers.
type Fake struct {
	t      *testing.T
	URL    string
	Key    *rsa.PrivateKey
	KID    string
	AppleP8 string // a PEM .p8 the server signs its Apple client secret with
	ApplePub *ecdsa.PublicKey

	mu      sync.Mutex
	codes   map[string]grant
	Revoked []Revocation
	// Calls to the token endpoints, by provider.
	TokenCalls map[string]int
}

type grant struct {
	provider string
	claims   jwt.MapClaims
	refresh  string
}

// Revocation is one call to Apple's revoke endpoint.
type Revocation struct{ ClientID, Token string }

// New starts the fake and points social.Endpoints at it for the test's duration.
func New(t *testing.T) *Fake {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKCS8PrivateKey(ec)
	f := &Fake{t: t, Key: key, KID: "fake-kid-1", codes: map[string]grant{}, TokenCalls: map[string]int{},
		AppleP8: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), ApplePub: &ec.PublicKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/jwks", f.jwks)
	mux.HandleFunc("/google/token", f.token("google"))
	mux.HandleFunc("/apple/token", f.token("apple"))
	mux.HandleFunc("/apple/revoke", f.revoke)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.URL = srv.URL

	old := social.Endpoints
	social.Endpoints.GoogleAuth = srv.URL + "/google/auth"
	social.Endpoints.GoogleToken = srv.URL + "/google/token"
	social.Endpoints.GoogleJWKS = srv.URL + "/jwks"
	social.Endpoints.AppleAuth = srv.URL + "/apple/auth"
	social.Endpoints.AppleToken = srv.URL + "/apple/token"
	social.Endpoints.AppleRevoke = srv.URL + "/apple/revoke"
	social.Endpoints.AppleJWKS = srv.URL + "/jwks"
	social.ResetKeys()
	t.Cleanup(func() { social.Endpoints = old; social.ResetKeys() })
	return f
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *Fake) jwks(w http.ResponseWriter, _ *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": f.KID, "alg": "RS256", "use": "sig",
		"n": b64(f.Key.N.Bytes()), "e": b64(big.NewInt(int64(f.Key.E)).Bytes()),
	}}})
}

// Sign makes an id_token with the fake's key (or another key, to test a forged one).
func (f *Fake) Sign(claims jwt.MapClaims, key *rsa.PrivateKey) string {
	if key == nil {
		key = f.Key
	}
	now := time.Now()
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = now.Unix()
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = now.Add(10 * time.Minute).Unix()
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.KID
	s, err := tok.SignedString(key)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// Code registers an authorization code the token endpoint will trade for an id_token with these
// claims (and, for Apple, a refresh token).
func (f *Fake) Code(provider string, claims jwt.MapClaims) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	code := provider + "-code-" + b64(b)
	f.mu.Lock()
	f.codes[code] = grant{provider: provider, claims: claims, refresh: "rt-" + b64(b)}
	f.mu.Unlock()
	return code
}

// RefreshFor is the refresh token the fake hands out for code.
func (f *Fake) RefreshFor(code string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.codes[code].refresh
}

func (f *Fake) checkAppleSecret(r *http.Request) bool {
	clientID := r.PostForm.Get("client_id")
	tok, err := jwt.Parse(r.PostForm.Get("client_secret"), func(*jwt.Token) (any, error) { return f.ApplePub, nil },
		jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience("https://appleid.apple.com"), jwt.WithSubject(clientID))
	return err == nil && tok.Valid && tok.Header["kid"] != ""
}

func (f *Fake) token(provider string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		f.TokenCalls[provider]++
		g, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		if !ok || g.provider != provider {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		if provider == "apple" && !f.checkAppleSecret(r) {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		if provider == "google" && (r.PostForm.Get("client_secret") == "" || r.PostForm.Get("code_verifier") == "") {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
			return
		}
		claims := jwt.MapClaims{}
		for k, v := range g.claims {
			claims[k] = v
		}
		if _, ok := claims["aud"]; !ok {
			claims["aud"] = r.PostForm.Get("client_id")
		}
		out := map[string]string{"access_token": "at", "id_token": f.Sign(claims, nil)}
		if provider == "apple" {
			out["refresh_token"] = g.refresh
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

func (f *Fake) revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !f.checkAppleSecret(r) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.Revoked = append(f.Revoked, Revocation{ClientID: r.PostForm.Get("client_id"), Token: r.PostForm.Get("token")})
	f.mu.Unlock()
}

// Revocations returns a copy of the revoke calls so far.
func (f *Fake) Revocations() []Revocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Revocation(nil), f.Revoked...)
}
