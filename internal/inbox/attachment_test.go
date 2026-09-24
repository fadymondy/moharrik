package inbox

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
	"time"
)

func rnd() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TestAttachmentURLIsolation checks the property the download endpoint relies
// on entirely (attachment.go: it reads on the OWNER pool, with no RLS,
// because the signature IS the authorization): a URL signed for one
// (workspace, attachment) pair must not verify for any other workspace, any
// other attachment, a tampered expiry, or a lapsed one. This is the
// cross-workspace isolation property for MH-556's signed URLs, checked at the
// unit level because /api/attachments/ carries no session for the isolation
// sweep (internal/server/isolation_test.go) to substitute a second workspace
// into — verified there is that the route is 401/404-free for an ANONYMOUS
// caller with no signature at all, and here that a correctly-shaped but
// wrong-workspace signature is refused.
func TestAttachmentURLIsolation(t *testing.T) {
	t.Setenv("AUTH_SECRET", "attachment-isolation-fixture-0123456789")

	wsA, wsB := "ws-a-"+rnd(), "ws-b-"+rnd()
	attA := "att-a-" + rnd()

	url := AttachmentURL(wsA, attA)
	if url == "" {
		t.Fatal("AttachmentURL returned empty")
	}
	ws, attID, exp, sig := parseAttachmentURL(t, url)
	if ws != wsA || attID != attA {
		t.Fatalf("URL does not carry back the workspace/attachment it was signed for: ws=%s att=%s", ws, attID)
	}
	if err := verifyAttachmentURL(ws, attID, exp, sig); err != nil {
		t.Fatalf("a freshly minted URL must verify: %v", err)
	}

	// The same signature under a DIFFERENT workspace must not verify — this is
	// the one property that stands in for RLS on this unauthenticated route.
	if err := verifyAttachmentURL(wsB, attID, exp, sig); err == nil {
		t.Error("a signature minted for workspace A verified under workspace B")
	}
	// ...or for a different attachment id in the same workspace.
	if err := verifyAttachmentURL(ws, "att-other-"+rnd(), exp, sig); err == nil {
		t.Error("a signature minted for one attachment verified for another")
	}
	// ...or with the expiry moved out (a longer-lived link than was granted).
	longer := strconv.FormatInt(time.Now().Add(48*time.Hour).Unix(), 10)
	if err := verifyAttachmentURL(ws, attID, longer, sig); err == nil {
		t.Error("changing exp without re-signing verified")
	}
	// ...or with the signature simply flipped (garbled/truncated).
	bad := sig[:len(sig)-2] + "00"
	if bad == sig {
		t.Fatal("test fixture did not actually change the signature")
	}
	if err := verifyAttachmentURL(ws, attID, exp, bad); err == nil {
		t.Error("a tampered signature verified")
	}
	// An expired link, even with a correct signature over that (past) exp.
	expired := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	expiredSig, err := signAttachment(ws, attID, mustParseInt64(t, expired))
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAttachmentURL(ws, attID, expired, expiredSig); err == nil {
		t.Error("an expired (but correctly signed) link verified")
	}
}

func mustParseInt64(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// parseAttachmentURL reads back what AttachmentURL encoded, so the test does
// not have to know its exact format — only that ws/exp/sig round-trip.
func parseAttachmentURL(t *testing.T, u string) (ws, id, exp, sig string) {
	t.Helper()
	// /api/attachments/{id}?ws=..&exp=..&sig=..
	path, query, ok := strings.Cut(u, "?")
	if !ok {
		t.Fatalf("no query string in %q", u)
	}
	const prefix = "/api/attachments/"
	if !strings.HasPrefix(path, prefix) {
		t.Fatalf("unexpected path shape: %q", path)
	}
	id = strings.TrimPrefix(path, prefix)
	for _, kv := range strings.Split(query, "&") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "ws":
			ws = v
		case "exp":
			exp = v
		case "sig":
			sig = v
		}
	}
	return
}
