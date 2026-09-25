// Package inbox is Moharrik's shared inbox: one conversation per customer,
// whatever channel they arrived on, and the messages in it. P0 ships the chat
// channel — an embeddable widget that posts to a public, key-authenticated
// endpoint — with email and WhatsApp reusing the same tables when their
// transports land.
//
// Same rules as internal/marketing, internal/crm and internal/notifications:
// every workspace-scoped handler runs on the app (RLS) pool AND names
// workspace_id in every predicate. The exceptions are the two widget routes,
// POST/GET /api/chat/{key}/messages, which by definition have no session —
// routes.go's public section explains how they still end up correctly scoped.
package inbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var channelSet = map[string]bool{
	"chat": true, "email": true, "whatsapp": true,
	// Telegram, Slack, Discord (MH-563)
	"telegram": true, "slack": true, "discord": true,
	// Browser-workflow channels (MH-564)
	"linkedin": true, "facebook": true, "instagram": true, "x": true,
}

var statuses = map[string]bool{"open": true, "snoozed": true, "closed": true}

// ---- wire types -----------------------------------------------------------

type ConversationOut struct {
	ID              string    `json:"id"`
	Channel         string    `json:"channel"`
	Subject         string    `json:"subject"`
	Status          string    `json:"status"`
	CustomerName    string    `json:"customer_name"`
	CustomerEmail   string    `json:"customer_email"`
	ContactID       *string   `json:"contact_id"`
	AssigneeUserID  *string   `json:"assignee_user_id"`
	MailboxID       *string   `json:"mailbox_id,omitempty" doc:"Which mailbox (MH-555) this email thread arrived on; absent for chat/WhatsApp."`
	Unread          bool      `json:"unread"`
	LastMessageAt   time.Time `json:"last_message_at"`
	CreatedAt       time.Time `json:"created_at"`
	Preview         string    `json:"preview" doc:"The most recent message, trimmed."`
	MessageCount    int       `json:"message_count"`
	PageURL         string    `json:"page_url" doc:"The page a chat visitor opened the conversation from; empty for other channels."`
	UserAgent       string    `json:"user_agent" doc:"The chat visitor's browser, as it identified itself."`
	Locale          string    `json:"locale" doc:"The chat visitor's language, e.g. ar or en-GB."`
	CustomerPhone   string    `json:"customer_phone" doc:"A WhatsApp customer's number; empty for other channels."`
	CustomerHandle  string    `json:"customer_handle" doc:"A Telegram/Slack/Discord handle or a workflow channel's profile URL; empty for other channels."`
	// IsGroup (MH-604) marks a Telegram/Slack/Discord thread with more than
	// one possible sender — a group chat/channel, never a 1:1 DM, and never
	// true for chat/email/WhatsApp (WhatsApp groups are deliberately never
	// filed). Drives the UI's per-message sender avatar: shown only here.
	IsGroup         bool      `json:"is_group"`
	// AvatarURL is MH-602's single answer for "what photo, if any" — a linked
	// contact's uploaded/provider photo when there is one, else this
	// conversation's own cached WhatsApp photo (set for an unlinked customer),
	// else empty so the client renders initials. Never build this from
	// Contact.* directly; crm.AvatarURL already picked the right source.
	AvatarURL       string    `json:"avatar_url"`
	SourceChannelID *string   `json:"source_channel_id" doc:"Which of this workspace's connections of that kind this conversation came from; null for chat/email/WhatsApp."`
	// refreshPhoto is set by scanConversation when this is a WhatsApp row
	// whose photo has never been fetched or is past the refresh window
	// (MH-602 defect 1). It never reaches JSON (unexported); ListConversations
	// and GetConversation consume it to queue a bounded, deduped background
	// fetch right after listing/opening, instead of waiting for the next
	// inbound message.
	refreshPhoto *photoRefreshCandidate
	// The CRM side (MH-379).
	Contact  *LinkedContact `json:"contact" doc:"The CRM contact this conversation is linked to, if any."`
	LinkedBy string         `json:"contact_link" enum:",auto,manual" doc:"How the contact was linked: auto (an exact email or phone match when the message was filed) or manual (by a member). manual with no contact means a member unlinked it, and no automatic match will relink it."`
	Deal     *LinkedDeal    `json:"deal" doc:"The deal this conversation was turned into, if any."`
	// Snooze and resolution (MH-383).
	SnoozedUntil *time.Time `json:"snoozed_until" doc:"When a snoozed conversation wakes up; null when it is not snoozed, or is snoozed until the customer writes again."`
	ClosedAt     *time.Time `json:"closed_at" doc:"When it was closed; null while open or snoozed."`
}

// LinkedContact is the CRM contact on a conversation.
type LinkedContact struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Email   string     `json:"email"`
	Company *LinkedRef `json:"company"`
}

// LinkedRef names a CRM row.
type LinkedRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// LinkedDeal is the deal a conversation became.
type LinkedDeal struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type MessageOut struct {
	ID           string    `json:"id"`
	Direction    string    `json:"direction" doc:"in = the customer wrote it, out = the team replied."`
	AuthorUserID *string   `json:"author_user_id"`
	AuthorEmail  string    `json:"author_email"`
	Body         string    `json:"body" doc:"Plain text. For chat/WhatsApp this is Markdown source; for email it is the text/plain rendering."`
	BodyHTML     string    `json:"body_html,omitempty" doc:"Sanitised HTML (MH-556): an email's own HTML part, or the Markdown in Body rendered. Empty when there is nothing to add over Body (plain-text-only email)."`
	MailboxID    *string   `json:"mailbox_id,omitempty" doc:"Which mailbox this email arrived on or went out through; absent for chat/WhatsApp."`
	// Subject (MH-600): an email keeps its OWN subject line even after every
	// sender's email folds into one conversation. Empty for chat/WhatsApp,
	// where a message has no subject of its own — the client shows this only
	// when it differs from the previous message's, so a run of replies on the
	// same subject shows it once.
	Subject      string          `json:"subject,omitempty" doc:"The email's own subject line (email channel only); empty for chat/WhatsApp."`
	Attachments  []AttachmentOut `json:"attachments,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	// Sender identity (MH-604), set only for an inbound message on a group
	// thread (ConversationOut.IsGroup) — a 1:1 thread's sender is the
	// conversation's own contact/customer_name, already surfaced there.
	SenderName       string  `json:"sender_name,omitempty" doc:"The group-thread sender's display name; empty outside a group thread."`
	SenderContactID  *string `json:"sender_contact_id,omitempty" doc:"The CRM contact resolved for this group-thread sender, if any."`
	SenderExternalID string  `json:"-" doc:"The sender's raw platform id (Telegram/Slack/Discord handle); internal only, never serialized — SenderContactID/SenderAvatarURL are what the client renders from."`
	SenderAvatarURL  string  `json:"sender_avatar_url,omitempty" doc:"MH-604's per-sender counterpart to ConversationOut.AvatarURL: the sender's photo if cached, else empty so the client renders initials."`
}

// AttachmentOut is one attachment's metadata — never the bytes; those are
// fetched separately over a signed URL (MH-556, attachment.go).
type AttachmentOut struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	Inline      bool   `json:"inline"`
	ContentID   string `json:"content_id,omitempty"`
	URL         string `json:"url" doc:"A signed, time-limited download URL. Not authenticated any other way, so it can be used directly as an <img src> or a download link."`
}

type WidgetOut struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	PublicKey      string    `json:"public_key"`
	AllowedOrigins []string  `json:"allowed_origins"`
	Enabled        bool      `json:"enabled"`
	Embed          string    `json:"embed" doc:"The snippet to paste on the customer's site."`
	CreatedAt      time.Time `json:"created_at"`
	// What it shows and does on the page (MH-382, widget_settings.go).
	WidgetSettings
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type idPath struct {
	wsHeader
	ID string `path:"id"`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// ---- helpers --------------------------------------------------------------

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}

func strp(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	v := n.String
	return &v
}

// nullable turns "" into SQL NULL (the wire convention for "unlink").
func nullable(s string) any {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return s
}

// update collects SET clauses for a PATCH. Args start after the two fixed
// predicates ($1 = workspace_id, $2 = id).
type update struct {
	sets []string
	args []any
}

func (u *update) set(col string, v any) {
	u.args = append(u.args, v)
	u.sets = append(u.sets, col+" = $"+strconv.Itoa(len(u.args)+2))
}

// token makes an unguessable handle: a widget's public key and a visitor's
// resume token are both "the URL/value IS the authorization", so both are 32
// random bytes, hex.
func token(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

// originAllowed checks a browser Origin against a widget's comma-separated
// allow-list. An empty list allows nothing — a widget with no origins set is
// blocked rather than open, the same default the Mahaam feedback key uses.
func originAllowed(list, origin string) bool {
	origin = strings.TrimRight(strings.TrimSpace(strings.ToLower(origin)), "/")
	if origin == "" {
		return false
	}
	for _, raw := range strings.Split(list, ",") {
		if a := strings.TrimRight(strings.TrimSpace(strings.ToLower(raw)), "/"); a != "" && a == origin {
			return true
		}
	}
	return false
}

func splitOrigins(s string) []string {
	outv := []string{}
	for _, raw := range strings.Split(s, ",") {
		if v := strings.TrimRight(strings.TrimSpace(raw), "/"); v != "" {
			outv = append(outv, v)
		}
	}
	return outv
}

func trim(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
