package googlecontacts_test

/*
MH-646 REST coverage, riding contacts_db_test.go's full-stack fixture (the
workspace default_country setting and the vcard reparse-phones route are
mounted on the same router as this package's own sync routes, so a shared
fixture is the natural place for them): setting/clearing default_country
through /api/w/settings/default-country (with its role gate and 422 on an
unknown code), and the google sync route's full=true query parameter
forcing a full resync end to end over HTTP.
*/

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestMH646RESTUpdateDefaultCountry(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	cur := alice.must(t, "GET", "/api/w/current", nil, http.StatusOK)
	ws, _ := cur["workspace"].(map[string]any)
	if ws == nil || (ws["default_country"] != nil && ws["default_country"] != "") {
		t.Fatalf("a brand new workspace must start with no default_country: %+v", ws)
	}

	set := alice.must(t, "PUT", "/api/w/settings/default-country", map[string]string{"country": "eg"}, http.StatusOK)
	if set["default_country"] != "EG" {
		t.Fatalf("set default_country = %+v, want EG (upper-cased)", set)
	}

	cur2 := alice.must(t, "GET", "/api/w/current", nil, http.StatusOK)
	ws2, _ := cur2["workspace"].(map[string]any)
	if ws2["default_country"] != "EG" {
		t.Fatalf("current-workspace after set: %+v", ws2)
	}

	// An unrecognised country is a 422, never a silent no-op or a 500.
	alice.must(t, "PUT", "/api/w/settings/default-country", map[string]string{"country": "ZZ"}, http.StatusUnprocessableEntity)

	// Clearing it is allowed (falls back to inference).
	cleared := alice.must(t, "PUT", "/api/w/settings/default-country", map[string]string{"country": ""}, http.StatusOK)
	if cleared["default_country"] != "" {
		t.Fatalf("clearing default_country = %+v, want empty", cleared)
	}
}

// TestMH646RESTUpdateDefaultCountryRequiresAdmin: a plain member cannot
// change workspace settings — this is authz.WorkspaceUpdate, admin/owner
// only, the same gate as everything else under /api/w/settings (mirrors
// internal/server/graphql_contact_test.go's invite-then-accept pattern for
// getting a second, lower-role user into the same workspace).
func TestMH646RESTUpdateDefaultCountryRequiresAdmin(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)

	jar, _ := cookiejar.New(nil)
	bob := &client{f: f, jar: jar, http: &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	bobEmail := "mh646-member-" + rnd() + "@example.test"
	bob.must(t, "POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-9"}, http.StatusCreated)

	inv := alice.must(t, "POST", "/api/w/invitations", map[string]string{"email": bobEmail, "role": "member"}, http.StatusCreated)
	token := strings.TrimPrefix(inv["accept_url"].(string), "/invite/")
	bob.must(t, "POST", "/api/invitations/accept", map[string]string{"token": token}, http.StatusOK)
	bob.ws = alice.ws

	bob.must(t, "PUT", "/api/w/settings/default-country", map[string]string{"country": "EG"}, http.StatusForbidden)

	// Owner unaffected: the setting is still unset after the member's
	// rejected attempt.
	cur := alice.must(t, "GET", "/api/w/current", nil, http.StatusOK)
	ws, _ := cur["workspace"].(map[string]any)
	if ws["default_country"] != nil && ws["default_country"] != "" {
		t.Fatalf("a forbidden write must not have changed anything: %+v", ws)
	}
}

// TestMH646RESTGoogleSyncFullQueryParamForcesFullResync: an already-synced
// connection's normal sync is incremental (nothing new at Google => added
// 0); passing full=true must re-import everyone again regardless of the
// stored sync token, the "Re-import everything" control the import panel
// exposes.
func TestMH646RESTGoogleSyncFullQueryParamForcesFullResync(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)
	f.setAccount("sub-mh646-full", "mh646-full@example.test")
	connID := alice.connect(t, "product=contacts")

	f.fake.SetContact("sub-mh646-full", "people/1", map[string]any{
		"names": []map[string]any{{"givenName": "Once"}},
	})

	first := alice.must(t, "POST", "/api/w/google/contacts/"+connID+"/sync", nil, http.StatusOK)
	if first["full_resync"] != true || int(first["added"].(float64)) != 1 {
		t.Fatalf("first sync: %+v", first)
	}

	incremental := alice.must(t, "POST", "/api/w/google/contacts/"+connID+"/sync", nil, http.StatusOK)
	if incremental["full_resync"] == true || int(incremental["added"].(float64)) != 0 {
		t.Fatalf("second sync must be incremental with nothing new: %+v", incremental)
	}

	forced := alice.must(t, "POST", "/api/w/google/contacts/"+connID+"/sync?full=true", nil, http.StatusOK)
	if forced["full_resync"] != true {
		t.Fatalf("full=true must force a full resync: %+v", forced)
	}
	// The contact already exists (matched by its google resourceName
	// identity), so a forced resync of the same, unchanged person merges
	// rather than adding a duplicate — that idempotence is exactly what
	// the count assertion below also checks.
	if int(forced["added"].(float64)) != 0 || int(forced["updated"].(float64)) != 1 {
		t.Fatalf("a full resync of an unchanged contact must merge, not add: %+v", forced)
	}

	// Idempotent: a forced full resync of an unchanged contact never
	// duplicates it.
	var count int
	if err := f.a.SQLDB.QueryRowContext(withWorkspace(t.Context(), alice.ws),
		`SELECT count(*) FROM contacts WHERE workspace_id = $1`, alice.ws).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 contact after two full resyncs of the same person, got %d", count)
	}
}

// TestMH646RESTGoogleSyncFillsPhonesOnceCountrySetThenReparse covers the
// production scenario the card describes end to end over HTTP: a workspace
// with no default_country syncs Google contacts whose phones are
// local-format and so get kept raw instead of dropped; setting
// default_country and re-parsing (POST reparse-phones) fills them in
// without needing a second Google round trip, and is safe to call again.
func TestMH646RESTGoogleSyncFillsPhonesOnceCountrySetThenReparse(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	alice.workspace(t)
	f.setAccount("sub-mh646-raw", "mh646-raw@example.test")
	connID := alice.connect(t, "product=contacts")

	f.fake.SetContact("sub-mh646-raw", "people/raw1", map[string]any{
		"names":        []map[string]any{{"givenName": "Raw", "familyName": "Phone"}},
		"phoneNumbers": []map[string]any{{"value": "01007778888"}},
	})

	sync := alice.must(t, "POST", "/api/w/google/contacts/"+connID+"/sync", nil, http.StatusOK)
	if int(sync["needs_country"].(float64)) != 1 {
		t.Fatalf("sync must flag the phone needing a country: %+v", sync)
	}
	if int(sync["added"].(float64)) != 1 {
		t.Fatalf("the contact itself (with a name) must still be created: %+v", sync)
	}

	ctx := withWorkspace(t.Context(), alice.ws)
	var phone, rawPhone string
	if err := f.a.SQLDB.QueryRowContext(ctx,
		`SELECT phone, coalesce(meta->>'phone_raw','') FROM contacts WHERE workspace_id=$1 AND name=$2`,
		alice.ws, "Raw Phone").Scan(&phone, &rawPhone); err != nil {
		t.Fatal(err)
	}
	if phone != "" || rawPhone != "01007778888" {
		t.Fatalf("phone=%q phone_raw=%q, want phone blank and the raw number kept", phone, rawPhone)
	}

	alice.must(t, "PUT", "/api/w/settings/default-country", map[string]string{"country": "EG"}, http.StatusOK)

	reparse := alice.must(t, "POST", "/api/w/contacts/vcard/reparse-phones", nil, http.StatusOK)
	if reparse["default_country"] != "EG" || int(reparse["resolved"].(float64)) != 1 {
		t.Fatalf("reparse-phones: %+v", reparse)
	}

	if err := f.a.SQLDB.QueryRowContext(ctx,
		`SELECT phone, coalesce(meta->>'phone_raw','') FROM contacts WHERE workspace_id=$1 AND name=$2`,
		alice.ws, "Raw Phone").Scan(&phone, &rawPhone); err != nil {
		t.Fatal(err)
	}
	if phone == "" || rawPhone != "" {
		t.Fatalf("after reparse: phone=%q phone_raw=%q, want phone filled and raw meta cleared", phone, rawPhone)
	}

	// Cheap and idempotent to run again with nothing left to resolve.
	again := alice.must(t, "POST", "/api/w/contacts/vcard/reparse-phones", nil, http.StatusOK)
	if int(again["resolved"].(float64)) != 0 {
		t.Fatalf("second reparse call: %+v", again)
	}
}
