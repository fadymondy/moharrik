package whatsapp

import (
	"encoding/json"
	"testing"
)

// A privacy-mode chat's id is a "…@lid" — 15 digits that are not a phone. The
// real number, when Evolution has it, is the key's remoteJidAlt (MH-562).
func TestPhoneOfSkipsPrivacyIDs(t *testing.T) {
	for _, tc := range []struct {
		jids []string
		want string
	}{
		{[]string{"201015550101@s.whatsapp.net"}, "201015550101"},
		{[]string{"144985244578026@lid", "201015550101@s.whatsapp.net"}, "201015550101"},
		{[]string{"144985244578026@lid", ""}, ""},
		{[]string{"120363011234567890@g.us"}, ""},
		{[]string{"status@broadcast"}, ""},
		{[]string{"201015550101:12@s.whatsapp.net"}, "201015550101"},
		{[]string{"+20 101 555 0101"}, "201015550101"},
	} {
		if got := PhoneOf(tc.jids...); got != tc.want {
			t.Errorf("PhoneOf(%q) = %q, want %q", tc.jids, got, tc.want)
		}
	}
}

func TestChatPhoneReadsTheAltJIDOfAPrivacyChat(t *testing.T) {
	var chats []Chat
	if err := json.Unmarshal([]byte(`[
	 {"remoteJid":"144985244578026@lid","pushName":"Rana","unreadCount":2,
	  "lastMessage":{"key":{"id":"A","fromMe":true,"remoteJid":"144985244578026@lid","remoteJidAlt":"201015550101@s.whatsapp.net","addressingMode":"lid"},
	   "message":{"conversation":"hi"},"messageTimestamp":1789999999}},
	 {"remoteJid":"156401385095367@lid","lastMessage":{"key":{"remoteJid":"156401385095367@lid"},"message":{"conversation":"x"},"messageTimestamp":"1789999999"}}
	]`), &chats); err != nil {
		t.Fatal(err)
	}
	if got := chats[0].Phone(); got != "201015550101" {
		t.Errorf("lid chat with alt: phone %q", got)
	}
	if got := chats[1].Phone(); got != "" {
		t.Errorf("lid chat without alt must have no phone, got %q", got)
	}
	if chats[0].DisplayName() != "Rana" || chats[0].UnreadCount != 2 || chats[0].LastMessage.Text() != "hi" {
		t.Errorf("chat read wrong: %+v", chats[0])
	}
	if chats[1].LastMessage.Timestamp != 1789999999 {
		t.Errorf("string timestamp: %d", chats[1].LastMessage.Timestamp)
	}
}

func TestReadFilesAPrivacySenderUnderTheirRealPhone(t *testing.T) {
	var m Message
	_ = json.Unmarshal([]byte(`{"key":{"remoteJid":"144985244578026@lid","remoteJidAlt":"201015550101@s.whatsapp.net","id":"B"},
	  "pushName":"Rana","message":{"conversation":"is my order shipped?"},"messageTimestamp":1789999999}`), &m)
	in, ok := m.Read()
	if !ok || in.Phone != "201015550101" {
		t.Fatalf("got %+v ok=%v", in, ok)
	}
}
