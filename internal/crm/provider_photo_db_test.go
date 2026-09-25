package crm

/*
The provider-photo download endpoint (MH-602 defect 2), against a real
Postgres database with RLS on: a WhatsApp-cached photo (photo_url as a
data: URI) is served through a signed /api/provider-photos/{kind}/{id} link
instead of ever sitting in a JSON response body — see internal/inbox's
conversationSelect for the list-query half of this, and getContact
(queries.go) for the single-contact half.

Domain separation matters here specifically: signAvatar's "contact-avatar:..."
and signProviderPhoto's "provider-photo:..." must never be interchangeable,
or an uploaded-avatar link handed to a browser would also open this endpoint
(and vice versa) for a row the caller was never actually signed into.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/crm/ -run ProviderPhoto -v
*/

import (
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
)

// tinyPNGBytes is tinyPNGBase64 decoded, for comparing served bytes exactly.
var tinyPNGBytes, _ = base64.StdEncoding.DecodeString(tinyPNGBase64)

func TestProviderPhotoServesTheImageBytes(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Has Provider Photo")
	dataURI := "data:image/png;base64," + tinyPNGBase64
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE contacts SET photo_url = $1, photo_source = 'whatsapp' WHERE workspace_id = $2 AND id = $3`,
		dataURI, f.wsID, contactID); err != nil {
		t.Fatal(err)
	}

	_, api := humatest.New(t)
	RegisterProviderPhotos(api, a)

	url := ProviderPhotoURL(f.wsID, "contact", contactID)
	if url == "" {
		t.Fatal("expected a signed provider-photo URL")
	}
	if strings.Contains(url, "data:") {
		t.Fatalf("the signed URL itself must never carry the data: URI, got %q", url)
	}

	resp := api.Get(url)
	if resp.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
	}
	if ct := resp.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected Content-Type image/png, got %q", ct)
	}
	if resp.Body.String() != string(tinyPNGBytes) {
		t.Error("the served bytes must match the decoded data: URI exactly")
	}
}

func TestProviderPhotoRejectsATamperedSignature(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Tamper Target")
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE contacts SET photo_url = $1 WHERE workspace_id = $2 AND id = $3`,
		"data:image/png;base64,"+tinyPNGBase64, f.wsID, contactID); err != nil {
		t.Fatal(err)
	}

	_, api := humatest.New(t)
	RegisterProviderPhotos(api, a)

	url := ProviderPhotoURL(f.wsID, "contact", contactID)
	// Flip the signature's last hex character — still a valid-looking
	// signature shape (hex), just the wrong one.
	tampered := url
	if strings.HasSuffix(tampered, "0") {
		tampered = tampered[:len(tampered)-1] + "1"
	} else {
		tampered = tampered[:len(tampered)-1] + "0"
	}

	resp := api.Get(tampered)
	if resp.Code != 403 {
		t.Fatalf("a tampered signature must be rejected with 403, got %d", resp.Code)
	}
}

func TestProviderPhotoRejectsAnExpiredSignature(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Expired Target")
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE contacts SET photo_url = $1 WHERE workspace_id = $2 AND id = $3`,
		"data:image/png;base64,"+tinyPNGBase64, f.wsID, contactID); err != nil {
		t.Fatal(err)
	}

	_, api := humatest.New(t)
	RegisterProviderPhotos(api, a)

	exp := time.Now().Add(-time.Hour).Unix()
	sig, err := signProviderPhoto("contact", f.wsID, contactID, exp)
	if err != nil {
		t.Fatal(err)
	}
	url := "/api/provider-photos/contact/" + contactID + "?ws=" + f.wsID + "&exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig

	resp := api.Get(url)
	if resp.Code != 403 {
		t.Fatalf("an expired signature must be rejected with 403, got %d", resp.Code)
	}
}

// TestProviderPhotoRejectsAContactAvatarSignature is the domain-separation
// check: a signature minted for /api/contact-avatars/{id} (signAvatar's
// "contact-avatar:..." domain) must not be replayable against
// /api/provider-photos/ (signProviderPhoto's distinct "provider-photo:..."
// domain), even for the exact same workspace and id.
func TestProviderPhotoRejectsAContactAvatarSignature(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Cross-Domain Target")
	if _, err := a.SQLDB.ExecContext(f.ctx, `UPDATE contacts SET photo_url = $1 WHERE workspace_id = $2 AND id = $3`,
		"data:image/png;base64,"+tinyPNGBase64, f.wsID, contactID); err != nil {
		t.Fatal(err)
	}

	_, api := humatest.New(t)
	RegisterProviderPhotos(api, a)

	exp := time.Now().Add(time.Hour).Unix()
	avatarSig, err := signAvatar(f.wsID, contactID, exp)
	if err != nil {
		t.Fatal(err)
	}
	url := "/api/provider-photos/contact/" + contactID + "?ws=" + f.wsID + "&exp=" + strconv.FormatInt(exp, 10) + "&sig=" + avatarSig

	resp := api.Get(url)
	if resp.Code != 403 {
		t.Fatalf("a contact-avatar signature must be rejected on the provider-photo endpoint, got %d", resp.Code)
	}
}

// TestProviderPhotoWorkspaceIsolation: workspace B cannot read workspace A's
// photo by swapping the ws query param onto A's signed URL — the signature
// covers ws, so the swap invalidates it instead of just changing which row
// gets queried.
func TestProviderPhotoWorkspaceIsolation(t *testing.T) {
	a := testApp(t)
	fA := newCRMWorkspace(t, a)
	fB := newCRMWorkspace(t, a)
	contactID := newContact(t, a, fA.wsID, "Workspace A's Contact")
	if _, err := a.SQLDB.ExecContext(fA.ctx, `UPDATE contacts SET photo_url = $1 WHERE workspace_id = $2 AND id = $3`,
		"data:image/png;base64,"+tinyPNGBase64, fA.wsID, contactID); err != nil {
		t.Fatal(err)
	}

	_, api := humatest.New(t)
	RegisterProviderPhotos(api, a)

	url := ProviderPhotoURL(fA.wsID, "contact", contactID)
	swapped := strings.Replace(url, "ws="+fA.wsID, "ws="+fB.wsID, 1)
	if swapped == url {
		t.Fatal("test setup: the ws param was not found in the URL to swap")
	}

	resp := api.Get(swapped)
	if resp.Code != 403 {
		t.Fatalf("swapping in another workspace's id must be rejected with 403, got %d", resp.Code)
	}
}
