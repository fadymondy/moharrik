package whatsapp

import (
	"context"
	"net/http"
	"strings"
)

/*
Reading history, not just sending it (MH-562): the calls an import needs on
top of the sending Client — listing what chats and groups an instance has, and
paging one chat's messages. Evolution API v2 exposes these under /chat and
/group; nothing in this package called them before (the webhook only ever
pushed new messages in).
*/

// ChatMessagePreview is the shape a chat/group list embeds for "what did they
// last say" — the same fields FindMessages returns one of, kept small on
// purpose since a list of hundreds of chats should not carry full bodies.
type ChatMessagePreview struct {
	Text      string `json:"message"`
	Timestamp int64  `json:"messageTimestamp"`
	FromMe    bool   `json:"fromMe"`
}

// Chat is one 1:1 conversation Evolution knows about.
type Chat struct {
	ID          string               `json:"id"`
	RemoteJID   string               `json:"remoteJid"`
	Name        string               `json:"name"`
	UnreadCount int                  `json:"unreadMessages"`
	LastMessage *ChatMessagePreview  `json:"lastMessage,omitempty"`
	UpdatedAt   string               `json:"updatedAt,omitempty"`
}

// Phone is the chat's number, digits only.
func (c Chat) Phone() string { return Number(c.RemoteJID) }

// GroupParticipant is one member of a group, as Evolution reports it.
type GroupParticipant struct {
	ID    string `json:"id"` // a JID, e.g. "201234567890@s.whatsapp.net"
	Name  string `json:"name,omitempty"`
	Admin string `json:"admin,omitempty" doc:"'admin', 'superadmin', or empty"`
}

// Phone is the participant's number, digits only.
func (p GroupParticipant) Phone() string { return Number(p.ID) }

// Group is one group chat Evolution knows about.
type Group struct {
	ID           string               `json:"id"` // the group JID, e.g. "1203…-1234@g.us"
	Subject      string               `json:"subject"`
	Size         int                  `json:"size"`
	Participants []GroupParticipant   `json:"participants"`
	LastMessage  *ChatMessagePreview  `json:"lastMessage,omitempty"`
}

// ListChats lists every 1:1 chat the instance has ever exchanged messages
// with. Evolution has no server-side filter for "not a group" here — findChats
// only ever returns non-group entries; fetchAllGroups is the separate call
// for groups (ListGroups).
func (c *Client) ListChats(ctx context.Context) ([]Chat, error) {
	var out []Chat
	err := c.do(ctx, http.MethodPost, "/chat/findChats/"+c.inst(), map[string]any{}, &out)
	return out, err
}

// ListGroups lists every group the instance is a member of, with participants
// (getParticipants=true — without it Evolution omits the list entirely).
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	err := c.do(ctx, http.MethodGet, "/group/fetchAllGroups/"+c.inst()+"?getParticipants=true", nil, &out)
	return out, err
}

// HistoryMessage is one record from FindMessages: a past message in a chat or
// group, in delivery order.
type HistoryMessage struct {
	Key struct {
		RemoteJID string `json:"remoteJid"`
		FromMe    bool   `json:"fromMe"`
		ID        string `json:"id"`
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
	} `json:"message"`
}

// Text reads the message body out of whichever shape WhatsApp used, the same
// rule messageText applies to a live webhook delivery.
func (m HistoryMessage) Text() string {
	for _, v := range []string{m.Message.Conversation, m.Message.Extended.Text, m.Message.Image.Caption, m.Message.Document.Caption} {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	if m.Message.Document.FileName != "" {
		return "[file: " + clip(m.Message.Document.FileName, 80) + "]"
	}
	return ""
}

// FindMessages pages one chat or group's history, newest first, at most limit
// messages (capped at 200 — an import reads recent history, not the whole
// archive).
func (c *Client) FindMessages(ctx context.Context, remoteJID string, limit int) ([]HistoryMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var out struct {
		Messages struct {
			Records []HistoryMessage `json:"records"`
		} `json:"messages"`
	}
	body := map[string]any{
		"where": map[string]any{"key": map[string]any{"remoteJid": remoteJID}},
		"limit": limit,
		"page":  1,
	}
	err := c.do(ctx, http.MethodPost, "/chat/findMessages/"+c.inst(), body, &out)
	return out.Messages.Records, err
}
