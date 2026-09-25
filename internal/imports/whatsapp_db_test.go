package imports_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/imports"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
	"github.com/fadymondy/moharrik/internal/whatsapp"
	"github.com/togo-framework/togo"
)

/*
MH-562: the WhatsApp import source against a fake Evolution API (httptest —
never the real one, per the card's safety rule). Covers the shape a real
import goes through: list chats and groups (Arabic included), import them
into contacts + inbox threads, then re-import and see nothing duplicate.
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "imports", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "imports-test-secret-imports-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")

	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	return a
}

func workspaceIn(t *testing.T, a *app.App) string {
	t.Helper()
	var wsID string
	slug := "imports-test-" + time.Now().Format("150405.000000000")
	if err := a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('Imports Test', $1, 'test') RETURNING id`,
		slug).Scan(&wsID); err != nil {
		t.Fatal(err)
	}
	return wsID
}

// fakeEvolution is the same shape web/e2e/fake-evolution.mjs gives a person
// clicking through the app: real-format chats, a group, and Arabic content.
// The chat is the exact shape Evolution v2's findChats returns (MH-562): the
// name is pushName, the count is unreadCount, and lastMessage is a full message
// record whose body is an object — a string there once 500'd every preview.
func fakeEvolution(t *testing.T) *httptest.Server {
	t.Helper()
	now := time.Now().Unix()
	chats := []map[string]any{
		{
			"id": "cmchat0001", "remoteJid": "201015550101@s.whatsapp.net", "pushName": "Rana Fathy", "unreadCount": 1,
			"lastMessage": map[string]any{
				"key":              map[string]any{"id": "wamid.002", "fromMe": false, "remoteJid": "201015550101@s.whatsapp.net"},
				"pushName":         "Rana Fathy",
				"messageType":      "conversation",
				"message":          map[string]any{"conversation": "هل وصل طلبي رقم NC-20417؟"},
				"messageTimestamp": now - 3600,
			},
		},
	}
	groups := []map[string]any{
		{
			"id": "120363011234567890@g.us", "subject": "عملاء القهوة - القاهرة", "size": 2,
			"participants": []map[string]any{
				{"id": "201015550101@s.whatsapp.net", "name": "Rana Fathy", "admin": "superadmin"},
				{"id": "201098765432@s.whatsapp.net", "name": "Laila Hassan"},
			},
			"lastMessage": map[string]any{
				"key":              map[string]any{"id": "wamid.101", "fromMe": false, "remoteJid": "120363011234567890@g.us"},
				"message":          map[string]any{"extendedTextMessage": map[string]any{"text": "الشحنة الجديدة وصلت اليوم"}},
				"messageTimestamp": fmt.Sprint(now - 1800),
			},
		},
	}
	history := map[string][]map[string]any{
		"201015550101@s.whatsapp.net": {
			{"key": map[string]any{"remoteJid": "201015550101@s.whatsapp.net", "fromMe": false, "id": "wamid.001"}, "pushName": "Rana Fathy", "messageTimestamp": now - 7200, "message": map[string]any{"conversation": "مرحبًا"}},
			{"key": map[string]any{"remoteJid": "201015550101@s.whatsapp.net", "fromMe": true, "id": "wamid.002"}, "messageTimestamp": now - 3600, "message": map[string]any{"conversation": "هل وصل طلبي رقم NC-20417؟"}},
		},
		"120363011234567890@g.us": {
			{"key": map[string]any{"remoteJid": "120363011234567890@g.us", "fromMe": false, "id": "wamid.101"}, "pushName": "Laila Hassan", "messageTimestamp": now - 1800, "message": map[string]any{"conversation": "الشحنة الجديدة وصلت اليوم"}},
		},
	}

	return fakeEvolutionWith(t, chats, groups, history)
}

func fakeEvolutionWith(t *testing.T, chats, groups []map[string]any, history map[string][]map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/instance/connectionState/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"instance": map[string]any{"state": "open"}})
	})
	mux.HandleFunc("/chat/findChats/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(chats)
	})
	mux.HandleFunc("/group/fetchAllGroups/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(groups)
	})
	mux.HandleFunc("/chat/findMessages/", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Where struct {
				Key struct {
					RemoteJID string `json:"remoteJid"`
				} `json:"key"`
			} `json:"where"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": map[string]any{"records": history[in.Where.Key.RemoteJID]}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func waitJob(t *testing.T, ctx context.Context, a *app.App, wsID, jobID string) *imports.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := imports.JobStatus(ctx, a, wsID, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status != "running" {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("import job never finished")
	return nil
}

func TestWhatsAppImportIdempotent(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)
	evo := fakeEvolution(t)

	if _, err := whatsapp.Ensure(context.Background(), a, wsID, "u-test"); err != nil {
		t.Fatal(err)
	}
	whatsapp.SetState(context.Background(), a, wsID, "open", "201000000000")

	cfg := whatsapp.Config{URL: evo.URL, Key: "fake"}
	imports.RegisterProvider(imports.WhatsAppProvider{Config: &cfg})

	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})

	// Sources: ready, since state is open. RegisterProvider is a
	// process-wide registry (MH-562 added Slack/Discord/Telegram/workflow
	// providers, each registered by their own test elsewhere in this
	// package), so ListSources can return more than just whatsapp here —
	// find whatsapp's own entry rather than assuming it is the only one.
	sources, err := imports.ListSources(ctx, a, wsID)
	if err != nil {
		t.Fatal(err)
	}
	var wa *imports.SourceInfo
	for i := range sources {
		if sources[i].Source == "whatsapp" {
			wa = &sources[i]
		}
	}
	if wa == nil || len(wa.Connections) != 1 || !wa.Connections[0].Ready {
		t.Fatalf("whatsapp source not ready: %+v", sources)
	}

	// Preview: one direct chat, one Arabic group.
	items, err := imports.Preview(ctx, a, wsID, "whatsapp", "", imports.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d: %+v", len(items), items)
	}
	var directID, groupID string
	for _, it := range items {
		if it.AlreadyImported {
			t.Fatalf("nothing imported yet, but %q says already_imported", it.ID)
		}
		// Evolution's real lastMessage is an object; the preview must read its
		// body and time, and a direct chat's name comes from pushName.
		if it.LastMessage == "" || it.LastMessageAt == nil {
			t.Fatalf("%q: last message not read: %+v", it.ID, it)
		}
		if it.Kind == "direct" {
			directID = it.ID
			if it.Name != "Rana Fathy" || it.LastMessage != "هل وصل طلبي رقم NC-20417؟" {
				t.Fatalf("direct chat read wrong: %+v", it)
			}
		} else {
			groupID = it.ID
			if len(it.Participants) != 2 {
				t.Fatalf("group participants: got %d, want 2", len(it.Participants))
			}
		}
	}
	if directID == "" || groupID == "" {
		t.Fatalf("missing a direct or group item: %+v", items)
	}

	// Import both, with participants.
	job, err := imports.StartImport(ctx, a, wsID, "whatsapp", imports.ImportRequest{
		Items: []string{directID, groupID}, ImportParticipants: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, ctx, a, wsID, job.ID)
	if job.Status != "done" {
		t.Fatalf("job failed: %+v", job)
	}
	if job.ConversationsCreated != 2 {
		t.Fatalf("conversations_created: got %d, want 2", job.ConversationsCreated)
	}
	if job.ContactsCreated != 2 { // Rana (direct) + Laila (participant); Rana is reused for the group
		t.Fatalf("contacts_created: got %d, want 2", job.ContactsCreated)
	}
	if job.MessagesImported != 3 {
		t.Fatalf("messages_imported: got %d, want 3", job.MessagesImported)
	}

	assertDatedByMessages(t, ctx, a, wsID)

	// No opt-in implied (MH-410): importing history must never set consent.
	var optIns int
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND whatsapp_opt_in_at IS NOT NULL`, wsID).Scan(&optIns); err != nil {
		t.Fatal(err)
	}
	if optIns != 0 {
		t.Fatalf("import set whatsapp_opt_in_at on %d contacts — no opt-in is implied by an import", optIns)
	}

	// Preview again: both now say already_imported.
	items2, err := imports.Preview(ctx, a, wsID, "whatsapp", "", imports.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items2 {
		if !it.AlreadyImported {
			t.Fatalf("%q should be already_imported after the import", it.ID)
		}
	}

	// Re-import: idempotent, nothing new.
	job2, err := imports.StartImport(ctx, a, wsID, "whatsapp", imports.ImportRequest{
		Items: []string{directID, groupID}, ImportParticipants: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	job2 = waitJob(t, ctx, a, wsID, job2.ID)
	if job2.Status != "done" || job2.ConversationsCreated != 0 || job2.ContactsCreated != 0 || job2.MessagesImported != 0 {
		t.Fatalf("re-import was not a no-op: %+v", job2)
	}
	// 3 contacts reused: Rana (the direct chat's contact), then Rana again
	// plus Laila as the group's two participants.
	if job2.ConversationsReused != 2 || job2.ContactsReused != 3 {
		t.Fatalf("re-import did not report the reuse: %+v", job2)
	}

	// The group conversation's summary: 2 people, both group messages inbound.
	var groupConvID string
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT id FROM conversations WHERE workspace_id = $1 AND is_group = true`, wsID).Scan(&groupConvID); err != nil {
		t.Fatal(err)
	}
	sum, err := imports.ChatSummary(ctx, a, wsID, groupConvID)
	if err != nil {
		t.Fatal(err)
	}
	if !sum.IsGroup || sum.ParticipantCount != 2 || sum.MessageCount != 1 || sum.InboundCount != 1 {
		t.Fatalf("group summary: %+v", sum)
	}
}

// One person, two chats: WhatsApp holds Rana under her phone JID and under a
// privacy "…@lid" id whose phone is only in remoteJidAlt, and a third chat is a
// privacy id with no phone at all. She already wrote in live, so the inbox has
// her thread, filed by digits (inbox.FileWhatsApp). The import must list her
// once, merge both chats' history into that live thread, and skip the
// phoneless chat — never mint contacts or threads from a privacy id (MH-562).
func TestWhatsAppImportMergesPrivacyChatsIntoTheLiveThread(t *testing.T) {
	a := testApp(t)
	wsID := workspaceIn(t, a)
	now := time.Now().Unix()
	msg := func(jid, alt, id, text string, at int64) map[string]any {
		return map[string]any{
			"key":              map[string]any{"remoteJid": jid, "remoteJidAlt": alt, "fromMe": false, "id": id},
			"pushName":         "Rana Fathy",
			"message":          map[string]any{"conversation": text},
			"messageTimestamp": at,
		}
	}
	const phoneJID, lid, orphan = "201015550101@s.whatsapp.net", "144985244578026@lid", "156401385095367@lid"
	chats := []map[string]any{
		{"remoteJid": phoneJID, "pushName": "Rana Fathy", "lastMessage": msg(phoneJID, "", "w1", "first", now-7200)},
		{"remoteJid": lid, "pushName": "Rana F.", "lastMessage": msg(lid, phoneJID, "w2", "second", now-60)},
		{"remoteJid": orphan, "pushName": "Hidden", "lastMessage": msg(orphan, "", "w3", "no phone", now-30)},
	}
	history := map[string][]map[string]any{
		phoneJID: {msg(phoneJID, "", "w1", "first", now-7200), msg(phoneJID, "", "w0", "live one", now-9000)},
		lid:      {msg(lid, phoneJID, "w2", "second", now-60)},
		orphan:   {msg(orphan, "", "w3", "no phone", now-30)},
	}
	evo := fakeEvolutionWith(t, chats, nil, history)

	if _, err := whatsapp.Ensure(context.Background(), a, wsID, "u-test"); err != nil {
		t.Fatal(err)
	}
	whatsapp.SetState(context.Background(), a, wsID, "open", "201000000000")
	cfg := whatsapp.Config{URL: evo.URL, Key: "fake"}
	imports.RegisterProvider(imports.WhatsAppProvider{Config: &cfg})
	ctx := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: wsID, UserID: "u-test", Email: "owner@example.test", Role: "owner", Via: "session"})

	// Her live thread, as the webhook files it: digits only, no external id,
	// already holding message w0.
	var liveID string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject)
		VALUES ($1, 'whatsapp', 'Rana Fathy', '201015550101', 'live one') RETURNING id`, wsID).Scan(&liveID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		INSERT INTO messages (workspace_id, conversation_id, direction, body, external_id)
		VALUES ($1, $2, 'in', 'live one', 'w0')`, wsID, liveID); err != nil {
		t.Fatal(err)
	}

	items, err := imports.Preview(ctx, a, wsID, "whatsapp", "", imports.Filters{Kind: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("want Rana once and the phoneless chat left out, got %+v", items)
	}
	it := items[0]
	if it.ID != phoneJID || it.Identity != "+201015550101" || it.LastMessage != "second" || it.Name != "Rana F." {
		t.Fatalf("merged item read wrong: %+v", it)
	}

	job, err := imports.StartImport(ctx, a, wsID, "whatsapp", imports.ImportRequest{Items: []string{it.ID}})
	if err != nil {
		t.Fatal(err)
	}
	job = waitJob(t, ctx, a, wsID, job.ID)
	if job.Status != "done" || job.ConversationsCreated != 0 || job.ConversationsReused != 1 || job.MessagesImported != 2 {
		t.Fatalf("import did not merge into the live thread: %+v", job)
	}

	var threads, msgs int
	var ext string
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1`, wsID).Scan(&threads); err != nil {
		t.Fatal(err)
	}
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE workspace_id = $1 AND conversation_id = $2`, wsID, liveID).Scan(&msgs); err != nil {
		t.Fatal(err)
	}
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT external_thread_id FROM conversations WHERE id = $1`, liveID).Scan(&ext); err != nil {
		t.Fatal(err)
	}
	if threads != 1 || msgs != 3 || ext != phoneJID {
		t.Fatalf("threads=%d messages=%d external_thread_id=%q; want 1, 3, %q", threads, msgs, ext, phoneJID)
	}
	var fakePhones int
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND (phone LIKE '%144985244578026%' OR phone LIKE '%156401385095367%')`, wsID).Scan(&fakePhones); err != nil {
		t.Fatal(err)
	}
	if fakePhones != 0 {
		t.Fatalf("%d contacts were created with a privacy id as their phone", fakePhones)
	}

	again, err := imports.Preview(ctx, a, wsID, "whatsapp", "", imports.Filters{Kind: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 1 || !again[0].AlreadyImported {
		t.Fatalf("preview after import: %+v", again)
	}
}

// assertDatedByMessages fails when an imported conversation is dated by the
// import rather than by its messages (MH-590): its last-message time must be
// its newest message, it must not start after its oldest one, and — every
// fake's history being at least 30 minutes old — neither may read as "now".
func assertDatedByMessages(t *testing.T, ctx context.Context, a *app.App, wsID string) {
	t.Helper()
	var misdated int
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT count(*) FROM conversations c
		  JOIN LATERAL (SELECT max(created_at) AS newest, min(created_at) AS oldest
		                  FROM messages m WHERE m.conversation_id = c.id) m ON true
		 WHERE c.workspace_id = $1
		   AND (c.last_message_at <> m.newest OR c.created_at > m.oldest OR c.last_message_at > now() - interval '20 minutes')`,
		wsID).Scan(&misdated); err != nil {
		t.Fatal(err)
	}
	if misdated != 0 {
		t.Fatalf("%d imported conversations are dated by the import, not by their messages", misdated)
	}
}
