package whatsapp

import (
	"encoding/json"
	"testing"
)

// MH-656: Evolution's messages.update receipts become the thread's ticks.
func TestReceiptsReadEveryShapeEvolutionSends(t *testing.T) {
	cases := []struct {
		name, body, id, status string
		ok                     bool
	}{
		{"v2 flat", `{"keyId":"ABC","fromMe":true,"status":"DELIVERY_ACK"}`, "ABC", "delivered", true},
		{"v2 read", `{"keyId":"ABC","fromMe":true,"status":"READ"}`, "ABC", "read", true},
		{"voice note played", `{"keyId":"ABC","fromMe":true,"status":"PLAYED"}`, "ABC", "read", true},
		{"server ack", `{"keyId":"ABC","fromMe":true,"status":"SERVER_ACK"}`, "ABC", "sent", true},
		{"nested, numeric", `{"key":{"id":"XYZ","fromMe":true},"update":{"status":4}}`, "XYZ", "read", true},
		{"a customer's own message", `{"keyId":"ABC","fromMe":false,"status":"READ"}`, "", "", false},
		{"unknown status", `{"keyId":"ABC","fromMe":true,"status":"DELETED"}`, "", "", false},
		{"no id", `{"fromMe":true,"status":"READ"}`, "", "", false},
	}
	for _, c := range cases {
		rs := receipts(json.RawMessage(c.body))
		if len(rs) != 1 {
			t.Fatalf("%s: got %d receipts", c.name, len(rs))
		}
		id, status, ok := rs[0].Read()
		if ok != c.ok || (ok && (id != c.id || status != c.status)) {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", c.name, id, status, ok, c.id, c.status, c.ok)
		}
	}
	if n := len(receipts(json.RawMessage(`[{"keyId":"A","fromMe":true,"status":"READ"},{"keyId":"B","fromMe":true,"status":"READ"}]`))); n != 2 {
		t.Errorf("a batch of two receipts read as %d", n)
	}
}

// A number linked before MESSAGES_UPDATE (or PRESENCE_UPDATE, MH-660) existed
// is widened once, and one that already has every event is left alone.
func TestMissingEvents(t *testing.T) {
	if !missingEvents([]string{"MESSAGES_UPSERT", "CONNECTION_UPDATE"}) {
		t.Error("an old subscription without MESSAGES_UPDATE was not seen as missing it")
	}
	if !missingEvents([]string{"messages.upsert", "messages.update", "connection.update"}) {
		t.Error("MH-660: a subscription linked before PRESENCE_UPDATE existed must be seen as missing it, so the next delivery widens it")
	}
	if missingEvents([]string{"messages.upsert", "messages.update", "connection.update", "presence.update"}) {
		t.Error("a complete subscription in Evolution's dotted spelling was seen as missing events")
	}
}
