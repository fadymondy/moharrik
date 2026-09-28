package whatsapp

// MH-664: reactions and quoted replies — parsing what Evolution delivers, and
// the exact bodies sent back to it. The server is a FAKE (httptest); nothing
// here reaches a real WhatsApp.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func reactionOf(t *testing.T, raw string) (Reaction, bool) {
	t.Helper()
	var m Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m.Reaction()
}

func TestReactionFromTheCustomer(t *testing.T) {
	r, ok := reactionOf(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "R1"},
	  "message": {"reactionMessage": {"key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": true, "id": "3EB0OURS"}, "text": "👍"}}
	}`)
	if !ok || r.Phone != "201234567890" || r.TargetID != "3EB0OURS" || r.Emoji != "👍" || r.FromMe {
		t.Errorf("got %+v, %v", r, ok)
	}
	// And a reaction is never read as a message.
	if _, isMsg := read(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "R1"},
	  "message": {"reactionMessage": {"key": {"id": "3EB0OURS"}, "text": "👍"}}
	}`); isMsg {
		t.Error("a reactionMessage was read as a message")
	}
}

func TestReactionFromOurOwnPhone(t *testing.T) {
	r, ok := reactionOf(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": true, "id": "R2"},
	  "message": {"reactionMessage": {"key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "3EB0THEIRS"}, "text": "❤️"}}
	}`)
	if !ok || !r.FromMe || r.Phone != "201234567890" || r.TargetID != "3EB0THEIRS" || r.Emoji != "❤️" {
		t.Errorf("got %+v, %v", r, ok)
	}
}

func TestReactionRemovedWithEmptyText(t *testing.T) {
	r, ok := reactionOf(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "R3"},
	  "message": {"reactionMessage": {"key": {"id": "3EB0OURS"}, "text": ""}}
	}`)
	if !ok || r.Emoji != "" || r.TargetID != "3EB0OURS" {
		t.Errorf("a removal must still be a reaction with an empty emoji: %+v, %v", r, ok)
	}
}

func TestReactionIgnoredInGroupsAndWithoutTarget(t *testing.T) {
	for _, raw := range []string{
		`{"key": {"remoteJid": "1203630@g.us", "fromMe": false, "id": "R4"}, "message": {"reactionMessage": {"key": {"id": "X"}, "text": "👍"}}}`,
		`{"key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "R5"}, "message": {"reactionMessage": {"key": {"id": ""}, "text": "👍"}}}`,
		`{"key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "R6"}, "message": {"conversation": "hi"}}`,
	} {
		if r, ok := reactionOf(t, raw); ok {
			t.Errorf("%s: read as a reaction: %+v", raw, r)
		}
	}
}

func TestInboundQuote(t *testing.T) {
	// The usual shape: a text reply carries its quote inside extendedTextMessage.
	in, ok := read(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "Q1"},
	  "message": {"extendedTextMessage": {"text": "yes please",
	    "contextInfo": {"stanzaId": "3EB0OURS", "participant": "201000000000@s.whatsapp.net",
	      "quotedMessage": {"conversation": "shall I book Tuesday?"}}}}
	}`)
	if !ok || in.Text != "yes please" || in.Quote == nil || in.Quote.ExternalID != "3EB0OURS" || in.Quote.Body != "shall I book Tuesday?" {
		t.Fatalf("got %+v / quote %+v", in, in.Quote)
	}
	// Evolution v2 also lifts contextInfo to the top level of the payload.
	in, ok = read(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "Q2"},
	  "message": {"conversation": "this one"},
	  "contextInfo": {"stanzaId": "3EB0PIC", "quotedMessage": {"imageMessage": {"caption": "the red one"}}}
	}`)
	if !ok || in.Quote == nil || in.Quote.ExternalID != "3EB0PIC" || in.Quote.Body != "the red one" {
		t.Fatalf("top-level contextInfo: %+v", in.Quote)
	}
	// A quote on media: the image's own contextInfo.
	in, _ = read(t, `{
	  "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "Q3"},
	  "message": {"imageMessage": {"caption": "like this", "contextInfo": {"stanzaId": "3EB0Q"}}}
	}`)
	if in.Quote == nil || in.Quote.ExternalID != "3EB0Q" {
		t.Errorf("media quote: %+v", in.Quote)
	}
	// No quote at all.
	in, _ = read(t, `{"key": {"remoteJid": "201234567890@s.whatsapp.net", "id": "Q4"}, "message": {"conversation": "plain"}}`)
	if in.Quote != nil {
		t.Errorf("a plain message got a quote: %+v", in.Quote)
	}
}

// recorder is a fake Evolution that keeps every body it is sent.
type recorder struct {
	mu     sync.Mutex
	bodies map[string]map[string]any
}

func fakeEvolutionRecorder(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{bodies: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("apikey") != "test-key" {
			http.Error(w, "missing apikey", http.StatusUnauthorized)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.bodies[r.URL.Path[:strings.LastIndex(r.URL.Path, "/")]] = body
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"key":{"id":"SENT-1"}}`)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func (r *recorder) body(t *testing.T, path string) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.bodies[path]
	if !ok {
		t.Fatalf("nothing sent to %s (have %v)", path, r.bodies)
	}
	return b
}

func TestSendReactionBody(t *testing.T) {
	srv, rec := fakeEvolutionRecorder(t)
	c := clientFor(t, srv.URL)
	if _, err := c.SendReaction(context.Background(), "+20 123 456 7890", "3EB0THEIRS", false, "👍"); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(rec.body(t, "/message/sendReaction"))
	want := `{"key":{"fromMe":false,"id":"3EB0THEIRS","remoteJid":"201234567890@s.whatsapp.net"},"reaction":"👍"}`
	if string(got) != want {
		t.Errorf("sendReaction body:\n got %s\nwant %s", got, want)
	}
	// "" takes it back; fromMe marks a reaction on our own message.
	if _, err := c.SendReaction(context.Background(), "201234567890", "3EB0OURS", true, ""); err != nil {
		t.Fatal(err)
	}
	got, _ = json.Marshal(rec.body(t, "/message/sendReaction"))
	want = `{"key":{"fromMe":true,"id":"3EB0OURS","remoteJid":"201234567890@s.whatsapp.net"},"reaction":""}`
	if string(got) != want {
		t.Errorf("removal body:\n got %s\nwant %s", got, want)
	}
}

func TestQuotedOnEverySend(t *testing.T) {
	srv, rec := fakeEvolutionRecorder(t)
	c := clientFor(t, srv.URL)
	ctx := WithOutboundQuote(context.Background(), OutboundQuote{ExternalID: "3EB0Q", Text: "shall I book Tuesday?"})
	if _, err := c.SendText(ctx, "201234567890", "yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendMedia(ctx, "201234567890", "image", "image/png", []byte("png"), "a.png", "look"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendWhatsAppAudio(ctx, "201234567890", []byte("ogg")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SendLocation(ctx, "201234567890", 30.0, 31.2, "Office", "Cairo"); err != nil {
		t.Fatal(err)
	}
	want := `{"key":{"id":"3EB0Q"},"message":{"conversation":"shall I book Tuesday?"}}`
	for _, p := range []string{"/message/sendText", "/message/sendMedia", "/message/sendWhatsAppAudio", "/message/sendLocation"} {
		got, _ := json.Marshal(rec.body(t, p)["quoted"])
		if string(got) != want {
			t.Errorf("%s quoted:\n got %s\nwant %s", p, got, want)
		}
	}
	// Without a quote nothing is added.
	if _, err := c.SendText(context.Background(), "201234567890", "plain"); err != nil {
		t.Fatal(err)
	}
	if q, ok := rec.body(t, "/message/sendText")["quoted"]; ok {
		t.Errorf("an unquoted send carried quoted: %v", q)
	}
	// An empty id is not a quote.
	if _, err := c.SendText(WithOutboundQuote(context.Background(), OutboundQuote{Text: "x"}), "201234567890", "plain"); err != nil {
		t.Fatal(err)
	}
	if q, ok := rec.body(t, "/message/sendText")["quoted"]; ok {
		t.Errorf("a quote with no id was sent: %v", q)
	}
}
