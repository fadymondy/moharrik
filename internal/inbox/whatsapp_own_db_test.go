package inbox

import (
	"testing"

	"github.com/fadymondy/moharrik/internal/whatsapp"
)

// MH-665: what the linked number sent itself. A reply typed on the phone is
// the team's outbound message in the thread; the echo of this app's own send
// adds nothing; a campaign's echo never starts a conversation.
func TestFileWhatsAppOwn(t *testing.T) {
	a := testApp(t)
	f := ccWorkspace(t, a)
	phone := "20155502" + numericIDSuffix()[:4]

	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := a.SQLDB.QueryRowContext(f.ctx, q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	convOf := func(p string) (id string, unread bool) {
		t.Helper()
		_ = a.SQLDB.QueryRowContext(f.ctx, `
			SELECT id, unread FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
			f.wsID, p).Scan(&id, &unread)
		return
	}

	// The customer writes; the owner answers from the phone.
	if err := FileWhatsApp(f.ctx, a, f.wsID, phone, "Rana", "is my order ready?", "WAIN"+idSuffix(), nil, nil); err != nil {
		t.Fatal(err)
	}
	conv, unread := convOf(phone)
	if conv == "" || !unread {
		t.Fatalf("the inbound message did not open an unread thread (%q, %v)", conv, unread)
	}
	phoneID := "WAPHONE" + idSuffix()
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, phone, "yes, it ships *today*", phoneID, nil, nil); err != nil {
		t.Fatal(err)
	}
	var dir, status, body string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT direction, status, body FROM messages WHERE workspace_id = $1 AND external_id = $2`,
		f.wsID, phoneID).Scan(&dir, &status, &body); err != nil {
		t.Fatalf("the phone reply was not filed: %v", err)
	}
	if dir != "out" || status != "sent" || body != "yes, it ships *today*" {
		t.Errorf("the phone reply is %s/%s %q, want out/sent", dir, status, body)
	}
	if _, unread := convOf(phone); unread {
		t.Errorf("answering from the phone left the thread unread")
	}
	// A redelivery is the same row.
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, phone, "yes, it ships *today*", phoneID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM messages WHERE workspace_id = $1 AND conversation_id = $2`, f.wsID, conv); n != 2 {
		t.Errorf("thread has %d messages after a redelivery, want 2", n)
	}

	// The echo of an inbox reply: already here under the same id, nothing added.
	replyID := "WAREPLY" + idSuffix()
	if _, err := recordOutbound(f.ctx, a, f.wsID, f.userID, "owner@x.test", conv, "", "from the inbox", replyID); err != nil {
		t.Fatal(err)
	}
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, phone, "from the inbox", replyID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM messages WHERE workspace_id = $1 AND external_id = $2`, f.wsID, replyID); n != 1 {
		t.Errorf("the echo of an inbox reply was filed again (%d rows)", n)
	}

	// The echo landing FIRST: the reply claims the row and names its author,
	// rather than failing and reporting a sent message as not sent.
	raceID := "WARACE" + idSuffix()
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, phone, "raced", raceID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := recordOutbound(f.ctx, a, f.wsID, f.userID, "owner@x.test", conv, "", "raced", raceID); err != nil {
		t.Fatalf("the reply failed after its echo landed first: %v", err)
	}
	if n := count(`SELECT count(*) FROM messages WHERE workspace_id = $1 AND external_id = $2 AND author_user_id = $3`,
		f.wsID, raceID, f.userID); n != 1 {
		t.Errorf("the reply did not claim its echo's row (%d authored rows)", n)
	}

	// The team writes first from the phone: the thread starts, read.
	fresh := "20155503" + numericIDSuffix()[:4]
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, fresh, "hello from the shop", "WAFIRST"+idSuffix(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if c, unread := convOf(fresh); c == "" || unread {
		t.Errorf("a chat started from the phone: conversation %q, unread %v", c, unread)
	}

	// A location sent from the phone, no caption: filed with its attachment.
	locID := "WALOC" + idSuffix()
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, phone, "", locID,
		&whatsapp.Media{Kind: "location", Lat: 30.04, Lng: 31.23, LocationName: "Shop"}, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM messages m JOIN message_attachments t ON t.message_id = m.id
		WHERE m.workspace_id = $1 AND m.external_id = $2 AND m.direction = 'out' AND t.kind = 'location'`,
		f.wsID, locID); n != 1 {
		t.Errorf("a location sent from the phone: %d out messages with a location, want 1", n)
	}

	// A campaign's echo (logged in whatsapp_sends) to a number with no thread
	// starts nothing.
	bcast := "20155504" + numericIDSuffix()[:4]
	bcastID := "WABCAST" + idSuffix()
	if _, err := a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO whatsapp_sends (workspace_id, kind, phone, ok, external_id, error)
		VALUES ($1, 'broadcast', $2, true, $3, '')`, f.wsID, bcast, bcastID); err != nil {
		t.Fatal(err)
	}
	if err := FileWhatsAppOwn(f.ctx, a, f.wsID, bcast, "our new menu", bcastID, nil, nil); err != nil {
		t.Fatal(err)
	}
	if c, _ := convOf(bcast); c != "" {
		t.Errorf("a broadcast's echo started a conversation")
	}
}
