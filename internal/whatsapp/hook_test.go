package whatsapp

import (
	"encoding/json"
	"testing"
)

func read(t *testing.T, raw string) (Inbound, bool) {
	t.Helper()
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m.Read()
}

func TestReadACustomerMessage(t *testing.T) {
	in, ok := read(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "3EB0ABC"},
	  "pushName": "Dana",
	  "message": {"conversation": "  is my order shipped?  "},
	  "messageTimestamp": 1789999999}`)
	if !ok {
		t.Fatal("a plain text message from a customer must be filed")
	}
	if in.Phone != "201234567890" {
		t.Errorf("phone = %q", in.Phone)
	}
	if in.Name != "Dana" || in.Text != "is my order shipped?" {
		t.Errorf("got %+v", in)
	}
	if in.ID != "3EB0ABC" {
		t.Errorf("id = %q — it is what makes a redelivery the same row", in.ID)
	}
}

func TestReadIgnoresWhatTheInboxCannotAnswer(t *testing.T) {
	// A group chat is not a customer conversation.
	if _, ok := read(t, `{"key":{"remoteJid":"12345-67890@g.us","fromMe":false,"id":"A"},"message":{"conversation":"hi all"}}`); ok {
		t.Error("a group message must not open a conversation")
	}
	// A message with no text at all (a reaction, a protocol message).
	if _, ok := read(t, `{"key":{"remoteJid":"201234567890@s.whatsapp.net","fromMe":false,"id":"B"},"message":{}}`); ok {
		t.Error("a message with nothing in it must not be filed")
	}
	// No number to answer.
	if _, ok := read(t, `{"key":{"remoteJid":"","fromMe":false,"id":"C"},"message":{"conversation":"x"}}`); ok {
		t.Error("a chat with no number must not be filed")
	}
}

func TestReadKeepsOurOwnEchoRecognisable(t *testing.T) {
	// Evolution echoes back what WE sent. It parses fine, and FromMe is the
	// flag that stops it being filed as the customer writing.
	in, ok := read(t, `{"key":{"remoteJid":"201234567890@s.whatsapp.net","fromMe":true,"id":"D"},"message":{"conversation":"we shipped it"}}`)
	if !ok {
		t.Fatal("our own message still parses")
	}
	if !in.FromMe {
		t.Error("from_me must survive: filing our own message as inbound makes the team look like the customer")
	}
}

func TestReadDescribesAttachments(t *testing.T) {
	in, ok := read(t, `{"key":{"remoteJid":"201234567890@s.whatsapp.net","fromMe":false,"id":"E"},
	  "message":{"documentMessage":{"fileName":"receipt.pdf"}}}`)
	if !ok || in.Text != "[file: receipt.pdf]" {
		t.Errorf("got %q — an attachment with no caption is described, not dropped", in.Text)
	}

	in, ok = read(t, `{"key":{"remoteJid":"201234567890@s.whatsapp.net","fromMe":false,"id":"F"},
	  "message":{"audioMessage":{"seconds":7}}}`)
	if !ok || in.Text != "[voice message]" {
		t.Errorf("got %q", in.Text)
	}

	// A caption is the message when there is one.
	in, _ = read(t, `{"key":{"remoteJid":"201234567890@s.whatsapp.net","fromMe":false,"id":"G"},
	  "message":{"imageMessage":{"caption":"this arrived broken"}}}`)
	if in.Text != "this arrived broken" {
		t.Errorf("got %q", in.Text)
	}
}

func TestNumberStripsEverythingButDigits(t *testing.T) {
	for in, want := range map[string]string{
		"201234567890@s.whatsapp.net":    "201234567890",
		"201234567890:12@s.whatsapp.net": "201234567890",
		"+20 123 456 7890":               "201234567890",
		"12345-67890@g.us":               "1234567890",
		"":                               "",
	} {
		if got := Number(in); got != want {
			t.Errorf("Number(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameTokenRefusesTheEmptyCase(t *testing.T) {
	if !SameToken("abc", "abc") {
		t.Error("equal tokens must match")
	}
	if SameToken("abc", "abd") {
		t.Error("different tokens must not match")
	}
	// The dangerous one: a link with no token stored must not accept a caller
	// that sends no token.
	if SameToken("", "") {
		t.Error("empty must never authorise — that is a public endpoint with no check at all")
	}
	if SameToken("", "abc") || SameToken("abc", "") {
		t.Error("a missing token on either side must not authorise")
	}
}
