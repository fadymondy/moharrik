package automation

import (
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, layout, value, zone string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Skipf("timezone database not available: %v", err)
	}
	got, err := time.ParseInLocation(layout, value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestNextAfterUsesTheSchedulesOwnTimezone(t *testing.T) {
	// 07:00 in Cairo is 05:00 UTC in winter. A server that computed this in UTC
	// would run it two hours early, every day, and nobody would notice until
	// someone looked at a timestamp.
	after := mustTime(t, "2006-01-02 15:04", "2026-01-10 03:00", "UTC")
	next, err := NextAfter("0 7 * * *", "Africa/Cairo", after)
	if err != nil {
		t.Fatal(err)
	}
	if next.UTC().Format("2006-01-02 15:04") != "2026-01-10 05:00" {
		t.Errorf("next = %s, want 05:00 UTC (07:00 in Cairo)", next.UTC())
	}
}

func TestNextAfterSurvivesTheClocksChanging(t *testing.T) {
	// Cairo moved to summer time on 2026-04-24 (last Friday of April). The same
	// wall-clock schedule must still be 07:00 LOCAL on both sides of it, which
	// is a different UTC hour — the whole reason the timezone is stored.
	winter, err := NextAfter("0 7 * * *", "Africa/Cairo", mustTime(t, "2006-01-02 15:04", "2026-04-20 12:00", "UTC"))
	if err != nil {
		t.Fatal(err)
	}
	summer, err := NextAfter("0 7 * * *", "Africa/Cairo", mustTime(t, "2006-01-02 15:04", "2026-05-20 12:00", "UTC"))
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Africa/Cairo")
	if winter.In(loc).Format("15:04") != "07:00" || summer.In(loc).Format("15:04") != "07:00" {
		t.Errorf("local times = %s and %s, both should be 07:00", winter.In(loc).Format("15:04"), summer.In(loc).Format("15:04"))
	}
	if winter.UTC().Hour() == summer.UTC().Hour() {
		t.Errorf("both computed to %02d:00 UTC — the timezone is being ignored", winter.UTC().Hour())
	}
}

func TestNextAfterRefusesWhatCannotBeScheduled(t *testing.T) {
	now := time.Now()
	for name, tc := range map[string]struct{ expr, zone string }{
		"empty expression": {"", "UTC"},
		"not cron at all":  {"every morning", "UTC"},
		"too many fields":  {"0 0 7 * * *", "UTC"}, // a seconds field: not accepted
		"unknown timezone": {"0 7 * * *", "Mars/Olympus"},
		"nonsense minute":  {"99 7 * * *", "UTC"},
	} {
		if _, err := NextAfter(tc.expr, tc.zone, now); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}

	// Descriptors are accepted, because people write them.
	if _, err := NextAfter("@daily", "UTC", now); err != nil {
		t.Errorf("@daily should be accepted: %v", err)
	}
}

func TestNextAfterAlwaysMovesForward(t *testing.T) {
	// A schedule that returned "now" would fire in a loop, every tick.
	now := mustTime(t, "2006-01-02 15:04", "2026-03-01 07:00", "UTC")
	next, err := NextAfter("0 7 * * *", "UTC", now)
	if err != nil {
		t.Fatal(err)
	}
	if !next.After(now) {
		t.Errorf("next = %s is not after %s — the schedule would fire in a loop", next, now)
	}
}

func TestValidateScheduleChecksTheCatchUpPolicy(t *testing.T) {
	if err := ValidateSchedule("0 7 * * *", "UTC", CatchUpSkip); err != nil {
		t.Errorf("skip should be valid: %v", err)
	}
	if err := ValidateSchedule("0 7 * * *", "UTC", CatchUpOnce); err != nil {
		t.Errorf("once should be valid: %v", err)
	}
	err := ValidateSchedule("0 7 * * *", "UTC", "whenever")
	if err == nil {
		t.Fatal("an unknown catch-up policy should be refused")
	}
	// The message has to name the choices, or someone has to read the source.
	if !strings.Contains(err.Error(), CatchUpSkip) || !strings.Contains(err.Error(), CatchUpOnce) {
		t.Errorf("unhelpful message: %q", err.Error())
	}
	if err := ValidateSchedule("nope", "UTC", CatchUpSkip); err == nil {
		t.Error("a bad expression should be refused before it is saved")
	}
}
