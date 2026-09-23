package realtime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
)

var member = Viewer{UserID: "u-member", Role: "member"}

func recv(t *testing.T, ch <-chan []byte) Event {
	t.Helper()
	select {
	case b := <-ch:
		var ev Event
		if err := json.Unmarshal(b, &ev); err != nil {
			t.Fatalf("payload is not an event: %v", err)
		}
		return ev
	case <-time.After(time.Second):
		t.Fatal("no event arrived")
		return Event{}
	}
}

func silent(t *testing.T, ch <-chan []byte, who string) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("%s received %s — it must not", who, got)
	case <-time.After(150 * time.Millisecond):
	}
}

func inbound(conv string) Event {
	return Event{Kind: "message", ConversationID: conv, Direction: "in", Sender: "Visitor", Snippet: "hello there",
		Permission: authz.InboxRead, Alerting: true}
}

func TestPublishReachesOnlyItsOwnWorkspace(t *testing.T) {
	h := NewHub()
	a, closeA := h.Subscribe("ws-a", member)
	defer closeA()
	b, closeB := h.Subscribe("ws-b", member)
	defer closeB()

	h.Publish("ws-a", inbound("c1"))

	if ev := recv(t, a); ev.Kind != "message" || ev.ConversationID != "c1" {
		t.Errorf("workspace A got %+v", ev)
	}
	silent(t, b, "workspace B")
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, stop := h.Subscribe("ws", member)
	if n := h.Subscribers("ws"); n != 1 {
		t.Fatalf("subscribers = %d", n)
	}
	stop()
	if n := h.Subscribers("ws"); n != 0 {
		t.Errorf("after stop, subscribers = %d", n)
	}
	h.Publish("ws", inbound("c")) // must not panic on the closed channel

	if _, open := <-ch; open {
		t.Error("the channel should be closed and drained")
	}
	stop() // idempotent: a handler that unsubscribes twice must not panic
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	h := NewHub()
	_, stop := h.Subscribe("ws", member) // never read from
	defer stop()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ { // far past the 16-slot buffer
			h.Publish("ws", inbound("c"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a subscriber that stopped reading")
	}
}

func TestPublishWithoutWorkspaceIsDropped(t *testing.T) {
	h := NewHub()
	ch, stop := h.Subscribe("", member)
	defer stop()

	h.Publish("", inbound("c"))
	silent(t, ch, "an unscoped subscriber")
}

// An event that does not say who may see it reaches nobody: a new kind of
// event is closed until someone decides its audience.
func TestEventWithoutPermissionReachesNobody(t *testing.T) {
	h := NewHub()
	ch, stop := h.Subscribe("ws", Viewer{UserID: "u-owner", Role: "owner"})
	defer stop()

	h.Publish("ws", Event{Kind: "message", ConversationID: "c", Snippet: "secret"})
	silent(t, ch, "the owner, for an event with no permission")
}

// The owner-gated rule: a visitor's socket (no role) and a client-role member
// (no inbox:read, no notifications:read) hear nothing — not a message, not a
// notification item, not even that something happened.
func TestVisitorAndClientSocketsGetNothing(t *testing.T) {
	h := NewHub()
	visitor, stopV := h.Subscribe("ws", Viewer{})
	defer stopV()
	client, stopC := h.Subscribe("ws", Viewer{UserID: "u-client", Role: "client"})
	defer stopC()
	m, stopM := h.Subscribe("ws", member)
	defer stopM()

	h.Publish("ws", inbound("c1"))
	h.Publish("ws", Event{Kind: "notification", ItemID: "i1", Title: "Deploy failed", Permission: authz.NotificationsRead, Alerting: true})

	if ev := recv(t, m); ev.Kind != "message" || ev.Snippet != "hello there" || !ev.Alert {
		t.Errorf("a member should get the message with its snippet, alerting: %+v", ev)
	}
	if ev := recv(t, m); ev.Kind != "notification" || !ev.Alert {
		t.Errorf("a member should get the notification item, alerting: %+v", ev)
	}
	silent(t, visitor, "a visitor's socket")
	silent(t, client, "a client-role member")
}

// An assigned conversation alerts its assignee only; everyone else who may read
// the inbox still gets the event (for the badge), without the alert.
func TestAssignedConversationAlertsOnlyTheAssignee(t *testing.T) {
	h := NewHub()
	assignee := Viewer{UserID: "u-assignee", Role: "member"}
	other := Viewer{UserID: "u-other", Role: "admin"}
	a, stopA := h.Subscribe("ws", assignee)
	defer stopA()
	o, stopO := h.Subscribe("ws", other)
	defer stopO()

	ev := inbound("c1")
	ev.AlertOnly = assignee.UserID
	h.Publish("ws", ev)

	if got := recv(t, a); !got.Alert {
		t.Errorf("the assignee must be alerted: %+v", got)
	}
	if got := recv(t, o); got.Alert || got.ConversationID != "c1" {
		t.Errorf("another member must see the event (badge) but not be alerted: %+v", got)
	}
}

// A member's own reply never alerts them — or anyone: replies are not alerting.
func TestOwnReplyNeverAlerts(t *testing.T) {
	ev := Event{Kind: "message", Direction: "out", ConversationID: "c", AuthorUserID: member.UserID, Permission: authz.InboxRead}
	if got, ok := ev.For(member); !ok || got.Alert {
		t.Errorf("a reply must reach the author's tab quietly: ok=%v %+v", ok, got)
	}
	// Even an alerting event is quiet for its own author.
	ev.Alerting = true
	if got, _ := ev.For(member); got.Alert {
		t.Error("the author of an event must never be alerted by it")
	}
}

// Conversation content is stripped for a viewer who cannot read the inbox,
// even if the event was (wrongly) published under a permission they hold.
func TestSnippetNeverReachesAViewerWithoutInboxRead(t *testing.T) {
	ev := Event{Kind: "message", Sender: "Visitor", Snippet: "my card number is…", Permission: authz.WorkspaceRead, Alerting: true}
	got, ok := ev.For(Viewer{UserID: "u-client", Role: "client"})
	if !ok {
		t.Fatal("workspace:read is held by a client; the event itself is delivered")
	}
	if got.Snippet != "" || got.Sender != "" {
		t.Errorf("a viewer without inbox:read received conversation content: %+v", got)
	}
}
