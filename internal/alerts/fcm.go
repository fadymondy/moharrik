package alerts

/*
Native push through Firebase Cloud Messaging, HTTP v1 — the phone half of an
alert (docs/apps-plan.md §4).

Ported from Zekra's plugins/brain/internal/brain/push_fcm.go (itself from
fadymondy.com internal/push/fcm.go) and kept free of Moharrik imports, so the
Hosbah apps can copy this file as-is. FCM HTTP v1 rather than the legacy server
key: a service-account JWT signed here with the standard library (RS256),
traded for an access token cached until a minute before it expires. iOS devices
are reached the same way — the app registers an FCM token and Firebase relays
to APNs with the key uploaded in the Firebase console.

Configuration (environment only; nothing is hard-coded):

	FCM_SERVICE_ACCOUNT_JSON        the service-account key: raw JSON, or base64 of it
	GOOGLE_APPLICATION_CREDENTIALS  a path to the same JSON (used when the above is unset)
	FCM_PROJECT_ID                  overrides the key's project_id (optional)
	FCM_DRY_RUN=1                   validate_only: FCM checks every message and delivers none

With no key configured, native push is OFF and web push works exactly as before.
*/

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	fcmTokenURL = "https://oauth2.googleapis.com/token"
	fcmScope    = "https://www.googleapis.com/auth/firebase.messaging"
	fcmBaseURL  = "https://fcm.googleapis.com"
	// Google is a third party: a short leash per call.
	fcmTimeout = 10 * time.Second
)

// ErrFCMNotConfigured means no service account is available: native push is off.
var ErrFCMNotConfigured = errors.New("push: no FCM service account is configured")

// ErrDeadToken is what Send returns when FCM says a device token will never
// work again (uninstalled app, rotated token, another project's token). The
// caller deletes the device.
var ErrDeadToken = errors.New("push: device token is no longer valid")

// NativeMessage is one notification for one device.
type NativeMessage struct {
	Title string
	Body  string
	// Collapse replaces a pending notification with the same key (a second
	// message on the same thread), on both Android (tag) and iOS (apns-collapse-id).
	Collapse string
	// Data rides in the FCM data block (strings only); the app routes on it.
	Data map[string]string
}

// NativeSender delivers one message to one FCM token. *FCM is the production
// implementation; tests substitute a fake through fcmFromEnv.
type NativeSender interface {
	Send(ctx context.Context, token string, m NativeMessage) error
}

// serviceAccount is the subset of a Google service-account key FCM needs.
type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	ProjectID   string `json:"project_id"`
	TokenURI    string `json:"token_uri"`
}

// FCM is the FCM HTTP v1 sender with a cached access token.
type FCM struct {
	sa       *serviceAccount
	key      *rsa.PrivateKey
	client   *http.Client
	tokenURL string // OAuth token endpoint (overridden in tests)
	baseURL  string // FCM API base (overridden in tests)
	dryRun   bool

	mu      sync.Mutex
	access  string
	expires time.Time
}

var (
	fcmOnce   sync.Once
	fcmShared *FCM
	fcmErr    error
)

// FCMFromEnv returns the process's sender, built once from the environment, or
// ErrFCMNotConfigured.
func FCMFromEnv() (*FCM, error) {
	fcmOnce.Do(func() {
		sa, err := loadServiceAccount()
		if err != nil {
			fcmErr = err
			return
		}
		fcmShared, fcmErr = newFCM(sa)
	})
	return fcmShared, fcmErr
}

// fcmFromEnv is what the alert path asks for a sender (a seam for the tests).
var fcmFromEnv = func() (NativeSender, bool) {
	f, err := FCMFromEnv()
	if err != nil || f == nil {
		return nil, false
	}
	return f, true
}

// NativeConfigured reports whether native push can be sent from this server.
func NativeConfigured() bool { _, ok := fcmFromEnv(); return ok }

func loadServiceAccount() (*serviceAccount, error) {
	raw := strings.TrimSpace(os.Getenv("FCM_SERVICE_ACCOUNT_JSON"))
	if raw == "" {
		if p := strings.TrimSpace(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); p != "" {
			b, err := os.ReadFile(p) //#nosec G304 -- an operator-set path to the operator's own key
			if err != nil {
				return nil, fmt.Errorf("push: read GOOGLE_APPLICATION_CREDENTIALS: %w", err)
			}
			raw = strings.TrimSpace(string(b))
		}
	}
	if raw == "" {
		return nil, ErrFCMNotConfigured
	}
	return parseServiceAccount(raw)
}

func parseServiceAccount(raw string) (*serviceAccount, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "{") {
		// Base64 is how a JSON key survives an env-var UI or a CI secret intact.
		dec, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, errors.New("push: FCM_SERVICE_ACCOUNT_JSON is neither JSON nor base64 of JSON")
		}
		raw = strings.TrimSpace(string(dec))
	}
	var sa serviceAccount
	if err := json.Unmarshal([]byte(raw), &sa); err != nil {
		return nil, fmt.Errorf("push: service account is not valid JSON: %w", err)
	}
	if p := strings.TrimSpace(os.Getenv("FCM_PROJECT_ID")); p != "" {
		sa.ProjectID = p
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" || sa.ProjectID == "" {
		return nil, errors.New("push: service account is missing client_email, private_key or project_id")
	}
	// A key pasted into an env var often arrives with literal \n sequences.
	sa.PrivateKey = strings.ReplaceAll(sa.PrivateKey, `\n`, "\n")
	return &sa, nil
}

func parseRSAPrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("push: private_key is not valid PEM")
	}
	// Service-account keys are PKCS#8; PKCS#1 is accepted for a hand-converted key.
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if key, ok := parsed.(*rsa.PrivateKey); ok {
			return key, nil
		}
		return nil, errors.New("push: private_key is not an RSA key")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// NewFCMFromJSON builds a sender from a service-account key (raw JSON or base64).
func NewFCMFromJSON(raw string) (*FCM, error) {
	sa, err := parseServiceAccount(raw)
	if err != nil {
		return nil, err
	}
	return newFCM(sa)
}

func newFCM(sa *serviceAccount) (*FCM, error) {
	key, err := parseRSAPrivateKey(sa.PrivateKey)
	if err != nil {
		return nil, err
	}
	tokenURL := fcmTokenURL
	// Honour the key's own token_uri only when it is Google's: a key file is
	// not trusted to redirect the signed assertion anywhere else.
	if u, err := url.Parse(sa.TokenURI); err == nil && u.Scheme == "https" && u.Host == "oauth2.googleapis.com" {
		tokenURL = sa.TokenURI
	}
	return &FCM{
		sa: sa, key: key,
		client:   &http.Client{Timeout: fcmTimeout},
		tokenURL: tokenURL, baseURL: fcmBaseURL,
		dryRun: os.Getenv("FCM_DRY_RUN") == "1" || os.Getenv("FCM_DRY_RUN") == "true",
	}, nil
}

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// assertion builds the RS256-signed JWT the OAuth token endpoint exchanges.
func (f *FCM) assertion(now time.Time) (string, error) {
	header := b64url([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss":   f.sa.ClientEmail,
		"scope": fcmScope,
		"aud":   f.tokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	input := header + "." + b64url(claims)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return input + "." + b64url(sig), nil
}

// accessToken returns a cached token, minting a new one a minute before expiry.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.access != "" && time.Now().Before(f.expires.Add(-time.Minute)) {
		return f.access, nil
	}
	now := time.Now()
	jwt, err := f.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {jwt},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("push: token exchange failed (%d): %s", res.StatusCode, clipBytes(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		return "", errors.New("push: no access_token in the token response")
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 3600
	}
	f.access = out.AccessToken
	f.expires = now.Add(time.Duration(out.ExpiresIn) * time.Second)
	return f.access, nil
}

// fcmPayload is the v1 messages:send body for one device.
func fcmPayload(token string, m NativeMessage, validateOnly bool) map[string]any {
	androidNote := map[string]any{"sound": "default", "channel_id": "messages"}
	apnsHeaders := map[string]any{"apns-priority": "10", "apns-push-type": "alert"}
	if m.Collapse != "" {
		androidNote["tag"] = m.Collapse
		apnsHeaders["apns-collapse-id"] = clipString(m.Collapse, 64)
	}
	msg := map[string]any{
		"token":        token,
		"notification": map[string]any{"title": m.Title, "body": m.Body},
		// Someone is waiting on these: delivered promptly, with the default sound.
		"android": map[string]any{"priority": "HIGH", "notification": androidNote},
		"apns": map[string]any{
			"headers": apnsHeaders,
			"payload": map[string]any{"aps": map[string]any{"sound": "default", "mutable-content": 1}},
		},
	}
	if m.Collapse != "" {
		msg["android"].(map[string]any)["collapse_key"] = clipString(m.Collapse, 64)
	}
	// Omitted rather than sent empty: an empty data map is noise at best.
	if len(m.Data) > 0 {
		msg["data"] = m.Data
	}
	out := map[string]any{"message": msg}
	if validateOnly {
		out["validate_only"] = true
	}
	return out
}

// Send delivers one message to one device token.
func (f *FCM) Send(ctx context.Context, token string, m NativeMessage) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(fcmPayload(token, m, f.dryRun))
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/v1/projects/%s/messages:send", f.baseURL, url.PathEscape(f.sa.ProjectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	res, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode == http.StatusOK:
		return nil
	case res.StatusCode == http.StatusUnauthorized:
		// The cached token was revoked early: drop it so the next send re-mints.
		f.mu.Lock()
		f.access = ""
		f.mu.Unlock()
		return fmt.Errorf("push: FCM rejected the access token (401)")
	case isDeadFCMToken(res.StatusCode, body):
		return ErrDeadToken
	default:
		return fmt.Errorf("push: FCM refused the message (HTTP %d)", res.StatusCode)
	}
}

// isDeadFCMToken reports whether FCM is saying this token will never work
// again, as opposed to a transient failure (quota, 5xx) or a bug in our message.
func isDeadFCMToken(status int, body []byte) bool {
	switch {
	case status == http.StatusNotFound:
		return true // UNREGISTERED
	case bytes.Contains(body, []byte("UNREGISTERED")):
		return true
	case status == http.StatusForbidden && bytes.Contains(body, []byte("SENDER_ID_MISMATCH")):
		return true // a token minted for another Firebase project
	case status == http.StatusBadRequest && bytes.Contains(body, []byte("INVALID_ARGUMENT")):
		// Only when the complaint is about the token itself; a malformed
		// message is our bug, not a dead device.
		return bytes.Contains(body, []byte("message.token")) || bytes.Contains(body, []byte("registration token"))
	}
	return false
}

func clipBytes(b []byte) string {
	if len(b) > 300 {
		return string(b[:300])
	}
	return string(b)
}

func clipString(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
