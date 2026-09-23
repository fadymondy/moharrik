package server

/*
TestInboxCRM — the inbox reaching the CRM (MH-379) and the inbox's productivity
tools (MH-383), end to end through the real stack: a visitor's chat through the
public widget route, then linking, creating a contact, opening a deal with the
transcript, the automatic email and WhatsApp match, the contact/company/deal
timelines, snooze and its wake-up, canned replies with their permissions, and
the stats checked against the database.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run InboxCRM -v
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func visitorWrites(t *testing.T, base, key, origin string, body map[string]any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, base+"/api/chat/"+key+"/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/chat/%s/messages: %d %s", key, res.StatusCode, raw)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func arr(v any) []any          { a, _ := v.([]any); return a }

func TestInboxCRM(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "inbox-crm-test-secret-inbox-crm-test-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("SNOOZE_WAKER", "off")
	defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	aliceEmail := "crm-a-" + rnd() + "@example.test"
	alice.must("POST", "/api/auth/register", map[string]string{"email": aliceEmail, "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Inbox CRM " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	const origin = "https://shop.test"
	wgt := obj(alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "Site", "allowed_origins": []string{origin}}, 201)["widget"])
	key := wgt["public_key"].(string)

	// ---- a visitor writes, leaving their email and phone in the text --------
	tok := visitorWrites(t, srv.URL, key, origin, map[string]any{"body": "Hi, I need 40 bags of coffee a month.", "name": "Mona Adel", "locale": "ar"})["visitor_token"].(string)
	visitorWrites(t, srv.URL, key, origin, map[string]any{"body": "Reach me on mona@acme-roasters.test or +20 101 234 5678", "visitor_token": tok})
	convs := arr(alice.must("GET", "/api/w/conversations", nil, 200)["conversations"])
	if len(convs) != 1 {
		t.Fatalf("want one conversation, got %d", len(convs))
	}
	convID := obj(convs[0])["id"].(string)
	alice.must("POST", "/api/w/conversations/"+convID+"/reply", map[string]any{"body": "Thanks Mona, a quote is coming."}, 201)

	detail := alice.must("GET", "/api/w/conversations/"+convID, nil, 200)
	v := obj(detail["visitor"])
	if v["name"] != "Mona Adel" || v["email"] != "mona@acme-roasters.test" || v["phone"] != "+20 101 234 5678" {
		t.Errorf("the prefill should come from what the visitor gave: %v", v)
	}
	if detail["reply_language"] != "ar" || v["locale"] != "ar" {
		t.Errorf("an Arabic widget's visitor is answered in Arabic: %v %v", detail["reply_language"], v["locale"])
	}

	// ---- create a contact from it -------------------------------------------
	coID := obj(alice.must("POST", "/api/w/companies", map[string]any{"name": "Acme Roasters", "domain": "acme-roasters.test"}, 201)["company"])["id"].(string)
	made := alice.must("POST", "/api/w/conversations/"+convID+"/contact", map[string]any{
		"email": "Mona@Acme-Roasters.test", "name": "Mona Adel", "phone": "+20 101 234 5678", "locale": "ar",
	}, 201)
	contactID := made["contact_id"].(string)
	if made["contact_created"] != true {
		t.Errorf("a new address makes a new contact: %v", made)
	}
	c := obj(made["conversation"])
	if c["contact_id"] != contactID || c["contact_link"] != "manual" || obj(obj(c["contact"])["company"])["id"] != coID {
		t.Errorf("the conversation should be linked to the contact, at the company with that domain: %v", c)
	}
	// The same address again links the existing contact rather than a second one.
	again := alice.must("POST", "/api/w/conversations/"+convID+"/contact", map[string]any{"email": "mona@acme-roasters.test"}, 201)
	if again["contact_id"] != contactID || again["contact_created"] != false {
		t.Errorf("an existing email is reused: %v", again)
	}

	// ---- unlink, then link to an existing contact by id -----------------------
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"contact_id": ""}, 200)
	linked := obj(alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"contact_id": contactID}, 200)["conversation"])
	if linked["contact_id"] != contactID {
		t.Errorf("link by id: %v", linked)
	}

	// ---- open a deal with the transcript --------------------------------------
	deal := alice.must("POST", "/api/w/conversations/"+convID+"/deal", map[string]any{"value_minor": 250000}, 201)
	dealID := deal["deal_id"].(string)
	if obj(obj(deal["conversation"])["deal"])["id"] != dealID {
		t.Errorf("the conversation should name its deal: %v", deal["conversation"])
	}
	if code, _ := alice.do("POST", "/api/w/conversations/"+convID+"/deal", map[string]any{}); code != http.StatusConflict {
		t.Errorf("a second deal from the same conversation: got %d, want 409", code)
	}
	dd := alice.must("GET", "/api/w/deals/"+dealID, nil, 200)
	d := obj(dd["deal"])
	if obj(d["contact"])["id"] != contactID || obj(d["company"])["id"] != coID || d["value_minor"].(float64) != 250000 {
		t.Errorf("the deal should be on the contact and their company: %v", d)
	}
	acts := arr(dd["activities"])
	if len(acts) != 1 {
		t.Fatalf("the transcript is the deal's first activity; got %d activities", len(acts))
	}
	body := obj(acts[0])["body"].(string)
	for _, want := range []string{"Mona Adel", "Hi, I need 40 bags of coffee a month.", "mona@acme-roasters.test or +20 101 234 5678",
		aliceEmail + " (team)", "Thanks Mona, a quote is coming.", "Times are UTC"} {
		if !strings.Contains(body, want) {
			t.Errorf("the transcript is missing %q:\n%s", want, body)
		}
	}
	// Who said what, in order: the visitor's two lines before the team's reply.
	if strings.Index(body, "40 bags") > strings.Index(body, "a quote is coming") {
		t.Errorf("the transcript is out of order:\n%s", body)
	}
	if len(arr(dd["conversations"])) != 1 {
		t.Errorf("the deal page should list the conversation it came from: %v", dd["conversations"])
	}

	// ---- the contact's and the company's timelines show the conversation ----
	ct := alice.must("GET", "/api/w/contacts/"+contactID, nil, 200)
	if cs := arr(ct["conversations"]); len(cs) != 1 || obj(cs[0])["id"] != convID || obj(cs[0])["message_count"].(float64) != 3 {
		t.Errorf("the contact's timeline should show the conversation: %v", ct["conversations"])
	}
	if !strings.Contains(mustJSON(ct["activities"]), "40 bags") {
		t.Errorf("the transcript should be on the contact's timeline too")
	}
	co := alice.must("GET", "/api/w/companies/"+coID, nil, 200)
	if cs := arr(co["conversations"]); len(cs) != 1 || obj(cs[0])["id"] != convID {
		t.Errorf("the company's timeline should show its people's conversations: %v", co["conversations"])
	}

	// ---- email and WhatsApp threads link themselves on an exact match -------
	ctx := context.Background()
	known := "omar-" + rnd() + "@buyer.test"
	omarID := alice.must("POST", "/api/w/contacts", map[string]any{"email": known, "name": "Omar"}, 201)["contact"].(map[string]any)["id"].(string)
	alice.must("PATCH", "/api/w/contacts/"+omarID, map[string]any{"phone": "+966 51 234 5678"}, 200)
	if err := inbox.File(ctx, a, wsID, &inbox.Email{MessageID: "m1-" + rnd() + "@buyer.test", From: strings.ToUpper(known), FromName: "Omar", Subject: "Order", Body: "Hello", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := inbox.File(ctx, a, wsID, &inbox.Email{MessageID: "m2-" + rnd() + "@x.test", From: "stranger-" + rnd() + "@x.test", Subject: "Hi", Body: "Hello", Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := inbox.FileWhatsApp(ctx, a, wsID, "966512345678", "Omar", "مرحبا، أريد طلبية", "wa-"+rnd()); err != nil {
		t.Fatal(err)
	}
	byChannel := map[string][]map[string]any{}
	for _, raw := range arr(alice.must("GET", "/api/w/conversations", nil, 200)["conversations"]) {
		cv := obj(raw)
		byChannel[cv["channel"].(string)] = append(byChannel[cv["channel"].(string)], cv)
	}
	matched := 0
	for _, cv := range byChannel["email"] {
		if cv["contact_id"] == omarID {
			matched++
			if cv["contact_link"] != "auto" {
				t.Errorf("an automatic link says so: %v", cv)
			}
		} else if cv["contact_id"] != nil {
			t.Errorf("an unknown sender must not be linked: %v", cv)
		}
	}
	if matched != 1 {
		t.Errorf("the email from a known address should be linked to its contact")
	}
	if len(byChannel["whatsapp"]) != 1 || byChannel["whatsapp"][0]["contact_id"] != omarID {
		t.Fatalf("the WhatsApp number should match the contact's phone digits: %v", byChannel["whatsapp"])
	}
	waID := byChannel["whatsapp"][0]["id"].(string)
	if l := alice.must("GET", "/api/w/conversations/"+waID, nil, 200)["reply_language"]; l != "ar" {
		t.Errorf("a customer writing Arabic on WhatsApp is answered in Arabic, got %v", l)
	}
	// A member who unlinks a match keeps it unlinked through the next message.
	alice.must("PATCH", "/api/w/conversations/"+waID, map[string]any{"contact_id": ""}, 200)
	if err := inbox.FileWhatsApp(ctx, a, wsID, "966512345678", "Omar", "again", "wa-"+rnd()); err != nil {
		t.Fatal(err)
	}
	if cv := obj(alice.must("GET", "/api/w/conversations/"+waID, nil, 200)["conversation"]); cv["contact_id"] != nil || cv["contact_link"] != "manual" {
		t.Errorf("an unlinked thread must not be relinked automatically: %v", cv)
	}

	// ---- snooze ----------------------------------------------------------------
	until := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	sn := obj(alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"snoozed_until": until.Format(time.RFC3339)}, 200)["conversation"])
	if sn["status"] != "snoozed" || sn["snoozed_until"] == nil {
		t.Errorf("snoozing: %v", sn)
	}
	if code, _ := alice.do("PATCH", "/api/w/conversations/"+convID, map[string]any{"snoozed_until": time.Now().Add(-time.Minute).Format(time.RFC3339)}); code != 422 {
		t.Errorf("a snooze in the past: got %d, want 422", code)
	}
	if n := len(arr(alice.must("GET", "/api/w/conversations?status=snoozed", nil, 200)["conversations"])); n != 1 {
		t.Errorf("the snoozed filter: %d", n)
	}
	// Unsnooze by hand.
	un := obj(alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"snoozed_until": ""}, 200)["conversation"])
	if un["status"] != "open" || un["snoozed_until"] != nil {
		t.Errorf("unsnoozing: %v", un)
	}
	// A snooze whose time has passed wakes on the next read, unread again.
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"snoozed_until": until.Format(time.RFC3339)}, 200)
	if _, err := a.SysDB.Exec(`UPDATE conversations SET snoozed_until = now() - interval '1 minute' WHERE id = $1`, convID); err != nil {
		t.Fatal(err)
	}
	for _, raw := range arr(alice.must("GET", "/api/w/conversations", nil, 200)["conversations"]) {
		if cv := obj(raw); cv["id"] == convID && (cv["status"] != "open" || cv["unread"] != true || cv["snoozed_until"] != nil) {
			t.Errorf("a due snooze should wake, unread: %v", cv)
		}
	}
	// And the waker does the same across workspaces without anybody reading.
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"snoozed_until": until.Format(time.RFC3339)}, 200)
	_, _ = a.SysDB.Exec(`UPDATE conversations SET snoozed_until = now() - interval '1 minute' WHERE id = $1`, convID)
	if n, err := inbox.WakeAllDue(ctx, a); err != nil || n < 1 {
		t.Errorf("the waker: %d %v", n, err)
	}
	// A customer writing wakes a snoozed thread too.
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"snoozed_until": until.Format(time.RFC3339)}, 200)
	visitorWrites(t, srv.URL, key, origin, map[string]any{"body": "Any news?", "visitor_token": tok})
	if cv := obj(alice.must("GET", "/api/w/conversations/"+convID, nil, 200)["conversation"]); cv["status"] != "open" || cv["snoozed_until"] != nil {
		t.Errorf("an inbound message should wake the thread: %v", cv)
	}

	// ---- close records closed_at ------------------------------------------------
	cl := obj(alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"status": "closed"}, 200)["conversation"])
	if cl["closed_at"] == nil {
		t.Errorf("closing records when: %v", cl)
	}

	// ---- canned replies -----------------------------------------------------------
	r1 := obj(alice.must("POST", "/api/w/canned_replies", map[string]any{"title": "Thanks", "body_en": "Thank you!", "body_ar": "شكرًا لك!"}, 201)["reply"])
	alice.must("POST", "/api/w/canned_replies", map[string]any{"title": "Arabic only", "body_ar": "أهلاً"}, 201)
	if code, _ := alice.do("POST", "/api/w/canned_replies", map[string]any{"title": "Empty"}); code != 422 {
		t.Errorf("a canned reply with no text: got %d, want 422", code)
	}
	if code, _ := alice.do("PATCH", "/api/w/canned_replies/"+r1["id"].(string), map[string]any{"body_en": "", "body_ar": ""}); code != 422 {
		t.Errorf("emptying both variants: got %d, want 422", code)
	}
	alice.must("PATCH", "/api/w/canned_replies/"+r1["id"].(string), map[string]any{"body_en": "Thank you very much!"}, 200)
	list := arr(alice.must("GET", "/api/w/canned_replies", nil, 200)["replies"])
	if len(list) != 2 || obj(list[0])["body_en"] != "Thank you very much!" {
		t.Errorf("canned replies in order: %v", list)
	}
	// A member reads and uses them; only a manager writes them.
	carol := newClient(t, srv.URL)
	carolEmail := "crm-c-" + rnd() + "@example.test"
	carol.must("POST", "/api/auth/register", map[string]string{"email": carolEmail, "password": "correct-horse-3"}, 201)
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = $2`, wsID, carolEmail); err != nil {
		t.Fatal(err)
	}
	carol.ws = wsID
	if n := len(arr(carol.must("GET", "/api/w/canned_replies", nil, 200)["replies"])); n != 2 {
		t.Errorf("a member reads the canned replies: %d", n)
	}
	if code, _ := carol.do("POST", "/api/w/canned_replies", map[string]any{"title": "x", "body_en": "y"}); code != http.StatusForbidden {
		t.Errorf("a member adding a canned reply: got %d, want 403", code)
	}
	alice.must("DELETE", "/api/w/canned_replies/"+r1["id"].(string), nil, 204)

	// ---- stats match the database ------------------------------------------------
	st := alice.must("GET", "/api/w/inbox/stats?days=30", nil, 200)
	var total, chats, answered, resolved int64
	var frMedian float64
	if err := a.SysDB.QueryRow(`
		SELECT count(*), count(*) FILTER (WHERE channel = 'chat'), count(*) FILTER (WHERE status = 'closed')
		FROM conversations WHERE workspace_id = $1 AND created_at >= now() - interval '30 days'`, wsID).Scan(&total, &chats, &resolved); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRow(`
		WITH f AS (
		  SELECT (SELECT min(created_at) FROM messages WHERE conversation_id = c.id AND direction = 'in') fi, c.id
		  FROM conversations c WHERE c.workspace_id = $1),
		g AS (SELECT fi, (SELECT min(created_at) FROM messages WHERE conversation_id = f.id AND direction = 'out' AND created_at >= f.fi) fo FROM f)
		SELECT count(fo), coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM fo - fi)), 0) FROM g WHERE fo IS NOT NULL`, wsID).Scan(&answered, &frMedian); err != nil {
		t.Fatal(err)
	}
	tot := obj(st["total"])
	if int64(tot["conversations"].(float64)) != total || int64(tot["resolved"].(float64)) != resolved || int64(tot["answered"].(float64)) != answered {
		t.Errorf("stats total %v, database: %d conversations, %d resolved, %d answered", tot, total, resolved, answered)
	}
	if got := tot["first_response_median_seconds"].(float64); math.Abs(got-frMedian) > 0.001 {
		t.Errorf("first-response median %v, database %v", got, frMedian)
	}
	chs := arr(st["channels"])
	if len(chs) != 3 || obj(chs[0])["channel"] != "chat" || int64(obj(chs[0])["conversations"].(float64)) != chats {
		t.Errorf("per channel: %v (database: %d chats)", chs, chats)
	}
	if total != 4 || resolved != 1 || answered != 1 {
		t.Errorf("this test made 4 conversations (1 chat, 2 email, 1 WhatsApp), closed 1 and answered 1; the database says %d, %d, %d", total, resolved, answered)
	}
	// An empty inbox is zeros and nulls, never made-up times.
	bob := newClient(t, srv.URL)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "crm-b-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	bob.ws = bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Empty " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	bt := obj(bob.must("GET", "/api/w/inbox/stats", nil, 200)["total"])
	if bt["conversations"].(float64) != 0 || bt["first_response_median_seconds"] != nil || bt["resolution_rate"] != nil {
		t.Errorf("an empty inbox has no stats to show: %v", bt)
	}
	// And B's stats, links and canned replies never see A's.
	if code, _ := bob.do("POST", "/api/w/conversations/"+convID+"/deal", map[string]any{}); code != http.StatusNotFound {
		t.Errorf("B opening a deal from A's conversation: got %d, want 404", code)
	}
	if code, _ := bob.do("POST", "/api/w/conversations/"+convID+"/contact", map[string]any{"email": "x@y.test"}); code != http.StatusNotFound {
		t.Errorf("B making a contact from A's conversation: got %d, want 404", code)
	}
	if code, _ := bob.do("PATCH", "/api/w/conversations/"+waID, map[string]any{"contact_id": omarID}); code != http.StatusNotFound {
		t.Errorf("B linking A's conversation: got %d, want 404", code)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestTimelinePagesCarryTheirConversations: a record's linked conversations are
// paged with its activities (MH-379 on MH-467's paged timeline). Each page
// carries the conversations in its own stretch of time, so none shows below
// activities that have not been loaded, and each appears on exactly one page.
func TestTimelinePagesCarryTheirConversations(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "timeline-conv-test-secret-timeline-conv-test-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "tl-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Timeline " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID
	ctID := obj(alice.must("POST", "/api/w/contacts", map[string]any{"email": "paged-" + rnd() + "@example.test", "name": "Paged"}, 201)["contact"])["id"].(string)

	// 60 notes, one a minute into the past; the first page is the newest 50,
	// so it ends at the note 50 minutes ago.
	var userID string
	if err := a.SysDB.QueryRow(`SELECT user_id FROM workspace_members WHERE workspace_id = $1`, wsID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i := 1; i <= 60; i++ {
		if _, err := a.SysDB.Exec(`INSERT INTO activities (workspace_id, kind, body, contact_id, created_by, created_at) VALUES ($1, 'note', $2, $3, $4, $5)`,
			wsID, "note "+strconv.Itoa(i), ctID, userID, now.Add(-time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	// Three conversations: one inside the first page's stretch, one between the
	// first page's last note and the next, one older than every note.
	conv := func(minutesAgo int) string {
		var id string
		if err := a.SysDB.QueryRow(`INSERT INTO conversations (workspace_id, channel, contact_id, contact_link, customer_name, last_message_at)
			VALUES ($1, 'chat', $2, 'manual', $3, $4) RETURNING id`,
			wsID, ctID, "conv "+strconv.Itoa(minutesAgo), now.Add(-time.Duration(minutesAgo)*time.Minute)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	recent, between, oldest := conv(5), conv(55), conv(600)

	ids := func(v any) []string {
		out := []string{}
		for _, c := range arr(v) {
			out = append(out, obj(c)["id"].(string))
		}
		return out
	}
	first := alice.must("GET", "/api/w/contacts/"+ctID, nil, 200)
	if n := len(arr(first["activities"])); n != 50 || first["activities_total"].(float64) != 60 || first["activities_next_cursor"] == "" {
		t.Fatalf("the first page should be 50 of 60 with a cursor: %d %v", n, first["activities_total"])
	}
	if got := ids(first["conversations"]); len(got) != 1 || got[0] != recent {
		t.Errorf("the first page should carry only the conversation in its stretch of time: %v (want %s)", got, recent)
	}
	next := alice.must("GET", "/api/w/activities?contact_id="+ctID+"&cursor="+first["activities_next_cursor"].(string), nil, 200)
	if n := len(arr(next["activities"])); n != 10 || next["next_cursor"] != "" {
		t.Errorf("the last page should be the remaining 10: %d %v", n, next["next_cursor"])
	}
	if got := ids(next["conversations"]); len(got) != 2 || got[0] != between || got[1] != oldest {
		t.Errorf("the last page should carry the older conversations, newest first: %v (want %s, %s)", got, between, oldest)
	}
	// With one page covering everything, all three come at once.
	all := alice.must("GET", "/api/w/activities?contact_id="+ctID+"&limit=200", nil, 200)
	if got := ids(all["conversations"]); len(got) != 3 {
		t.Errorf("a single page carries every conversation: %v", got)
	}
}
