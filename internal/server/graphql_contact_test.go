package server

/*
TestGraphQLContact — MH-591: contacts (identities, meta, Person-parity
fields) were missing from the GraphQL schema, breaking the API-first rule
(every resource must be on both GraphQL and REST/OpenAPI). This drives the
new /graphql contact/contacts/createContact/updateContact/deleteContact/
linkContactIdentity/unlinkContactIdentity surface over real HTTP, against the
real stack (guard, RLS pool) — the same way TestCrossWorkspaceIsolation and
TestMCPSurface already exercise REST and MCP.

It reuses bootSurface (mcp_surface_test.go, same package) for the boot/env
boilerplate rather than duplicating it a third time.
*/

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// gqlDo posts one GraphQL operation and decodes the standard {data,errors}
// envelope. It does not fail the test on a GraphQL-level error (those are
// asserted explicitly per case) — only on a transport-level surprise.
func gqlDo(t *testing.T, c *client, query string, vars map[string]any) (int, map[string]any, []any) {
	t.Helper()
	code, b := c.do(http.MethodPost, "/graphql", map[string]any{"query": query, "variables": vars})
	var out struct {
		Data   map[string]any `json:"data"`
		Errors []any          `json:"errors"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("graphql response not JSON: %s: %s", err, b)
	}
	return code, out.Data, out.Errors
}

const contactFields = `
	id name email phone handle title companyId ownerUserId tags lifecycle locale
	givenName familyName middleName namePrefix nameSuffix
	phoneticGivenName phoneticFamilyName nickname birthday biography
	avatarUrl meta { key value } identities { id channel externalId label isPrimary }
	createdAt updatedAt
`

func TestGraphQLContact(t *testing.T) {
	sf := bootSurface(t)
	alice := sf.alice // owner of sf.ws — satisfies authz.GraphQL's admin-role gate

	// ---- createContact, with Person-parity fields ---------------------------
	email := "gql-" + rnd() + "@example.test"
	createQ := `mutation($in: CreateContactInput!) { createContact(input: $in) { ` + contactFields + ` } }`
	code, data, errs := gqlDo(t, alice, createQ, map[string]any{"in": map[string]any{
		"email": email, "name": "GraphQL Test", "locale": "en",
	}})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("createContact: %d %v %v", code, errs, data)
	}
	ct := data["createContact"].(map[string]any)
	id := ct["id"].(string)
	if ct["email"] != email || ct["name"] != "GraphQL Test" {
		t.Fatalf("createContact returned wrong fields: %v", ct)
	}

	// ---- query it back --------------------------------------------------------
	getQ := `query($id: ID!) { contact(id: $id) { ` + contactFields + ` } }`
	code, data, errs = gqlDo(t, alice, getQ, map[string]any{"id": id})
	if code != 200 || len(errs) != 0 || data["contact"] == nil {
		t.Fatalf("contact(id): %d %v %v", code, errs, data)
	}

	// ---- updateContact: parity fields + Arabic meta ----------------------------
	updQ := `mutation($id: ID!, $in: UpdateContactInput!) { updateContact(id: $id, input: $in) { ` + contactFields + ` } }`
	code, data, errs = gqlDo(t, alice, updQ, map[string]any{"id": id, "in": map[string]any{
		"givenName":  "Fady",
		"familyName": "Mondy",
		"nickname":   "Fady",
		"biography":  "GraphQL parity test",
		"meta": []map[string]any{
			{"key": "المدينة", "value": "القاهرة"},
			{"key": "plan", "value": "pro"},
		},
	}})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("updateContact: %d %v %v", code, errs, data)
	}
	updated := data["updateContact"].(map[string]any)
	if updated["givenName"] != "Fady" || updated["familyName"] != "Mondy" {
		t.Fatalf("updateContact did not set parity fields: %v", updated)
	}
	meta := updated["meta"].([]any)
	if len(meta) != 2 {
		t.Fatalf("updateContact meta: want 2 entries, got %v", meta)
	}
	foundArabic := false
	for _, m := range meta {
		e := m.(map[string]any)
		if e["key"] == "المدينة" && e["value"] == "القاهرة" {
			foundArabic = true
		}
	}
	if !foundArabic {
		t.Fatalf("Arabic meta key/value not round-tripped: %v", meta)
	}

	// ---- linkContactIdentity: a second WhatsApp number -------------------------
	linkQ := `mutation($cid: ID!, $in: LinkContactIdentityInput!) { linkContactIdentity(contactId: $cid, input: $in) { identities { id channel externalId isPrimary } } }`
	code, data, errs = gqlDo(t, alice, linkQ, map[string]any{"cid": id, "in": map[string]any{
		"channel": "whatsapp", "externalId": "+201001234567",
	}})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("linkContactIdentity: %d %v %v", code, errs, data)
	}

	// ---- query again: 2 identities (the denormalised email + the new WhatsApp) --
	code, data, errs = gqlDo(t, alice, getQ, map[string]any{"id": id})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("contact(id) after link: %d %v %v", code, errs, data)
	}
	idents := data["contact"].(map[string]any)["identities"].([]any)
	if len(idents) != 2 {
		t.Fatalf("want 2 identities after linking WhatsApp, got %d: %v", len(idents), idents)
	}
	var whatsappIdentityID string
	for _, i := range idents {
		e := i.(map[string]any)
		if e["channel"] == "whatsapp" {
			whatsappIdentityID = e["id"].(string)
		}
	}
	if whatsappIdentityID == "" {
		t.Fatalf("no whatsapp identity among: %v", idents)
	}

	// ---- unlinkContactIdentity: back down to 1 ---------------------------------
	unlinkQ := `mutation($cid: ID!, $iid: ID!) { unlinkContactIdentity(contactId: $cid, identityId: $iid) { identities { id channel } } }`
	code, data, errs = gqlDo(t, alice, unlinkQ, map[string]any{"cid": id, "iid": whatsappIdentityID})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("unlinkContactIdentity: %d %v %v", code, errs, data)
	}
	if idents := data["unlinkContactIdentity"].(map[string]any)["identities"].([]any); len(idents) != 1 {
		t.Fatalf("want 1 identity after unlinking, got %d: %v", len(idents), idents)
	}

	// ---- contacts(search): the list resolver, hydrated with the same fields ----
	listQ := `query($q: String) { contacts(search: $q) { total contacts { id email } } }`
	code, data, errs = gqlDo(t, alice, listQ, map[string]any{"q": email})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("contacts(search): %d %v %v", code, errs, data)
	}
	page := data["contacts"].(map[string]any)
	if int(page["total"].(float64)) < 1 {
		t.Fatalf("contacts(search) found nothing for its own email: %v", page)
	}

	// ---- deleteContact ----------------------------------------------------------
	delQ := `mutation($id: ID!) { deleteContact(id: $id) }`
	code, data, errs = gqlDo(t, alice, delQ, map[string]any{"id": id})
	if code != 200 || len(errs) != 0 || data["deleteContact"] != true {
		t.Fatalf("deleteContact: %d %v %v", code, errs, data)
	}
	code, data, errs = gqlDo(t, alice, getQ, map[string]any{"id": id})
	if code != 200 || len(errs) == 0 || data["contact"] != nil {
		t.Fatalf("contact(id) after delete: want a not-found error and null data, got %d %v %v", code, errs, data)
	}

	// ==== workspace isolation: B cannot read A's contact by id ===================
	bob := newClient(t, sf.srv.URL)
	bobEmail := "gql-bob-" + rnd() + "@example.test"
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-9"}, 201)
	wsB := bob.must("POST", "/api/me/workspaces", map[string]string{"name": "GQL B " + rnd()}, 201)["workspace"].(map[string]any)
	bob.ws = wsB["id"].(string)

	// A fresh contact in A, to test cross-tenant reads against (the one above
	// was deleted).
	code, data, errs = gqlDo(t, alice, createQ, map[string]any{"in": map[string]any{
		"email": "gql-a-" + rnd() + "@example.test", "name": "A only",
	}})
	if code != 200 || len(errs) != 0 {
		t.Fatalf("createContact (for isolation check): %d %v %v", code, errs, data)
	}
	aOnlyID := data["createContact"].(map[string]any)["id"].(string)

	code, data, errs = gqlDo(t, bob, getQ, map[string]any{"id": aOnlyID})
	if code != 200 || len(errs) == 0 || data["contact"] != nil {
		t.Fatalf("workspace B read A's contact by id: want a not-found error and null, got %d %v %v", code, errs, data)
	}

	// ==== a read-only (non-admin) member cannot even reach /graphql ==============
	// authz.GraphQL requires the admin role (internal/server/guard.go): the
	// whole endpoint is gated before any resolver runs, for every operation,
	// read or write.
	dave := newClient(t, sf.srv.URL)
	daveEmail := "gql-dave-" + rnd() + "@example.test"
	dave.must("POST", "/api/auth/register", map[string]string{"email": daveEmail, "password": "correct-horse-11"}, 201)
	inv := alice.must("POST", "/api/w/invitations", map[string]string{"email": daveEmail, "role": "member"}, 201)
	dave.must("POST", "/api/invitations/accept", map[string]string{"token": strings.TrimPrefix(inv["accept_url"].(string), "/invite/")}, 200)
	dave.ws = sf.ws

	code, b := dave.do(http.MethodPost, "/graphql", map[string]any{"query": getQ, "variables": map[string]any{"id": aOnlyID}})
	if code != http.StatusForbidden || !strings.Contains(string(b), "GraphQL needs the admin role in this workspace") {
		t.Fatalf("a member reading via GraphQL: want 403 'GraphQL needs the admin role in this workspace', got %d %s", code, b)
	}
	code, b = dave.do(http.MethodPost, "/graphql", map[string]any{"query": createQ, "variables": map[string]any{"in": map[string]any{"email": "should-not-be-created@example.test"}}})
	if code != http.StatusForbidden || !strings.Contains(string(b), "GraphQL needs the admin role in this workspace") {
		t.Fatalf("a member writing via GraphQL: want 403 'GraphQL needs the admin role in this workspace', got %d %s", code, b)
	}
}
