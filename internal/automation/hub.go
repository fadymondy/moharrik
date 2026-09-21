package automation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

/*
The live registry of connected browsers.

This is NOT internal/realtime. That hub is a fan-out bus for the app's own
tabs: broadcast-only, no request/response, and the subscriber is a page holding
a session. An agent channel is the opposite shape — one peer addressed by id,
every command expects a reply, and the peer is a program holding a token.

The socket carries CONTROL traffic only: commands, replies, heartbeats. It is
small, ordered and needs server push. Anything bulky (a screenshot, a large
extraction) is summarised on the step and kept out of the pumps.
*/

const (
	// Control frames only. Generous enough for a page of extracted rows, small
	// enough that a runaway peer cannot exhaust memory.
	maxMessageBytes = 256 << 10

	// CloseHalted turns a browser away while the kill switch is on. It is in
	// the 4000 range reserved for applications, and deliberately NOT 1008: an
	// extension treats 1008 as "stop trying", and a halted browser should keep
	// retrying so it comes back by itself when someone resumes automation.
	CloseHalted = 4001

	writeWait = 10 * time.Second
	pongWait  = 45 * time.Second

	// 20 seconds, and the number matters: the peer is an MV3 service worker,
	// which Chrome terminates after 30 SECONDS of inactivity — taking the
	// socket with it. Traffic on the socket resets that timer, so the server
	// pings inside the window rather than at the usual "nine tenths of the
	// pong deadline", which would have been 54s and let every browser fall
	// asleep between runs. It looked exactly like a flaky connection.
	pingPeriod = 20 * time.Second

	// How long a freshly opened socket may stay silent before it must have
	// authenticated. A browser's WebSocket API cannot set headers, so the token
	// arrives in the first frame rather than in Authorization — and without a
	// deadline an unauthenticated socket could sit open indefinitely, costing a
	// connection and a goroutine for nothing.
	helloWait = 10 * time.Second
)

// Envelope is every frame, in both directions. `id` correlates a command with
// its reply; a frame pushed by the browser has no id and expects none. One
// shape in both directions means the pumps never have to know who sent what.
type Envelope struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Run     string          `json:"run,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// Connected is the identity behind a live socket.
type Connected struct {
	AgentID     string    `json:"agent_id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Platform    string    `json:"platform"`
	Version     string    `json:"extension_version"`
	ConnectedAt time.Time `json:"connected_at"`
}

// AgentError is returned when the browser answered and its answer was a
// refusal. The text is the browser's own.
type AgentError struct{ Message string }

func (e AgentError) Error() string { return e.Message }

type conn struct {
	agent Connected
	ws    *websocket.Conn
	send  chan []byte

	mu      sync.Mutex
	pending map[string]chan Envelope
	closed  bool
}

// Hub holds the live sockets, keyed by agent id.
type Hub struct {
	log *slog.Logger

	mu    sync.RWMutex
	conns map[string]*conn // agent id -> socket
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{log: log, conns: make(map[string]*conn)}
}

// Online reports whether a browser is connected right now. The database's
// status column is the last known value; this is the truth.
func (h *Hub) Online(agentID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[agentID]
	return ok
}

// OnlineIn returns the connected agent ids of one workspace. Scoped, because
// "which browsers are up" must not be answerable across tenants.
func (h *Hub) OnlineIn(workspaceID string) map[string]bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string]bool, len(h.conns))
	for id, c := range h.conns {
		if c.agent.WorkspaceID == workspaceID {
			out[id] = true
		}
	}
	return out
}

// Call sends one command to a browser and waits for the reply with that id.
//
// The workspace is passed in and checked against the socket's own: an agent id
// is a uuid, but "the caller supplied the id" is not the same as "the caller is
// allowed to drive it", and this is the last place that can still tell.
func (h *Hub) Call(ctx context.Context, workspaceID, agentID string, env Envelope, timeout time.Duration) (Envelope, error) {
	h.mu.RLock()
	c := h.conns[agentID]
	h.mu.RUnlock()
	if c == nil || c.agent.WorkspaceID != workspaceID {
		return Envelope{}, ErrOffline
	}

	env.ID = newID()
	reply := make(chan Envelope, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Envelope{}, ErrOffline
	}
	c.pending[env.ID] = reply
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, env.ID)
		c.mu.Unlock()
	}()

	frame, err := json.Marshal(env)
	if err != nil {
		return Envelope{}, err
	}
	if !c.trySend(frame) {
		return Envelope{}, ErrOffline
	}

	select {
	case got, ok := <-reply:
		if !ok {
			return Envelope{}, ErrOffline // the socket closed under us
		}
		if got.Error != "" {
			return got, AgentError{Message: got.Error}
		}
		return got, nil
	case <-time.After(timeout):
		return Envelope{}, errors.New("the browser did not answer in time")
	case <-ctx.Done():
		return Envelope{}, ctx.Err()
	}
}

// Disconnect drops a browser's socket — used when its token is revoked, and by
// the kill switch. A revoked browser that keeps its socket keeps its access.
func (h *Hub) Disconnect(agentID string, code int, reason string) {
	h.mu.RLock()
	c := h.conns[agentID]
	h.mu.RUnlock()
	if c == nil {
		return
	}
	writeClose(c.ws, code, reason)
	_ = c.ws.Close()
}

// DisconnectWorkspace drops every browser of one workspace. The kill switch
// uses it: "paused" that leaves sockets open is not a kill switch.
func (h *Hub) DisconnectWorkspace(workspaceID string, reason string) {
	h.mu.RLock()
	var ids []string
	for id, c := range h.conns {
		if c.agent.WorkspaceID == workspaceID {
			ids = append(ids, id)
		}
	}
	h.mu.RUnlock()
	for _, id := range ids {
		h.Disconnect(id, CloseHalted, reason)
	}
}

func (h *Hub) add(c *conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// A second socket for the same browser replaces the first: a reconnect
	// after a laptop woke up would otherwise leave a dead socket registered and
	// every command would go to it.
	if old := h.conns[c.agent.AgentID]; old != nil {
		go func() { writeClose(old.ws, websocket.CloseNormalClosure, "replaced"); _ = old.ws.Close() }()
	}
	h.conns[c.agent.AgentID] = c
}

func (h *Hub) remove(c *conn) {
	h.mu.Lock()
	if h.conns[c.agent.AgentID] == c {
		delete(h.conns, c.agent.AgentID)
	}
	h.mu.Unlock()
	c.close()
}

func (c *conn) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.send)
	// Waiters are released rather than left to time out: "gone" is a faster and
	// more accurate answer than a timeout, and the caller can say so.
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}

// deliver hands a reply to whoever is waiting for it. It reports whether
// anyone was.
func (c *conn) deliver(env Envelope) bool {
	c.mu.Lock()
	ch, ok := c.pending[env.ID]
	if ok {
		delete(c.pending, env.ID)
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	ch <- env
	close(ch)
	return true
}

// trySend never blocks: a peer that has stopped reading must not stall the
// caller. It reports whether the frame was queued.
func (c *conn) trySend(frame []byte) bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return false
	}
	select {
	case c.send <- frame:
		return true
	default:
		return false
	}
}

func writeClose(ws *websocket.Conn, code int, reason string) {
	_ = ws.SetWriteDeadline(time.Now().Add(writeWait))
	_ = ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason))
}

// The registry of live sockets is per PROCESS, not per request, and
// internal/app cannot hold it: automation imports app (for the pools), so a
// field there would be an import cycle. It lives here instead, set once at
// boot by server.Boot and read by anything that needs to reach a browser —
// the REST handlers get it passed in, MCP reads it from here.
var defaultHub *Hub

// SetDefault records the process's hub. Called once, from server.Boot.
func SetDefault(h *Hub) { defaultHub = h }

// Default returns it, or nil before boot (the OpenAPI export never boots).
func Default() *Hub { return defaultHub }
