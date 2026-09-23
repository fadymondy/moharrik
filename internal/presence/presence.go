/*
Package presence answers one question for a chat widget: is anybody on this
workspace's team around right now? (MH-382, ported from fadymondy.com's
internal/presence, FM-139.)

fadymondy.com has one owner, so it keeps one timestamp. Moharrik has
workspaces, and a visitor on a customer's site is asking about THAT
workspace's team, so the heartbeat is per workspace: every request by a member
who can answer the inbox stamps their workspace (internal/server/guard.go).
Nothing is instrumented per client; if someone who can reply is using
Moharrik, the widget says so.

State lives in memory on purpose, as it does on fadymondy.com. A restart
resets it, which then reads as offline until the next request. That is honest
in the direction that matters: the failure is saying the team is away when it
is here, never the reverse.

What leaves this package is coarse: a state, never a timestamp and never who.
"Last active 02:41" tells a stranger when a team sleeps, and "Mona is online"
tells them who works there. The widget only needs a dot and a line.
*/
package presence

import (
	"sync"
	"time"
)

// How recent a heartbeat has to be for each state (fadymondy.com's windows).
const (
	OnlineWindow = 5 * time.Minute
	AwayWindow   = 45 * time.Minute
)

// State is the answer a visitor gets.
type State string

const (
	Online  State = "online"
	Away    State = "away"
	Offline State = "offline"
)

var (
	mu   sync.RWMutex
	seen = map[string]time.Time{} // workspace id -> last heartbeat
	now  = time.Now
)

// Touch records that someone on the workspace's team was active. Called on
// every qualifying request, so it must stay cheap: a lock and an assignment,
// no I/O. It is bounded by the number of workspaces, which it never exceeds.
func Touch(workspaceID string) {
	if workspaceID == "" {
		return
	}
	t := now()
	mu.Lock()
	seen[workspaceID] = t
	mu.Unlock()
}

// Current is the workspace's state. A workspace nobody has touched since the
// process started is offline.
func Current(workspaceID string) State {
	mu.RLock()
	at, ok := seen[workspaceID]
	mu.RUnlock()
	if !ok {
		return Offline
	}
	switch since := now().Sub(at); {
	case since <= OnlineWindow:
		return Online
	case since <= AwayWindow:
		return Away
	default:
		return Offline
	}
}

// Reset forgets every heartbeat. Tests only.
func Reset() {
	mu.Lock()
	seen = map[string]time.Time{}
	mu.Unlock()
}

// SetClock replaces the clock and returns a function restoring it. Tests only.
func SetClock(f func() time.Time) func() {
	mu.Lock()
	prev := now
	now = f
	mu.Unlock()
	return func() {
		mu.Lock()
		now = prev
		mu.Unlock()
	}
}
