package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

// TestWhatsAppReactionsAndQuotes covers MH-664 end to end through the real
// routes, the real webhook and a fake Evolution API: a reaction goes out as
// sendReaction with the target's key, toggles and replaces, arrives from the
// customer and from the linked phone without ever becoming a message row, a
// reply quotes its target on sendText, an inbound quoted reply is linked to
// the message it quotes, and none of it reaches across workspaces.
func TestWhatsAppReactionsAndQuotes(t *testing.T) {
	old := whatsapp.OwnDelay
	whatsapp.OwnDelay = 0
	t.Cleanup(func() { whatsapp.OwnDelay = old })

	sf := bootSurface(t)
	alice, a, ws := sf.alice, sf.app, sf.ws
	sctx := tenant.WithWorkspace(context.Background(), ws)
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)
	link, err := whatsapp.Load(sctx, a, ws)
	if err != nil {
		t.Fatal(err)
	}
	post := func(message map[string]any, fromMe bool, jid, id string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"event": "messages.upsert", "instance": link.Instance, "data": map[string]any{
			"key":      map[string]any{"remoteJid": jid, "fromMe": fromMe, "id": id},
			"pushName": "Mona", "message": message, "messageTimestamp": time.Now().Unix()}})
		req, _ := http.NewRequest("POST", sf.srv.URL+"/api/whatsapp/hook/"+link.HookID, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(whatsapp.TokenHeader, link.Token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode/100 != 2 {
			t.Fatalf("hook: %d %s", res.StatusCode, body)
		}
	}
	const customer = "201700006464"
	jid := customer + "@s.whatsapp.net"
	count := func() (n int) {
		t.Helper()
		_ = a.SysDB.QueryRow(`SELECT count(*) FROM messages WHERE workspace_id = $1`, ws).Scan(&n)
		return n
	}
	idByExt := func(ext string) (id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if a.SysDB.QueryRow(`SELECT id FROM messages WHERE workspace_id = $1 AND external_id = $2`, ws, ext).Scan(&id) == nil {
				return id
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("no message with external id %s", ext)
		return ""
	}

	type reaction struct {
		Emoji    string `json:"emoji"`
		Count    int    `json:"count"`
		Mine     bool   `json:"mine"`
		Reactors []struct {
			Kind   string `json:"kind"`
			UserID string `json:"user_id"`
		} `json:"reactors"`
	}
	type message struct {
		ID        string     `json:"id"`
		Body      string     `json:"body"`
		Direction string     `json:"direction"`
		Reactions []reaction `json:"reactions"`
		ReplyTo   *struct {
			MessageID *string `json:"message_id"`
			Body      string  `json:"body"`
			Direction string  `json:"direction"`
		} `json:"reply_to"`
	}
	thread := func(convID string) map[string]message {
		t.Helper()
		raw, _ := json.Marshal(alice.must("GET", "/api/w/conversations/"+convID, nil, 200))
		var got struct {
			Messages []message `json:"messages"`
		}
		_ = json.Unmarshal(raw, &got)
		out := map[string]message{}
		for _, m := range got.Messages {
			out[m.ID] = m
		}
		return out
	}
	lastBody := func(prefix string) map[string]any {
		t.Helper()
		bs := sf.evo.sentBodies()
		for i := len(bs) - 1; i >= 0; i-- {
			if strings.HasPrefix(bs[i].Path, prefix) {
				return bs[i].Body
			}
		}
		t.Fatalf("nothing was sent to %s", prefix)
		return nil
	}

	// The customer writes.
	inExt := "WA-IN-" + rnd()
	post(map[string]any{"conversation": "is the red one in stock?"}, false, jid, inExt)
	inID := idByExt(inExt)
	var convID string
	_ = a.SysDB.QueryRow(`SELECT conversation_id FROM messages WHERE id = $1`, inID).Scan(&convID)

	// 1. We react 👍 — out as sendReaction with the customer message's key.
	sentBefore := len(sf.evo.sentBodies())
	res := alice.must("PUT", "/api/w/messages/"+inID+"/reaction", map[string]any{"emoji": "👍"}, 200)
	if res["emoji"] != "👍" || res["delivered"] != true {
		t.Errorf("react: %+v", res)
	}
	body := lastBody("/message/sendReaction/")
	key, _ := body["key"].(map[string]any)
	if body["reaction"] != "👍" || key["id"] != inExt || key["fromMe"] != false || key["remoteJid"] != jid {
		t.Errorf("sendReaction body: %+v", body)
	}
	if m := thread(convID)[inID]; len(m.Reactions) != 1 || m.Reactions[0].Emoji != "👍" || !m.Reactions[0].Mine || m.Reactions[0].Count != 1 {
		t.Errorf("thread after reacting: %+v", m.Reactions)
	}

	// 2. A different emoji replaces it; the same one again takes it back ("").
	alice.must("PUT", "/api/w/messages/"+inID+"/reaction", map[string]any{"emoji": "❤️"}, 200)
	if b := lastBody("/message/sendReaction/"); b["reaction"] != "❤️" {
		t.Errorf("replacing: %+v", b)
	}
	if m := thread(convID)[inID]; len(m.Reactions) != 1 || m.Reactions[0].Emoji != "❤️" {
		t.Errorf("a different emoji should replace, one per member: %+v", m.Reactions)
	}
	res = alice.must("PUT", "/api/w/messages/"+inID+"/reaction", map[string]any{"emoji": "❤️"}, 200)
	if b := lastBody("/message/sendReaction/"); b["reaction"] != "" || res["emoji"] != "" {
		t.Errorf("toggling off should send \"\": %+v / %+v", b, res)
	}
	if m := thread(convID)[inID]; len(m.Reactions) != 0 {
		t.Errorf("toggled off, still shown: %+v", m.Reactions)
	}
	// DELETE with nothing on it sends nothing more.
	n := len(sf.evo.sentBodies())
	alice.must("DELETE", "/api/w/messages/"+inID+"/reaction", nil, 200)
	if len(sf.evo.sentBodies()) != n {
		t.Errorf("removing a reaction that is not there went to WhatsApp")
	}
	if got := len(sf.evo.sentBodies()) - sentBefore; got != 3 {
		t.Errorf("sendReaction calls: %d, want 3", got)
	}

	// 3. A reply quoting the customer's message: sendText carries "quoted".
	reply := alice.must("POST", "/api/w/conversations/"+convID+"/reply", map[string]any{"body": "yes — in stock", "reply_to_message_id": inID}, 201)
	rawReply, _ := json.Marshal(reply)
	var sent struct {
		Message message `json:"message"`
	}
	_ = json.Unmarshal(rawReply, &sent)
	if sent.Message.ReplyTo == nil || sent.Message.ReplyTo.MessageID == nil || *sent.Message.ReplyTo.MessageID != inID {
		t.Errorf("the sent reply does not show its quote: %s", rawReply)
	}
	tb := lastBody("/message/sendText/")
	q, _ := tb["quoted"].(map[string]any)
	qk, _ := q["key"].(map[string]any)
	qm, _ := q["message"].(map[string]any)
	if tb["text"] != "yes — in stock" || qk["id"] != inExt || qm["conversation"] != "is the red one in stock?" {
		t.Errorf("sendText quoted payload: %+v", tb)
	}
	outID := sent.Message.ID
	var outExt string
	_ = a.SysDB.QueryRow(`SELECT external_id FROM messages WHERE id = $1`, outID).Scan(&outExt)
	if outExt == "" {
		t.Fatal("the reply has no WhatsApp id")
	}
	if m := thread(convID)[outID]; m.ReplyTo == nil || m.ReplyTo.Body != "is the red one in stock?" || m.ReplyTo.Direction != "in" {
		t.Errorf("thread quote: %+v", m.ReplyTo)
	}

	// 4. The customer reacts to our reply — filed on it, never a new row.
	rows := count()
	post(map[string]any{"reactionMessage": map[string]any{"key": map[string]any{"remoteJid": jid, "fromMe": true, "id": outExt}, "text": "🙏"}}, false, jid, "WA-R-"+rnd())
	// 5. And the phone itself reacts 😂 to the customer's message (fromMe).
	post(map[string]any{"reactionMessage": map[string]any{"key": map[string]any{"remoteJid": jid, "fromMe": false, "id": inExt}, "text": "😂"}}, true, jid, "WA-R-"+rnd())
	// 6. A reaction to a message Moharrik never saw is dropped, not an error.
	post(map[string]any{"reactionMessage": map[string]any{"key": map[string]any{"remoteJid": jid, "fromMe": false, "id": "WA-NEVER-SEEN"}, "text": "👍"}}, false, jid, "WA-R-"+rnd())
	if count() != rows {
		t.Errorf("a reaction became a message row: %d → %d", rows, count())
	}
	th := thread(convID)
	if m := th[outID]; len(m.Reactions) != 1 || m.Reactions[0].Emoji != "🙏" || m.Reactions[0].Mine || len(m.Reactions[0].Reactors) != 1 || m.Reactions[0].Reactors[0].Kind != "customer" {
		t.Errorf("the customer's reaction: %+v", m.Reactions)
	}
	if m := th[inID]; len(m.Reactions) != 1 || m.Reactions[0].Emoji != "😂" || !m.Reactions[0].Mine || m.Reactions[0].Reactors[0].Kind != "phone" {
		t.Errorf("the phone's own reaction: %+v", m.Reactions)
	}
	var stray int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM message_reactions WHERE workspace_id = $1`, ws).Scan(&stray)
	if stray != 2 {
		t.Errorf("reaction rows: %d, want 2 (the unknown target must not be stored)", stray)
	}
	// 7. The customer takes theirs back with "".
	post(map[string]any{"reactionMessage": map[string]any{"key": map[string]any{"remoteJid": jid, "fromMe": true, "id": outExt}, "text": ""}}, false, jid, "WA-R-"+rnd())
	if m := thread(convID)[outID]; len(m.Reactions) != 0 {
		t.Errorf("the customer's removal: %+v", m.Reactions)
	}

	// 8. The customer quotes our reply — linked to it by its WhatsApp id.
	qExt := "WA-IN-" + rnd()
	post(map[string]any{"extendedTextMessage": map[string]any{"text": "great, reserve it",
		"contextInfo": map[string]any{"stanzaId": outExt, "participant": "me@s.whatsapp.net", "quotedMessage": map[string]any{"conversation": "yes — in stock"}}}}, false, jid, qExt)
	qID := idByExt(qExt)
	if m := thread(convID)[qID]; m.Body != "great, reserve it" || m.ReplyTo == nil || m.ReplyTo.MessageID == nil || *m.ReplyTo.MessageID != outID || m.ReplyTo.Body != "yes — in stock" || m.ReplyTo.Direction != "out" {
		t.Errorf("inbound quoted reply: %+v", m.ReplyTo)
	}

	// 9. Another workspace's message can be neither reacted to nor quoted, and
	// a widget reaction stays in Moharrik (delivered=false, nothing sent).
	otherWS := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Other " + rnd()}, 201)["workspace"].(map[string]any)["id"].(string)
	alice.ws = otherWS
	wgt := alice.must("POST", "/api/w/chat_widgets", map[string]any{"name": "W", "allowed_origins": []string{"https://w.test"}}, 201)["widget"].(map[string]any)
	creq, _ := http.NewRequest("POST", sf.srv.URL+"/api/chat/"+wgt["public_key"].(string)+"/messages", strings.NewReader(`{"body":"hello from the widget","name":"Visitor"}`))
	creq.Header.Set("Content-Type", "application/json")
	creq.Header.Set("Origin", "https://w.test")
	cres, err := http.DefaultClient.Do(creq)
	if err != nil {
		t.Fatal(err)
	}
	cres.Body.Close()
	var otherConv, otherMsg string
	_ = a.SysDB.QueryRow(`SELECT c.id, m.id FROM conversations c JOIN messages m ON m.conversation_id = c.id WHERE c.workspace_id = $1`, otherWS).Scan(&otherConv, &otherMsg)
	if otherMsg == "" {
		t.Fatal("the widget message was not filed")
	}
	n = len(sf.evo.sentBodies())
	wr := alice.must("PUT", "/api/w/messages/"+otherMsg+"/reaction", map[string]any{"emoji": "👀"}, 200)
	if wr["delivered"] != false || len(sf.evo.sentBodies()) != n {
		t.Errorf("a widget reaction should stay in Moharrik: %+v", wr)
	}
	// From the WhatsApp workspace: the other workspace's message is not found.
	alice.ws = ws
	for _, try := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/w/messages/" + otherMsg + "/reaction", map[string]any{"emoji": "👍"}},
		{"DELETE", "/api/w/messages/" + otherMsg + "/reaction", nil},
		{"POST", "/api/w/conversations/" + convID + "/reply", map[string]any{"body": "x", "reply_to_message_id": otherMsg}},
	} {
		if code, b := alice.do(try.method, try.path, try.body); code != http.StatusNotFound {
			t.Errorf("%s %s across workspaces: got %d, want 404: %s", try.method, try.path, code, b)
		}
	}
	if len(sf.evo.sentBodies()) != n {
		t.Errorf("a cross-workspace attempt reached WhatsApp")
	}
	// And the other way round: the widget thread cannot quote the WhatsApp message.
	alice.ws = otherWS
	if code, b := alice.do("POST", "/api/w/conversations/"+otherConv+"/reply", map[string]any{"body": "x", "reply_to_message_id": inID}); code != http.StatusNotFound {
		t.Errorf("quoting a message of another conversation: got %d, want 404: %s", code, b)
	}
}
