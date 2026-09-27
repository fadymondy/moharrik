package inbox

import "testing"

// MH-656: WhatsApp receipts move an outbound message's tick forward, never
// back, and touch nothing but our own outbound message.
func TestMarkStatusMovesTicksForwardOnly(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	var convID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_phone, subject, last_message_at)
		VALUES ($1, 'whatsapp', '201555010000', 'hi', now()) RETURNING id`, f.wsID).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	ext := "WAOUT" + idSuffix()
	inExt := "WAIN" + idSuffix()
	if _, err := a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id, status)
		VALUES ($1, $2, 'out', 'hello', $3, 'sent'), ($1, $2, 'in', 'hi', $4, '')`,
		f.wsID, convID, ext, inExt); err != nil {
		t.Fatal(err)
	}
	status := func(id string) string {
		var s string
		if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT status FROM messages WHERE workspace_id = $1 AND external_id = $2`, f.wsID, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	steps := []struct{ in, want string }{
		{"delivered", "delivered"},
		{"read", "read"},
		{"delivered", "read"}, // late receipt
		{"sent", "read"},
	}
	for _, s := range steps {
		if err := MarkStatus(f.ctx, a, f.wsID, ext, s.in); err != nil {
			t.Fatal(err)
		}
		if got := status(ext); got != s.want {
			t.Fatalf("after %q: status %q, want %q", s.in, got, s.want)
		}
	}
	if err := MarkStatus(f.ctx, a, f.wsID, inExt, "read"); err != nil {
		t.Fatal(err)
	}
	if got := status(inExt); got != "" {
		t.Errorf("an inbound message was given a tick: %q", got)
	}
	if err := MarkStatus(f.ctx, a, f.wsID, "NOT-OURS", "read"); err != nil {
		t.Errorf("a receipt for a message we never stored errored: %v", err)
	}

	page, err := threadPage(f.ctx, a.SQLDB, f.wsID, convID, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range page.Messages {
		if m.Direction == "out" && m.Status != "read" {
			t.Errorf("the thread shows the outbound message as %q", m.Status)
		}
	}
}
