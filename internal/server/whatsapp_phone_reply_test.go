package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

// TestWhatsAppPhoneReplyReachesTheThread covers MH-665 through the real
// webhook: a reply typed on the linked phone arrives as a fromMe
// messages.upsert and lands in the customer's thread as the team's outbound
// message — it used to be dropped as "our own echo", so a thread showed only
// the customer's side.
func TestWhatsAppPhoneReplyReachesTheThread(t *testing.T) {
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
	hook := func(fromMe bool, jid, id, text string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"event": "messages.upsert", "instance": link.Instance, "data": map[string]any{
			"key":      map[string]any{"remoteJid": jid, "fromMe": fromMe, "id": id},
			"pushName": "Owner Phone", "message": map[string]any{"conversation": text}, "messageTimestamp": time.Now().Unix()}})
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
	// The own-message filing runs off the request; wait for it.
	messageBy := func(id string) (dir string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if a.SysDB.QueryRow(`SELECT direction FROM messages WHERE workspace_id = $1 AND external_id = $2`, ws, id).Scan(&dir) == nil {
				return dir
			}
			time.Sleep(50 * time.Millisecond)
		}
		return ""
	}

	const customer = "201700007777"
	in, out := "WA-IN-"+rnd(), "WA-PHONE-"+rnd()
	hook(false, customer+"@s.whatsapp.net", in, "do you deliver to Maadi?")
	if d := messageBy(in); d != "in" {
		t.Fatalf("the customer's message: %q", d)
	}
	hook(true, customer+"@s.whatsapp.net", out, "yes, same day")
	if d := messageBy(out); d != "out" {
		t.Fatalf("the reply typed on the phone was not filed as outbound: %q", d)
	}
	var convs, msgs int
	var unread bool
	if err := a.SysDB.QueryRow(`
		SELECT count(DISTINCT c.id), count(m.id), bool_or(c.unread) FROM conversations c JOIN messages m ON m.conversation_id = c.id
		WHERE c.workspace_id = $1 AND c.customer_phone = $2`, ws, customer).Scan(&convs, &msgs, &unread); err != nil {
		t.Fatal(err)
	}
	if convs != 1 || msgs != 2 || unread {
		t.Errorf("thread: %d conversations, %d messages, unread %v; want 1, 2, false", convs, msgs, unread)
	}

	// The thread as the inbox shows it: both sides, in order.
	var convID string
	_ = a.SysDB.QueryRow(`SELECT id FROM conversations WHERE workspace_id = $1 AND customer_phone = $2`, ws, customer).Scan(&convID)
	th := alice.must("GET", "/api/w/conversations/"+convID, nil, 200)
	raw, _ := json.Marshal(th)
	var got struct {
		Messages []struct {
			Direction string `json:"direction"`
			Body      string `json:"body"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &got)
	if len(got.Messages) != 2 || got.Messages[0].Direction != "in" || got.Messages[1].Direction != "out" || got.Messages[1].Body != "yes, same day" {
		t.Errorf("the inbox thread: %+v", got.Messages)
	}
}
