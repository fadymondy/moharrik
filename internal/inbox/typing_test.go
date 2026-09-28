package inbox

import "testing"

// MH-660: presenceOf normalizes WhatsApp/Baileys presence names onto what the
// UI shows; everything else (paused, available, unavailable, unknown) clears
// the indicator.
func TestPresenceOf(t *testing.T) {
	cases := map[string]string{
		"composing":    "composing",
		"COMPOSING":    "composing",
		" recording ":  "recording",
		"paused":       "",
		"available":    "",
		"unavailable":  "",
		"":             "",
		"garbage-word": "",
	}
	for in, want := range cases {
		if got := presenceOf(in); got != want {
			t.Errorf("presenceOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// The composer sends "composing" on every keystroke tick; this must be
// throttled so it isn't a sendPresence call per character, but "paused" (stop
// typing or send) must never wait behind the throttle.
func TestShouldSendComposingThrottles(t *testing.T) {
	convID := "throttle-test-conv"
	t.Cleanup(func() { clearPresenceThrottle(convID) })

	if !shouldSendComposing(convID) {
		t.Fatal("the first composing signal for a conversation must go through")
	}
	if shouldSendComposing(convID) {
		t.Error("a second composing signal inside the throttle window must be suppressed")
	}
	clearPresenceThrottle(convID)
	if !shouldSendComposing(convID) {
		t.Error("clearing the throttle (on pause/send) must let the next composing through immediately")
	}
}
