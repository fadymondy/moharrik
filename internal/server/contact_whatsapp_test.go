package server

import (
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/inbox"
)

/*
TestContactWhatsApp covers MH-410: a member messages ONE contact on WhatsApp
from the contact's page, through the REST routes the page uses, against a fake
Evolution server. What it pins down:

  - the page is told WHY it cannot send (no opt-in, no phone, no linked number,
    a STOP, a limit) before anyone types, and nothing reaches Evolution then;
  - a new thread takes the confirm step (a code bound to contact + text), and
    the message then lands in the inbox thread, linked to the contact;
  - a thread that exists continues in one step;
  - the per-contact limit refuses the next message, with the reason;
  - every attempt is in the send log the page shows.
*/
func TestContactWhatsApp(t *testing.T) {
	sf := bootSurface(t)
	alice, evo := sf.alice, sf.evo

	ct := alice.must("POST", "/api/w/contacts", map[string]any{"email": "rana-" + rnd() + "@mail.example", "name": "Rana"}, 201)["contact"].(map[string]any)
	ctID := ct["id"].(string)
	status := func() map[string]any {
		t.Helper()
		return alice.must("GET", "/api/w/contacts/"+ctID+"/whatsapp", nil, 200)
	}

	// No opt-in: the reason, and no send.
	if st := status(); st["can_send"] != false || st["reason"] != "not_opted_in" {
		t.Fatalf("before the opt-in: %v", st)
	}
	if code, b := alice.do("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": "Hi"}); code != 409 || !strings.Contains(string(b), "not opted in") {
		t.Fatalf("sending without an opt-in: %d %s", code, b)
	}
	alice.must("POST", "/api/w/contacts/"+ctID+"/whatsapp/opt-in", nil, 200)
	if st := status(); st["reason"] != "no_phone" {
		t.Fatalf("opted in, no phone: %v", st)
	}
	alice.must("PATCH", "/api/w/contacts/"+ctID, map[string]any{"phone": "+20 16 5402 0101"}, 200)
	if st := status(); st["reason"] != "not_linked" || st["phone"] != "201654020101" {
		t.Fatalf("no linked number: %v", st)
	}
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)
	st := status()
	if st["can_send"] != true || st["reason"] != nil || st["conversation_id"] != "" {
		t.Fatalf("ready to send: %v", st)
	}
	if len(evo.sends()) != 0 {
		t.Fatalf("nothing may be sent by reading the status: %v", evo.sends())
	}

	// A new thread: the confirm step first.
	text := "Following up on our call: the proposal is in your inbox."
	first := alice.must("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": text}, 200)
	code6, _ := first["code"].(string)
	if first["confirmation_required"] != true || len(code6) != 6 || first["sent"] != false || len(evo.sends()) != 0 {
		t.Fatalf("a new thread must ask for confirmation first: %v", first)
	}
	if c, b := alice.do("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": text + " (edited)", "code": code6}); c != 422 {
		t.Fatalf("a code confirms only the text it was issued for: %d %s", c, b)
	}
	sent := alice.must("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": text, "code": code6}, 200)
	conv, _ := sent["conversation_id"].(string)
	if sent["sent"] != true || sent["new_thread"] != true || conv == "" {
		t.Fatalf("the confirmed send: %v", sent)
	}
	if s := evo.sends(); len(s) != 1 || s[0].Number != "201654020101" || s[0].Text != text {
		t.Fatalf("Evolution received %+v", s)
	}
	// It is in the inbox thread, linked to the contact.
	thread := alice.must("GET", "/api/w/conversations/"+conv, nil, 200)
	c := thread["conversation"].(map[string]any)
	msgs := thread["messages"].([]any)
	if c["channel"] != "whatsapp" || c["contact_id"] != ctID || len(msgs) != 1 || msgs[0].(map[string]any)["body"] != text {
		t.Fatalf("the inbox thread: %v", thread)
	}
	// The thread exists: the next message goes on it in one step.
	next := alice.must("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": "Call me when you have read it."}, 200)
	if next["sent"] != true || next["new_thread"] != false || next["conversation_id"] != conv {
		t.Fatalf("a message on an existing thread: %v", next)
	}

	// The page shows the log.
	st = status()
	if st["conversation_id"] != conv || len(st["recent"].([]any)) != 2 {
		t.Fatalf("status after two sends: %v", st)
	}

	// The per-contact limit refuses the next one, with the reason.
	old := inbox.DirectPerHour
	inbox.DirectPerHour = 2
	defer func() { inbox.DirectPerHour = old }()
	if st := status(); st["can_send"] != false || st["reason"] != "limit" {
		t.Fatalf("over the hourly limit: %v", st)
	}
	if code, b := alice.do("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": "One more"}); code != 429 || !strings.Contains(string(b), "last hour") {
		t.Fatalf("a send over the limit: %d %s", code, b)
	}
	if len(evo.sends()) != 2 {
		t.Fatalf("a refused send reached Evolution: %v", evo.sends())
	}
	inbox.DirectPerHour = old

	// A STOP outranks everything.
	if _, err := sf.sys.Exec(`UPDATE contacts SET whatsapp_opt_out_at = now() WHERE id = $1`, ctID); err != nil {
		t.Fatal(err)
	}
	if st := status(); st["reason"] != "opted_out" {
		t.Fatalf("after STOP: %v", st)
	}
	if code, _ := alice.do("POST", "/api/w/contacts/"+ctID+"/whatsapp/send", map[string]any{"text": "Hello?"}); code != 409 {
		t.Fatalf("sending after STOP: %d", code)
	}
}
