package notifications

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// MH-381: every kind verifies the way its sender signs, a wrong secret or an
// altered body is refused, and a generic delivery cannot be replayed outside
// the window. These run without a database.

const testSecret = "3f1c2a9b8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c5b4a3928170f6e5d4c"

func mac(secret string, parts ...string) string {
	m := hmac.New(sha256.New, []byte(secret))
	for _, p := range parts {
		m.Write([]byte(p))
	}
	return hex.EncodeToString(m.Sum(nil))
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func genericHeaders(secret, body string, at time.Time) http.Header {
	ts := strconv.FormatInt(at.Unix(), 10)
	return hdr("X-Webhook-Timestamp", ts, "X-Webhook-Signature", "sha256="+mac(secret, ts, ".", body))
}

func mustRefuse(t *testing.T, what string, err error) {
	t.Helper()
	var r refusal
	if err == nil {
		t.Fatalf("%s: accepted, want refused", what)
	}
	if !errors.As(err, &r) {
		t.Fatalf("%s: refused with a non-refusal error %T", what, err)
	}
}

func TestVerifyGitHub(t *testing.T) {
	now := time.Now()
	body := `{"zen":"Keep it logically awesome.","hook_id":1}`
	good := hdr("X-Hub-Signature-256", "sha256="+mac(testSecret, body), "X-GitHub-Event", "ping")

	v, err := Verify("github", testSecret, false, good, []byte(body), now)
	if err != nil {
		t.Fatalf("valid GitHub signature refused: %v", err)
	}
	if v.Event != "ping" || v.Legacy || v.Identity == "" {
		t.Fatalf("verification = %+v", v)
	}

	_, err = Verify("github", testSecret, false, hdr("X-Hub-Signature-256", "sha256="+mac("wrong-secret-wrong-secret", body)), []byte(body), now)
	mustRefuse(t, "wrong secret", err)

	_, err = Verify("github", testSecret, false, good, []byte(strings.Replace(body, "awesome", "AWESOME", 1)), now)
	mustRefuse(t, "tampered body", err)

	// GitHub always prefixes sha256=; a bare hex is not GitHub.
	_, err = Verify("github", testSecret, false, hdr("X-Hub-Signature-256", mac(testSecret, body)), []byte(body), now)
	mustRefuse(t, "unprefixed signature", err)

	// The generic header is not accepted on a (non-legacy) github source.
	_, err = Verify("github", testSecret, false, hdr("X-Webhook-Signature", "sha256="+mac(testSecret, body)), []byte(body), now)
	mustRefuse(t, "generic header on github", err)
}

func TestVerifySentry(t *testing.T) {
	now := time.Now()
	body := `{"action":"triggered","data":{"event":{"title":"boom"}}}`
	good := hdr("Sentry-Hook-Signature", mac(testSecret, body), "Sentry-Hook-Resource", "event_alert")

	v, err := Verify("sentry", testSecret, false, good, []byte(body), now)
	if err != nil {
		t.Fatalf("valid Sentry signature refused: %v", err)
	}
	if v.Event != "event_alert" {
		t.Fatalf("event = %q", v.Event)
	}

	_, err = Verify("sentry", testSecret, false, hdr("Sentry-Hook-Signature", mac("not-the-client-secret", body)), []byte(body), now)
	mustRefuse(t, "wrong secret", err)

	_, err = Verify("sentry", testSecret, false, good, []byte(body+" "), now)
	mustRefuse(t, "tampered body", err)

	_, err = Verify("sentry", testSecret, false, hdr("X-Hub-Signature-256", "sha256="+mac(testSecret, body)), []byte(body), now)
	mustRefuse(t, "missing Sentry-Hook-Signature", err)

	// A sentry source is created without a secret; until one is set, nothing
	// verifies — not even a signature made with the empty key.
	_, err = Verify("sentry", "", false, hdr("Sentry-Hook-Signature", mac("", body)), []byte(body), now)
	mustRefuse(t, "no secret set", err)
}

func TestVerifyGeneric(t *testing.T) {
	now := time.Now()
	body := `{"title":"Disk almost full","severity":"warning"}`

	v, err := Verify("generic", testSecret, false, genericHeaders(testSecret, body, now), []byte(body), now)
	if err != nil {
		t.Fatalf("valid generic signature refused: %v", err)
	}
	if !strings.HasPrefix(v.Identity, "ts:") {
		t.Fatalf("identity %q does not cover the timestamp", v.Identity)
	}

	// Bare hex (no sha256= prefix) is accepted too.
	ts := strconv.FormatInt(now.Unix(), 10)
	if _, err := Verify("generic", testSecret, false, hdr("X-Webhook-Timestamp", ts, "X-Webhook-Signature", mac(testSecret, ts, ".", body)), []byte(body), now); err != nil {
		t.Fatalf("bare-hex generic signature refused: %v", err)
	}

	_, err = Verify("generic", testSecret, false, genericHeaders("a-different-secret-entirely", body, now), []byte(body), now)
	mustRefuse(t, "wrong secret", err)

	_, err = Verify("generic", testSecret, false, genericHeaders(testSecret, body, now), []byte(strings.Replace(body, "warning", "info", 1)), now)
	mustRefuse(t, "tampered body", err)

	// The window is the card's ±300 s, pinned in absolute terms so widening the
	// constant fails here.
	if ReplayWindow != 300*time.Second {
		t.Fatalf("ReplayWindow = %v, want 300s", ReplayWindow)
	}
	// An old delivery, correctly signed, replayed: refused.
	old := now.Add(-301 * time.Second)
	_, err = Verify("generic", testSecret, false, genericHeaders(testSecret, body, old), []byte(body), now)
	mustRefuse(t, "timestamp 301s old", err)
	if !strings.Contains(err.Error(), "replay window") {
		t.Fatalf("old timestamp refused for the wrong reason: %v", err)
	}
	// ...and from too far in the future.
	_, err = Verify("generic", testSecret, false, genericHeaders(testSecret, body, now.Add(301*time.Second)), []byte(body), now)
	mustRefuse(t, "timestamp 301s ahead", err)
	// Just inside the window is fine.
	if _, err := Verify("generic", testSecret, false, genericHeaders(testSecret, body, now.Add(-298*time.Second)), []byte(body), now); err != nil {
		t.Fatalf("timestamp inside the window refused: %v", err)
	}

	// The replay the timestamp exists to stop: an old signed request with a
	// fresh X-Webhook-Timestamp pasted on. The signature covers the stamp.
	oldHeaders := genericHeaders(testSecret, body, old)
	oldHeaders.Set("X-Webhook-Timestamp", strconv.FormatInt(now.Unix(), 10))
	_, err = Verify("generic", testSecret, false, oldHeaders, []byte(body), now)
	mustRefuse(t, "fresh timestamp on an old signature", err)

	// The pre-MH-381 body-only signature is NOT accepted by a new source.
	_, err = Verify("generic", testSecret, false, hdr("X-Webhook-Signature", "sha256="+mac(testSecret, body)), []byte(body), now)
	mustRefuse(t, "body-only signature on a new source", err)

	_, err = Verify("generic", testSecret, false, hdr("X-Webhook-Timestamp", "yesterday", "X-Webhook-Signature", mac(testSecret, "yesterday", ".", body)), []byte(body), now)
	mustRefuse(t, "non-numeric timestamp", err)
}

func TestVerifyGenericReplayInsideWindowIsTheSameDelivery(t *testing.T) {
	now := time.Now()
	body := `{"title":"deploy finished"}`
	h := genericHeaders(testSecret, body, now)
	a, err := Verify("generic", testSecret, false, h, []byte(body), now)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Verify("generic", testSecret, false, h, []byte(body), now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if a.Identity != b.Identity {
		t.Fatal("a replay inside the window must carry the same identity, so it lands on the same row")
	}
	// The same body sent again LATER, freshly signed, is a new event.
	c, err := Verify("generic", testSecret, false, genericHeaders(testSecret, body, now.Add(time.Minute)), []byte(body), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if c.Identity == a.Identity {
		t.Fatal("the same body at a different time must be a different delivery")
	}
}

func TestVerifyLegacySourceStillAccepted(t *testing.T) {
	now := time.Now()
	body := `{"title":"from an integration configured before MH-381"}`
	bodyOnly := mac(testSecret, body)

	// Exactly what the old receiver accepted: either header, prefix optional.
	for _, h := range []http.Header{
		hdr("X-Webhook-Signature", "sha256="+bodyOnly),
		hdr("X-Webhook-Signature", bodyOnly),
		hdr("X-Hub-Signature-256", "sha256="+bodyOnly),
	} {
		v, err := Verify("generic", testSecret, true, h, []byte(body), now)
		if err != nil {
			t.Fatalf("legacy source refused its old signature %v: %v", h, err)
		}
		if !v.Legacy {
			t.Fatal("a delivery verified only by the old scheme must be flagged legacy")
		}
	}

	// A legacy source takes the new scheme too, and that is not flagged.
	v, err := Verify("generic", testSecret, true, genericHeaders(testSecret, body, now), []byte(body), now)
	if err != nil || v.Legacy {
		t.Fatalf("timestamped delivery to a legacy source: %+v %v", v, err)
	}

	// Legacy is not "anything goes": wrong secret and tampered body still fail.
	_, err = Verify("generic", testSecret, true, hdr("X-Webhook-Signature", mac("wrong-secret-wrong-secret", body)), []byte(body), now)
	mustRefuse(t, "legacy, wrong secret", err)
	_, err = Verify("generic", testSecret, true, hdr("X-Webhook-Signature", bodyOnly), []byte(body+"x"), now)
	mustRefuse(t, "legacy, tampered body", err)
	// A stale timestamped delivery does not slip through the legacy path.
	_, err = Verify("generic", testSecret, true, genericHeaders(testSecret, body, now.Add(-time.Hour)), []byte(body), now)
	mustRefuse(t, "legacy, stale timestamped delivery", err)
}
