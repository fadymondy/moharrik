// Package realtime carries small events from the API to the tabs a workspace
// has open, so the inbox does not have to poll.
//
// Scope is the whole point: a subscriber is registered under ONE workspace id
// and can only ever receive what was published for that id. The scaffold's
// /events stream was global — one broadcast to every subscriber regardless of
// tenant — which is why it was never mounted (see internal/server/server.go).
//
// Within a workspace, every event also names its AUDIENCE (MH-378): the
// permission a subscriber must hold to receive it at all, and who among those
// is to be alerted. An event that names no permission reaches nobody — a new
// kind of event is closed until someone decides who may see it, the same
// default-deny the guard applies to routes. A subscriber with no role (a
// visitor, or anything that is not a member) holds no permission and so hears
// nothing, whatever is published.
//
// The hub is in-process. One container is the deployment today; a second
// replica would mean each tab only hears events raised by the replica it is
// connected to, so a multi-replica deploy needs a shared bus (Postgres
// LISTEN/NOTIFY or NATS, both already in the estate). Same caveat as the trends
// collector, and the same reason it is written down rather than assumed.
package realtime

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/fadymondy/moharrik/internal/authz"
)

// Viewer is who is on the other end of a subscription: the member's user id
// and their role in THIS workspace. The zero Viewer is a visitor.
type Viewer struct {
	UserID string
	Role   string
}

// Event is what a tab receives. Kind says what changed. The payload stays thin
// — the client revalidates through the normal, RLS-scoped endpoints rather
// than trusting anything here as data — but an inbound message now carries
// the sender and a short snippet, because an alert that says only "something
// happened" makes the member open the app to find out what.
type Event struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
	ItemID         string `json:"item_id,omitempty"`
	// in = the customer wrote, out = a member replied (message events only).
	Direction string `json:"direction,omitempty"`
	Channel   string `json:"channel,omitempty"`
	// Sender and Snippet are conversation content: only ever sent to a viewer
	// who may read the inbox (see For).
	Sender  string `json:"sender,omitempty"`
	Snippet string `json:"snippet,omitempty"`
	// Notification-center items: the item's title, severity and source name.
	Title    string `json:"title,omitempty"`
	Severity string `json:"severity,omitempty"`
	Source   string `json:"source,omitempty"`
	// The member who caused it (a reply's author), so their own tab stays quiet.
	AuthorUserID string `json:"author_user_id,omitempty"`
	// Presence is a typing indicator (MH-660): composing, recording or paused.
	// Never stored — this is the only place it ever exists server-side, and a
	// dropped connection just means the indicator times out client-side instead
	// of clearing early.
	Presence string `json:"presence,omitempty"`
	// Alert is decided per viewer: true means pop, chime and push for THIS
	// member; false means refresh the badge and lists quietly.
	Alert bool `json:"alert,omitempty"`

	// ---- the audience: never on the wire ------------------------------------

	// Permission is the authz action a viewer must hold to receive the event.
	// Empty = nobody (fail closed).
	Permission string `json:"-"`
	// Alerting marks an event that may alert at all (a customer's message, a
	// new notification item). Replies, reads and assignments never do.
	Alerting bool `json:"-"`
	// AlertOnly, when set, restricts the alert to that one user: an assigned
	// conversation alerts its assignee, and everyone else just sees the badge.
	AlertOnly string `json:"-"`
	// Muted are the members who muted this conversation (MH-657): it still
	// refreshes their lists, and never pops, chimes or pushes for them.
	Muted []string `json:"-"`
}

// For renders the event as one viewer should see it, and reports whether they
// should see it at all. The hub and the web-push sender both go through here,
// so the tab and the lock screen cannot disagree about who is told what.
func (e Event) For(v Viewer) (Event, bool) {
	if e.Permission == "" || v.Role == "" || !authz.Can(v.Role, e.Permission) {
		return Event{}, false
	}
	out := e
	// Conversation content is inbox content, whatever permission the event was
	// published under: a member who cannot read the inbox never receives a
	// customer's words, not even by an event that was mislabelled.
	if !authz.Can(v.Role, authz.InboxRead) {
		out.Sender, out.Snippet = "", ""
	}
	out.Alert = e.Alerting && v.UserID != "" && v.UserID != e.AuthorUserID &&
		(e.AlertOnly == "" || e.AlertOnly == v.UserID) && !slices.Contains(e.Muted, v.UserID)
	return out, true
}

type subscriber struct {
	ch     chan []byte
	viewer Viewer
}

// Hub fans events out to the subscribers of one workspace at a time.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[*subscriber]struct{} // workspace id -> subscribers
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[*subscriber]struct{})}
}

// Subscribe returns a channel of encoded events for one workspace, as seen by
// one viewer, and the function that releases it. The channel is buffered: a tab
// that has stopped reading (a laptop lid closed mid-stream) must not be able to
// block the request that published the event.
func (h *Hub) Subscribe(workspaceID string, v Viewer) (<-chan []byte, func()) {
	s := &subscriber{ch: make(chan []byte, 16), viewer: v}

	h.mu.Lock()
	if h.subs[workspaceID] == nil {
		h.subs[workspaceID] = make(map[*subscriber]struct{})
	}
	h.subs[workspaceID][s] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if set := h.subs[workspaceID]; set != nil {
				delete(set, s)
				if len(set) == 0 {
					delete(h.subs, workspaceID) // an idle workspace costs nothing
				}
			}
			h.mu.Unlock()
			close(s.ch)
		})
	}
}

// Publish delivers ev to that workspace's subscribers who are in its audience,
// and to nobody else. It never blocks: a subscriber whose buffer is full misses
// this event, and its next revalidation catches it up. Dropping an event is the
// correct trade here — the event is a hint to refetch, not the data itself.
func (h *Hub) Publish(workspaceID string, ev Event) {
	if workspaceID == "" || ev.Permission == "" {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs[workspaceID] {
		mine, ok := ev.For(s.viewer)
		if !ok {
			continue
		}
		payload, err := json.Marshal(mine)
		if err != nil {
			continue
		}
		select {
		case s.ch <- payload:
		default:
		}
	}
}

// Subscribers reports how many tabs are listening for a workspace. Used by the
// tests, and worth having when someone asks whether the stream is being used.
func (h *Hub) Subscribers(workspaceID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[workspaceID])
}
