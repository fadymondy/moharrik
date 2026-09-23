package notifications

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

/*
Signature verification for POST /api/hooks/{id} (MH-381, ported from
fadymondy.com's internal/center/webhook.go).

Each kind is checked the way its sender signs, over the RAW body, with
hmac.Equal (constant time):

	github   X-Hub-Signature-256: sha256=<hex HMAC-SHA256(secret, body)>
	         X-GitHub-Event names the event.
	sentry   Sentry-Hook-Signature: <hex HMAC-SHA256(secret, body)>
	         Sentry-Hook-Resource names the event. The secret is the Sentry
	         integration's Client Secret, which Sentry generates — so a sentry
	         source's secret is SET (PUT .../secret), not generated here.
	generic  X-Webhook-Timestamp: <unix seconds>, within ReplayWindow of now
	         X-Webhook-Signature: [sha256=]<hex HMAC-SHA256(secret, timestamp + "." + body)>

The generic signature covers the timestamp. If it covered only the body, a
captured request could be replayed for ever with a fresh timestamp header and
the window would stop nothing.

# Identity, and why replays inside the window are harmless

A delivery's identity is derived only from SIGNED bytes — never from an
unsigned header such as X-GitHub-Delivery, which an attacker holding a captured
request could change at will to mint a "new" delivery. notification_items has a
unique index on (workspace, source, identity), so the same signed bytes
delivered twice are the same row:

	generic   "ts:" + hex(sha256(timestamp + "." + body)), or the body's own
	          external_id when it sends one (the body is signed, so it is too)
	github    hex(sha256(body))
	sentry    hex(sha256(body))

A generic delivery replayed inside the window therefore lands on the row it
already made, and outside the window is refused outright. No in-process seen-set
is needed, and there would be nothing to share between API instances anyway.

# Legacy signing

Before MH-381 every source accepted X-Webhook-Signature (or X-Hub-Signature-256)
over the body alone, with no timestamp. Sources that existed then are marked
legacy_signing and keep accepting exactly that, so no live integration broke on
deploy; the sources page flags them as replayable and the owner switches each
one off when its sender has moved to the timestamped scheme. New sources are
never legacy.
*/

// ReplayWindow is how far a generic delivery's timestamp may be from now, in
// either direction.
const ReplayWindow = 300 * time.Second

// Verification is what a successful check yields.
type Verification struct {
	// Event is the sender's event name (X-GitHub-Event, Sentry-Hook-Resource),
	// or "" for generic.
	Event string
	// Identity is the dedup key derived from signed bytes (see above). The
	// normaliser may replace it with a signed body field.
	Identity string
	// Legacy is true when only the pre-MH-381 body-only signature matched.
	Legacy bool
}

// refusal is every verification failure. The HTTP response never says which
// check failed; the reason goes to last_error on the source, for its owner.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

const sigMismatch = "signature does not match (wrong secret or altered body)"

func hmacSHA256(secret string, parts ...[]byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	for _, p := range parts {
		mac.Write(p)
	}
	return mac.Sum(nil)
}

// equalHex compares a hex signature with the expected MAC in constant time. A
// malformed header decodes to nothing and fails the same comparison.
func equalHex(got string, want []byte) bool {
	decoded, err := hex.DecodeString(strings.TrimSpace(got))
	if err != nil || len(decoded) == 0 {
		return false
	}
	return hmac.Equal(decoded, want)
}

func sha256Hex(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

/*
Verify checks one delivery against a source's secret. It reads only headers and
the raw body. legacy says whether this source still accepts the pre-MH-381
body-only signature; it is tried only after the source's own scheme fails, and
the first scheme's reason is the one reported when both do.
*/
func Verify(kind, secret string, legacy bool, h http.Header, body []byte, now time.Time) (Verification, error) {
	if secret == "" {
		return Verification{}, refusal{"this source has no signing secret yet"}
	}
	v, err := verifyKind(kind, secret, h, body, now)
	if err == nil || !legacy {
		return v, err
	}
	if verifyLegacy(secret, h, body) {
		return Verification{Event: legacyEvent(kind, h), Identity: sha256Hex(body), Legacy: true}, nil
	}
	return Verification{}, err
}

func verifyKind(kind, secret string, h http.Header, body []byte, now time.Time) (Verification, error) {
	switch kind {
	case "github":
		sig := strings.TrimSpace(h.Get("X-Hub-Signature-256"))
		if !strings.HasPrefix(sig, "sha256=") {
			return Verification{}, refusal{"missing X-Hub-Signature-256"}
		}
		if !equalHex(strings.TrimPrefix(sig, "sha256="), hmacSHA256(secret, body)) {
			return Verification{}, refusal{sigMismatch}
		}
		return Verification{Event: strings.TrimSpace(h.Get("X-GitHub-Event")), Identity: sha256Hex(body)}, nil

	case "sentry":
		sig := strings.TrimSpace(h.Get("Sentry-Hook-Signature"))
		if sig == "" {
			return Verification{}, refusal{"missing Sentry-Hook-Signature"}
		}
		if !equalHex(sig, hmacSHA256(secret, body)) {
			return Verification{}, refusal{sigMismatch}
		}
		return Verification{Event: strings.TrimSpace(h.Get("Sentry-Hook-Resource")), Identity: sha256Hex(body)}, nil

	case "generic":
		sig := strings.TrimPrefix(strings.TrimSpace(h.Get("X-Webhook-Signature")), "sha256=")
		stamp := strings.TrimSpace(h.Get("X-Webhook-Timestamp"))
		if sig == "" {
			return Verification{}, refusal{"missing X-Webhook-Signature"}
		}
		if stamp == "" {
			return Verification{}, refusal{"missing X-Webhook-Timestamp"}
		}
		seconds, perr := strconv.ParseInt(stamp, 10, 64)
		if perr != nil {
			return Verification{}, refusal{"X-Webhook-Timestamp is not unix seconds"}
		}
		// Signature first, window second: an unsigned request learns nothing
		// about the server's clock.
		if !equalHex(sig, hmacSHA256(secret, []byte(stamp), []byte("."), body)) {
			return Verification{}, refusal{sigMismatch}
		}
		skew := now.Sub(time.Unix(seconds, 0))
		if skew > ReplayWindow || skew < -ReplayWindow {
			return Verification{}, refusal{"X-Webhook-Timestamp is outside the 5-minute replay window"}
		}
		return Verification{Identity: "ts:" + sha256Hex([]byte(stamp), []byte("."), body)}, nil
	}
	return Verification{}, refusal{"unknown source kind"}
}

// verifyLegacy is the pre-MH-381 check, exactly: X-Webhook-Signature, else
// X-Hub-Signature-256, optionally prefixed sha256=, over the body alone.
func verifyLegacy(secret string, h http.Header, body []byte) bool {
	sig := strings.TrimPrefix(strings.TrimSpace(h.Get("X-Webhook-Signature")), "sha256=")
	if sig == "" {
		sig = strings.TrimPrefix(strings.TrimSpace(h.Get("X-Hub-Signature-256")), "sha256=")
	}
	return sig != "" && equalHex(sig, hmacSHA256(secret, body))
}

func legacyEvent(kind string, h http.Header) string {
	switch kind {
	case "github":
		return strings.TrimSpace(h.Get("X-GitHub-Event"))
	case "sentry":
		return strings.TrimSpace(h.Get("Sentry-Hook-Resource"))
	}
	return ""
}
