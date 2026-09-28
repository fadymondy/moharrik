package server

/*
TestContactsImportExportRoles covers MH-690: the vCard and Google Contacts
routes checked no role, so a client could bulk-import contacts or read the
connected Google account's address book, and any member could download the
whole list. Importing is contacts:write (member and up); exporting is
contacts:export (manager and up), over REST and over MCP alike.
*/

import (
	"strings"
	"testing"
)

func TestContactsImportExportRoles(t *testing.T) {
	sf := bootSurface(t)
	alice := sf.alice
	card := "BEGIN:VCARD\r\nVERSION:3.0\r\nFN:Role Check\r\nEMAIL:role-" + rnd() + "@example.test\r\nEND:VCARD\r\n"

	join := func(role string) (*client, string) {
		email := "roles-" + role + "-" + rnd() + "@example.test"
		c := newClient(t, sf.srv.URL)
		c.must("POST", "/api/auth/register", map[string]string{"email": email, "password": "correct-horse-7"}, 201)
		inv := alice.must("POST", "/api/w/invitations", map[string]string{"email": email, "role": role}, 201)
		c.must("POST", "/api/invitations/accept", map[string]string{"token": strings.TrimPrefix(inv["accept_url"].(string), "/invite/")}, 200)
		c.ws = sf.ws
		return c, email
	}
	manager, _ := join("manager")
	member, memberEmail := join("member")
	clientRole, _ := join("client")

	export := "/api/w/contacts/vcard/export"
	preview := "/api/w/contacts/vcard/preview"
	fakeConn := "/api/w/google/contacts/00000000-0000-0000-0000-000000000000/preview"

	// REST: export is a manager's call.
	for who, c := range map[string]*client{"owner": alice, "manager": manager} {
		if code, _ := c.do("GET", export, nil); code != 200 {
			t.Errorf("%s export: want 200, got %d", who, code)
		}
	}
	for who, c := range map[string]*client{"member": member, "client": clientRole} {
		if code, _ := c.do("GET", export, nil); code != 403 {
			t.Errorf("%s export: want 403, got %d", who, code)
		}
	}
	// REST: import is contacts:write — a member may, a client may not.
	member.must("POST", preview, map[string]any{"vcard": card}, 200)
	clientRole.must("POST", preview, map[string]any{"vcard": card}, 403)
	clientRole.must("POST", "/api/w/contacts/vcard/commit", map[string]any{"vcard": card}, 403)
	clientRole.must("POST", "/api/w/contacts/vcard/reparse-phones", nil, 403)
	clientRole.must("GET", "/api/w/google/contacts/connections", nil, 403)
	clientRole.must("GET", fakeConn, nil, 403)
	if code, _ := member.do("GET", fakeConn, nil); code == 403 {
		t.Errorf("a member reaches the Google Contacts preview (any answer but 403), got 403")
	}

	// MCP: the same lines, checked when the tool runs.
	mgrAgent, _ := mint(t, manager, "manager agent", []string{"contacts:read", "contacts:write"})
	mcpJSON(t, mgrAgent, "import_contacts_commit", map[string]any{"vcard": card})
	if code, text, isErr := mcpCall(mgrAgent, "/api/mcp", "export_contacts_vcard", map[string]any{}); code != 200 || isErr || !strings.Contains(text, "BEGIN:VCARD") {
		t.Errorf("manager export_contacts_vcard: %d %v %.200s", code, isErr, text)
	}
	memberAgent, _ := mint(t, member, "member agent", []string{"contacts:read", "contacts:write"})
	if txt := mcpRefused(t, memberAgent, "export_contacts_vcard", map[string]any{}); !strings.Contains(txt, "manager") {
		t.Errorf("member export_contacts_vcard refused without saying why: %s", txt)
	}
	mcpJSON(t, memberAgent, "import_contacts_preview", map[string]any{"vcard": card})
	// Demoted to client, the same token can no longer import.
	alice.must("PUT", "/api/w/members/"+memberID(t, alice, memberEmail), map[string]string{"role": "client"}, 200)
	mcpRefused(t, memberAgent, "import_contacts_preview", map[string]any{"vcard": card})
	mcpRefused(t, memberAgent, "import_contacts_commit", map[string]any{"vcard": card})
	mcpRefused(t, memberAgent, "google_contacts_sync", map[string]any{"connection_id": "00000000-0000-0000-0000-000000000000"})
}

func memberID(t *testing.T, owner *client, email string) string {
	t.Helper()
	for _, m := range owner.must("GET", "/api/w/members", nil, 200)["members"].([]any) {
		if mm := m.(map[string]any); strings.EqualFold(mm["email"].(string), email) {
			return mm["user_id"].(string)
		}
	}
	t.Fatalf("no member %s", email)
	return ""
}
