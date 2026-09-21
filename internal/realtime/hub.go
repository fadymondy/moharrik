// Package realtime carries small events from the API to the tabs a workspace
// has open, so the inbox does not have to poll.
//
// Scope is the whole point: a subscriber is registered under ONE workspace id
// and can only ever receive what was published for that id. The scaffold's
// /events stream was global — one broadcast to every subscriber regardless of
// tenant — which is why it was never mounted (see internal/server/server.go).
//
// The hub is in-process. One container is the deployment today; a second
// replica would mean each tab only hears events raised by the replica it is
// connected to, so a multi-replica deploy needs a shared bus (Postgres
// LISTEN/NOTIFY or NATS, both already in the estate). Same caveat as the trends
// collector, and the same reason it is written down rather than assumed.
package realtime

import (
	"encoding/json"
	"sync"
)

// Event is what a tab receives. Kind says what changed; the payload is
// deliberately thin — the client revalidates through the normal, RLS-scoped
// endpoints rather than trusting anything here as data.
type Event struct {
	Kind           string `json:"kind"`
	ConversationID string `json:"conversation_id,omitempty"`
}

// Hub fans events out to the subscribers of one workspace at a time.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{} // workspace id -> subscribers
}

func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan []byte]struct{})}
}

// Subscribe returns a channel of encoded events for one workspace, and the
// function that releases it. The channel is buffered: a tab that has stopped
// reading (a laptop lid closed mid-stream) must not be able to block the
// request that published the event.
func (h *Hub) Subscribe(workspaceID string) (<-chan []byte, func()) {
	ch := make(chan []byte, 16)

	h.mu.Lock()
	if h.subs[workspaceID] == nil {
		h.subs[workspaceID] = make(map[chan []byte]struct{})
	}
	h.subs[workspaceID][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if set := h.subs[workspaceID]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.subs, workspaceID) // an idle workspace costs nothing
				}
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}

// Publish delivers ev to that workspace's subscribers and to nobody else. It
// never blocks: a subscriber whose buffer is full misses this event, and its
// next revalidation catches it up. Dropping an event is the correct trade here
// — the event is a hint to refetch, not the data itself.
func (h *Hub) Publish(workspaceID string, ev Event) {
	if workspaceID == "" {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[workspaceID] {
		select {
		case ch <- payload:
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
