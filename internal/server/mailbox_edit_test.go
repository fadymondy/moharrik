package server

import (
	"strings"
	"testing"
)

// TestMailboxEditAndDelete covers MH-669: a saved mailbox can be edited and
// deleted. huma did not bind the {id} of a path struct embedded two levels
// deep, so every PUT, DELETE, test and check on a mailbox answered 404.
func TestMailboxEditAndDelete(t *testing.T) {
	sf := bootSurface(t)
	alice := sf.alice

	id := alice.must("POST", "/api/w/mailboxes", map[string]any{
		"name": "Support", "host": "127.0.0.1", "port": 9, "username": "mb-user", "password": "mb-pw", "tls_mode": "none",
	}, 201)["mailbox"].(map[string]any)["id"].(string)

	got := alice.must("PUT", "/api/w/mailboxes/"+id, map[string]any{
		"name": "Sales", "host": "127.0.0.1", "port": 9, "username": "mb-user", "tls_mode": "none",
	}, 200)["mailbox"].(map[string]any)
	if got["id"] != id || got["name"] != "Sales" {
		t.Fatalf("the edit came back as %v", got)
	}

	// The test and check actions reach the mailbox (the server on :9 is not
	// there, so they fail — but not with "not found").
	for _, p := range []string{"/test", "/check"} {
		if code, b := alice.do("POST", "/api/w/mailboxes/"+id+p, map[string]any{}); code == 404 {
			t.Errorf("POST %s: 404 %s", p, b)
		}
	}

	alice.must("DELETE", "/api/w/mailboxes/"+id, nil, 204)
	if _, b := alice.do("GET", "/api/w/mailboxes", nil); strings.Contains(string(b), id) {
		t.Errorf("the deleted mailbox is still listed: %s", b)
	}
	if code, _ := alice.do("PUT", "/api/w/mailboxes/"+id, map[string]any{
		"name": "Gone", "host": "127.0.0.1", "port": 9, "username": "mb-user", "tls_mode": "none",
	}); code != 404 {
		t.Errorf("editing a deleted mailbox: %d, want 404", code)
	}
}
