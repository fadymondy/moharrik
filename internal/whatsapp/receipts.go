package whatsapp

import (
	"context"
	"encoding/json"
	"strings"
)

/*
Delivery receipts (MH-656): the ticks under a sent WhatsApp message. Evolution
reports each step of one of our messages as a messages.update event — the
server took it, the phone got it, the customer read it (or played a voice
note) — keyed by the id SendText returned, which is the external_id the reply
was stored under.
*/

// Receipt is one messages.update payload. Evolution v2 names the message id
// keyId; older builds nest it under key.id — both are read.
type Receipt struct {
	KeyID     string `json:"keyId"`
	MessageID string `json:"messageId"`
	FromMe    bool   `json:"fromMe"`
	Status    string `json:"status"`
	Key       struct {
		ID     string `json:"id"`
		FromMe bool   `json:"fromMe"`
	} `json:"key"`
	Update struct {
		Status any `json:"status"`
	} `json:"update"`
}

// Read turns a receipt into (WhatsApp message id, status), where status is
// one of sent, delivered, read. ok is false for anything that is not a
// receipt for one of our own messages.
func (r Receipt) Read() (id, status string, ok bool) {
	id = firstNonEmpty(r.KeyID, r.Key.ID)
	if id == "" || !(r.FromMe || r.Key.FromMe) {
		return "", "", false
	}
	raw := r.Status
	if raw == "" {
		switch v := r.Update.Status.(type) {
		case string:
			raw = v
		case float64:
			// Baileys' numeric WAMessageStatus: 2 server, 3 delivered, 4 read, 5 played.
			raw = map[float64]string{2: "SERVER_ACK", 3: "DELIVERY_ACK", 4: "READ", 5: "PLAYED"}[v]
		}
	}
	status = ReceiptStatus(raw)
	return id, status, status != ""
}

// ReceiptStatus maps Evolution's status names onto the three ticks.
func ReceiptStatus(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "SERVER_ACK", "PENDING":
		return "sent"
	case "DELIVERY_ACK", "DELIVERED":
		return "delivered"
	case "READ", "PLAYED":
		return "read"
	}
	return ""
}

// StatusSink records a receipt against the stored message. Wired in at boot
// by the inbox, for the same reason Filer is.
type StatusSink func(ctx context.Context, workspaceID, externalID, status string) error

var statusSink StatusSink

// SetStatusSink wires the inbox's receipt handler in at boot.
func SetStatusSink(f StatusSink) { statusSink = f }

// receipts reads a messages.update delivery: one receipt, or several.
func receipts(data json.RawMessage) []Receipt {
	var many []Receipt
	if json.Unmarshal(data, &many) == nil {
		return many
	}
	var one Receipt
	if json.Unmarshal(data, &one) == nil {
		return []Receipt{one}
	}
	return nil
}
