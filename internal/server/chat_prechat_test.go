package server

/*
TestChatPrechat — the widget's pre-chat form through the real stack: a widget
that requires contact details refuses to open a conversation without a name
and an email or phone, files the visitor as a CRM contact (linking an
existing one, never editing or merging it), never asks a continuing thread
again, and a widget with the form off behaves as before. Also the business
name and picture in the public config.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ChatPrechat -v
*/

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestChatPrechat(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "chat-prechat-test-secret-chat-prechat-test-secret-0")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("NOTIFY_COLLECTOR", "off")
	defer inbox.SetLimiter(inbox.NewMemoryLimiter(), inbox.DefaultChatLimits())()

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "pre-a-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	wsName := "Prechat Shop " + rnd()
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": wsName}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID

	const origin = "https://shop.prechat.test"
	wgt := obj(alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "Site", "allowed_origins": []string{origin}}, 201)["widget"])
	if wgt["require_contact"] != true {
		t.Fatalf("a new widget asks for contact details by default: %v", wgt)
	}
	key := wgt["public_key"].(string)
	msgURL := srv.URL + "/api/chat/" + key + "/messages"

	// ---- the config: who the visitor talks to --------------------------------------
	cfgURL := srv.URL + "/api/chat/" + key + "/config"
	cfg := publicCall(t, "GET", cfgURL, origin, nil)
	if cfg.code != 200 || cfg.body["name"] != wsName || cfg.body["require_contact"] != true {
		t.Fatalf("config: %d %s", cfg.code, cfg.raw)
	}
	if _, ok := cfg.body["avatar_url"]; ok {
		t.Errorf("no picture yet, yet the config names one: %s", cfg.raw)
	}
	png := []byte("\x89PNG\r\n\x1a\nnot-really-a-png")
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_avatars (workspace_id, content_type, size_bytes, data) VALUES ($1, 'image/png', $2, $3)`, wsID, len(png), png); err != nil {
		t.Fatal(err)
	}
	cfg = publicCall(t, "GET", cfgURL, origin, nil)
	av, _ := cfg.body["avatar_url"].(string)
	if !strings.Contains(av, "/api/chat/"+key+"/avatar?v=") {
		t.Fatalf("avatar_url: %q", av)
	}
	// An <img> sends no Origin; the key alone serves the picture.
	res, err := http.Get(srv.URL + "/api/chat/" + key + "/avatar")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || string(body) != string(png) {
		t.Fatalf("avatar: %d %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res, _ := http.Get(srv.URL + "/api/chat/cwk_nope/avatar"); res.StatusCode != 404 {
		t.Errorf("avatar for an unknown key: %d", res.StatusCode)
	}

	// ---- blocked without contact details ---------------------------------------------
	for _, c := range []struct {
		name string
		body map[string]any
	}{
		{"nothing", map[string]any{"body": "hi"}},
		{"name only", map[string]any{"body": "hi", "name": "Mona"}},
		{"no name", map[string]any{"body": "hi", "email": "mona@prechat.test"}},
		{"bad email", map[string]any{"body": "hi", "name": "Mona", "email": "mona@"}},
		{"local phone", map[string]any{"body": "hi", "name": "Mona", "phone": "0101 234 5678"}},
	} {
		r := publicCall(t, "POST", msgURL, origin, c.body)
		if r.code != 422 || !strings.Contains(r.raw, "contact_required") {
			t.Errorf("%s: got %d want 422 contact_required: %s", c.name, r.code, r.raw)
		}
	}
	var n int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM conversations WHERE workspace_id = $1`, wsID).Scan(&n)
	if n != 0 {
		t.Fatalf("a refused visitor opened %d conversations", n)
	}

	// ---- with them: stored, filed, linked --------------------------------------------
	r := publicCall(t, "POST", msgURL, origin, map[string]any{"body": "Do you ship to Cairo?", "name": "  Mona   Adel ", "email": "Mona@Prechat.TEST", "phone": "+20 101 234 5678", "locale": "ar"})
	if r.code != 201 {
		t.Fatalf("send with contact: %d %s", r.code, r.raw)
	}
	tok := r.body["visitor_token"].(string)
	var convID, name, email, phone, link string
	var contactID *string
	if err := a.SysDB.QueryRow(`SELECT id, customer_name, customer_email, customer_phone, contact_id, contact_link FROM conversations WHERE workspace_id = $1`, wsID).
		Scan(&convID, &name, &email, &phone, &contactID, &link); err != nil {
		t.Fatal(err)
	}
	if name != "Mona Adel" || email != "mona@prechat.test" || phone != "+201012345678" || contactID == nil || link != "auto" {
		t.Fatalf("conversation: %q %q %q %v %q", name, email, phone, contactID, link)
	}
	var cName, cSource, cLocale string
	if err := a.SysDB.QueryRow(`SELECT name, source, locale FROM contacts WHERE id = $1`, *contactID).Scan(&cName, &cSource, &cLocale); err != nil {
		t.Fatal(err)
	}
	if cName != "Mona Adel" || cSource != "chat" || cLocale != "ar" {
		t.Errorf("contact: %q %q %q", cName, cSource, cLocale)
	}

	// A continuing thread is never asked again.
	if r := publicCall(t, "POST", msgURL, origin, map[string]any{"body": "And Alexandria?", "visitor_token": tok}); r.code != 201 {
		t.Fatalf("continuing without contact: %d %s", r.code, r.raw)
	}

	// ---- an existing contact is linked, never edited or merged ---------------------
	var known string
	if err := a.SysDB.QueryRow(`INSERT INTO contacts (workspace_id, name, email, source) VALUES ($1, 'Omar (real)', 'omar@prechat.test', 'manual') RETURNING id`, wsID).Scan(&known); err != nil {
		t.Fatal(err)
	}
	r = publicCall(t, "POST", msgURL, origin, map[string]any{"body": "hello", "name": "Someone Else", "email": "OMAR@prechat.test", "phone": "+20 101 234 5678"})
	if r.code != 201 {
		t.Fatalf("send as a known address: %d %s", r.code, r.raw)
	}
	var linked string
	if err := a.SysDB.QueryRow(`SELECT contact_id FROM conversations WHERE workspace_id = $1 AND id <> $2 ORDER BY created_at DESC LIMIT 1`, wsID, convID).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	var omarName string
	_ = a.SysDB.QueryRow(`SELECT name FROM contacts WHERE id = $1`, known).Scan(&omarName)
	if omarName != "Omar (real)" {
		t.Errorf("a visitor renamed an existing contact to %q", omarName)
	}
	var contacts int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsID).Scan(&contacts)
	if contacts != 2 {
		t.Errorf("a visitor matching two contacts merged or added one: %d contacts, want 2", contacts)
	}
	if linked != known && linked != *contactID {
		t.Errorf("linked to %q, want one of the existing contacts", linked)
	}

	// ---- the form off: as before ------------------------------------------------------
	alice.must("PATCH", "/api/w/chat_widgets/"+wgt["id"].(string), map[string]any{"require_contact": false}, 200)
	if r := publicCall(t, "POST", msgURL, origin, map[string]any{"body": "anonymous hi"}); r.code != 201 {
		t.Fatalf("form off, no contact: %d %s", r.code, r.raw)
	}
	var unlinked int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM conversations WHERE workspace_id = $1 AND contact_id IS NULL`, wsID).Scan(&unlinked)
	if unlinked != 1 {
		t.Errorf("form off: %d unlinked conversations, want 1", unlinked)
	}
}
