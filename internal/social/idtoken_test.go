package social_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/fadymondy/moharrik/internal/social"
	"github.com/fadymondy/moharrik/internal/social/socialtest"
)

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/en/app-signin?redirect_uri=moharrik%3A%2F%2Fauth%2Fcallback": "/en/app-signin?redirect_uri=moharrik%3A%2F%2Fauth%2Fcallback",
		"/workspaces":          "/workspaces",
		"":                     "",
		"//evil.example/x":     "",
		"/\\evil.example":      "",
		"https://evil.example": "",
		"javascript:alert(1)":  "",
		"relative":             "",
		"/x\r\nSet-Cookie: a":  "",
	} {
		if got := social.SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTruthy(t *testing.T) {
	for v, want := range map[any]bool{true: true, "true": true, "TRUE": true, false: false, "false": false, nil: false, 1: false} {
		if social.Truthy(v) != want {
			t.Errorf("truthy(%v)", v)
		}
	}
}

func TestVerifyIDToken(t *testing.T) {
	f := socialtest.New(t)
	ctx := context.Background()
	apple := []string{"https://appleid.apple.com"}
	aud := []string{"com.fadymondy.moharrik"}
	base := func() jwt.MapClaims {
		return jwt.MapClaims{"iss": "https://appleid.apple.com", "aud": "com.fadymondy.moharrik", "sub": "001.abc",
			"email": "Someone@PrivateRelay.AppleID.com", "email_verified": "true", "nonce": "raw-nonce-0123456789"}
	}
	jwks := social.Endpoints.AppleJWKS

	claims, err := social.VerifyIDToken(ctx, f.Sign(base(), nil), jwks, apple, aud, "raw-nonce-0123456789")
	if err != nil {
		t.Fatalf("a good token: %v", err)
	}
	p := social.PersonFrom("apple", claims)
	if p.Subject != "001.abc" || !p.EmailVerified || p.Email != "someone@privaterelay.appleid.com" || p.Audience != "com.fadymondy.moharrik" {
		t.Errorf("person: %+v", p)
	}

	// The app hands Apple sha256(nonce) and the server the raw nonce.
	hashed := base()
	sum := sha256.Sum256([]byte("raw-nonce-0123456789"))
	hashed["nonce"] = hex.EncodeToString(sum[:])
	if _, err := social.VerifyIDToken(ctx, f.Sign(hashed, nil), jwks, apple, aud, "raw-nonce-0123456789"); err != nil {
		t.Errorf("a hashed nonce: %v", err)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	bad := map[string]func() (string, string){
		"forged signature": func() (string, string) { return f.Sign(base(), other), "raw-nonce-0123456789" },
		"wrong audience": func() (string, string) {
			c := base()
			c["aud"] = "com.someone.else"
			return f.Sign(c, nil), "raw-nonce-0123456789"
		},
		"wrong issuer": func() (string, string) {
			c := base()
			c["iss"] = "https://evil.example"
			return f.Sign(c, nil), "raw-nonce-0123456789"
		},
		"expired": func() (string, string) {
			c := base()
			c["iat"] = time.Now().Add(-2 * time.Hour).Unix()
			c["exp"] = time.Now().Add(-time.Hour).Unix()
			return f.Sign(c, nil), "raw-nonce-0123456789"
		},
		"wrong nonce":   func() (string, string) { return f.Sign(base(), nil), "another-nonce-0123456" },
		"missing nonce": func() (string, string) { c := base(); delete(c, "nonce"); return f.Sign(c, nil), "raw-nonce-0123456789" },
		"no subject":    func() (string, string) { c := base(); delete(c, "sub"); return f.Sign(c, nil), "" },
		"garbage":       func() (string, string) { return "not.a.jwt", "" },
	}
	for name, mk := range bad {
		tok, nonce := mk()
		if _, err := social.VerifyIDToken(ctx, tok, jwks, apple, aud, nonce); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAppleClientSecret(t *testing.T) {
	f := socialtest.New(t)
	key, err := social.ParseAppleKey(f.AppleP8)
	if err != nil {
		t.Fatal(err)
	}
	// An env var that lost its newlines still parses.
	if _, err := social.ParseAppleKey(stringsReplaceNewlines(f.AppleP8)); err != nil {
		t.Errorf("escaped newlines: %v", err)
	}
	c := social.Config{AppleTeamID: "JW9HJH86GC", AppleKeyID: "KEY123", AppleKey: key}
	s, err := social.AppleClientSecret(c, "com.fadymondy.moharrik.web")
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jwt.Parse(s, func(*jwt.Token) (any, error) { return f.ApplePub, nil }, jwt.WithValidMethods([]string{"ES256"}),
		jwt.WithIssuer("JW9HJH86GC"), jwt.WithAudience("https://appleid.apple.com"), jwt.WithSubject("com.fadymondy.moharrik.web"))
	if err != nil || tok.Header["kid"] != "KEY123" {
		t.Fatalf("client secret: %v %v", err, tok.Header)
	}
	if _, err := social.AppleClientSecret(social.Config{}, "x"); err == nil {
		t.Error("an unconfigured key must not sign")
	}
}

func stringsReplaceNewlines(s string) string {
	out := []byte{}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, '\\', 'n')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}
