package inbox

import "sync"

/*
visitorHub tells a chat visitor's open stream that their conversation changed
(MH-382). Keyed by conversation id, never by workspace: a visitor is entitled
to exactly one thread, so that is the only thing they can subscribe to, and
there is no "everything in the workspace" channel for anyone to reach.

Same shape and the same caveats as internal/realtime's hub: in-process (a
second replica needs a shared bus), buffered, and it drops rather than blocks,
because an event is a hint to refetch and the widget polls as a fallback.
*/
type visitorHub struct {
	mu   sync.Mutex
	subs map[string]map[chan string]struct{}
}

var visitors = &visitorHub{subs: map[string]map[chan string]struct{}{}}

func (h *visitorHub) subscribe(convID string) (<-chan string, func()) {
	ch := make(chan string, 8)
	h.mu.Lock()
	if h.subs[convID] == nil {
		h.subs[convID] = map[chan string]struct{}{}
	}
	h.subs[convID][ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			if set := h.subs[convID]; set != nil {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.subs, convID)
				}
			}
			h.mu.Unlock()
			close(ch)
		})
	}
}

// publishTyping tells the conversation's visitor stream a member is typing
// (MH-660). presence is "composing", "recording", or "" to clear it. Encoded
// onto the same string channel as publish (typing:<presence>) rather than
// widening the channel's type, so every other kind and its callers are
// untouched.
func (h *visitorHub) publishTyping(convID, presence string) {
	h.publish(convID, "typing:"+presence)
}

// publish tells the conversation's visitor streams something changed: kind is
// "message" (a team reply), "status" (closed, reopened), or "typing:<presence>"
// (see publishTyping).
func (h *visitorHub) publish(convID, kind string) {
	if convID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[convID] {
		select {
		case ch <- kind:
		default:
		}
	}
}

// listeners is how many streams a conversation has open. Tests only.
func (h *visitorHub) listeners(convID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[convID])
}
