package whatsapp

import (
	"crypto/subtle"
	"encoding/json"
	"strings"
)

/*
What Evolution sends us, and what we make of it.

	{"event": "messages.upsert", "instance": "ws-abc", "data": {
	   "key": {"remoteJid": "201234567890@s.whatsapp.net", "fromMe": false, "id": "3EB0…"},
	   "pushName": "Dana",
	   "message": {"conversation": "hello"},
	   "messageTimestamp": 1789999999}}

The fields that decide everything:

	remoteJid  who the chat is with — the customer's number, or a @g.us group
	fromMe     the DIRECTION. true is a message this workspace sent, which
	           Evolution echoes back; filing it as inbound would make the team
	           look like the customer.
	id         WhatsApp's own message id, so a redelivery is the same row
*/

// TokenHeader is the header Evolution sends back with every webhook call. It
// carries a secret unique to that workspace's link, which is what makes the
// webhook's public URL safe to have.
const TokenHeader = "X-Moharrik-Token"

// Payload is one webhook delivery.
type Payload struct {
	Event    string          `json:"event"`
	Instance string          `json:"instance"`
	Data     json.RawMessage `json:"data"`
}

// Message is a messages.upsert payload.
type Message struct {
	Key struct {
		RemoteJID    string `json:"remoteJid"`
		RemoteJIDAlt string `json:"remoteJidAlt"`
		FromMe       bool   `json:"fromMe"`
		ID           string `json:"id"`
	} `json:"key"`
	PushName  string `json:"pushName"`
	Timestamp int64  `json:"messageTimestamp"`
	Message   struct {
		Conversation string `json:"conversation"`
		Extended     struct {
			Text string `json:"text"`
		} `json:"extendedTextMessage"`
		Image struct {
			Caption string `json:"caption"`
		} `json:"imageMessage"`
		Document struct {
			Caption  string `json:"caption"`
			FileName string `json:"fileName"`
		} `json:"documentMessage"`
		Audio struct {
			Seconds int `json:"seconds"`
		} `json:"audioMessage"`
	} `json:"message"`
}

// Connection is a connection.update payload.
type Connection struct {
	State string `json:"state"`
	// The number that ended up linked, when Evolution reports one.
	WUID string `json:"wuid"`
}

// Inbound is what the inbox needs from a message: who wrote, what they said,
// and the id that makes a redelivery the same row.
type Inbound struct {
	Phone  string
	Name   string
	Text   string
	ID     string
	FromMe bool
	Group  bool
}

// Read flattens a Message. It returns ok=false for anything the inbox has
// nothing to do with — a group chat, or a message with no text at all.
func (m Message) Read() (Inbound, bool) {
	jid := firstNonEmpty(m.Key.RemoteJID, m.Key.RemoteJIDAlt)
	in := Inbound{
		ID:     strings.TrimSpace(m.Key.ID),
		FromMe: m.Key.FromMe,
		Name:   strings.TrimSpace(m.PushName),
		Phone:  Number(jid),
		Group:  strings.Contains(jid, "@g.us"),
	}
	if in.Group || in.Phone == "" {
		// Groups are not customer conversations, and a chat with no number is
		// not one either. Filing them would fill the inbox with noise the team
		// cannot answer.
		return in, false
	}
	in.Text = messageText(m)
	if in.Text == "" {
		return in, false
	}
	return in, true
}

// messageText reads the text out of the several shapes WhatsApp uses. An
// attachment with no caption is described rather than dropped, so the team
// sees that something arrived.
func messageText(m Message) string {
	for _, v := range []string{m.Message.Conversation, m.Message.Extended.Text, m.Message.Image.Caption, m.Message.Document.Caption} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	switch {
	case m.Message.Document.FileName != "":
		return "[file: " + clip(m.Message.Document.FileName, 80) + "]"
	case m.Message.Audio.Seconds > 0:
		return "[voice message]"
	}
	return ""
}

// Number turns a JID into digits: "201234567890@s.whatsapp.net" -> "201234567890".
func Number(jid string) string {
	jid = strings.TrimSpace(jid)
	if i := strings.IndexByte(jid, '@'); i >= 0 {
		jid = jid[:i]
	}
	// A JID can carry a device suffix (":12") and a LID can carry other junk.
	if i := strings.IndexByte(jid, ':'); i >= 0 {
		jid = jid[:i]
	}
	var b strings.Builder
	for _, r := range jid {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SameToken compares the webhook token in constant time. The token is the
// whole authorization for a public endpoint, so a timing-variable comparison
// is not good enough.
func SameToken(got, want string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
