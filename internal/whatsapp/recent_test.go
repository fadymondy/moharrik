package whatsapp

import (
	"testing"
	"time"
)

// A reconnect or first sync replays old messages through messages.upsert;
// only what was sent from (a day before) the link on is filed.
func TestHookDropsMessagesFromBeforeTheLink(t *testing.T) {
	linked := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	l := &Link{LinkedAt: &linked}
	if !l.tooOld(time.Date(2021, 1, 25, 0, 0, 0, 0, time.UTC).Unix()) {
		t.Error("a 2021 message was filed on a number linked in 2026")
	}
	if l.tooOld(linked.Add(-time.Hour).Unix()) {
		t.Error("a message from an hour before linking was dropped; it is within the grace")
	}
	if l.tooOld(linked.Add(48 * time.Hour).Unix()) {
		t.Error("a new message was dropped")
	}
	if l.tooOld(0) {
		t.Error("a message with no timestamp was dropped")
	}
	if (&Link{}).tooOld(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC).Unix()) {
		t.Error("a link with no linked_at dropped a message; it has nothing to compare against")
	}
}
