package server

/*
TestGA4Conversions (MH-449): a workspace's funnel reaches its own GA4 property
through the Measurement Protocol, end to end through the real stack, against
the fake Google (internal/google/googletest) — never the real one.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run GA4Conversions -v
*/

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/conversions"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/leads"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestGA4Conversions(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "ga4-conversions-test-secret-ga4-conversions-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("NOTIFY_COLLECTOR", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")
	// The sender's ticker is off: the test drains the outbox itself, so it
	// knows exactly when each event was sent.
	t.Setenv("GA4_SENDER", "off")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1,::1")
	t.Setenv("CLIENT_IP_HEADER", "")
	fake := googletest.New()
	defer fake.Close()
	t.Setenv("APP_URL", "http://127.0.0.1")
	t.Setenv("GOOGLE_API_BASE", fake.URL)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", fake.ClientID)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", fake.ClientSecret)

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()
	t.Setenv("APP_URL", srv.URL) // the Google callback lands here
	defer leads.SetLimiter(inbox.NewMemoryLimiter(), leads.DefaultInquiryLimits())()
	drain := func() int { return conversions.Drain(context.Background(), a, 50) }
	// The sender drains every workspace's outbox. Whatever an earlier run of this
	// test left waiting in this database is not this run's to send.
	settleOld := func() {
		_, _ = a.SysDB.Exec(`UPDATE ga4_conversion_events SET status = 'skipped', error = 'left by an earlier test run' WHERE status = 'pending'`)
	}
	settleOld()
	defer settleOld()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "ga4-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	ws := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "GA4 " + rnd()}, 201)["workspace"].(map[string]any)
	wsID := ws["id"].(string)
	alice.ws = wsID
	const apiSecret = "Mp-S3cret-" + "never-shown-4f2a"
	const site = "https://acme-site.test"

	t.Run("an admin's page; a member is refused", func(t *testing.T) {
		p := alice.must("GET", "/api/w/ga4-conversions", nil, 200)
		st := p["settings"].(map[string]any)
		if st["measurement_id"] != "" || st["api_secret_set"] != false || st["events_enabled"] != false || len(p["events"].([]any)) != 4 {
			t.Fatalf("a fresh workspace's page: %v", p)
		}
		bob := newClient(t, srv.URL)
		email := "ga4-member-" + rnd() + "@example.test"
		bob.must("POST", "/api/auth/register", map[string]string{"email": email, "password": "correct-horse-2"}, 201)
		if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status)
			SELECT $1, id, 'member', 'active' FROM users WHERE email = $2`, wsID, email); err != nil {
			t.Fatal(err)
		}
		bob.ws = wsID
		for _, r := range [][2]string{{"GET", "/api/w/ga4-conversions"}, {"PATCH", "/api/w/ga4-conversions"}, {"POST", "/api/w/ga4-conversions/test"}, {"GET", "/api/w/ga4-conversions/streams"}} {
			if code, b := bob.do(r[0], r[1], map[string]any{}); code != http.StatusForbidden {
				t.Errorf("a member: %s %s -> %d %s", r[0], r[1], code, b)
			}
		}
	})

	t.Run("switched on only when it can work", func(t *testing.T) {
		for _, c := range []struct {
			body map[string]any
			want string
		}{
			{map[string]any{"events_enabled": true}, "needs both"},
			{map[string]any{"tag_in_embeds": true}, "needs the measurement id"},
			{map[string]any{"measurement_id": "UA-12345-1"}, "G-ABC123XYZ"},
			{map[string]any{"measurement_id": "G-ABCD1234", "events_enabled": true}, "needs both"},
			{map[string]any{"api_secret": "has a space"}, "without spaces"},
		} {
			if code, b := alice.do("PATCH", "/api/w/ga4-conversions", c.body); code != 422 || !strings.Contains(string(b), c.want) {
				t.Errorf("PATCH %v: %d %s", c.body, code, b)
			}
		}
	})

	// The measurement id is read from the connected Google account's GA4 property.
	var mid string
	t.Run("the measurement id comes from the connected GA4 property", func(t *testing.T) {
		if s := alice.must("GET", "/api/w/ga4-conversions/streams", nil, 200); len(s["streams"].([]any)) != 0 {
			t.Fatalf("streams before any Google account: %v", s)
		}
		fake.Set(func(s *googletest.Server) {
			s.Account = googletest.Account{Sub: "ga4-sub-" + rnd(), Email: "ga4-" + rnd() + "@example.test"}
		})
		consentURL := alice.must("GET", "/api/w/google/connect?product=analytics", nil, 200)["url"].(string)
		back, err := fake.Consent(consentURL)
		if err != nil {
			t.Fatal(err)
		}
		noFollow := &http.Client{Jar: alice.http.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := noFollow.Get(back)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if loc := res.Header.Get("Location"); res.StatusCode != http.StatusFound || !strings.Contains(loc, "google=connected") {
			t.Fatalf("google connect: %d %s", res.StatusCode, loc)
		}
		google.WaitBackground()
		if r := alice.must("GET", "/api/w/google/resources", nil, 200)["resources"].([]any); len(r) != 1 || r[0].(map[string]any)["enabled"] != true {
			t.Fatalf("the GA4 property was not discovered and enabled: %v", r)
		}
		s := alice.must("GET", "/api/w/ga4-conversions/streams", nil, 200)
		list := s["streams"].([]any)
		if len(list) != 1 {
			t.Fatalf("streams: %v", s)
		}
		st := list[0].(map[string]any)
		mid = st["measurement_id"].(string)
		if mid != googletest.FakeMeasurementID(st["property"].(string)) {
			t.Fatalf("stream: %v", st)
		}
	})

	t.Run("saved, and the secret is never handed back", func(t *testing.T) {
		p := alice.must("PATCH", "/api/w/ga4-conversions", map[string]any{
			"measurement_id": strings.ToLower(mid), "api_secret": apiSecret, "events_enabled": true, "tag_in_embeds": true, "debug_mode": true,
		}, 200)
		b, _ := json.Marshal(p)
		st := p["settings"].(map[string]any)
		if st["measurement_id"] != mid || st["api_secret_set"] != true || st["events_enabled"] != true || strings.Contains(string(b), apiSecret) {
			t.Fatalf("after saving: %s", b)
		}
		var sealed string
		_ = a.SysDB.QueryRow(`SELECT api_secret_sealed FROM ga4_conversion_settings WHERE workspace_id = $1`, wsID).Scan(&sealed)
		if sealed == "" || strings.Contains(sealed, apiSecret) {
			t.Fatalf("the secret is not sealed at rest")
		}
		// Keeping the secret: a PATCH without api_secret leaves it as it is.
		if p := alice.must("PATCH", "/api/w/ga4-conversions", map[string]any{"debug_mode": true}, 200); p["settings"].(map[string]any)["api_secret_set"] != true {
			t.Fatalf("a PATCH without the secret dropped it: %v", p)
		}
		// The audit log records the change with the secret redacted.
		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND entity_type = 'ga4_conversion_settings' AND changes::text LIKE '%' || $2 || '%'`,
			wsID, apiSecret).Scan(&n)
		if n != 0 {
			t.Fatal("the audit log recorded the API secret")
		}
	})

	var formKey string
	t.Run("the embed snippets carry the GA4 tag with Consent Mode", func(t *testing.T) {
		f := alice.must("POST", "/api/w/subscribe-forms", map[string]any{"kind": "inquiry", "name": "Contact", "allowed_origins": []string{site}}, 201)["form"].(map[string]any)
		formKey = f["public_key"].(string)
		embed := f["embed"].(string)
		if !strings.Contains(embed, `gtag("config", "`+mid+`")`) || !strings.Contains(embed, `analytics_storage: "denied"`) || !strings.Contains(embed, "ga_client_id") {
			t.Fatalf("inquiry embed: %s", embed)
		}
		forms := alice.must("GET", "/api/w/subscribe-forms", nil, 200)["forms"].([]any)
		if !strings.Contains(forms[0].(map[string]any)["embed"].(string), "googletagmanager.com/gtag/js?id="+mid) {
			t.Fatal("the listed form's embed has no tag")
		}
	})

	inquire := func(t *testing.T, ip string, extra map[string]any) string {
		t.Helper()
		email := "visitor-" + rnd() + "@acme.test"
		body := map[string]any{"name": "Visitor", "email": email, "message": "A quote, please.", "utm_source": "newsletter"}
		for k, v := range extra {
			body[k] = v
		}
		if r := postInquiry(t, srv.URL, formKey, site, ip, body); r.code != 200 {
			t.Fatalf("inquiry: %d %s", r.code, r.raw)
		}
		var id string
		if err := a.SysDB.QueryRow(`SELECT id FROM leads WHERE workspace_id = $1 AND email = $2`, wsID, email).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	hitsFor := func(name string) []googletest.MPHit {
		var out []googletest.MPHit
		for _, h := range fake.MPHits() {
			if h.Debug || h.MeasurementID != mid {
				continue
			}
			for _, e := range h.Body["events"].([]any) {
				if e.(map[string]any)["name"] == name {
					out = append(out, h)
				}
			}
		}
		return out
	}
	params := func(h googletest.MPHit) map[string]any {
		return h.Body["events"].([]any)[0].(map[string]any)["params"].(map[string]any)
	}

	var leadID string
	t.Run("a lead through the funnel: created, qualified, converted, won", func(t *testing.T) {
		leadID = inquire(t, "203.0.113.1", map[string]any{"ga_client_id": "GA1.1.1234567890.1712345678", "budget": "5000",
			"ga_ad_user_data": "granted", "ga_ad_personalization": "denied"})
		if n := drain(); n < 1 {
			t.Fatalf("drained %d, want the generate_lead", n)
		}
		gen := hitsFor("generate_lead")
		if len(gen) != 1 {
			t.Fatalf("generate_lead hits: %d", len(gen))
		}
		h := gen[0]
		consent, _ := h.Body["consent"].(map[string]any)
		if h.MeasurementID != mid || h.APISecret != apiSecret || h.Body["client_id"] != "1234567890.1712345678" ||
			consent["ad_user_data"] != "GRANTED" || consent["ad_personalization"] != "DENIED" ||
			params(h)["lead_source"] != "newsletter" || params(h)["debug_mode"] != true || h.Body["timestamp_micros"] == nil {
			t.Fatalf("generate_lead: %+v", h)
		}
		for _, k := range []string{"email", "name", "phone", "company", "message"} {
			if _, ok := params(h)[k]; ok {
				t.Errorf("personal data %q sent to GA4", k)
			}
		}
		if b, _ := json.Marshal(h.Body); strings.Contains(string(b), "@acme.test") || strings.Contains(string(b), "Visitor") {
			t.Errorf("personal data in the payload: %s", b)
		}

		// Qualified twice (away and back) is ONE qualify_lead.
		alice.must("PATCH", "/api/w/leads/"+leadID, map[string]any{"status": "qualified"}, 200)
		alice.must("PATCH", "/api/w/leads/"+leadID, map[string]any{"status": "contacted"}, 200)
		alice.must("PATCH", "/api/w/leads/"+leadID, map[string]any{"status": "qualified"}, 200)
		conv := alice.must("POST", "/api/w/leads/"+leadID+"/convert", map[string]any{}, 200)
		dealID := conv["deal_id"].(string)
		drain()
		if q := hitsFor("qualify_lead"); len(q) != 1 || q[0].Body["client_id"] != "1234567890.1712345678" {
			t.Fatalf("qualify_lead hits: %+v", q)
		}
		c := hitsFor("close_convert_lead")
		if len(c) != 1 || params(c[0])["value"] != 5000.0 || params(c[0])["currency"] != "USD" {
			t.Fatalf("close_convert_lead: %+v", c)
		}

		// Won: the deal enters the pipeline's "won" stage. Its visitor is the lead's.
		stages := alice.must("GET", "/api/w/pipeline/stages", nil, 200)["stages"].([]any)
		var won, open string
		for _, s := range stages {
			m := s.(map[string]any)
			if m["kind"] == "won" {
				won = m["id"].(string)
			} else if m["kind"] == "open" && open == "" {
				open = m["id"].(string)
			}
		}
		alice.must("PATCH", "/api/w/deals/"+dealID, map[string]any{"stage_id": won}, 200)
		// Out of won and back in is not a second win.
		alice.must("PATCH", "/api/w/deals/"+dealID, map[string]any{"stage_id": open}, 200)
		alice.must("PATCH", "/api/w/deals/"+dealID, map[string]any{"stage_id": won}, 200)
		drain()
		w := hitsFor("deal_won")
		if len(w) != 1 || w[0].Body["client_id"] != "1234567890.1712345678" || params(w[0])["value"] != 5000.0 || params(w[0])["currency"] != "USD" {
			t.Fatalf("deal_won: %+v", w)
		}
		// A deal won that no lead brought in is counted too, under its own pseudonymous id.
		plain := alice.must("POST", "/api/w/deals", map[string]any{"title": "Walk-in", "value_minor": 12345, "stage_id": won}, 201)["deal"].(map[string]any)["id"].(string)
		drain()
		if w := hitsFor("deal_won"); len(w) != 2 || w[1].Body["client_id"] == "1234567890.1712345678" || params(w[1])["value"] != 123.45 {
			t.Fatalf("a walk-in deal's win: %+v (deal %s)", w, plain)
		}

		p := alice.must("GET", "/api/w/ga4-conversions", nil, 200)
		sent := 0
		for _, e := range p["recent"].([]any) {
			m := e.(map[string]any)
			if m["status"] == "sent" {
				sent++
			}
			if m["subject_id"] == leadID && m["attributed"] != true {
				t.Errorf("the lead's events should be attributed: %v", m)
			}
		}
		if sent != 5 {
			t.Errorf("recent shows %d sent, want 5: %v", sent, p["recent"])
		}
	})

	t.Run("no GA client id: counted, not attributed, consent denied", func(t *testing.T) {
		id := inquire(t, "203.0.113.2", nil)
		drain()
		gen := hitsFor("generate_lead")
		h := gen[len(gen)-1]
		consent, _ := h.Body["consent"].(map[string]any)
		cid, _ := h.Body["client_id"].(string)
		if !conversions.ValidClientID(cid) || consent["ad_user_data"] != "DENIED" || consent["ad_personalization"] != "DENIED" {
			t.Fatalf("an unattributed lead: %+v", h)
		}
		var attributed bool
		_ = a.SysDB.QueryRow(`SELECT attributed FROM ga4_conversion_events WHERE subject_id = $1`, id).Scan(&attributed)
		if attributed {
			t.Error("an event without the visitor's client id was marked attributed")
		}
	})

	secretFree := func(t *testing.T) {
		t.Helper()
		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM ga4_conversion_events WHERE error LIKE '%' || $1 || '%'`, apiSecret).Scan(&n)
		if n != 0 {
			t.Fatal("an event's error quotes the API secret")
		}
		if _, b := alice.do("GET", "/api/w/ga4-conversions", nil); strings.Contains(string(b), apiSecret) {
			t.Fatal("the page quotes the API secret")
		}
	}

	t.Run("Google down: kept and retried; refused: failed; neither quotes the secret", func(t *testing.T) {
		fake.MPStatus(503)
		id := inquire(t, "203.0.113.3", nil)
		drain()
		var status, errText string
		var attempts int
		_ = a.SysDB.QueryRow(`SELECT status, error, attempts FROM ga4_conversion_events WHERE subject_id = $1`, id).Scan(&status, &errText, &attempts)
		if status != "pending" || attempts != 1 || !strings.Contains(errText, "503") {
			t.Fatalf("after a 503: %s %d %q", status, attempts, errText)
		}
		// Refused outright: failed, not retried.
		fake.MPStatus(400)
		_, _ = a.SysDB.Exec(`UPDATE ga4_conversion_events SET next_attempt_at = now() WHERE subject_id = $1`, id)
		drain()
		_ = a.SysDB.QueryRow(`SELECT status, error FROM ga4_conversion_events WHERE subject_id = $1`, id).Scan(&status, &errText)
		if status != "failed" || !strings.Contains(errText, "HTTP 400") {
			t.Fatalf("after a 400: %s %q", status, errText)
		}
		fake.MPStatus(0)

		// Unreachable: the transport's error names the URL, which carries the secret.
		t.Setenv("GOOGLE_API_BASE", "http://127.0.0.1:1")
		id = inquire(t, "203.0.113.4", nil)
		drain()
		_ = a.SysDB.QueryRow(`SELECT status, error FROM ga4_conversion_events WHERE subject_id = $1`, id).Scan(&status, &errText)
		if status != "pending" || !strings.Contains(errText, "could not reach Google Analytics") {
			t.Fatalf("unreachable: %s %q", status, errText)
		}
		t.Setenv("GOOGLE_API_BASE", fake.URL)
		secretFree(t)
	})

	t.Run("the test event: validated, then sent for DebugView", func(t *testing.T) {
		before := len(fake.MPHits())
		r := alice.must("POST", "/api/w/ga4-conversions/test", nil, 200)
		if r["valid"] != true || r["sent"] != true {
			t.Fatalf("test: %v", r)
		}
		hits := fake.MPHits()[before:]
		if len(hits) != 2 || !hits[0].Debug || hits[1].Debug || hits[1].Body["events"].([]any)[0].(map[string]any)["name"] != conversions.EventTest {
			t.Fatalf("test hits: %+v", hits)
		}
		if b, _ := json.Marshal(r); strings.Contains(string(b), apiSecret) {
			t.Fatal("the test answer quotes the secret")
		}
	})

	t.Run("switched off: nothing new is recorded, and what was waiting is skipped", func(t *testing.T) {
		id := inquire(t, "203.0.113.5", nil) // recorded while on …
		alice.must("PATCH", "/api/w/ga4-conversions", map[string]any{"events_enabled": false, "tag_in_embeds": false}, 200)
		drain() // … and skipped once off
		var status string
		_ = a.SysDB.QueryRow(`SELECT status FROM ga4_conversion_events WHERE subject_id = $1`, id).Scan(&status)
		if status != "skipped" {
			t.Fatalf("an event waiting when sending was switched off: %s", status)
		}
		id = inquire(t, "203.0.113.6", nil)
		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM ga4_conversion_events WHERE subject_id = $1`, id).Scan(&n)
		if n != 0 {
			t.Fatal("an event was recorded while sending is off")
		}
		forms := alice.must("GET", "/api/w/subscribe-forms", nil, 200)["forms"].([]any)
		if strings.Contains(forms[0].(map[string]any)["embed"].(string), "gtag") {
			t.Fatal("the tag stayed in the embed after it was switched off")
		}
		secretFree(t)
	})
}
