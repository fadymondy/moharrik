package googlecontacts_test

/*
REST-level coverage for MH-585's two importer surfaces: the Google CSV export
path through internal/vcard's existing preview/commit routes (source:
"google_csv"), and internal/googlecontacts's own connection-scoped routes —
preview/sync/status plus the member-visible connections list this card
added — driven as real HTTP requests against the fake Google, the same way a
browser would call them. Package-level tests elsewhere in this package call
Preview/Sync/Status directly; these go through the router and guard instead,
so a routing, gating or JSON-shape regression shows up here even if the
underlying function is untouched.

	TEST_DATABASE_URL=postgres://postgres:…@localhost:55432/moharrik_tak?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app:…@localhost:55432/moharrik_tak?sslmode=disable \
	go test -count=1 ./internal/googlecontacts/... -run REST -v
*/

import (
	"net/http"
	"testing"

	"github.com/fadymondy/moharrik/internal/google/googletest"
)

const googleCSV = "First Name,Last Name,E-mail 1 - Value,Phone 1 - Value\n" +
	"Rest,Import,rest-import@example.test,01001234567\n"

// TestRESTGoogleCSVPreviewAndCommit: source:"google_csv" through the same
// /api/w/contacts/vcard/preview -> commit routes the vCard file path uses,
// end to end over HTTP.
func TestRESTGoogleCSVPreviewAndCommit(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	pv := alice.must(t, "POST", "/api/w/contacts/vcard/preview", map[string]any{
		"source": "google_csv", "csv": googleCSV,
	}, http.StatusOK)
	if got := int(pv["new_count"].(float64)); got != 1 {
		t.Fatalf("preview new_count = %d, want 1: %+v", got, pv)
	}
	if got := int(pv["card_count"].(float64)); got != 1 {
		t.Fatalf("preview card_count = %d, want 1: %+v", got, pv)
	}

	commit := alice.must(t, "POST", "/api/w/contacts/vcard/commit", map[string]any{
		"source": "google_csv", "csv": googleCSV,
	}, http.StatusOK)
	if got := int(commit["created_count"].(float64)); got != 1 {
		t.Fatalf("commit created_count = %d, want 1: %+v", got, commit)
	}
	ids, _ := commit["contact_ids"].([]any)
	if len(ids) != 1 {
		t.Fatalf("commit contact_ids: %+v", commit)
	}

	// Reload: the imported contact really persisted (not just echoed back).
	got := alice.must(t, "GET", "/api/w/contacts/"+ids[0].(string), nil, http.StatusOK)
	contact, _ := got["contact"].(map[string]any)
	if contact == nil || contact["email"] != "rest-import@example.test" {
		t.Fatalf("contact did not persist: %+v", got)
	}

	// A second commit of the same CSV merges rather than duplicating.
	commit2 := alice.must(t, "POST", "/api/w/contacts/vcard/commit", map[string]any{
		"source": "google_csv", "csv": googleCSV,
	}, http.StatusOK)
	if got := int(commit2["merged_count"].(float64)); got != 1 {
		t.Fatalf("second commit merged_count = %d, want 1: %+v", got, commit2)
	}
	if got := int(commit2["created_count"].(float64)); got != 0 {
		t.Fatalf("second commit created_count = %d, want 0: %+v", got, commit2)
	}
}

// TestRESTGoogleCSVPreviewMissingCSVIs422: the field the route needs is
// missing, and it must say so with a 4xx rather than a 500.
func TestRESTGoogleCSVPreviewMissingCSVIs422(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	alice.must(t, "POST", "/api/w/contacts/vcard/preview", map[string]any{"source": "google_csv"}, http.StatusUnprocessableEntity)
}

// TestRESTGoogleContactsConnectionsListsOnlyContactsProduct: the new
// member-visible GET /api/w/google/contacts/connections lists a connection
// once it has granted Contacts, and excludes an Analytics-only one.
func TestRESTGoogleContactsConnectionsListsOnlyContactsProduct(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	empty := alice.must(t, "GET", "/api/w/google/contacts/connections", nil, http.StatusOK)
	if conns, _ := empty["connections"].([]any); len(conns) != 0 {
		t.Fatalf("expected no connections yet: %+v", empty)
	}

	f.setAccount("sub-rest-conn", "rest-conn@example.test")
	analyticsOnly := alice.connect(t, "product=analytics")

	afterAnalytics := alice.must(t, "GET", "/api/w/google/contacts/connections", nil, http.StatusOK)
	if conns, _ := afterAnalytics["connections"].([]any); len(conns) != 0 {
		t.Fatalf("an analytics-only connection must not appear in the contacts list: %+v", afterAnalytics)
	}

	contactsConn := alice.connect(t, "product=contacts&connection="+analyticsOnly)
	if contactsConn != analyticsOnly {
		t.Fatalf("reconnect for an added product should keep the same connection id, got %s want %s", contactsConn, analyticsOnly)
	}

	after := alice.must(t, "GET", "/api/w/google/contacts/connections", nil, http.StatusOK)
	conns, _ := after["connections"].([]any)
	if len(conns) != 1 {
		t.Fatalf("expected exactly one contacts connection: %+v", after)
	}
	c := conns[0].(map[string]any)
	if c["id"] != contactsConn || c["email"] != "rest-conn@example.test" {
		t.Fatalf("unexpected connection summary: %+v", c)
	}
	// The member-visible summary is deliberately trimmed: no scopes, no
	// connected-by identity.
	if _, ok := c["scopes"]; ok {
		t.Fatalf("connections list must not leak scopes to a non-admin member: %+v", c)
	}
	if _, ok := c["connected_by"]; ok {
		t.Fatalf("connections list must not leak connected_by to a non-admin member: %+v", c)
	}
}

// TestRESTGoogleContactsSyncAgainstFake: preview + sync + status through the
// real HTTP routes, against the fake People API — the "Sync now" button's
// exact call sequence.
func TestRESTGoogleContactsSyncAgainstFake(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	f.setAccount("sub-rest-sync", "rest-sync@example.test")
	connID := alice.connect(t, "product=contacts")
	f.fake.SetContact("sub-rest-sync", "people/rest1", googletest.ContactFields{
		"names":  []map[string]any{{"givenName": "Sync", "familyName": "Now"}},
		"emails": []map[string]any{{"value": "sync-now@example.test"}},
	})

	status0 := alice.must(t, "GET", "/api/w/google/contacts/"+connID+"/status", nil, http.StatusOK)
	if status0["last_synced_at"] != nil {
		t.Fatalf("a fresh connection must report unsynced: %+v", status0)
	}

	pv := alice.must(t, "GET", "/api/w/google/contacts/"+connID+"/preview", nil, http.StatusOK)
	if got := int(pv["new_count"].(float64)); got != 1 {
		t.Fatalf("preview new_count = %d, want 1: %+v", got, pv)
	}

	sync := alice.must(t, "POST", "/api/w/google/contacts/"+connID+"/sync", nil, http.StatusOK)
	if got := int(sync["added"].(float64)); got != 1 {
		t.Fatalf("sync added = %d, want 1: %+v", got, sync)
	}
	if sync["full_resync"] != true {
		t.Fatalf("first sync for a connection must be a full resync: %+v", sync)
	}

	status1 := alice.must(t, "GET", "/api/w/google/contacts/"+connID+"/status", nil, http.StatusOK)
	if status1["last_synced_at"] == nil {
		t.Fatalf("status after a sync must report last_synced_at: %+v", status1)
	}
	if status1["last_error"] != "" {
		t.Fatalf("status after a clean sync must have no error: %+v", status1)
	}

	// Running again with nothing changed at Google adds nothing: the
	// connection's syncToken made it incremental.
	again := alice.must(t, "POST", "/api/w/google/contacts/"+connID+"/sync", nil, http.StatusOK)
	if got := int(again["added"].(float64)); got != 0 {
		t.Fatalf("second sync added = %d, want 0 (incremental, nothing changed): %+v", got, again)
	}
	if again["full_resync"] == true {
		t.Fatalf("second sync must be incremental, not a full resync: %+v", again)
	}
}

// TestRESTGoogleContactsSyncUnknownConnectionIs404: a connection id from
// nowhere (or another workspace) must 404, never leak a 200/500.
func TestRESTGoogleContactsSyncUnknownConnectionIs404(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	alice.must(t, "POST", "/api/w/google/contacts/00000000-0000-0000-0000-000000000000/sync", nil, http.StatusNotFound)
	alice.must(t, "GET", "/api/w/google/contacts/00000000-0000-0000-0000-000000000000/status", nil, http.StatusNotFound)
	alice.must(t, "GET", "/api/w/google/contacts/00000000-0000-0000-0000-000000000000/preview", nil, http.StatusNotFound)
}
