package server

/*
TestInboxCRM — the inbox reaching the CRM (MH-379), end to end through the
real stack: a visitor's chat through the public widget route, then linking,
creating a contact, opening a deal with the transcript, the automatic email
and WhatsApp match, and the contact, company and deal timelines.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run InboxCRM -v
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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

	bob := newClient(t, srv.URL)
	bob.must("POST", "/api/auth/register", map[string]string{"email": "crm-b-" + rnd() + "@example.test", "password": "correct-horse-2"}, 201)
	bob.ws = bob.must("POST", "/api/me/workspaces", map[string]string{"name": "Empty " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	// B never reaches A's conversations: no deal, no contact, no link.
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
