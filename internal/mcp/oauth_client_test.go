package mcp

/*
OAuth client identification (MH-391): the redirect-URI rules, Client ID
Metadata Documents (what claude.ai offers) and private_key_jwt (what ChatGPT's
metadata document declares), against a fake fetcher — never the internet.
The assertion tests need a database for the single-use jti.
*/

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestRedirectURIRules(t *testing.T) {
	for uri, want := range map[string]bool{
		"https://claude.ai/api/mcp/auth_callback":               true,
		"https://chatgpt.com/connector_platform_oauth_redirect": true,
		"http://127.0.0.1:6274/oauth/callback":                  true, // RFC 8252 loopback, any port
		"http://localhost:33418/cb":                             true,
		"http://[::1]:9/cb":                                     true,
		"http://claude.ai/cb":                                   false, // plain http off loopback
		"https://claude.ai/cb#fragment":                         false,
		"https://user:pass@claude.ai/cb":                        false,
		"com.example.app:/cb":                                   false,
		"/relative/cb":                                          false,
		"":                                                      false,
		"https://" + strings.Repeat("a", 2001):                  false,
	} {
		if got := validRedirectURI(uri); got != want {
			t.Errorf("validRedirectURI(%q) = %v, want %v", uri, got, want)
		}
	}
	c := &oauthClient{RedirectURIs: []string{"https://client.example/cb"}}
	for uri, want := range map[string]bool{
		"https://client.example/cb":     true,
		"https://client.example/cb/":    false,
		"https://client.example/cb?x=1": false,
		"https://CLIENT.example/cb":     false,
		"https://client.example/c":      false,
	} {
		if got := c.allowsRedirect(uri); got != want {
			t.Errorf("allowsRedirect(%q) = %v, want %v (exact match only)", uri, got, want)
		}
	}
}

func TestPKCE(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 appendix B.
	if !pkceMatches(verifier, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM") {
		t.Error("the RFC 7636 example does not verify")
	}
	if pkceMatches(verifier, verifier) {
		t.Error("plain must not verify as S256")
	}
}

// ---- a fixture with a database ------------------------------------------------

type cimdFix struct {
	t    *testing.T
	s    *oauthServer
	docs map[string]string
	key  *rsa.PrivateKey
}

const cimdID = "https://connector.example/oauth/client.json"
const jwksURI = "https://connector.example/oauth/jwks.json"

func newCIMDFix(t *testing.T) *cimdFix {
	t.Helper()
	owner, appURL := testdb.Setup(t, "mcp", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("OAUTH_ISSUER", "https://app.moharrik.test")
	k := togo.New()
	t.Cleanup(k.Close)
	a := app.New(context.Background(), k)
	if a.SysDB == nil {
		t.Fatal("no database")
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &cimdFix{t: t, s: newOAuthServer(a), docs: map[string]string{}, key: key}
	fetch := func(_ context.Context, u string) ([]byte, error) {
		if d, ok := f.docs[u]; ok {
			return []byte(d), nil
		}
		return nil, errors.New("not found")
	}
	f.s.fetchCIMD, f.s.fetchJWKS = fetch, fetch
	_, _ = a.SysDB.Exec(`DELETE FROM oauth_clients WHERE id = $1`, cimdID)
	t.Cleanup(func() { _, _ = a.SysDB.Exec(`DELETE FROM oauth_clients WHERE id = $1`, cimdID) })
	pub := key.Public().(*rsa.PublicKey)
	f.docs[jwksURI] = `{"keys":[{"kty":"RSA","kid":"k1","use":"sig","alg":"RS256","n":"` +
		b64(pub.N.Bytes()) + `","e":"` + b64(big.NewInt(int64(pub.E)).Bytes()) + `"}]}`
	return f
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (f *cimdFix) doc(m map[string]any) {
	b, _ := json.Marshal(m)
	f.docs[cimdID] = string(b)
}

func (f *cimdFix) assertion(alg string, mutate func(map[string]any)) string {
	now := time.Now().Unix()
	claims := map[string]any{"iss": cimdID, "sub": cimdID, "aud": OAuthIssuer() + "/api/oauth/token",
		"exp": now + 60, "iat": now, "jti": "j-" + randomToken(8)}
	if mutate != nil {
		mutate(claims)
	}
	h, _ := json.Marshal(map[string]string{"alg": alg, "kid": "k1", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	input := b64(h) + "." + b64(p)
	var sig []byte
	switch alg {
	case "RS256":
		sum := sha256.Sum256([]byte(input))
		sig, _ = rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	case "HS256":
		m := hmac.New(sha256.New, []byte("the public key as a secret"))
		m.Write([]byte(input))
		sig = m.Sum(nil)
	}
	return input + "." + b64(sig)
}

func TestClientIDMetadataDocuments(t *testing.T) {
	f := newCIMDFix(t)
	ctx := context.Background()

	f.doc(map[string]any{"client_id": cimdID, "client_name": "Connector", "redirect_uris": []string{"https://connector.example/cb"}})
	c, err := f.s.loadClient(ctx, cimdID, true)
	if err != nil || c.Name != "Connector" || c.AuthMethod != "none" || !c.CIMD {
		t.Fatalf("a public metadata document: %v %+v", err, c)
	}
	// Stored, so the token step can use it without fetching.
	if c2, err := f.s.loadClient(ctx, cimdID, false); err != nil || c2.RedirectURIs[0] != "https://connector.example/cb" {
		t.Errorf("the stored copy: %v %+v", err, c2)
	}

	for name, doc := range map[string]map[string]any{
		"client_id does not match its URL": {"client_id": "https://evil.example/client.json", "client_name": "X", "redirect_uris": []string{"https://connector.example/cb"}},
		"plain-http redirect":              {"client_id": cimdID, "client_name": "X", "redirect_uris": []string{"http://connector.example/cb"}},
		"no redirect URIs":                 {"client_id": cimdID, "client_name": "X"},
		"a shared secret method":           {"client_id": cimdID, "client_name": "X", "redirect_uris": []string{"https://connector.example/cb"}, "token_endpoint_auth_method": "client_secret_post"},
		"private_key_jwt without keys":     {"client_id": cimdID, "client_name": "X", "redirect_uris": []string{"https://connector.example/cb"}, "token_endpoint_auth_method": "private_key_jwt"},
	} {
		f.doc(doc)
		if _, err := f.s.loadClient(ctx, cimdID, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if isCIMDClientID("https://connector.example/") || isCIMDClientID("http://connector.example/client.json") || !isCIMDClientID(cimdID) {
		t.Error("a metadata-document client id is an https URL with a path")
	}
}

func TestPrivateKeyJWT(t *testing.T) {
	f := newCIMDFix(t)
	ctx := context.Background()
	// ChatGPT's shape: private_key_jwt, RS256, keys at a jwks_uri.
	f.doc(map[string]any{"client_id": cimdID, "client_name": "ChatGPT-like", "redirect_uris": []string{"https://connector.example/cb"},
		"token_endpoint_auth_method": "private_key_jwt", "token_endpoint_auth_signing_alg": "RS256", "jwks_uri": jwksURI})
	c, err := f.s.loadClient(ctx, cimdID, true)
	if err != nil || c.AuthMethod != "private_key_jwt" || c.JWKSURI != jwksURI {
		t.Fatalf("the metadata document: %v %+v", err, c)
	}

	good := f.assertion("RS256", nil)
	if err := f.s.verifyClientAssertion(ctx, c, good); err != nil {
		t.Fatalf("a valid assertion: %v", err)
	}
	if err := f.s.verifyClientAssertion(ctx, c, good); err == nil {
		t.Error("a replayed assertion (same jti) was accepted")
	}
	for name, a := range map[string]string{
		"HMAC with a public key": f.assertion("HS256", nil),
		"another audience":       f.assertion("RS256", func(m map[string]any) { m["aud"] = "https://elsewhere.example/token" }),
		"expired":                f.assertion("RS256", func(m map[string]any) { m["exp"] = time.Now().Add(-time.Minute).Unix() }),
		"a lifetime too long":    f.assertion("RS256", func(m map[string]any) { m["exp"] = time.Now().Add(time.Hour).Unix() }),
		"iss is not the client":  f.assertion("RS256", func(m map[string]any) { m["iss"] = "https://evil.example/client.json" }),
		"no jti":                 f.assertion("RS256", func(m map[string]any) { delete(m, "jti") }),
		"tampered":               strings.Replace(f.assertion("RS256", nil), ".", ".e30", 1),
	} {
		if err := f.s.verifyClientAssertion(ctx, c, a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// Through the token endpoint's client authentication: the assertion alone
	// names the client (RFC 7523 lets client_id be omitted).
	form := url.Values{"grant_type": {"authorization_code"}, "client_assertion_type": {clientAssertionType},
		"client_assertion": {f.assertion("RS256", nil)}}
	req := &http.Request{Method: http.MethodPost, PostForm: form, Header: http.Header{}}
	req = req.WithContext(ctx)
	if got, ok := f.s.authenticateClient(req); !ok || got.ID != cimdID {
		t.Errorf("private_key_jwt client authentication: %v %+v", ok, got)
	}
	form.Set("client_secret", "also-a-secret")
	if _, ok := f.s.authenticateClient((&http.Request{Method: http.MethodPost, PostForm: form, Header: http.Header{}}).WithContext(ctx)); ok {
		t.Error("a private_key_jwt client presenting a secret too was accepted")
	}
}
