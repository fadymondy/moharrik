package presence

import (
	"testing"
	"time"
)

func TestPresenceWindows(t *testing.T) {
	Reset()
	base := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	clock := base
	defer SetClock(func() time.Time { return clock })()

	if got := Current("ws-a"); got != Offline {
		t.Fatalf("never touched: got %s, want offline", got)
	}
	Touch("ws-a")
	for _, c := range []struct {
		after time.Duration
		want  State
	}{
		{0, Online},
		{OnlineWindow, Online},
		{OnlineWindow + time.Second, Away},
		{AwayWindow, Away},
		{AwayWindow + time.Second, Offline},
	} {
		clock = base.Add(c.after)
		if got := Current("ws-a"); got != c.want {
			t.Errorf("%s after the heartbeat: got %s, want %s", c.after, got, c.want)
		}
	}
	// One workspace's team says nothing about another's.
	clock = base
	if got := Current("ws-b"); got != Offline {
		t.Errorf("ws-b was never touched but reads %s", got)
	}
	Touch("")
	if got := Current(""); got != Offline {
		t.Errorf("the empty workspace must never be online, got %s", got)
	}
}
