package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
TestMemberWhatsAppNotifications covers MH-411 end to end, against a fake
Evolution server and with the notifier's clock in the test's hands:

  - a member verifies their own number with a code sent from the workspace's
    connection (wrong code refused, right code accepted);
  - a failing automation run and a new lead from a website inquiry (MH-390)
    each reach them on WhatsApp — in one batched message;
  - a burst of 5 events arrives as ONE message;
  - quiet hours hold messages until they end, then send what waited;
  - the daily cap holds the rest until the next day;
  - a member whose role cannot read an event never gets it queued.
*/
func TestMemberWhatsAppNotifications(t *testing.T) {
	t.Setenv("WHATSAPP_NOTIFY", "off") // the test drives the ticks itself
	sf := bootSurface(t)
	alice, evo := sf.alice, sf.evo

	// The fake clock: today at 12:00 UTC, moved by hand.
	y, m, d := time.Now().UTC().Date()
	clock := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	whatsapp.Clock = func() time.Time { return clock }
	defer func() { whatsapp.Clock = time.Now }()
	a := sf.app
	// One workspace's pass (what NotifyTick does for each): the test database
	// is shared across runs, and a global tick would flush other runs' rows.
	sctx := tenant.WithWorkspace(context.Background(), sf.ws)
	tick := func(at time.Time) {
		t.Helper()
		clock = at
		if err := whatsapp.Collect(sctx, a, sf.ws); err != nil {
			t.Fatal(err)
		}
		if _, err := whatsapp.FlushWorkspace(sctx, a, sf.ws); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = sf.sys.Exec(`DELETE FROM member_whatsapp WHERE workspace_id = $1`, sf.ws) })
	sentTo := func(number string) []string {
		var out []string
		for _, s := range evo.sends() {
			if s.Number == number {
				out = append(out, s.Text)
			}
		}
		return out
	}

	// Before the workspace has a number, verifying is refused with the reason.
	if code, b := alice.do("POST", "/api/w/notify/whatsapp/verify", map[string]any{"phone": "+20 17 0000 1111"}); code != 409 {
		t.Fatalf("verify before the workspace is linked: %d %s", code, b)
	}
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)

	// ---- verification ----------------------------------------------------------
	const me = "201700001111"
	st := alice.must("POST", "/api/w/notify/whatsapp/verify", map[string]any{"phone": "+20 17 0000 1111", "locale": "en"}, 200)
	if st["verified"] != false || st["code_pending"] != true || st["phone"] != me {
		t.Fatalf("after asking for a code: %v", st)
	}
	msgs := sentTo(me)
	if len(msgs) != 1 {
		t.Fatalf("the code should have gone to the member's number: %v", evo.sends())
	}
	code := regexp.MustCompile(`\b\d{6}\b`).FindString(msgs[0])
	if code == "" {
		t.Fatalf("no code in %q", msgs[0])
	}
	if c, _ := alice.do("POST", "/api/w/notify/whatsapp/verify", map[string]any{"phone": me}); c != 429 {
		t.Fatalf("a second code within a minute should be refused, got %d", c)
	}
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if c, _ := alice.do("POST", "/api/w/notify/whatsapp/confirm", map[string]any{"code": wrong}); c != 422 {
		t.Fatalf("a wrong code: %d", c)
	}
	st = alice.must("POST", "/api/w/notify/whatsapp/confirm", map[string]any{"code": code}, 200)
	if st["verified"] != true || st["enabled"] != true {
		t.Fatalf("after the right code: %v", st)
	}
	// Quiet hours 22:00–07:00, a 5-minute batch window, UTC, a cap of 3.
	alice.must("PUT", "/api/w/notify/whatsapp", map[string]any{
		"enabled": true, "kinds": []string{"automation_failed", "lead"}, "quiet_start": 22 * 60, "quiet_end": 7 * 60,
		"timezone": "UTC", "daily_cap": 3, "batch_minutes": 5, "locale": "en",
	}, 200)

	// A client-role member signed up too: they cannot read automation or
	// leads, so nothing is ever queued for them.
	bob := newClient(t, sf.srv.URL)
	bobEmail := "bob-" + rnd() + "@example.test"
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-8"}, 201)
	var bobID string
	if err := sf.sys.QueryRow(`SELECT id FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'client')`,
		`INSERT INTO member_whatsapp (workspace_id, user_id, phone, verified_at, kinds, quiet_start, quiet_end, batch_minutes)
		 VALUES ($1, $2, '201700002222', now(), '{automation_failed,lead,notification}', 0, 0, 0)`,
	} {
		if _, err := sf.sys.Exec(q, sf.ws, bobID); err != nil {
			t.Fatal(err)
		}
	}
	bob.ws = sf.ws
	if c, b := bob.do("PUT", "/api/w/notify/whatsapp", map[string]any{"enabled": true, "kinds": []string{"lead"}, "quiet_start": 0, "quiet_end": 0, "daily_cap": 5, "batch_minutes": 0}); c != 422 {
		t.Fatalf("a client choosing lead events: %d %s", c, b)
	}

	// The first tick sets the watermarks: nothing from before is sent.
	tick(clock)

	// ---- a failing automation run and a new lead arrive together ------------------
	var agentID string
	if err := sf.sys.QueryRow(`INSERT INTO browser_agents (workspace_id, name, token_hash, created_by) VALUES ($1, 'Office laptop', $2, '') RETURNING id`,
		sf.ws, "tok-"+rnd()).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	failRun := func(why string) {
		t.Helper()
		if _, err := sf.sys.Exec(`INSERT INTO automation_runs (workspace_id, agent_id, status, error, finished_at) VALUES ($1, $2, 'failed', $3, now())`,
			sf.ws, agentID, why); err != nil {
			t.Fatal(err)
		}
	}
	failRun("the supplier page asked for a captcha")
	submitLead(t, alice, sf.srv.URL, "Rana Adel")
	time.Sleep(20 * time.Millisecond)

	tick(clock) // collected, but gathering for the batch window
	if got := sentTo(me); len(got) != 1 {
		t.Fatalf("the batch window should hold the first events: %v", got)
	}
	tick(clock.Add(6 * time.Minute))
	got := sentTo(me)
	if len(got) != 2 {
		t.Fatalf("after the window, one message: %v", got)
	}
	if msg := got[1]; !strings.Contains(msg, "2 updates") || !strings.Contains(msg, "Automation failed: Office laptop") ||
		!strings.Contains(msg, "captcha") || !strings.Contains(msg, "New lead: Rana Adel") || !strings.Contains(msg, "/automation/runs/") {
		t.Fatalf("the batched message: %q", msg)
	}
	if n := len(sentTo("201700002222")); n != 0 {
		t.Fatalf("the client-role member was messaged %d times", n)
	}
	var queued int
	_ = sf.sys.QueryRow(`SELECT count(*) FROM member_whatsapp_events WHERE user_id = $1`, bobID).Scan(&queued)
	if queued != 0 {
		t.Fatalf("%d events were queued for a member who may not read them", queued)
	}

	// ---- a burst of 5 is one message ---------------------------------------------
	for i := 0; i < 5; i++ {
		failRun("step " + string(rune('A'+i)) + " failed")
	}
	time.Sleep(20 * time.Millisecond)
	t0 := clock
	tick(t0.Add(time.Minute)) // queued now; the window runs to t0+6m
	tick(t0.Add(2 * time.Minute))
	tick(t0.Add(5 * time.Minute))
	if n := len(sentTo(me)); n != 2 {
		t.Fatalf("the burst went out before the window closed: %d messages", n)
	}
	tick(t0.Add(6*time.Minute + 30*time.Second))
	got = sentTo(me)
	if len(got) != 3 || !strings.Contains(got[2], "5 updates") || strings.Count(got[2], "• ") != 5 {
		t.Fatalf("the burst of 5 should be ONE message listing 5: %q", got)
	}

	// ---- quiet hours hold messages until morning -----------------------------------
	night := time.Date(y, m, d, 23, 0, 0, 0, time.UTC)
	tick(night)
	failRun("the night run failed")
	time.Sleep(20 * time.Millisecond)
	tick(night)
	tick(night.Add(30 * time.Minute))
	tick(time.Date(y, m, d, 23, 59, 0, 0, time.UTC).Add(7 * time.Hour)) // 06:59 next day
	if n := len(sentTo(me)); n != 3 {
		t.Fatalf("a message went out in quiet hours: %d", n)
	}
	st = alice.must("GET", "/api/w/notify/whatsapp", nil, 200)
	if st["pending"].(float64) != 1 {
		t.Fatalf("the night event should be pending: %v", st)
	}
	morning := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(24*time.Hour + 7*time.Hour + time.Minute) // 07:01
	tick(morning)
	got = sentTo(me)
	if len(got) != 4 || !strings.Contains(got[3], "the night run failed") {
		t.Fatalf("at the end of quiet hours the held message should go: %q", got)
	}

	// ---- the daily cap --------------------------------------------------------------
	// Cap 3; today (the new day) one has gone. Two more go, the third waits.
	for i := 1; i <= 3; i++ {
		failRun("morning failure " + string(rune('0'+i)))
		time.Sleep(20 * time.Millisecond)
		at := morning.Add(time.Duration(i) * 10 * time.Minute)
		tick(at)
		tick(at.Add(6 * time.Minute))
	}
	if n := len(sentTo(me)); n != 6 {
		t.Fatalf("with a cap of 3 a day, 3 messages today: got %d in total", n)
	}
	st = alice.must("GET", "/api/w/notify/whatsapp", nil, 200)
	if st["sent_today"].(float64) != 3 || st["pending"].(float64) != 1 {
		t.Fatalf("status at the cap: %v", st)
	}
	failRun("over the cap")
	time.Sleep(20 * time.Millisecond)
	tick(morning.Add(2 * time.Hour))
	tick(morning.Add(3 * time.Hour))
	if n := len(sentTo(me)); n != 6 {
		t.Fatalf("a message went past the daily cap: %d", n)
	}
	// The next day, after quiet hours, it goes.
	tick(morning.Add(24 * time.Hour))
	if got := sentTo(me); len(got) != 7 || !strings.Contains(got[6], "over the cap") || !strings.Contains(got[6], "morning failure 3") {
		t.Fatalf("the capped event should go the next day: %q", got)
	}

	// The log the settings page shows.
	st = alice.must("GET", "/api/w/notify/whatsapp", nil, 200)
	if len(arr(st["recent"])) == 0 || obj(arr(st["recent"])[0])["ok"] != true {
		t.Fatalf("the send log: %v", st["recent"])
	}
	// Removing the number stops everything and forgets it.
	alice.must("DELETE", "/api/w/notify/whatsapp", nil, 204)
	if st := alice.must("GET", "/api/w/notify/whatsapp", nil, 200); st["phone"] != "" || st["verified"] != false {
		t.Fatalf("after removing: %v", st)
	}
}

// submitLead files a lead the way a customer's website does: an inquiry form,
// then the public route.
func submitLead(t *testing.T, c *client, base, name string) {
	t.Helper()
	form := c.must("POST", "/api/w/subscribe-forms", map[string]any{"kind": "inquiry", "name": "Site " + rnd(), "allowed_origins": []string{"https://shop.test"}}, 201)
	key := form["form"].(map[string]any)["public_key"].(string)
	req, _ := http.NewRequest("POST", base+"/api/inquiry/"+key, bytes.NewReader([]byte(`{"email":"lead-`+rnd()+`@mail.example","name":"`+name+`","message":"We need a quote"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://shop.test")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("inquiry: %d %s", res.StatusCode, b)
	}
}
