package server

/*
TestWhatsAppMultiNumber covers several WhatsApp numbers in one workspace
through the real routes, webhook and MCP tools, against a stand-in
Evolution (nothing is paired):

  - adding a second number leaves the first one's instance, hook and token
    untouched, and the first stays the default;
  - a message that arrives on a number files into a thread on that number,
    so the same customer writing to two numbers has two threads;
  - a reply leaves from the thread's number; a new message goes from the
    default number unless from_number picks another;
  - each number has its own daily cap: one number at its cap still lets the
    other send;
  - list_channels shows one row per number;
  - unlinking one number leaves the others linked, and unlinking the default
    hands the default to a remaining number.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run WhatsAppMultiNumber -v
*/

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

func TestWhatsAppMultiNumber(t *testing.T) {
	sf := bootSurface(t)
	alice, a, ws, evo := sf.alice, sf.app, sf.ws, sf.evo
	sctx := tenant.WithWorkspace(context.Background(), ws)

	// ---- the first number, then a second one --------------------------------
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)
	first, err := whatsapp.LoadDefault(sctx, a, ws)
	if err != nil {
		t.Fatal(err)
	}
	added := alice.must("POST", "/api/w/whatsapp/numbers", map[string]any{"label": "Assistant"}, 200)
	secondID, _ := added["id"].(string)
	if secondID == "" || secondID == first.ID || added["label"] != "Assistant" || added["is_default"] != false {
		t.Fatalf("adding a number: %v", added)
	}
	second, err := whatsapp.LoadByID(sctx, a, ws, secondID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := whatsapp.LoadByID(sctx, a, ws, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Instance != first.Instance || again.HookID != first.HookID || again.Token != first.Token || !again.IsDefault {
		t.Fatalf("adding a number touched the first one: before %+v after %+v", first, again)
	}
	if second.Instance == first.Instance || second.HookID == first.HookID {
		t.Fatalf("the second number shares the first one's session: %+v / %+v", first, second)
	}
	const firstPhone, secondPhone = "201207860084", "201050028002"
	whatsapp.SetLinkState(sctx, a, ws, first.ID, "open", firstPhone)
	whatsapp.SetLinkState(sctx, a, ws, second.ID, "open", secondPhone)

	code, raw := alice.do("GET", "/api/w/whatsapp/numbers", nil)
	var numbers []map[string]any
	if code != 200 || json.Unmarshal(raw, &numbers) != nil || len(numbers) != 2 {
		t.Fatalf("GET numbers: %d %s", code, raw)
	}
	if numbers[0]["id"] != first.ID || numbers[0]["is_default"] != true || numbers[1]["id"] != second.ID {
		t.Errorf("numbers are not listed default first: %s", raw)
	}

	// ---- inbound: each number files into its own thread ------------------------
	hook := func(l *whatsapp.Link, jid, id, text string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"event": "messages.upsert", "instance": l.Instance, "data": map[string]any{
			"key":      map[string]any{"remoteJid": jid, "fromMe": false, "id": id},
			"pushName": "Customer", "message": map[string]any{"conversation": text}, "messageTimestamp": time.Now().Unix()}})
		req, _ := http.NewRequest("POST", sf.srv.URL+"/api/whatsapp/hook/"+l.HookID, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(whatsapp.TokenHeader, l.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode/100 != 2 {
			t.Fatalf("hook: %d", res.StatusCode)
		}
	}
	threadOn := func(linkID, phone string) string {
		t.Helper()
		var id string
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if a.SysDB.QueryRow(`SELECT id FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp'
				AND customer_phone = $2 AND whatsapp_link_id = $3`, ws, phone, linkID).Scan(&id) == nil {
				return id
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("no thread for %s on number %s", phone, linkID)
		return ""
	}
	const customer = "201700001234"
	hook(second, customer+"@s.whatsapp.net", "WA-B-"+rnd(), "hi assistant")
	onSecond := threadOn(second.ID, customer)
	hook(first, customer+"@s.whatsapp.net", "WA-A-"+rnd(), "hi main")
	onFirst := threadOn(first.ID, customer)
	if onFirst == onSecond {
		t.Fatal("the same customer on two numbers shares one thread")
	}
	conv := obj(alice.must("GET", "/api/w/conversations/"+onSecond, nil, 200)["conversation"])
	if n, _ := conv["whatsapp_number"].(map[string]any); n == nil || n["id"] != second.ID || n["label"] != "Assistant" {
		t.Errorf("the thread does not show its number: %v", conv["whatsapp_number"])
	}
	list := alice.must("GET", "/api/w/conversations?channel=whatsapp&whatsapp_number_id="+second.ID, nil, 200)
	if convs := arr(list["conversations"]); len(convs) != 1 || obj(convs[0])["id"] != onSecond {
		t.Errorf("the inbox filter by number: %v", list["conversations"])
	}

	// ---- outbound: replies leave from the thread's number ----------------------
	lastInstance := func() string {
		t.Helper()
		bodies := evo.sentBodies()
		for i := len(bodies) - 1; i >= 0; i-- {
			if strings.HasPrefix(bodies[i].Path, "/message/sendText/") {
				return strings.TrimPrefix(bodies[i].Path, "/message/sendText/")
			}
		}
		return ""
	}
	alice.must("POST", "/api/w/conversations/"+onSecond+"/reply", map[string]any{"body": "from the assistant"}, 201)
	if got := lastInstance(); got != second.Instance {
		t.Errorf("a reply on the second number's thread went from %q, want %q", got, second.Instance)
	}
	alice.must("POST", "/api/w/conversations/"+onFirst+"/reply", map[string]any{"body": "from the main number"}, 201)
	if got := lastInstance(); got != first.Instance {
		t.Errorf("a reply on the first number's thread went from %q, want %q", got, first.Instance)
	}

	// A new message: the default number, unless a sender is picked.
	if _, err := whatsapp.Send(sctx, a, ws, "201700005555", "default sender"); err != nil {
		t.Fatal(err)
	}
	if got := lastInstance(); got != first.Instance {
		t.Errorf("a new message without a sender went from %q, want the default %q", got, first.Instance)
	}
	for _, ref := range []string{second.ID, "+20 105 002 8002", "assistant"} {
		pick, err := whatsapp.Resolve(sctx, a, ws, ref)
		if err != nil || pick.ID != second.ID {
			t.Errorf("Resolve(%q) = %v, %v; want the second number", ref, pick, err)
		}
	}
	if _, err := whatsapp.Resolve(sctx, a, ws, "201000000000"); !errors.Is(err, whatsapp.ErrNoLink) {
		t.Errorf("Resolve of a number the workspace does not have: %v, want ErrNoLink", err)
	}
	if _, err := whatsapp.Send(whatsapp.WithLink(sctx, second.ID), a, ws, "201700005555", "picked sender"); err != nil {
		t.Fatal(err)
	}
	if got := lastInstance(); got != second.Instance {
		t.Errorf("a message sent from the second number went from %q", got)
	}
	// The send is counted on the number it went from (per-number caps and pacing).
	var sentFrom string
	_ = a.SysDB.QueryRow(`SELECT link_id FROM whatsapp_sends WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, ws).Scan(&sentFrom)
	if sentFrom != second.ID {
		t.Errorf("the send was counted on %q, want the second number", sentFrom)
	}

	// ---- per-number daily caps ---------------------------------------------------
	alice.must("PATCH", "/api/w/whatsapp/numbers/"+second.ID, map[string]any{"daily_cap": 1}, 200)
	if _, err := whatsapp.Send(whatsapp.WithLink(sctx, second.ID), a, ws, "201700006666", "over the cap"); !errors.Is(err, whatsapp.ErrDailyCap) {
		t.Errorf("the second number past its cap: %v, want ErrDailyCap", err)
	}
	if _, err := whatsapp.Send(whatsapp.WithLink(sctx, first.ID), a, ws, "201700006666", "the other number still sends"); err != nil {
		t.Errorf("one number at its cap stopped the other: %v", err)
	}

	// ---- list_channels: one row per number ---------------------------------------
	agent, _ := mint(t, alice, "multi number agent", allUserScopes())
	chans := mcpJSON(t, agent, "list_channels", map[string]any{"type": []string{"whatsapp"}})
	ids := map[string]bool{}
	for _, c := range arr(chans["channels"]) {
		if obj(c)["type"] == "whatsapp" {
			ids[obj(c)["id"].(string)] = true
		}
	}
	if len(ids) != 2 || !ids[first.ID] || !ids[second.ID] {
		t.Errorf("list_channels WhatsApp rows: %v", chans["channels"])
	}
	status := mcpJSON(t, agent, "whatsapp_status", map[string]any{})
	if len(arr(status["numbers"])) != 2 {
		t.Errorf("whatsapp_status numbers: %v", status["numbers"])
	}

	// ---- unlinking one number leaves the others ----------------------------------
	third := alice.must("POST", "/api/w/whatsapp/numbers", map[string]any{"label": "Brand"}, 200)["id"].(string)
	alice.must("DELETE", "/api/w/whatsapp/numbers/"+second.ID, nil, 204)
	if _, err := whatsapp.LoadByID(sctx, a, ws, second.ID); !errors.Is(err, whatsapp.ErrNoLink) {
		t.Errorf("the unlinked number is still there: %v", err)
	}
	still, err := whatsapp.LoadByID(sctx, a, ws, first.ID)
	if err != nil || !still.IsDefault || still.Instance != first.Instance || still.State != "open" {
		t.Fatalf("unlinking the second number touched the first: %+v %v", still, err)
	}
	var kept int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM conversations WHERE id = ANY($1)`, []string{onFirst, onSecond}).Scan(&kept)
	if kept != 2 {
		t.Errorf("unlinking a number removed threads: %d of 2 left", kept)
	}
	// A thread whose number is gone answers from the default number.
	alice.must("POST", "/api/w/conversations/"+onSecond+"/reply", map[string]any{"body": "after unlink"}, 201)
	if got := lastInstance(); got != first.Instance {
		t.Errorf("a thread on an unlinked number replied from %q, want the default", got)
	}

	alice.must("DELETE", "/api/w/whatsapp/numbers/"+first.ID, nil, 204)
	next, err := whatsapp.LoadDefault(sctx, a, ws)
	if err != nil || next.ID != third {
		t.Fatalf("unlinking the default: the default is now %+v %v, want the remaining number", next, err)
	}
}
