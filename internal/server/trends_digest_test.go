package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/trends"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
TestTrendsDigest covers MH-389 on the real stack, the mail server and the
Evolution server faked:

  - a member opts in to a daily digest in Arabic; it goes at their hour, once
    per day, to their own email, with the top open stories best first, each
    with its reason in Arabic and a link to the story;
  - before the hour nothing goes; a second tick the same day sends nothing;
  - a weekly digest waits for its day;
  - with no open stories nothing is sent and the page says so ("empty");
  - "trends" / "ترندات" from a member's verified WhatsApp number is answered
    with the top stories in their language and is NOT filed as a
    conversation; the same word from a customer's number is filed;
  - a member without trends:read is told so, and gets no stories.
*/
func TestTrendsDigest(t *testing.T) {
	t.Setenv("WHATSAPP_NOTIFY", "off")
	sf := bootSurface(t)
	alice, a, ws := sf.alice, sf.app, sf.ws
	ctx := context.Background()
	sctx := tenant.WithWorkspace(ctx, ws)
	var aliceID, aliceEmail string
	if err := a.SysDB.QueryRow(`SELECT wm.user_id, u.email FROM workspace_members wm JOIN users u ON u.id = wm.user_id WHERE wm.workspace_id = $1`, ws).Scan(&aliceID, &aliceEmail); err != nil {
		t.Fatal(err)
	}
	sink := startRuleSMTPSink(t)

	// The page's view before anything is set: off, no email service yet.
	st := alice.must("GET", "/api/w/notify/digest?locale=ar", nil, 200)
	if st["frequency"] != "off" || st["email_ready"] != false || st["email"] != aliceEmail || st["locale"] != "ar" {
		t.Fatalf("fresh digest state: %v", st)
	}
	if code, _ := alice.do("POST", "/api/w/notify/digest/test", map[string]any{}); code != 409 {
		t.Fatalf("a test with no email service should say so: %d", code)
	}
	alice.must("POST", "/api/w/smtp-services", map[string]any{"name": "Sink", "host": "127.0.0.1", "port": sink.port,
		"from_email": "digest@mail.example", "tls_mode": "none"}, 201)
	if code, _ := alice.do("PUT", "/api/w/notify/digest", map[string]any{"frequency": "daily", "hour": 8, "weekday": 0, "timezone": "Mars/Olympus", "locale": "ar", "top": 3}); code != 422 {
		t.Fatalf("an unknown time zone: %d", code)
	}

	// Stories: four open ones of different scores, one reviewed, one old.
	now := time.Now().UTC()
	story := func(title string, score float64, reasons string, seen time.Time, reviewed bool) {
		t.Helper()
		q := `INSERT INTO trend_clusters (workspace_id, title, summary, score, reasons, last_seen_at, reviewed_at) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)`
		var rev any
		if reviewed {
			rev = now
		}
		if _, err := a.SysDB.Exec(q, ws, title, title+" — what happened.", score, reasons, seen, rev); err != nil {
			t.Fatal(err)
		}
	}
	story("Open models reach parity", 82, `[{"kind":"sources","points":65,"label":"Carried by 4 sources","count":4}]`, now.Add(-2*time.Hour), false)
	story("Coffee prices climb", 64, `[{"kind":"velocity","points":30,"label":"Rising","signal":"burst","count":5}]`, now.Add(-3*time.Hour), false)
	story("Specialty cafes in Cairo", 40, `[{"kind":"relevance","points":20,"label":"Matches","detail":["cafes","Cairo"]}]`, now.Add(-4*time.Hour), false)
	story("A small story", 12, `[{"kind":"sources","points":5,"label":"Carried by 1 source","count":1}]`, now.Add(-5*time.Hour), false)
	story("Already reviewed", 99, `[]`, now.Add(-1*time.Hour), true)
	story("Last month's news", 95, `[]`, now.Add(-40*24*time.Hour), false)

	// ---- the daily digest, in Arabic, at 08:00 Cairo ----------------------------------
	loc, _ := time.LoadLocation("Africa/Cairo")
	day := time.Date(now.Year(), now.Month(), now.Day(), 7, 30, 0, 0, loc)
	trends.DigestClock = func() time.Time { return day }
	defer func() { trends.DigestClock = time.Now }()
	alice.must("PUT", "/api/w/notify/digest", map[string]any{"frequency": "daily", "hour": 8, "weekday": 0, "timezone": "Africa/Cairo", "locale": "ar", "top": 3}, 200)
	st = alice.must("GET", "/api/w/notify/digest", nil, 200)
	if st["frequency"] != "daily" || st["email_ready"] != true || st["next_at"] == nil {
		t.Fatalf("after opting in: %v", st)
	}
	// Before the hour: nothing.
	trends.DigestTick(ctx, a)
	if n := len(sink.mails()); n != 0 {
		t.Fatalf("a digest went before its hour: %d", n)
	}
	day = day.Add(40 * time.Minute) // 08:10
	trends.DigestTick(ctx, a)
	trends.DigestTick(ctx, a) // the same day again: nothing more
	mails := sink.mails()
	if len(mails) != 1 {
		t.Fatalf("one digest a day, got %d", len(mails))
	}
	m := mails[0]
	if !strings.HasPrefix(m, aliceEmail+"\n") {
		t.Fatalf("the digest went to the wrong address: %.80s", m)
	}
	body := strings.NewReplacer(isolateOpen, "", isolateClose, "").Replace(decodeQP(m))
	for _, want := range []string{"Open models reach parity", "Coffee prices climb", "Specialty cafes in Cairo", "4 مصادر", "صاعد: 5 تقارير خلال 6 ساعات", "يهمك: يطابق cafes، Cairo", "/ar/w/", "/trends/"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Arabic digest is missing %q", want)
		}
	}
	for _, not := range []string{"A small story", "Already reviewed", "Last month's news"} {
		if strings.Contains(body, not) {
			t.Errorf("the digest should not carry %q (top 3, open, recent)", not)
		}
	}
	if strings.Index(body, "Open models") > strings.Index(body, "Coffee prices") {
		t.Error("the stories should be best first")
	}
	st = alice.must("GET", "/api/w/notify/digest", nil, 200)
	if st["last_status"] != "sent" || st["last_sent_at"] == nil {
		t.Fatalf("after the digest: %v", st)
	}

	// ---- weekly waits for its day -----------------------------------------------------
	other := (int(day.Weekday()) + 1) % 7
	alice.must("PUT", "/api/w/notify/digest", map[string]any{"frequency": "weekly", "hour": 8, "weekday": other, "timezone": "Africa/Cairo", "locale": "en", "top": 5}, 200)
	day = day.Add(time.Hour)
	trends.DigestTick(ctx, a)
	if n := len(sink.mails()); n != 1 {
		t.Fatalf("a weekly digest went on the wrong day: %d", n)
	}
	day = day.Add(24 * time.Hour)
	trends.DigestTick(ctx, a)
	if n := len(sink.mails()); n != 2 {
		t.Fatalf("the weekly digest did not go on its day: %d", n)
	}
	weekly := strings.NewReplacer(isolateOpen, "", isolateClose, "").Replace(decodeQP(sink.mails()[1]))
	if !strings.Contains(weekly, "This week's trends") || !strings.Contains(weekly, "A small story") || !strings.Contains(weekly, "4 sources") {
		t.Fatalf("the English weekly digest: %.600s", weekly)
	}

	// ---- the test button, and nothing to send ----------------------------------------
	res := alice.must("POST", "/api/w/notify/digest/test", map[string]any{}, 200)
	if res["status"] != "sent" || res["topics"].(float64) != 4 {
		t.Fatalf("test digest: %v", res)
	}
	if code, _ := alice.do("POST", "/api/w/notify/digest/test", map[string]any{}); code != 429 {
		t.Fatalf("a second test within a minute: %d", code)
	}
	if _, err := a.SysDB.Exec(`UPDATE trend_clusters SET reviewed_at = now() WHERE workspace_id = $1`, ws); err != nil {
		t.Fatal(err)
	}
	day = day.Add(7 * 24 * time.Hour)
	trends.DigestTick(ctx, a)
	if n := len(sink.mails()); n != 3 {
		t.Fatalf("with no open stories nothing should be sent: %d mails", n)
	}
	if st := alice.must("GET", "/api/w/notify/digest", nil, 200); st["last_status"] != "empty" {
		t.Fatalf("an empty period should say so: %v", st)
	}
	if _, err := a.SysDB.Exec(`UPDATE trend_clusters SET reviewed_at = NULL, last_seen_at = now() WHERE workspace_id = $1 AND title <> 'Already reviewed'`, ws); err != nil {
		t.Fatal(err)
	}

	// ---- the WhatsApp command ------------------------------------------------------------
	trends.DigestClock = time.Now
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)
	const member = "201700005555"
	if _, err := a.SysDB.Exec(`INSERT INTO member_whatsapp (workspace_id, user_id, phone, verified_at, locale) VALUES ($1, $2, $3, now(), 'ar')`, ws, aliceID, member); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.SysDB.Exec(`DELETE FROM member_whatsapp WHERE workspace_id = $1`, ws) })
	link, err := whatsapp.Load(sctx, a, ws)
	if err != nil {
		t.Fatal(err)
	}
	inbound := func(from, text string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"event": "messages.upsert", "instance": link.Instance, "data": map[string]any{
			"key":      map[string]any{"remoteJid": from + "@s.whatsapp.net", "fromMe": false, "id": "WA-IN-" + rnd()},
			"pushName": "Someone", "message": map[string]any{"conversation": text}, "messageTimestamp": time.Now().Unix()}})
		req, _ := http.NewRequest("POST", sf.srv.URL+"/api/whatsapp/hook/"+link.HookID, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(whatsapp.TokenHeader, link.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode/100 != 2 {
			t.Fatalf("hook: %d", res.StatusCode)
		}
	}
	sentTo := func(n string) []string {
		var out []string
		for _, s := range sf.evo.sends() {
			if s.Number == n {
				out = append(out, s.Text)
			}
		}
		return out
	}
	convs := func() int {
		var n int
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp'`, ws).Scan(&n)
		return n
	}
	before := convs()
	inbound(member, "ترندات")
	// Titles are wrapped in bidi isolates (U+2068/U+2069); compare without them.
	answers := sentTo(member)
	for i := range answers {
		answers[i] = strings.NewReplacer(isolateOpen, "", isolateClose, "").Replace(answers[i])
	}
	if len(answers) != 1 || !strings.Contains(answers[0], "أبرز الترندات") || !strings.Contains(answers[0], "Open models reach parity (82) — 4 مصادر") || !strings.Contains(answers[0], "/ar/w/") {
		t.Fatalf("the member's answer: %q", answers)
	}
	if convs() != before {
		t.Fatal("a member's trends command was filed as a customer conversation")
	}
	inbound(member, "TRENDS") // within 30 s: not answered twice
	if n := len(sentTo(member)); n != 1 {
		t.Fatalf("a second command within 30 s was answered: %d", n)
	}
	// A customer who writes "trends" is a customer.
	const customer = "201700006666"
	inbound(customer, "trends")
	if n := len(sentTo(customer)); n != 0 || convs() != before+1 {
		t.Fatalf("a customer's 'trends' should be filed, not answered: sent %d, conversations %d->%d", n, before, convs())
	}
	// A client-role member cannot read trends: told so, shown nothing.
	bob := newClient(t, sf.srv.URL)
	bobEmail := "bob-" + rnd() + "@example.test"
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-8"}, 201)
	const client = "201700007777"
	for _, q := range []string{
		`INSERT INTO workspace_members (workspace_id, user_id, role) SELECT $1, id, 'client' FROM users WHERE email = $2`,
		`INSERT INTO member_whatsapp (workspace_id, user_id, phone, verified_at) SELECT $1, id, '` + client + `', now() FROM users WHERE email = $2`,
	} {
		if _, err := a.SysDB.Exec(q, ws, bobEmail); err != nil {
			t.Fatal(err)
		}
	}
	inbound(client, "trends")
	if got := sentTo(client); len(got) != 1 || strings.Contains(got[0], "Open models") || !strings.Contains(got[0], "does not include the trends center") {
		t.Fatalf("a client's trends command: %q", got)
	}
}

// decodeQP undoes quoted-printable soft breaks and escapes well enough to read
// a message body in a test.
func decodeQP(s string) string {
	s = strings.ReplaceAll(s, "=\r\n", "")
	s = strings.ReplaceAll(s, "=\n", "")
	var b bytes.Buffer
	for i := 0; i < len(s); i++ {
		if s[i] == '=' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			v := unhex(s[i+1])<<4 | unhex(s[i+2])
			b.WriteByte(v)
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isHex(c byte) bool { return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') }
func unhex(c byte) byte {
	if c >= 'A' {
		return c - 'A' + 10
	}
	return c - '0'
}

// The bidi isolates the digest wraps titles and names in (trends.isolate).
const (
	isolateOpen  = string(rune(0x2068))
	isolateClose = string(rune(0x2069))
)
