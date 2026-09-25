package recency

import (
	"testing"
	"time"
)

func TestTooOld(t *testing.T) {
	connected := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		sent time.Time
		want bool
	}{
		{"years before", time.Date(2021, 1, 25, 0, 0, 0, 0, time.UTC), true},
		{"just past the grace", connected.Add(-Grace - time.Minute), true},
		{"within the grace", connected.Add(-time.Hour), false},
		{"after connecting", connected.Add(time.Hour), false},
		{"no date", time.Time{}, false},
	}
	for _, c := range cases {
		if got := TooOld(c.sent, connected); got != c.want {
			t.Errorf("%s: TooOld = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCutoffZeroConnectedIsNow(t *testing.T) {
	if got := Cutoff(time.Time{}); time.Since(got) < Grace-time.Minute || time.Since(got) > Grace+time.Minute {
		t.Fatalf("Cutoff(zero) = %v, want about now minus the grace", got)
	}
}
