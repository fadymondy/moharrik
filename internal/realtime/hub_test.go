package realtime

import (
	"encoding/json"
	"testing"
	"time"
)

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

func TestPublishReachesOnlyItsOwnWorkspace(t *testing.T) {
	h := NewHub()
	a, closeA := h.Subscribe("ws-a")
	defer closeA()
	b, closeB := h.Subscribe("ws-b")
	defer closeB()

	h.Publish("ws-a", Event{Kind: "message", ConversationID: "c1"})

	if ev := recv(t, a); ev.Kind != "message" || ev.ConversationID != "c1" {
		t.Errorf("workspace A got %+v", ev)
	}
	select {
	case got := <-b:
		t.Fatalf("workspace B received %s — the whole point is that it cannot", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, stop := h.Subscribe("ws")
	if n := h.Subscribers("ws"); n != 1 {
		t.Fatalf("subscribers = %d", n)
	}
	stop()
	if n := h.Subscribers("ws"); n != 0 {
		t.Errorf("after stop, subscribers = %d", n)
	}
	h.Publish("ws", Event{Kind: "message"}) // must not panic on the closed channel

	if _, open := <-ch; open {
		t.Error("the channel should be closed and drained")
	}
	stop() // idempotent: a handler that unsubscribes twice must not panic
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	h := NewHub()
	_, stop := h.Subscribe("ws") // never read from
	defer stop()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ { // far past the 16-slot buffer
			h.Publish("ws", Event{Kind: "message"})
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
	ch, stop := h.Subscribe("")
	defer stop()

	h.Publish("", Event{Kind: "message"})
	select {
	case got := <-ch:
		t.Fatalf("an unscoped publish delivered %s — that is the global stream this replaces", got)
	case <-time.After(150 * time.Millisecond):
	}
}
