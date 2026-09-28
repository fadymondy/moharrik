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
	PushName  string          `json:"pushName"`
	Timestamp int64           `json:"messageTimestamp"`
	Message   json.RawMessage `json:"message"`
	body      messageBody
	// raw is the WHOLE delivered object (key + message + pushName + …), the
	// exact shape GetMediaBase64 (evolution.go) forwards to Evolution's
	// getBase64FromMediaMessage — which needs the key alongside the message,
	// not the message sub-object alone.
	raw json.RawMessage
}

// messageBody is the union of every shape messageBody.message can take. Kept
// as a raw + parsed pair (see UnmarshalJSON below) so the ENTIRE original
// "message" object survives untouched as Message.Message — the exact bytes
// GetMediaBase64 (evolution.go) has to hand Evolution back, since a Go
// struct that only kept the fields this app reads would silently drop the
// mediaKey/fileEncSha256/directPath fields Evolution needs to decrypt media
// it does not still have cached.
type messageBody struct {
	Conversation string `json:"conversation"`
	Extended     struct {
		Text string `json:"text"`
	} `json:"extendedTextMessage"`
	Image struct {
		Caption  string `json:"caption"`
		Mimetype string `json:"mimetype"`
	} `json:"imageMessage"`
	Video struct {
		Caption  string `json:"caption"`
		Mimetype string `json:"mimetype"`
	} `json:"videoMessage"`
	Sticker struct {
		Mimetype string `json:"mimetype"`
	} `json:"stickerMessage"`
	Document struct {
		Caption  string `json:"caption"`
		FileName string `json:"fileName"`
		Mimetype string `json:"mimetype"`
	} `json:"documentMessage"`
	Audio struct {
		Seconds  int    `json:"seconds"`
		Ptt      bool   `json:"ptt"`
		Mimetype string `json:"mimetype"`
	} `json:"audioMessage"`
	Location struct {
		Latitude  float64 `json:"degreesLatitude"`
		Longitude float64 `json:"degreesLongitude"`
		Name      string  `json:"name"`
		Address   string  `json:"address"`
	} `json:"locationMessage"`
	Contact struct {
		DisplayName string `json:"displayName"`
		Vcard       string `json:"vcard"`
	} `json:"contactMessage"`
	ContactsArray struct {
		DisplayName string `json:"displayName"`
		Contacts    []struct {
			DisplayName string `json:"displayName"`
			Vcard       string `json:"vcard"`
		} `json:"contacts"`
	} `json:"contactsArrayMessage"`
}

// UnmarshalJSON keeps both the raw bytes (Message.Message, forwarded whole to
// Evolution for a media download) and the parsed convenience fields (m.body,
// used everywhere else in this file) from the SAME "message" object, so
// nothing has to parse it twice or risk the two disagreeing.
func (m *Message) UnmarshalJSON(data []byte) error {
	type alias Message
	aux := &struct{ *alias }{alias: (*alias)(m)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	m.raw = append(json.RawMessage(nil), data...)
	if len(m.Message) == 0 {
		return nil
	}
	return json.Unmarshal(m.Message, &m.body)
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
	// Media describes a downloadable/renderable attachment (MH-658). Nil for
	// a plain text message.
	Media *Media
	// raw is the exact upstream "message" object, forwarded byte-for-byte to
	// Evolution's getBase64FromMediaMessage when Media != nil.
	raw json.RawMessage
}

// Media is what one inbound message's non-text payload needs: enough to
// download it (Kind + the Inbound.raw it travels with) and enough to render
// it without downloading anything (location/contact, and the caption every
// media type but those two can carry).
type Media struct {
	Kind     string // image | video | audio | voice | document | sticker | location | contact
	MimeType string
	FileName string
	Caption  string
	Seconds  int

	Lat, Lng               float64
	LocationName, Address  string
	ContactName, ContactVC string
}

// Raw is the upstream message object Media was read from — what
// GetMediaBase64 (evolution.go) hands back to Evolution.
func (in Inbound) Raw() json.RawMessage { return in.raw }

// Read flattens a Message. It returns ok=false for anything the inbox has
// nothing to do with — a group chat, or a message with neither text nor a
// renderable attachment at all.
func (m Message) Read() (Inbound, bool) {
	jid := firstNonEmpty(m.Key.RemoteJID, m.Key.RemoteJIDAlt)
	in := Inbound{
		ID:     strings.TrimSpace(m.Key.ID),
		FromMe: m.Key.FromMe,
		Name:   strings.TrimSpace(m.PushName),
		Phone:  PhoneOf(m.Key.RemoteJID, m.Key.RemoteJIDAlt),
		Group:  strings.Contains(jid, "@g.us"),
		raw:    m.raw,
	}
	if in.Phone == "" {
		// A privacy-mode sender with no phone anywhere: keep the old
		// behaviour (file it under the id) rather than drop their message.
		in.Phone = Number(jid)
	}
	if in.Group || in.Phone == "" {
		// Groups are not customer conversations, and a chat with no number is
		// not one either. Filing them would fill the inbox with noise the team
		// cannot answer.
		return in, false
	}
	in.Text = messageText(m)
	in.Media = mediaOf(m)
	if in.Text == "" && in.Media == nil {
		return in, false
	}
	return in, true
}

// messageText reads the CAPTION out of the several shapes WhatsApp uses.
// Plain text messages resolve fully here; a media message with no caption
// resolves to "" — the message is still filed (Read, above), it just shows
// its attachment with no text line above it, same as an uncaptioned email.
func messageText(m Message) string {
	for _, v := range []string{m.body.Conversation, m.body.Extended.Text, m.body.Image.Caption, m.body.Video.Caption, m.body.Document.Caption} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// extFor maps a MIME type to a plausible file extension for the placeholder
// filename an attachment gets when WhatsApp sent no fileName of its own
// (every kind but documentMessage).
func extFor(mime string) string {
	switch {
	case strings.Contains(mime, "jpeg"):
		return ".jpg"
	case strings.Contains(mime, "png"):
		return ".png"
	case strings.Contains(mime, "webp"):
		return ".webp"
	case strings.Contains(mime, "gif"):
		return ".gif"
	case strings.Contains(mime, "mp4"):
		return ".mp4"
	case strings.Contains(mime, "ogg"):
		return ".ogg"
	case strings.Contains(mime, "mpeg") || strings.Contains(mime, "mp3"):
		return ".mp3"
	case strings.Contains(mime, "pdf"):
		return ".pdf"
	}
	return ""
}

// mediaOf reads whichever ONE of WhatsApp's media/location/contact shapes
// this message carries. WhatsApp never sets more than one of these on a
// single message, so the first match wins.
func mediaOf(m Message) *Media {
	b := m.body
	switch {
	case b.Image.Mimetype != "":
		return &Media{Kind: "image", MimeType: b.Image.Mimetype, Caption: strings.TrimSpace(b.Image.Caption), FileName: "image" + extFor(b.Image.Mimetype)}
	case b.Video.Mimetype != "":
		return &Media{Kind: "video", MimeType: b.Video.Mimetype, Caption: strings.TrimSpace(b.Video.Caption), FileName: "video" + extFor(b.Video.Mimetype)}
	case b.Sticker.Mimetype != "":
		return &Media{Kind: "sticker", MimeType: b.Sticker.Mimetype, FileName: "sticker" + extFor(b.Sticker.Mimetype)}
	case b.Audio.Mimetype != "" || b.Audio.Seconds > 0:
		kind := "audio"
		if b.Audio.Ptt {
			kind = "voice"
		}
		return &Media{Kind: kind, MimeType: firstNonEmpty(b.Audio.Mimetype, "audio/ogg"), FileName: kind + extFor(b.Audio.Mimetype), Seconds: b.Audio.Seconds}
	case b.Document.Mimetype != "" || b.Document.FileName != "":
		return &Media{
			Kind:     "document",
			MimeType: firstNonEmpty(b.Document.Mimetype, "application/octet-stream"),
			Caption:  strings.TrimSpace(b.Document.Caption),
			FileName: firstNonEmpty(clip(b.Document.FileName, 120), "file"+extFor(b.Document.Mimetype)),
		}
	case b.Location.Latitude != 0 || b.Location.Longitude != 0:
		return &Media{Kind: "location", Lat: b.Location.Latitude, Lng: b.Location.Longitude, LocationName: strings.TrimSpace(b.Location.Name), Address: strings.TrimSpace(b.Location.Address)}
	case b.Contact.Vcard != "":
		return &Media{Kind: "contact", ContactName: strings.TrimSpace(b.Contact.DisplayName), ContactVC: b.Contact.Vcard}
	case len(b.ContactsArray.Contacts) > 0 && b.ContactsArray.Contacts[0].Vcard != "":
		// A shared batch of contacts (MH-658 scope: the CARD renders one).
		// firstNonEmpty covers a per-contact name arriving blank.
		c := b.ContactsArray.Contacts[0]
		return &Media{Kind: "contact", ContactName: firstNonEmpty(strings.TrimSpace(c.DisplayName), strings.TrimSpace(b.ContactsArray.DisplayName)), ContactVC: c.Vcard}
	}
	return nil
}

// Number turns a JID into digits: "201234567890@s.whatsapp.net" -> "201234567890".
// PhoneOf is the phone number behind the first of jids that is a person's
// phone JID, digits only, or "" if none is. WhatsApp's privacy ids ("…@lid")
// are 15 digits that look like a number and are not one — Evolution then puts
// the real phone JID, when it has it, in remoteJidAlt (MH-562).
func PhoneOf(jids ...string) string {
	for _, j := range jids {
		j = strings.TrimSpace(j)
		if j == "" {
			continue
		}
		if i := strings.IndexByte(j, '@'); i >= 0 {
			switch j[i+1:] {
			case "s.whatsapp.net", "c.us":
			default:
				continue
			}
		}
		if n := Number(j); n != "" {
			return n
		}
	}
	return ""
}

// Presence is a presence.update payload. Evolution/Baileys shapes this as
// either a single presence keyed by the chat id, or a map of jid -> presence
// for a group; only the single-chat shape matters here since a customer's DM
// is what this app files.
//
//	{"id": "201234567890@s.whatsapp.net",
//	 "presences": {"201234567890@s.whatsapp.net": {"lastKnownPresence": "composing"}}}
type Presence struct {
	ID        string `json:"id"`
	Presences map[string]struct {
		LastKnownPresence string `json:"lastKnownPresence"`
	} `json:"presences"`
	// Some builds send it flat instead of nested under presences.
	LastKnownPresence string `json:"lastKnownPresence"`
}

// Read turns a presence update into (phone, state), where state is one of
// composing, recording, paused, available, unavailable. ok is false when
// there is no phone to attribute it to.
func (p Presence) Read() (phone, state string, ok bool) {
	phone = PhoneOf(p.ID)
	if phone == "" {
		return "", "", false
	}
	state = p.LastKnownPresence
	if state == "" {
		if v, ok := p.Presences[p.ID]; ok {
			state = v.LastKnownPresence
		}
	}
	if state == "" {
		for _, v := range p.Presences {
			state = v.LastKnownPresence
			break
		}
	}
	return phone, state, state != ""
}

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
