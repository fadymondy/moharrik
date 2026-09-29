package realtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/fadymondy/moharrik/internal/authz"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL (a throwaway database) to run the realtime bus tests")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestBusDeliversAcrossTwoHubs is the cross-process scenario itself: two Hubs
// (standing in for a web replica and a worker), each with its own Bus over
// the SAME database, wired the way server.Boot wires one. A NOTIFY from the
// "worker" Bus's Publish must reach a subscriber on the "web" Hub, with its
// scoping (Event.For) intact — and must NOT loop back onto the worker's own
// Hub a second time from its own NOTIFY.
func TestBusDeliversAcrossTwoHubs(t *testing.T) {
	db := testDB(t)
	dsn := os.Getenv("TEST_DATABASE_URL")

	webHub := NewHub()
	webBus := NewBus(dsn, db, nil)
	webHub.AttachBus(webBus)

	workerHub := NewHub()
	workerBus := NewBus(dsn, db, nil)
	workerHub.AttachBus(workerBus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	webBus.Listen(ctx, webHub)
	// The worker also listens, matching a real deployment where any process
	// may raise an event another process needs to see; this also proves
	// worker's own Bus discards ITS OWN notification.
	workerBus.Listen(ctx, workerHub)

	// A subscriber on the web Hub only, standing in for a browser tab.
	member := Viewer{UserID: "u1", Role: "owner"}
	ch, stop := webHub.Subscribe("ws-1", member)
	defer stop()

	// Nobody is subscribed on the worker Hub, so publishing there must not
	// panic or hang even though its own notification also arrives back at it.
	ev := Event{Kind: "lead", Permission: authz.LeadsRead}
	workerHub.Publish("ws-1", ev)

	select {
	case payload := <-ch:
		var got Event
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		if got.Kind != "lead" {
			t.Fatalf("got kind %q, want lead", got.Kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the web hub's subscriber never received the worker's event")
	}

	// Scoping is untouched: a viewer without the permission still hears
	// nothing, even delivered over the bus.
	nobody, stopNobody := webHub.Subscribe("ws-1", Viewer{})
	defer stopNobody()
	workerHub.Publish("ws-1", Event{Kind: "lead", Permission: authz.LeadsRead})
	select {
	case got := <-nobody:
		t.Fatalf("a visitor received an event over the bus: %s", got)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestBusIgnoresItsOwnNotification: a Bus's own NOTIFY must not be delivered
// a second time to its own Hub — Publish already delivered it locally.
func TestBusIgnoresItsOwnNotification(t *testing.T) {
	db := testDB(t)
	dsn := os.Getenv("TEST_DATABASE_URL")

	hub := NewHub()
	bus := NewBus(dsn, db, nil)
	hub.AttachBus(bus)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus.Listen(ctx, hub)

	member := Viewer{UserID: "u1", Role: "owner"}
	ch, stop := hub.Subscribe("ws-1", member)
	defer stop()

	hub.Publish("ws-1", Event{Kind: "lead", Permission: authz.LeadsRead})

	// One delivery (the local one) — a second would mean the bus re-delivered
	// its own notification.
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived")
	}
	select {
	case got := <-ch:
		t.Fatalf("a second (echoed) event arrived: %s", got)
	case <-time.After(500 * time.Millisecond):
	}
}
