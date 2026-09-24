package server

/*
TestMCPSurface drives every tool the MCP surface cards added (MH-380 inbox and
notifications, MH-386 trends, MH-393 CRM, MH-409 WhatsApp) end to end, the way
a real MCP client does: JSON-RPC over the HTTP transport at /api/mcp, with a
bearer token minted through the same REST route the settings page uses. The
real stack is booted (guard, RLS pool, MCP); only the outside world is faked —
an Evolution server for WhatsApp and an RSS feed for trends.

TestMCPTokenAdmin covers MH-396: an owner sees and revokes every member's
token, a member sees only their own, and a revoked token 401s at once.
*/

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/mcp"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/testdb"
	"github.com/fadymondy/moharrik/internal/trends"
)

// fakeEvolution is just enough of an Evolution API server.
type fakeEvolution struct {
	mu      sync.Mutex
	hookURL string
	sent    []evoSend
}

type evoSend struct {
	Number, Text string
	At           time.Time
}

func (f *fakeEvolution) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/instance/create":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			pairing := ""
			if n, _ := body["number"].(string); n != "" {
				pairing = "PAIR-" + n[len(n)-4:]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"qrcode": map[string]string{"code": "2@QR-PAYLOAD", "base64": "data:image/png;base64,iVBOR", "pairingCode": pairing}})
		case strings.HasPrefix(r.URL.Path, "/instance/connect/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "2@QR-PAYLOAD", "base64": "data:image/png;base64,iVBOR"})
		case strings.HasPrefix(r.URL.Path, "/webhook/set/"):
			var body struct {
				Webhook struct {
					URL string `json:"url"`
				} `json:"webhook"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.hookURL = body.Webhook.URL
			_, _ = w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/webhook/find/"):
			if f.hookURL == "" {
				_, _ = w.Write([]byte(`null`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "url": f.hookURL})
		case strings.HasPrefix(r.URL.Path, "/instance/connectionState/"):
			_, _ = w.Write([]byte(`{"instance":{"state":"open"}}`))
		case strings.HasPrefix(r.URL.Path, "/message/sendText/"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.sent = append(f.sent, evoSend{Number: body["number"], Text: body["text"], At: time.Now()})
			_ = json.NewEncoder(w).Encode(map[string]any{"key": map[string]string{"id": "WA-" + strconv.Itoa(len(f.sent))}})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	})
}

func (f *fakeEvolution) sends() []evoSend {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]evoSend(nil), f.sent...)
}

// fakeFeed is an RSS feed that gains one item every time it is fetched.
type fakeFeed struct {
	mu    sync.Mutex
	polls int
}

func (f *fakeFeed) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A Hacker News that answers like the real API: two stories carrying
		// the same headlines as the feed's first two, at other addresses, so
		// the trends center clusters them into cross-source stories.
		switch {
		case r.URL.Path == "/v0/topstories.json":
			_, _ = w.Write([]byte(`[7001, 7002]`))
			return
		case strings.HasPrefix(r.URL.Path, "/v0/item/"):
			n := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v0/item/"), ".json")
			fmt.Fprintf(w, `{"id":%s,"type":"story","title":"Surface story %s about agents","url":"https://hn.test/%s","score":120,"descendants":14,"time":%d}`,
				n, n[3:], n, time.Now().Add(-30*time.Minute).Unix())
			return
		}
		f.mu.Lock()
		f.polls++
		n := f.polls + 2
		f.mu.Unlock()
		var items strings.Builder
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&items, `<item><title>Surface story %d about agents</title><link>https://news.test/%d</link><guid>story-%d</guid><description>Item %d summary</description><pubDate>%s</pubDate></item>`,
				i, i, i, i, time.Now().Add(-time.Duration(n-i)*time.Hour).UTC().Format(time.RFC1123Z))
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Surface Wire</title><link>https://news.test</link>%s</channel></rss>`, items.String())
	})
}

// mcpJSON calls a tool and decodes its JSON answer, failing on an in-band error.
func mcpJSON(t *testing.T, c *client, tool string, args map[string]any) map[string]any {
	t.Helper()
	code, text, isErr := mcpCall(c, "/api/mcp", tool, args)
	if code != 200 || isErr {
		t.Fatalf("%s(%v): status %d isError=%v: %s", tool, args, code, isErr, text)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%s answered something that is not JSON: %s", tool, text)
	}
	return out
}

// mcpRefused calls a tool that must fail in-band and returns why.
func mcpRefused(t *testing.T, c *client, tool string, args map[string]any) string {
	t.Helper()
	code, text, isErr := mcpCall(c, "/api/mcp", tool, args)
	if code != 200 || !isErr {
		t.Fatalf("%s(%v) should have been refused, got status %d isError=%v: %s", tool, args, code, isErr, text)
	}
	return text
}

type surface struct {
	alice   *client
	ws      string
	srv     *httptest.Server
	evo     *fakeEvolution
	feed    *fakeFeed
	feedURL string
	sys     *sql.DB
	app     *app.App
}

func bootSurface(t *testing.T) *surface {
	t.Helper()
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "mcp-surface-secret-mcp-surface-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("NOTIFY_COLLECTOR", "off")
	t.Setenv("VAPID_PUBLIC_KEY", "")
	t.Setenv("VAPID_PRIVATE_KEY", "")

	evo := &fakeEvolution{}
	evoSrv := httptest.NewServer(evo.handler())
	t.Cleanup(evoSrv.Close)
	feed := &fakeFeed{}
	feedSrv := httptest.NewServer(feed.handler())
	t.Cleanup(feedSrv.Close)
	t.Setenv("EVOLUTION_API_URL", evoSrv.URL)
	t.Setenv("EVOLUTION_API_KEY", "evo-test-key")
	t.Setenv("WHATSAPP_HOOK_BASE", "")
	oldHN := trends.HNBase
	trends.HNBase = feedSrv.URL + "/v0"
	t.Cleanup(func() { trends.HNBase = oldHN })

	a := Boot()
	t.Cleanup(func() { a.Kernel.Close() })
	srv := httptest.NewServer(a.Kernel.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("APP_URL", srv.URL)
	t.Setenv("PUBLIC_URL", srv.URL)

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "surface-" + rnd() + "@example.test", "password": "correct-horse-7"}, 201)
	wsID := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Surface " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = wsID
	return &surface{alice: alice, ws: wsID, srv: srv, evo: evo, feed: feed, feedURL: feedSrv.URL + "/rss.xml", sys: a.SysDB, app: a}
}

// mint makes a token with these scopes through the settings page's route and
// returns a client that speaks MCP with it.
func mint(t *testing.T, c *client, name string, scopes []string) (*client, string) {
	t.Helper()
	res := c.must("POST", "/api/w/mcp_tokens", map[string]any{"name": name, "scopes": scopes}, 201)
	agent := newClient(t, c.base)
	agent.bearer = res["plaintext"].(string)
	return agent, res["token"].(map[string]any)["id"].(string)
}

func allUserScopes() []string {
	out := []string{}
	for _, s := range mcp.UserScopes {
		out = append(out, s.Scope)
	}
	return out
}

func TestMCPSurface(t *testing.T) {
	sf := bootSurface(t)
	alice, srv, evo, feed := sf.alice, sf.srv, sf.evo, sf.feed
	agent, _ := mint(t, alice, "surface agent", allUserScopes())

	// A real MCP client starts with initialize and tools/list.
	code, body := agent.do("POST", "/api/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}})
	if code != 200 || !strings.Contains(string(body), "2025-06-18") {
		t.Fatalf("initialize: %d %s", code, body)
	}
	code, body = agent.do("POST", "/api/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if code != 200 {
		t.Fatalf("tools/list: %d %s", code, body)
	}
	for _, name := range []string{"list_conversations", "get_conversation", "reply", "close_conversation", "assign_conversation",
		"list_notifications", "get_notification", "saved_notifications", "mark_notification", "save_notification", "mark_all_notifications_read",
		"list_webhook_sources", "create_webhook_source", "rotate_webhook_source", "disable_webhook_source", "enable_webhook_source",
		"list_notification_sources", "add_notification_source", "update_notification_source", "collect_notification_source",
		"get_contact", "search_contacts", "create_contact", "update_contact", "get_company", "search_companies", "update_company",
		"get_deal", "update_deal", "company_timeline", "list_activities",
		"snooze_conversation", "link_conversation_contact", "create_contact_from_conversation", "open_deal_from_conversation",
		"list_canned_replies", "inbox_stats",
		"list_trend_sources", "list_trend_catalog", "trend_budget", "list_trend_items", "list_saved_trend_items", "mark_trend_item", "collect_now",
		"add_trend_source", "update_trend_source", "list_trending_topics", "get_trending_topic", "review_trending_topic",
		"whatsapp_status", "whatsapp_link", "send_whatsapp"} {
		if !strings.Contains(string(body), `"name":"`+name+`"`) {
			t.Errorf("tools/list does not offer %s", name)
		}
	}

	members := mcpJSON(t, agent, "list_members", nil)
	aliceID := obj(arr(members["members"])[0])["user_id"].(string)
	aliceEmail := obj(arr(members["members"])[0])["email"].(string)
	if aliceID == "" {
		t.Fatal("list_members returned no user id")
	}

	// ---- MH-393: CRM ---------------------------------------------------------
	domain := "acme-" + rnd() + ".test"
	co := obj(mcpJSON(t, agent, "create_company", map[string]any{"name": "Acme Surface", "domain": domain, "industry": "Robotics"})["company"])
	coID := co["id"].(string)
	if coID == "" {
		t.Fatal("create_company returned no id")
	}
	found := mcpJSON(t, agent, "search_companies", map[string]any{"q": domain[:9]})
	if found["total"].(float64) != 1 || obj(arr(found["companies"])[0])["id"] != coID {
		t.Fatalf("search_companies by domain: %v", found)
	}
	if lc := mcpJSON(t, agent, "list_companies", map[string]any{"limit": 1}); lc["total"].(float64) < 1 || obj(arr(lc["companies"])[0])["id"] == "" {
		t.Errorf("list_companies: %v", lc)
	}
	up := obj(mcpJSON(t, agent, "update_company", map[string]any{"id": coID, "size": "11-50", "notes": "Met at a fair", "owner": aliceEmail})["company"])
	if up["size"] != "11-50" || up["owner_user_id"] != aliceID {
		t.Fatalf("update_company: %v", up)
	}

	ctRes := mcpJSON(t, agent, "create_contact", map[string]any{
		"email": "rana@" + domain, "name": "Rana Surface", "phone": "+20 101 555 0101", "title": "CTO", "company_id": coID,
	})
	ct := obj(ctRes["contact"])
	ctID := ct["id"].(string)
	if ct["phone"] != "+20 101 555 0101" || ct["title"] != "CTO" || obj(ct["company"])["id"] != coID {
		t.Fatalf("create_contact did not keep its CRM fields: %v", ctRes)
	}
	for _, q := range []string{"Rana", "rana@", "555 0101", "Acme Surface"} {
		res := mcpJSON(t, agent, "search_contacts", map[string]any{"q": q})
		if res["total"].(float64) != 1 || obj(arr(res["contacts"])[0])["id"] != ctID {
			t.Errorf("search_contacts %q: %v", q, res)
		}
	}
	upd := obj(mcpJSON(t, agent, "update_contact", map[string]any{"id": ctID, "title": "CEO", "tags": []string{"vip", "surface"}})["contact"])
	if upd["title"] != "CEO" || len(arr(upd["tags"])) != 2 {
		t.Fatalf("update_contact: %v", upd)
	}
	listed := mcpJSON(t, agent, "list_contacts", map[string]any{"limit": 1})
	if first := obj(arr(listed["contacts"])[0]); first["id"] == nil || first["id"] == "" {
		t.Fatalf("list_contacts returns no ids: %v", listed)
	}

	deal := obj(mcpJSON(t, agent, "create_deal", map[string]any{"title": "Surface pilot", "value": 1500.5, "contact_id": ctID, "company_id": coID})["deal"])
	dealID := deal["id"].(string)
	if deal["value_minor"].(float64) != 150050 {
		t.Fatalf("create_deal value: %v", deal)
	}
	act := obj(mcpJSON(t, agent, "log_activity", map[string]any{"kind": "call", "body": "Discovery call", "deal_id": dealID})["activity"])
	if act["id"] == "" {
		t.Fatal("log_activity returned no id")
	}
	got := mcpJSON(t, agent, "get_deal", map[string]any{"id": dealID})
	if len(arr(got["activities"])) != 1 {
		t.Fatalf("get_deal timeline: %v", got)
	}
	won := obj(mcpJSON(t, agent, "update_deal", map[string]any{"id": dealID, "stage": "Won", "value": 2000})["deal"])
	if obj(won["stage"])["kind"] != "won" || won["closed_at"] == nil || won["value_minor"].(float64) != 200000 {
		t.Fatalf("update_deal to Won: %v", won)
	}
	if txt := mcpRefused(t, agent, "update_deal", map[string]any{"id": dealID, "stage": "No such stage"}); !strings.Contains(txt, "no stage") {
		t.Errorf("an unknown stage: %s", txt)
	}
	contactFull := mcpJSON(t, agent, "get_contact", map[string]any{"id": ctID})
	if len(arr(contactFull["deals"])) != 1 || len(arr(contactFull["activities"])) != 1 {
		t.Fatalf("get_contact: %v", contactFull)
	}

	// ---- MH-380: inbox ---------------------------------------------------------
	const origin = "https://surface.test"
	key := alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "Site", "allowed_origins": []string{origin}}, 201)["widget"].(map[string]any)["public_key"].(string)
	visit, _ := json.Marshal(map[string]any{"body": "Hello, is the pilot still on?", "name": "Rana", "email": "rana@" + domain})
	vreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/chat/"+key+"/messages", bytes.NewReader(visit))
	vreq.Header.Set("Content-Type", "application/json")
	vreq.Header.Set("Origin", origin)
	vres, err := http.DefaultClient.Do(vreq)
	if err != nil || vres.StatusCode != 201 {
		t.Fatalf("visitor message: %v %v", err, vres)
	}
	var visitor struct {
		VisitorToken string `json:"visitor_token"`
	}
	_ = json.NewDecoder(vres.Body).Decode(&visitor)
	vres.Body.Close()

	convs := mcpJSON(t, agent, "list_conversations", map[string]any{"unread": true, "channel": "chat"})
	if convs["unread_count"].(float64) != 1 || len(arr(convs["conversations"])) != 1 {
		t.Fatalf("list_conversations: %v", convs)
	}
	convID := obj(arr(convs["conversations"])[0])["id"].(string)
	if q := mcpJSON(t, agent, "list_conversations", map[string]any{"q": "rana@" + domain}); len(arr(q["conversations"])) != 1 {
		t.Errorf("list_conversations q by email: %v", q)
	}
	thread := mcpJSON(t, agent, "get_conversation", map[string]any{"id": convID})
	if msgs := arr(thread["messages"]); len(msgs) != 1 || obj(msgs[0])["direction"] != "in" || thread["note"] == nil {
		t.Fatalf("get_conversation: %v", thread)
	}
	if obj(thread["conversation"])["unread"] != false {
		t.Errorf("opening a conversation over MCP should mark it read: %v", thread["conversation"])
	}
	rep := mcpJSON(t, agent, "reply", map[string]any{"id": convID, "body": "Yes — Tuesday at ten."})
	if obj(rep["message"])["id"] == "" || obj(rep["message"])["direction"] != "out" {
		t.Fatalf("reply: %v", rep)
	}
	// The reply went out through the chat channel: the visitor's widget sees it.
	preq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/chat/"+key+"/messages?visitor_token="+visitor.VisitorToken, nil)
	preq.Header.Set("Origin", origin)
	pres, err := http.DefaultClient.Do(preq)
	if err != nil {
		t.Fatal(err)
	}
	var poll struct {
		Messages []struct{ Body, Direction string } `json:"messages"`
	}
	_ = json.NewDecoder(pres.Body).Decode(&poll)
	pres.Body.Close()
	if len(poll.Messages) != 2 || poll.Messages[1].Body != "Yes — Tuesday at ten." {
		t.Fatalf("the visitor's widget does not show the MCP reply: %+v", poll.Messages)
	}
	assigned := obj(mcpJSON(t, agent, "assign_conversation", map[string]any{"id": convID, "assignee": aliceEmail})["conversation"])
	if assigned["assignee_user_id"] != aliceID {
		t.Fatalf("assign_conversation by email: %v", assigned)
	}
	if txt := mcpRefused(t, agent, "assign_conversation", map[string]any{"id": convID, "assignee": "nobody@example.test"}); !strings.Contains(txt, "not an active member") {
		t.Errorf("assigning a stranger: %s", txt)
	}
	closed := obj(mcpJSON(t, agent, "close_conversation", map[string]any{"id": convID})["conversation"])
	if closed["status"] != "closed" {
		t.Fatalf("close_conversation: %v", closed)
	}
	// Link the chat to the contact, so the company timeline can find it.
	alice.must("PATCH", "/api/w/conversations/"+convID, map[string]any{"contact_id": ctID}, 200)

	tl := mcpJSON(t, agent, "company_timeline", map[string]any{"id": coID})
	types := map[string]bool{}
	for _, e := range arr(tl["entries"]) {
		types[obj(e)["type"].(string)+":"+obj(e)["kind"].(string)] = true
		if obj(e)["id"] == "" {
			t.Errorf("a timeline entry without an id: %v", e)
		}
	}
	for _, want := range []string{"activity:call", "conversation:chat", "deal:opened", "deal:won"} {
		if !types[want] {
			t.Errorf("company_timeline is missing %s: %v", want, tl)
		}
	}
	gc := mcpJSON(t, agent, "get_company", map[string]any{"id": coID})
	if len(arr(gc["contacts"])) != 1 || gc["deals_total"].(float64) != 1 || gc["activities_total"].(float64) != 1 {
		t.Errorf("get_company: %v", gc)
	}
	// The timeline tool pages: one activity per page walks the same entries.
	var pagedActivities int
	for cursor, pages := "", 0; pages < 10; pages++ {
		args := map[string]any{"id": coID, "limit": 1}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page := mcpJSON(t, agent, "company_timeline", args)
		for _, e := range arr(page["entries"]) {
			if obj(e)["type"] == "activity" {
				pagedActivities++
			}
		}
		if cursor, _ = page["next_cursor"].(string); cursor == "" {
			break
		}
	}
	if pagedActivities != 1 {
		t.Errorf("company_timeline paged %d activities, want 1", pagedActivities)
	}
	if la := mcpJSON(t, agent, "list_activities", map[string]any{"deal_id": dealID}); la["total"].(float64) != 1 || len(arr(la["activities"])) != 1 {
		t.Errorf("list_activities: %v", la)
	}
	if txt := mcpRefused(t, agent, "list_activities", map[string]any{"deal_id": dealID, "contact_id": ctID}); !strings.Contains(txt, "exactly one") {
		t.Errorf("list_activities with two records: %s", txt)
	}
	// A token without inbox:read sees no conversations on a CRM timeline,
	// however broad its member's role (alice is the owner).
	crmOnly, _ := mint(t, alice, "crm only", []string{"contacts:read"})
	for _, e := range arr(mcpJSON(t, crmOnly, "company_timeline", map[string]any{"id": coID})["entries"]) {
		if obj(e)["type"] == "conversation" {
			t.Errorf("a token without inbox:read read a conversation: %v", e)
		}
	}

	// ---- MH-380 on MH-379/383: snooze, CRM links, canned replies, stats ----------
	agent, _ = mint(t, alice, "surface agent (inbox)", allUserScopes())
	until := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	sn := obj(mcpJSON(t, agent, "snooze_conversation", map[string]any{"id": convID, "until": until})["conversation"])
	if sn["status"] != "snoozed" || sn["snoozed_until"] == nil {
		t.Fatalf("snooze_conversation: %v", sn)
	}
	if open := arr(mcpJSON(t, agent, "list_conversations", map[string]any{"status": "open"})["conversations"]); len(open) != 0 {
		t.Errorf("a snoozed conversation is still open: %v", open)
	}
	if w := obj(mcpJSON(t, agent, "snooze_conversation", map[string]any{"id": convID, "wake": true})["conversation"]); w["status"] != "open" || w["snoozed_until"] != nil {
		t.Errorf("waking a conversation: %v", w)
	}
	if u := obj(mcpJSON(t, agent, "link_conversation_contact", map[string]any{"id": convID, "contact_id": ""})["conversation"]); u["contact_id"] != nil || u["contact_link"] != "manual" {
		t.Errorf("unlinking: %v", u)
	}
	inboxOnly, _ := mint(t, alice, "inbox only", []string{"inbox:read", "inbox:write"})
	if txt := mcpRefused(t, inboxOnly, "create_contact_from_conversation", map[string]any{"id": convID, "email": "x@example.test"}); !strings.Contains(txt, "contacts:write") {
		t.Errorf("create_contact_from_conversation without contacts:write: %s", txt)
	}
	made := mcpJSON(t, agent, "create_contact_from_conversation", map[string]any{"id": convID, "email": "rana@" + domain, "name": "Rana"})
	if made["contact_id"] != ctID || made["contact_created"] != false || obj(made["conversation"])["contact_id"] != ctID {
		t.Fatalf("create_contact_from_conversation (an existing email links, not duplicates): %v", made)
	}
	od := mcpJSON(t, agent, "open_deal_from_conversation", map[string]any{"id": convID, "value": 900})
	odID, _ := od["deal_id"].(string)
	if odID == "" || od["activity_id"] == "" || obj(obj(od["conversation"])["deal"])["id"] != odID {
		t.Fatalf("open_deal_from_conversation: %v", od)
	}
	odDeal := obj(mcpJSON(t, agent, "get_deal", map[string]any{"id": odID})["deal"])
	if obj(odDeal["company"])["id"] != coID || odDeal["value_minor"].(float64) != 90000 {
		t.Errorf("the deal from the conversation: %v", odDeal)
	}
	if txt := mcpRefused(t, agent, "open_deal_from_conversation", map[string]any{"id": convID}); !strings.Contains(txt, "already opened a deal") {
		t.Errorf("a second deal from one conversation: %s", txt)
	}
	alice.must("POST", "/api/w/canned_replies", map[string]any{"title": "Thanks", "body_en": "Thanks for writing.", "body_ar": "شكرًا لتواصلك."}, 201)
	if cr := mcpJSON(t, agent, "list_canned_replies", nil); len(arr(cr["replies"])) != 1 || obj(arr(cr["replies"])[0])["id"] == "" {
		t.Errorf("list_canned_replies: %v", cr)
	}
	if st := mcpJSON(t, agent, "inbox_stats", map[string]any{"days": 7}); st == nil || len(st) == 0 {
		t.Errorf("inbox_stats: %v", st)
	}
	if gcv := mcpJSON(t, agent, "get_conversation", map[string]any{"id": convID}); gcv["reply_language"] == "" || gcv["visitor"] == nil {
		t.Errorf("get_conversation lacks the reply language or the visitor: %v", gcv)
	}

	// ---- MH-380: notifications --------------------------------------------------
	// A fresh token per half: the MCP endpoint is rate limited per token
	// (120 calls a minute), and this test makes more calls than that.
	agent, _ = mint(t, alice, "surface agent 2", allUserScopes())
	created := mcpJSON(t, agent, "create_webhook_source", map[string]any{"name": "Surface hook"})
	src := obj(created["source"])
	srcID, hookURL, secret := src["id"].(string), src["url"].(string), created["secret"].(string)
	if srcID == "" || !strings.HasSuffix(hookURL, "/api/hooks/"+srcID) || secret == "" {
		t.Fatalf("create_webhook_source: %v", created)
	}
	deliver := func(secret, title string) int {
		raw := []byte(`{"title":"` + title + `","severity":"warning"}`)
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(stamp + "."))
		mac.Write(raw)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/hooks/"+srcID, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Timestamp", stamp)
		req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for i := 1; i <= 3; i++ {
		if code := deliver(secret, "Deploy "+strconv.Itoa(i)); code != 202 {
			t.Fatalf("delivery %d: %d", i, code)
		}
	}
	sources := mcpJSON(t, agent, "list_webhook_sources", nil)
	if s := obj(arr(sources["sources"])[0]); s["id"] != srcID || s["url"] != hookURL || s["delivery_count"].(float64) != 3 {
		t.Fatalf("list_webhook_sources: %v", sources)
	}
	page1 := mcpJSON(t, agent, "list_notifications", map[string]any{"limit": 2})
	if page1["unread_count"].(float64) != 3 || len(arr(page1["items"])) != 2 || page1["next_cursor"] == "" {
		t.Fatalf("list_notifications page 1: %v", page1)
	}
	page2 := mcpJSON(t, agent, "list_notifications", map[string]any{"limit": 2, "cursor": page1["next_cursor"]})
	if len(arr(page2["items"])) != 1 || page2["next_cursor"] != "" {
		t.Fatalf("list_notifications page 2: %v", page2)
	}
	itemID := obj(arr(page1["items"])[0])["id"].(string)
	if obj(arr(page1["items"])[0])["title"] != "Deploy 3" {
		t.Errorf("newest first: %v", page1)
	}
	detail := obj(mcpJSON(t, agent, "get_notification", map[string]any{"id": itemID})["item"])
	if !strings.Contains(detail["payload"].(string), "Deploy 3") || detail["severity"] != "warning" {
		t.Fatalf("get_notification: %v", detail)
	}
	if it := obj(mcpJSON(t, agent, "mark_notification", map[string]any{"id": itemID})["item"]); it["read"] != true {
		t.Fatalf("mark_notification read: %v", it)
	}
	if it := obj(mcpJSON(t, agent, "mark_notification", map[string]any{"id": itemID, "read": false})["item"]); it["read"] != false {
		t.Fatalf("mark_notification unread: %v", it)
	}
	if it := obj(mcpJSON(t, agent, "save_notification", map[string]any{"id": itemID})["item"]); it["saved"] != true {
		t.Fatalf("save_notification: %v", it)
	}
	saved := mcpJSON(t, agent, "saved_notifications", nil)
	if len(arr(saved["items"])) != 1 || obj(arr(saved["items"])[0])["id"] != itemID {
		t.Fatalf("saved_notifications: %v", saved)
	}
	if n := mcpJSON(t, agent, "mark_all_notifications_read", map[string]any{"source_id": srcID}); n["marked"].(float64) != 3 {
		t.Fatalf("mark_all_notifications_read: %v", n)
	}
	if left := mcpJSON(t, agent, "list_notifications", nil); len(arr(left["items"])) != 0 {
		t.Errorf("unread after mark all: %v", left)
	}
	if again := obj(mcpJSON(t, agent, "saved_notifications", nil)); len(arr(again["items"])) != 1 {
		t.Errorf("marking all read unsaved an item: %v", again)
	}
	rotated := mcpJSON(t, agent, "rotate_webhook_source", map[string]any{"id": srcID})
	newSecret := rotated["secret"].(string)
	if newSecret == "" || newSecret == secret {
		t.Fatalf("rotate_webhook_source: %v", rotated)
	}
	if code := deliver(secret, "with the old secret"); code != http.StatusUnauthorized {
		t.Errorf("the old secret still verifies after rotation: %d", code)
	}
	if code := deliver(newSecret, "with the new secret"); code != 202 {
		t.Errorf("the new secret does not verify: %d", code)
	}
	if s := obj(mcpJSON(t, agent, "disable_webhook_source", map[string]any{"id": srcID})["source"]); s["enabled"] != false {
		t.Fatalf("disable_webhook_source: %v", s)
	}
	if code := deliver(newSecret, "while disabled"); code != http.StatusForbidden {
		t.Errorf("a disabled source accepted a delivery: %d", code)
	}
	if s := obj(mcpJSON(t, agent, "enable_webhook_source", map[string]any{"id": srcID})["source"]); s["enabled"] != true {
		t.Fatalf("enable_webhook_source: %v", s)
	}

	// ---- MH-386: trends, on the trends center (MH-385/387) ----------------------
	if txt := mcpRefused(t, agent, "add_trend_source", map[string]any{"url": "ftp://nope"}); !strings.Contains(txt, "http") {
		t.Errorf("a non-http feed URL: %s", txt)
	}
	if txt := mcpRefused(t, agent, "add_trend_source", map[string]any{"catalog_key": "no-such-entry"}); !strings.Contains(txt, "no catalog source") {
		t.Errorf("an unknown catalog key: %s", txt)
	}
	added := mcpJSON(t, agent, "add_trend_source", map[string]any{"url": sf.feedURL})
	feedSrc := obj(added["source"])
	feedID := feedSrc["id"].(string)
	if added["added"].(float64) != 3 || feedSrc["name"] != "Surface Wire" || feedSrc["interval_minutes"].(float64) != 720 || feedSrc["provider"] != "feed" {
		t.Fatalf("add_trend_source by URL: %v", added)
	}
	// From the catalog, by key, with its own interval.
	var hnEntry map[string]any
	for _, e := range arr(mcpJSON(t, agent, "list_trend_catalog", nil)["entries"]) {
		if obj(e)["key"] == "hackernews" {
			hnEntry = obj(e)
		}
	}
	if hnEntry == nil || hnEntry["source_id"] != nil {
		t.Fatalf("list_trend_catalog has no untaken hackernews entry: %v", hnEntry)
	}
	hn := mcpJSON(t, agent, "add_trend_source", map[string]any{"catalog_key": "hackernews", "interval_minutes": 1440})
	hnSrc := obj(hn["source"])
	hnID := hnSrc["id"].(string)
	if hnSrc["provider"] != "hn" || hnSrc["catalog_key"] != "hackernews" || hnSrc["interval_minutes"].(float64) != 1440 || hn["added"].(float64) != 2 {
		t.Fatalf("add_trend_source from the catalog: %v", hn)
	}
	for _, e := range arr(mcpJSON(t, agent, "list_trend_catalog", nil)["entries"]) {
		if obj(e)["key"] == "hackernews" && obj(e)["source_id"] != hnID {
			t.Errorf("the catalog does not show hackernews as added: %v", e)
		}
	}
	if s := mcpJSON(t, agent, "list_trend_sources", nil); len(arr(s["sources"])) != 2 {
		t.Fatalf("list_trend_sources: %v", s)
	}
	if txt := mcpRefused(t, agent, "update_trend_source", map[string]any{"id": feedID, "interval_minutes": 100}); !strings.Contains(txt, "360, 720 or 1440") {
		t.Errorf("a bad interval: %s", txt)
	}
	if s := obj(mcpJSON(t, agent, "update_trend_source", map[string]any{"id": feedID, "interval_minutes": 360, "name": "Wire"})["source"]); s["interval_minutes"].(float64) != 360 || s["name"] != "Wire" {
		t.Fatalf("update_trend_source: %v", s)
	}

	// Trending topics: the feed's and Hacker News's shared headlines were
	// clustered when the sources were added (recluster after collection).
	tt := mcpJSON(t, agent, "list_trending_topics", nil)
	topics := arr(tt["topics"])
	if len(topics) == 0 || obj(tt["counts"])["open"].(float64) != float64(len(topics)) {
		t.Fatalf("list_trending_topics: %v", tt)
	}
	var cross map[string]any
	for _, x := range topics {
		if obj(x)["source_count"].(float64) >= 2 {
			cross = obj(x)
			break
		}
	}
	if cross == nil {
		t.Fatalf("no trending topic was clustered across the feed and Hacker News: %v", tt)
	}
	topicID := cross["id"].(string)
	topicDetail := mcpJSON(t, agent, "get_trending_topic", map[string]any{"id": topicID})
	if len(arr(topicDetail["items"])) < 2 || len(arr(topicDetail["outlets"])) < 2 || obj(topicDetail["topic"])["id"] != topicID {
		t.Fatalf("get_trending_topic: %v", topicDetail)
	}
	inTopic := mcpJSON(t, agent, "list_trend_items", map[string]any{"trending_topic_id": topicID})
	if len(arr(inTopic["items"])) != len(arr(topicDetail["items"])) {
		t.Errorf("list_trend_items by trending topic: %v", inTopic)
	}
	if r := obj(mcpJSON(t, agent, "review_trending_topic", map[string]any{"id": topicID, "reviewed": true})["topic"]); r["state"] != "reviewed" || r["reviewed_at"] == nil {
		t.Fatalf("review_trending_topic: %v", r)
	}
	for _, x := range arr(mcpJSON(t, agent, "list_trending_topics", nil)["topics"]) {
		if obj(x)["id"] == topicID {
			t.Error("a reviewed topic is still in the open list")
		}
	}
	if rv := arr(mcpJSON(t, agent, "list_trending_topics", map[string]any{"state": "reviewed"})["topics"]); len(rv) != 1 || obj(rv[0])["id"] != topicID {
		t.Errorf("the reviewed list: %v", rv)
	}
	if r := obj(mcpJSON(t, agent, "review_trending_topic", map[string]any{"id": topicID, "saved": true})["topic"]); r["state"] != "saved" {
		t.Errorf("saving a topic: %v", r)
	}
	if r := obj(mcpJSON(t, agent, "review_trending_topic", map[string]any{"id": topicID, "dismissed": true})["topic"]); r["state"] != "dismissed" || r["saved_at"] != nil {
		t.Errorf("dismissing takes a topic out of saved: %v", r)
	}

	// Items: pages, search, save.
	p1 := mcpJSON(t, agent, "list_trend_items", map[string]any{"limit": 2, "source_id": feedID})
	if len(arr(p1["items"])) != 2 || p1["has_more"] != true || p1["next_cursor"] == "" {
		t.Fatalf("list_trend_items page 1: %v", p1)
	}
	p2 := mcpJSON(t, agent, "list_trend_items", map[string]any{"limit": 2, "source_id": feedID, "cursor": p1["next_cursor"]})
	if len(arr(p2["items"])) != 1 || p2["has_more"] != false {
		t.Fatalf("list_trend_items page 2: %v", p2)
	}
	if q := mcpJSON(t, agent, "list_trend_items", map[string]any{"q": "story 3"}); len(arr(q["items"])) != 1 {
		t.Errorf("list_trend_items q: %v", q)
	}
	newsID := obj(arr(p1["items"])[0])["id"].(string)
	if m := mcpJSON(t, agent, "mark_trend_item", map[string]any{"id": newsID}); m["saved"] != true {
		t.Fatalf("mark_trend_item: %v", m)
	}
	if sv := mcpJSON(t, agent, "list_saved_trend_items", nil); len(arr(sv["items"])) != 1 || obj(arr(sv["items"])[0])["id"] != newsID {
		t.Fatalf("list_saved_trend_items: %v", sv)
	}
	if m := mcpJSON(t, agent, "mark_trend_item", map[string]any{"id": newsID, "saved": false}); m["saved"] != false {
		t.Fatalf("unsave: %v", m)
	}

	// The budget: each add fetched once, so two of today's fetches are spent.
	budget := obj(mcpJSON(t, agent, "trend_budget", nil)["budget"])
	if budget["fetches_today"].(float64) != 2 || budget["fetch_budget_per_day"].(float64) != float64(trends.DefaultBudget) {
		t.Fatalf("trend_budget: %v", budget)
	}
	// collect_now within the budget: both sources are fetched, the feed's new
	// item arrives, and the workspace is reclustered.
	coll := mcpJSON(t, agent, "collect_now", nil)
	if len(arr(coll["sources"])) != 2 || coll["failed"].(float64) != 0 || coll["over_budget"].(float64) != 0 ||
		coll["added"].(float64) < 1 || coll["clustered"] != true || obj(coll["budget"])["fetches_today"].(float64) != 4 {
		t.Fatalf("collect_now: %v", coll)
	}
	// Every item collected so far has been placed into a trending topic.
	for _, x := range arr(mcpJSON(t, agent, "list_trend_items", map[string]any{"limit": 200})["items"]) {
		if obj(x)["trending_topic_id"] == nil {
			t.Errorf("an item was not clustered: %v", x)
		}
	}
	// collect_now at the edge of the budget: one fetch left for two sources.
	alice.must("PATCH", "/api/w/trend_settings", map[string]any{"fetch_budget_per_day": 5}, 200)
	coll = mcpJSON(t, agent, "collect_now", nil)
	if coll["over_budget"].(float64) != 1 || obj(coll["budget"])["fetches_today"].(float64) != 5 {
		t.Fatalf("collect_now past the budget: %v", coll)
	}
	statuses := map[string]bool{}
	for _, x := range arr(coll["sources"]) {
		statuses[obj(x)["status"].(string)] = true
	}
	if !statuses["ok"] || !statuses["over_budget"] {
		t.Errorf("one source should be fetched and one refused: %v", coll)
	}
	// The same collection over REST, with the budget spent: nothing is fetched.
	feed.mu.Lock()
	before := feed.polls
	feed.mu.Unlock()
	restColl := alice.must("POST", "/api/w/feed_sources/collect", nil, 200)
	feed.mu.Lock()
	after := feed.polls
	feed.mu.Unlock()
	if restColl["over_budget"].(float64) != 2 || restColl["added"].(float64) != 0 || after != before {
		t.Fatalf("POST /api/w/feed_sources/collect with the budget spent: %v (feed polled %d -> %d)", restColl, before, after)
	}
	// A disabled source is not collected at all.
	alice.must("PATCH", "/api/w/trend_settings", map[string]any{"fetch_budget_per_day": 100}, 200)
	mcpJSON(t, agent, "update_trend_source", map[string]any{"id": hnID, "enabled": false})
	if c := mcpJSON(t, agent, "collect_now", nil); len(arr(c["sources"])) != 1 || obj(arr(c["sources"])[0])["id"] != feedID {
		t.Errorf("collect_now fetched a disabled source: %v", c)
	}

	// ---- MH-409: WhatsApp ---------------------------------------------------------
	st := mcpJSON(t, agent, "whatsapp_status", nil)
	if st["linked"] != false || obj(st["webhook"])["status"] != "not_linked" {
		t.Fatalf("whatsapp_status before linking: %v", st)
	}
	link := mcpJSON(t, agent, "whatsapp_link", map[string]any{"number": "+20 101 555 9999"})
	if link["qr_code"] != "2@QR-PAYLOAD" || link["pairing_code"] != "PAIR-9999" {
		t.Fatalf("whatsapp_link: %v", link)
	}
	st = mcpJSON(t, agent, "whatsapp_status", nil)
	if st["linked"] != true || st["state"] != "open" || obj(st["webhook"])["status"] != "ok" || st["sent_today"].(float64) != 0 {
		t.Fatalf("whatsapp_status after linking: %v", st)
	}

	// MH-320: no opt-in, no message.
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Hi Rana"}); !strings.Contains(txt, "not opted in") {
		t.Fatalf("send_whatsapp without an opt-in: %s", txt)
	}
	alice.must("POST", "/api/w/contacts/"+ctID+"/whatsapp/opt-in", nil, 200)
	first := mcpJSON(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Hi Rana, the pilot is confirmed."})
	code6, _ := first["code"].(string)
	if first["confirmation_required"] != true || len(code6) != 6 || first["sent"] != false || len(evo.sends()) != 0 {
		t.Fatalf("send_whatsapp to a new thread must ask for a code first: %v (sends %v)", first, evo.sends())
	}
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "A different text", "code": code6}); !strings.Contains(txt, "not valid") {
		t.Errorf("a code used for another text: %s", txt)
	}
	sent := mcpJSON(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Hi Rana, the pilot is confirmed.", "code": code6})
	waConv, _ := sent["conversation_id"].(string)
	if sent["sent"] != true || sent["new_thread"] != true || waConv == "" {
		t.Fatalf("send_whatsapp with the code: %v", sent)
	}
	// A code is bound to its contact as well as its text.
	other := obj(mcpJSON(t, agent, "create_contact", map[string]any{"email": "omar@" + domain, "phone": "+20 101 555 0202"})["contact"])["id"].(string)
	alice.must("POST", "/api/w/contacts/"+other+"/whatsapp/opt-in", nil, 200)
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": other, "text": "Hi Rana, the pilot is confirmed.", "code": code6}); !strings.Contains(txt, "not valid") {
		t.Errorf("rana's code used for omar: %s", txt)
	}
	if s := evo.sends(); len(s) != 1 || s[0].Number != "201015550101" || s[0].Text != "Hi Rana, the pilot is confirmed." {
		t.Fatalf("Evolution received %+v", s)
	}
	// The thread exists now: the next message needs no code, and is paced.
	next := mcpJSON(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Bring the demo unit."})
	if next["sent"] != true || next["new_thread"] != false || next["conversation_id"] != waConv {
		t.Fatalf("send_whatsapp on an existing thread: %v", next)
	}
	// Reading and replying to the WhatsApp thread is the inbox tools' job.
	waThread := mcpJSON(t, agent, "get_conversation", map[string]any{"id": waConv})
	if c := obj(waThread["conversation"]); c["channel"] != "whatsapp" || c["contact_id"] != ctID || len(arr(waThread["messages"])) != 2 {
		t.Fatalf("the WhatsApp thread: %v", waThread)
	}
	mcpJSON(t, agent, "reply", map[string]any{"id": waConv, "body": "See you Tuesday."})
	s := evo.sends()
	if len(s) != 3 || s[2].Text != "See you Tuesday." {
		t.Fatalf("the reply did not go out on WhatsApp: %+v", s)
	}
	for i := 1; i < len(s); i++ {
		if gap := s[i].At.Sub(s[i-1].At); gap < 1150*time.Millisecond {
			t.Errorf("sends %d and %d were %v apart; WhatsApp sends are paced", i-1, i, gap)
		}
	}
	if st := mcpJSON(t, agent, "whatsapp_status", nil); st["sent_today"].(float64) != 3 {
		t.Errorf("whatsapp_status sent_today: %v", st)
	}
	// A STOP outranks the opt-in.
	if _, err := sf.sys.Exec(`UPDATE contacts SET whatsapp_opt_out_at = now() WHERE id = $1`, ctID); err != nil {
		t.Fatal(err)
	}
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "One more thing"}); !strings.Contains(txt, "asked to stop") {
		t.Errorf("send_whatsapp to an opted-out contact: %s", txt)
	}
	if len(evo.sends()) != 3 {
		t.Error("a message reached an opted-out contact")
	}

	// ---- scopes: a token without the scope is refused, naming it -----------------
	narrow, _ := mint(t, alice, "narrow", []string{"members:read"})
	byName := map[string]string{}
	for _, tool := range mcp.UserTools(&research.Engine{}) {
		byName[tool.Name] = tool.Scope
	}
	for _, name := range []string{"list_conversations", "reply", "close_conversation", "assign_conversation", "get_conversation",
		"list_notifications", "get_notification", "mark_notification", "save_notification", "rotate_webhook_source", "disable_webhook_source",
		"list_notification_sources", "add_notification_source", "update_notification_source", "collect_notification_source",
		"get_contact", "search_contacts", "create_contact", "update_contact", "get_company", "search_companies", "update_company",
		"get_deal", "update_deal", "company_timeline", "list_trend_items", "mark_trend_item", "collect_now", "add_trend_source",
		"trend_budget", "list_trending_topics", "get_trending_topic", "review_trending_topic",
		"list_activities", "snooze_conversation", "link_conversation_contact", "create_contact_from_conversation",
		"open_deal_from_conversation", "list_canned_replies", "inbox_stats",
		"whatsapp_status", "whatsapp_link", "send_whatsapp"} {
		scope := byName[name]
		if scope == "" {
			t.Errorf("%s is not registered", name)
			continue
		}
		if txt := mcpRefused(t, narrow, name, map[string]any{"id": convID}); !strings.Contains(txt, scope) {
			t.Errorf("%s refused without naming %s: %s", name, scope, txt)
		}
	}
	// The read scope does not write.
	reader, _ := mint(t, alice, "reader", []string{"inbox:read", "notifications:read", "trends:read", "contacts:read"})
	for name, args := range map[string]map[string]any{
		"reply":                 {"id": convID, "body": "x"},
		"mark_notification":     {"id": itemID},
		"rotate_webhook_source": {"id": srcID},
		"mark_trend_item":       {"id": newsID},
		"review_trending_topic": {"id": topicID, "reviewed": true},
		"collect_now":           {},
		"update_contact":        {"id": ctID, "title": "x"},
		"send_whatsapp":         {"contact_id": ctID, "text": "x"},
	} {
		if txt := mcpRefused(t, reader, name, args); !strings.Contains(txt, "scope") {
			t.Errorf("a read-only token ran %s: %s", name, txt)
		}
	}
}

// TestMCPTokenAdmin: MH-396.
func TestMCPTokenAdmin(t *testing.T) {
	sf := bootSurface(t)
	alice, wsID := sf.alice, sf.ws
	bob := newClient(t, sf.srv.URL)
	bobEmail := "bob-" + rnd() + "@example.test"
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-8"}, 201)
	inv := alice.must("POST", "/api/w/invitations", map[string]string{"email": bobEmail, "role": "member"}, 201)
	bob.must("POST", "/api/invitations/accept", map[string]string{"token": strings.TrimPrefix(inv["accept_url"].(string), "/invite/")}, 200)
	bob.ws = wsID

	// A member cannot mint a manager's scope.
	if code, b := bob.do("POST", "/api/w/mcp_tokens", map[string]any{"name": "too much", "scopes": []string{"inbox:manage"}}); code != 422 {
		t.Errorf("a member minting inbox:manage: %d %s", code, b)
	}
	bobAgent, bobTokID := mint(t, bob, "bob's agent", []string{"members:read", "inbox:read"})
	_, aliceTokID := mint(t, alice, "alice's agent", []string{"members:read"})
	if _, _, isErr := mcpCall(bobAgent, "/api/mcp", "whoami", nil); isErr {
		t.Fatal("bob's token does not work")
	}

	// The owner sees every member's token, with who holds it.
	var all struct {
		All    bool `json:"all"`
		Tokens []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			UserEmail  string `json:"user_email"`
			Mine       bool   `json:"mine"`
			Scopes     []string
			CreatedAt  time.Time  `json:"created_at"`
			LastUsedAt *time.Time `json:"last_used_at"`
		} `json:"tokens"`
	}
	code, b := alice.do("GET", "/api/w/mcp_tokens", nil)
	if code != 200 || json.Unmarshal(b, &all) != nil {
		t.Fatalf("owner's list: %d %s", code, b)
	}
	seen := map[string]bool{}
	for _, tk := range all.Tokens {
		seen[tk.ID] = true
		if tk.ID == bobTokID && (tk.Mine || !strings.EqualFold(tk.UserEmail, bobEmail) || len(tk.Scopes) != 2 || tk.LastUsedAt == nil) {
			t.Errorf("bob's token as the owner sees it: %+v", tk)
		}
	}
	if !all.All || !seen[bobTokID] || !seen[aliceTokID] {
		t.Fatalf("the owner must see every member's token: %s", b)
	}

	// A member sees only their own.
	code, b = bob.do("GET", "/api/w/mcp_tokens", nil)
	if code != 200 || strings.Contains(string(b), aliceTokID) || !strings.Contains(string(b), bobTokID) || !strings.Contains(string(b), `"all":false`) {
		t.Fatalf("a member's list: %d %s", code, b)
	}
	// ...and cannot revoke someone else's.
	if code, _ := bob.do("DELETE", "/api/w/mcp_tokens/"+aliceTokID, nil); code != 404 {
		t.Errorf("a member revoking the owner's token: %d", code)
	}

	// The owner revokes bob's token: it stops working on the very next call.
	alice.must("DELETE", "/api/w/mcp_tokens/"+bobTokID, nil, 204)
	if code, _, _ := mcpCall(bobAgent, "/api/mcp", "whoami", nil); code != http.StatusUnauthorized {
		t.Fatalf("a revoked token answered %d, want 401", code)
	}
}
