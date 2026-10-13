package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Inbox"}

// maxMessage caps one message. Long enough for a real support message, short
// enough that a scripted visitor cannot fill the database one POST at a time.
const maxMessage = 8000

func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

const conversationSelect = `
	SELECT c.id, c.channel, c.subject, c.status, c.customer_name, c.customer_email, c.contact_id,
	       c.assignee_user_id, c.unread, c.last_message_at, c.created_at, c.page_url, c.user_agent, c.locale,
	       coalesce((SELECT m.body FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
	                 ORDER BY m.created_at DESC LIMIT 1), ''),
	       (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id),
	       c.customer_phone, c.contact_link, ct.name, ct.email, ct.company_id, co.name, c.deal_id, d.title,
	       c.snoozed_until, c.closed_at, c.mailbox_id, c.customer_handle, c.source_channel_id, c.is_group,
	       ct.photo_attachment_id,
	       (ct.photo_url LIKE 'data:%'), CASE WHEN ct.photo_url LIKE 'data:%' THEN '' ELSE coalesce(ct.photo_url, '') END,
	       ct.photo_fetched_at,
	       (c.photo_url LIKE 'data:%'), CASE WHEN c.photo_url LIKE 'data:%' THEN '' ELSE coalesce(c.photo_url, '') END,
	       c.photo_fetched_at,
	       p.pinned_at IS NOT NULL, p.archived_at IS NOT NULL,
	       CASE WHEN p.muted_until > now() THEN p.muted_until END, coalesce(p.favourite, false),
	       c.color, c.icon, c.blocked_at IS NOT NULL, c.cleared_at,
	       coalesce((SELECT json_agg(json_build_object('id', l.id, 'name', l.name, 'color', l.color) ORDER BY lower(l.name))
	                 FROM conversation_label_links ll JOIN conversation_labels l ON l.workspace_id = ll.workspace_id AND l.id = ll.label_id
	                 WHERE ll.workspace_id = c.workspace_id AND ll.conversation_id = c.id), '[]')
	FROM conversations c
	-- $2 is the signed-in member (MH-657): their own pin/archive/mute/favourite.
	LEFT JOIN conversation_prefs p ON p.workspace_id = c.workspace_id AND p.conversation_id = c.id AND p.user_id = $2
	LEFT JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
	LEFT JOIN companies co ON co.workspace_id = ct.workspace_id AND co.id = ct.company_id
	LEFT JOIN deals d ON d.workspace_id = c.workspace_id AND d.id = c.deal_id
	WHERE c.workspace_id = $1`

func scanConversation(wsID string, row interface{ Scan(...any) error }) (ConversationOut, error) {
	var c ConversationOut
	var contactID, assignee, ctName, ctEmail, companyID, coName, dealID, dealTitle, mailboxID, sourceChannelID sql.NullString
	var ctPhotoAttachmentID sql.NullString
	// MH-602 defect 2: never select the photo_url blob itself in this list —
	// a bool (from LIKE, not the value) plus any non-data: external URL, so
	// the megabytes of base64 stay in Postgres instead of the response body.
	var ctHasProviderPhoto, hasProviderPhoto sql.NullBool
	var ctExternalPhotoURL, ownExternalPhotoURL sql.NullString
	var ctPhotoFetchedAt, ownPhotoFetchedAt sql.NullTime
	var snoozed, closed, mutedUntil, cleared sql.NullTime
	var labels []byte
	var preview string
	if err := row.Scan(&c.ID, &c.Channel, &c.Subject, &c.Status, &c.CustomerName, &c.CustomerEmail, &contactID,
		&assignee, &c.Unread, &c.LastMessageAt, &c.CreatedAt, &c.PageURL, &c.UserAgent, &c.Locale, &preview, &c.MessageCount,
		&c.CustomerPhone, &c.LinkedBy, &ctName, &ctEmail, &companyID, &coName, &dealID, &dealTitle, &snoozed, &closed,
		&mailboxID, &c.CustomerHandle, &sourceChannelID, &c.IsGroup,
		&ctPhotoAttachmentID,
		&ctHasProviderPhoto, &ctExternalPhotoURL, &ctPhotoFetchedAt,
		&hasProviderPhoto, &ownExternalPhotoURL, &ownPhotoFetchedAt,
		&c.Pinned, &c.Archived, &mutedUntil, &c.Favourite, &c.Color, &c.Icon, &c.Blocked, &cleared, &labels); err != nil {
		return c, err
	}
	// MH-602: a linked contact's photo (uploaded or provider) wins; an
	// unlinked WhatsApp conversation falls back to its OWN cached photo.
	// AvatarURLLite never sees the actual data: URI — just whether one
	// exists — and turns that into a signed /api/provider-photos/ link.
	if contactID.Valid {
		c.AvatarURL = crm.AvatarURLLite(wsID, "contact", contactID.String, ctPhotoAttachmentID.String, ctHasProviderPhoto.Bool, ctExternalPhotoURL.String)
	} else {
		c.AvatarURL = crm.AvatarURLLite(wsID, "conversation", c.ID, "", hasProviderPhoto.Bool, ownExternalPhotoURL.String)
	}
	// MH-602 defect 1: a WhatsApp row whose relevant photo (the linked
	// contact's, else the conversation's own) has never been fetched or is
	// past the refresh window gets queued here — the SAME select already
	// read photo_fetched_at, so this costs nothing extra. ListConversations
	// and GetConversation are what actually fire it, bounded and deduped.
	if c.Channel == "whatsapp" {
		if contactID.Valid {
			if photoIsStale(ctPhotoFetchedAt) {
				c.refreshPhoto = &photoRefreshCandidate{kind: "contact", id: contactID.String, phone: c.CustomerPhone}
			}
		} else if photoIsStale(ownPhotoFetchedAt) {
			c.refreshPhoto = &photoRefreshCandidate{kind: "conversation", id: c.ID, phone: c.CustomerPhone}
		}
	}
	if sourceChannelID.Valid {
		c.SourceChannelID = &sourceChannelID.String
	}
	c.ContactID = strp(contactID)
	c.AssigneeUserID = strp(assignee)
	c.MailboxID = strp(mailboxID)
	c.Preview = trim(previewLine(preview), 140)
	if contactID.Valid {
		c.Contact = &LinkedContact{ID: contactID.String, Name: ctName.String, Email: ctEmail.String}
		if companyID.Valid {
			c.Contact.Company = &LinkedRef{ID: companyID.String, Name: coName.String}
		}
	}
	if dealID.Valid {
		c.Deal = &LinkedDeal{ID: dealID.String, Title: dealTitle.String}
	}
	if snoozed.Valid {
		t := snoozed.Time
		c.SnoozedUntil = &t
	}
	if closed.Valid {
		t := closed.Time
		c.ClosedAt = &t
	}
	if mutedUntil.Valid {
		t := mutedUntil.Time
		c.MutedUntil = &t
	}
	if cleared.Valid {
		t := cleared.Time
		c.ClearedAt = &t
	}
	c.Labels = []LabelRef{}
	if len(labels) > 0 {
		if err := json.Unmarshal(labels, &c.Labels); err != nil {
			return c, err
		}
	}
	return c, nil
}

// readConversation is one conversation by id, in the list's shape.
// The signed-in member's own prefs come with it; a context with no member (a
// webhook, the waker) reads none.
func readConversation(ctx context.Context, db *sql.DB, wsID, id string) (ConversationOut, error) {
	return scanConversation(wsID, db.QueryRowContext(ctx, conversationSelect+` AND c.id = $3`, wsID, memberOf(ctx), id))
}

// Register mounts the inbox endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }
	registerContactWhatsApp(api, a) // MH-410
	registerLinkPreview(api, a)     // MH-656
	registerOrganise(api, a)        // MH-657
	registerThreadMenu(api, a)      // MH-662
	registerReactions(api, a)     // MH-664
	registerMediaGallery(api, a)    // MH-662

	// ---- conversations -----------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-conversations", Method: http.MethodGet, Path: "/api/w/conversations",
		Summary: "The inbox: conversations, most recent first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Status        string `query:"status" enum:",open,snoozed,closed"`
		Unread        bool   `query:"unread" doc:"Only conversations the team has not opened since the last inbound message."`
		Channel       string `query:"channel" maxLength:"40" doc:"chat, email, whatsapp, telegram, slack, discord, or any other channel this workspace has (MH-568's channel_counts lists them); empty for every channel."`
		ContactID     string `query:"contact_id" doc:"Only conversations linked to this CRM contact."`
		MailboxID     string `query:"mailbox_id" doc:"Only email conversations on this mailbox (MH-555)."`
		CustomerEmail string `query:"customer_email" maxLength:"320" doc:"MH-600: only conversations from this sender (case-insensitive). Since MH-600 folds every sender's email into one conversation, this normally narrows to at most one row."`
		Q             string `query:"q" maxLength:"200" doc:"Full-text search (MH-556) over the customer's name, email and phone, the subject, and every message in the thread."`
		Cursor        string `query:"cursor" doc:"next_cursor from the page before this one; empty for the newest page."`
		Limit         int    `query:"limit" minimum:"0" maximum:"200"`
		Order         string `query:"order" enum:",recent,unread_first" doc:"recent (the default): newest first. unread_first (MH-592): every unread conversation, newest first, then the read ones."`
		Archived      bool   `query:"archived" doc:"MH-657: only YOUR archived chats. Without it archived chats are left out (unless contact_id or customer_email narrows the list)."`
		Favourites    bool   `query:"favourites" doc:"MH-657: only YOUR favourites."`
		Groups        bool   `query:"groups" doc:"MH-657: only group threads."`
		LabelID       string `query:"label_id" doc:"MH-657: only chats carrying this label."`
		Keep          string `query:"keep" maxLength:"4000" doc:"unread_first only (MH-599): comma-separated conversation ids to keep sorted among the unread ones (the ones this tab opened while unread), so opening a thread does not move it."`
	}) (*body[ConversationList], error) {
		res, err := ListConversations(ctx, a, ConversationFilter{
			Status: in.Status, Channel: in.Channel, Unread: in.Unread, ContactID: in.ContactID, MailboxID: in.MailboxID,
			CustomerEmail: in.CustomerEmail, Q: in.Q, Cursor: in.Cursor, Limit: in.Limit, Order: in.Order, Keep: strings.Split(in.Keep, ","),
			Archived: in.Archived, Favourites: in.Favourites, Groups: in.Groups, LabelID: in.LabelID,
		})
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-conversation", Method: http.MethodGet, Path: "/api/w/conversations/{id}",
		Summary: "One conversation and the newest page of its thread (opening it marks it read)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[conversationDetail], error) {
		res, err := GetConversation(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-reply-conversation", Method: http.MethodPost, Path: "/api/w/conversations/{id}/reply",
		DefaultStatus: http.StatusCreated, Summary: "Reply to a conversation as the signed-in member", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Body    string `json:"body" minLength:"1" maxLength:"8000"`
			Subject string `json:"subject,omitempty" maxLength:"500" doc:"Email only: overrides the default Re: <original subject>. Ignored on other channels."`
			ReplyToMessageID string `json:"reply_to_message_id,omitempty" maxLength:"64" doc:"MH-664: quote this earlier message of the same conversation. WhatsApp shows it as a quoted reply; email answers it (In-Reply-To)."`
		}
	}) (*body[struct {
		Message MessageOut `json:"message"`
	}], error) {
		m, err := Reply(WithReplyTo(ctx, in.Body.ReplyToMessageID), a, in.ID, in.Body.Body, in.Body.Subject)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Message MessageOut `json:"message"`
		}{*m}), nil
	})

	// MH-659: the composer's attach button, mic button and location control —
	// one multipart endpoint alongside the plain-text one above, since a file
	// or a recorded voice note cannot ride in a JSON body. At most one
	// attachment per send (the same limit WhatsApp itself has), matching
	// message_attachments and the kinds migration 181 already defined.
	huma.Register(api, huma.Operation{
		OperationID: "w-reply-conversation-attachment", Method: http.MethodPost, Path: "/api/w/conversations/{id}/reply/attachment",
		DefaultStatus: http.StatusCreated,
		// A 20MB video on a slow line takes longer than huma's 5s default.
		BodyReadTimeout: 2 * time.Minute,
		Summary:         "Reply to a conversation with one attachment: an image, video, document, voice note or location",
		Tags:            tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID      string `path:"id"`
		RawBody huma.MultipartFormFiles[struct {
			File huma.FormFile `form:"file" required:"false" doc:"The image, video, document or voice-note recording to send."`
		}]
	}) (*body[struct {
		Message MessageOut `json:"message"`
	}], error) {
		data := in.RawBody.Data()
		form := in.RawBody.Form
		val := func(k string) string {
			if form != nil {
				if v := form.Value[k]; len(v) > 0 {
					return strings.TrimSpace(v[0])
				}
			}
			return ""
		}
		text := val("body")
		subject := val("subject")
		kind := val("kind") // image, video, document, voice or location

		var attachments []ReplyAttachment
		switch {
		case kind == "location":
			lat, latErr := strconv.ParseFloat(val("lat"), 64)
			lng, lngErr := strconv.ParseFloat(val("lng"), 64)
			if latErr != nil || lngErr != nil {
				return nil, huma.Error422UnprocessableEntity("a location needs a valid lat and lng")
			}
			attachments = append(attachments, ReplyAttachment{
				Kind: "location", Lat: lat, Lng: lng,
				LocationName: val("location_name"), LocationAddress: val("location_address"),
			})
		case data.File.IsSet:
			b, rerr := io.ReadAll(data.File)
			if rerr != nil {
				return nil, huma.Error400BadRequest("could not read the attached file")
			}
			if int64(len(b)) > maxAttachmentSize {
				return nil, huma.Error422UnprocessableEntity("attachments are limited to 20MB")
			}
			if kind == "" {
				kind = "document"
			}
			if kind != "image" && kind != "video" && kind != "document" && kind != "voice" {
				return nil, huma.Error422UnprocessableEntity("kind must be image, video, document, voice or location")
			}
			filename := data.File.Filename
			if filename == "" {
				filename = val("filename")
			}
			attachments = append(attachments, ReplyAttachment{
				Kind: kind, Filename: filename, ContentType: data.File.ContentType, Data: b,
			})
		default:
			return nil, huma.Error422UnprocessableEntity("an attachment (file or location) is required")
		}

		m, err := ReplyWithAttachments(WithReplyTo(ctx, val("reply_to_message_id")), a, in.ID, text, subject, attachments)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Message MessageOut `json:"message"`
		}{*m}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-conversation", Method: http.MethodPatch, Path: "/api/w/conversations/{id}",
		Summary: "Assign, close, reopen, snooze or retitle a conversation, or link it to a CRM contact", Tags: tags,
		Description: "Snoozing: send `snoozed_until` (a future time, at most a year ahead) and the conversation becomes snoozed until then, " +
			"when it reopens as unread. `status: snoozed` with no time snoozes it until the customer writes again. " +
			"Any status other than snoozed clears the time; closing records `closed_at` for the inbox stats.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body ConversationPatch
	}) (*body[struct {
		Conversation ConversationOut `json:"conversation"`
	}], error) {
		c, err := UpdateConversation(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Conversation ConversationOut `json:"conversation"`
		}{*c}), nil
	})

	// ---- chat widgets ------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-chat-widgets", Method: http.MethodGet, Path: "/api/w/chat_widgets",
		Summary: "Chat widgets this workspace can embed", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Widgets []WidgetOut `json:"widgets"`
	}], error) {
		list, err := ListWidgets(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Widgets []WidgetOut `json:"widgets"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-chat-widget", Method: http.MethodPost, Path: "/api/w/chat_widgets",
		DefaultStatus: http.StatusCreated, Summary: "Create a chat widget (its key is public; the origin allow-list is what protects it)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name           string   `json:"name" minLength:"1" maxLength:"80"`
			AllowedOrigins []string `json:"allowed_origins,omitempty" doc:"e.g. https://example.com — with none set the widget is blocked."`
			WidgetPatch
		}
	}) (*body[struct {
		Widget WidgetOut `json:"widget"`
	}], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		settings := defaultSettings()
		if err := in.Body.WidgetPatch.apply(&settings); err != nil {
			return nil, err
		}
		key, err := token("cwk_")
		if err != nil {
			return nil, huma.Error500InternalServerError("could not generate a key")
		}
		if bad := badOrigin(in.Body.AllowedOrigins); bad != "" {
			return nil, originError(bad)
		}
		origins := strings.Join(splitOrigins(strings.Join(in.Body.AllowedOrigins, ",")), ",")
		var id string
		if err := db().QueryRowContext(ctx, `
			INSERT INTO chat_widgets (workspace_id, name, public_key, allowed_origins, created_by)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			s.WorkspaceID, name, key, origins, s.UserID,
		).Scan(&id); err != nil {
			return nil, huma.Error500InternalServerError("could not create the chat widget")
		}
		if _, err := db().ExecContext(ctx, `UPDATE chat_widgets SET `+settingsSet+` WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, settingsArgs(settings)...)...); err != nil {
			return nil, huma.Error500InternalServerError("could not save the chat widget's settings")
		}
		w, err := readWidget(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the chat widget")
		}
		return out(struct {
			Widget WidgetOut `json:"widget"`
		}{w}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-chat-widget", Method: http.MethodPatch, Path: "/api/w/chat_widgets/{id}",
		Summary: "Rename a widget, change its allowed origins or greeting, enable or disable it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body WidgetUpdate
	}) (*body[struct {
		Widget WidgetOut `json:"widget"`
	}], error) {
		w, err := UpdateWidget(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Widget WidgetOut `json:"widget"`
		}{*w}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-chat-widget", Method: http.MethodDelete, Path: "/api/w/chat_widgets/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a chat widget (its conversations stay, unlinked)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM chat_widgets WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the chat widget")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("chat widget not found")
		}
		return &struct{}{}, nil
	})

	registerCRM(api, a)
	registerCanned(api, a)
	registerEmailTemplates(api, a)
	registerStats(api, a)
	registerPublicChat(api, a)
	registerThread(api, a)
	registerAgentTyping(api, a)
}

// thread reads a conversation's newest messages on the app (RLS) pool, oldest
// first: what the visitor's widget polls. It deliberately does NOT join `users`
// for the author's email: the app role has no grant on that table at all
// (cmd/migrate REVOKEs it), so the join fails outright. The UI maps
// author_user_id -> email from /api/w/members, the same way the CRM's
// useMemberNames does for activity authors and deal owners.
//
// It used to be the FIRST 500 (ascending, LIMIT 500), so a thread past 500
// stopped showing new messages at all (MH-471). The member's thread pages
// through all of it (thread.go).
func thread(ctx context.Context, db *sql.DB, wsID, convID string) ([]MessageOut, error) {
	// "" (MH-662): this helper reads for search/context/export/AI, not for a
	// signed-in member's own view, so there is no member to hide-filter or
	// star against.
	page, err := threadPage(ctx, db, wsID, convID, "", threadPageMax, "")
	if err != nil {
		return nil, err
	}
	return page.Messages, nil
}

// threadFromStart reads a thread's first messages, oldest first: a transcript
// starts at the beginning, and cuts itself at maxTranscript long before this.
func threadFromStart(ctx context.Context, db *sql.DB, wsID, convID string) ([]MessageOut, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, direction, author_user_id, body, created_at
		FROM messages WHERE workspace_id = $1 AND conversation_id = $2 ORDER BY created_at, id LIMIT 500`, wsID, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []MessageOut{}
	for rows.Next() {
		var m MessageOut
		var author sql.NullString
		if err := rows.Scan(&m.ID, &m.Direction, &author, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.AuthorUserID = strp(author)
		list = append(list, m)
	}
	return list, rows.Err()
}

// checkMember refuses an assignee who is not an active member of the workspace
// — otherwise a conversation could be parked on someone who cannot see it.
func checkMember(ctx context.Context, db *sql.DB, wsID, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 AND status = 'active'`, wsID, userID).Scan(&n); err != nil {
		return huma.Error500InternalServerError("could not check the assignee")
	}
	if n == 0 {
		return huma.Error422UnprocessableEntity("that person is not an active member of this workspace")
	}
	return nil
}

// ---- public: the chat widget ----------------------------------------------
//
// POST/GET /api/chat/{key}/messages have no session and no X-Workspace-ID: the
// widget's public key in the path plus the browser Origin allow-list ARE the
// authorization, the same shape internal/notifications uses for /api/hooks/{id}
// and internal/marketing for its mail links. The key is resolved on the owner
// pool (the one cross-tenant read), and everything after runs on the app pool
// scoped to the workspace it resolved to. Both are listed as public prefixes in
// internal/server/guard.go's classify().
//
// A visitor continues their own thread with visitor_token, which the widget
// keeps in localStorage — unguessable, and scoped to one conversation, so it
// can read and write that thread and nothing else.
//
// The key and the Origin say WHICH inbox, not WHO may write to it: both are
// public. So the write is rate limited per visitor, per client address and
// per widget (ratelimit.go, MH-377), and answers 429 with Retry-After.

type chatIn struct {
	Key            string `path:"key"`
	Origin         string `header:"Origin"`
	UserAgent      string `header:"User-Agent"`
	Referer        string `header:"Referer"`
	AcceptLanguage string `header:"Accept-Language"`
	Body           struct {
		Body         string `json:"body" maxLength:"8000"`
		VisitorToken string `json:"visitor_token,omitempty" doc:"From a previous reply; omit to start a conversation."`
		Name         string `json:"name,omitempty" maxLength:"120"`
		Email        string `json:"email,omitempty" maxLength:"200"`
		Phone        string `json:"phone,omitempty" maxLength:"40" doc:"With its country code (+20 100 123 4567). When the widget requires contact details, a name and an email or a phone are needed to open a conversation."`
		PageURL      string `json:"page_url,omitempty" maxLength:"2000" doc:"The page the visitor is on. Kept only when it is on the request's own (allowed) origin; otherwise the Referer is used."`
		Locale       string `json:"locale,omitempty" maxLength:"35" doc:"The widget's language, e.g. ar or en-GB. Falls back to Accept-Language."`
	}
}

func registerPublicChat(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "chat-send", Method: http.MethodPost, Path: "/api/chat/{key}/messages",
		DefaultStatus: http.StatusCreated, Summary: "A visitor writes (public: the widget key plus the Origin allow-list are the authorization)", Tags: tags,
		Description: "Rate limited per visitor token, per client address and per widget, with opening a conversation limited more tightly than writing in one. Over a limit the answer is 429 with Retry-After (seconds).",
		Responses: map[string]*huma.Response{
			"429": {Description: "Too many messages. Retry-After says how many seconds to wait."},
		},
	}, func(ctx context.Context, in *chatIn) (*body[struct {
		VisitorToken string `json:"visitor_token" doc:"Keep this to continue the conversation."`
		Sent         bool   `json:"sent"`
	}], error) {
		text := strings.TrimSpace(in.Body.Body)
		if text == "" {
			return nil, huma.Error422UnprocessableEntity("a message is required")
		}
		if len(text) > maxMessage {
			return nil, huma.Error400BadRequest("that message is too long")
		}
		w, err := resolveWidget(ctx, a, in.Key, in.Origin)
		if err != nil {
			return nil, err
		}
		wsID, widgetID := w.WorkspaceID, w.ID
		visitor := strings.TrimSpace(in.Body.VisitorToken)
		// Who the visitor is, checked before anything is counted or written:
		// only opening a conversation asks (prechat.go).
		var who visitorContact
		if visitor == "" {
			if who, err = checkVisitorContact(in.Body.Name, in.Body.Email, in.Body.Phone, w.Settings.RequireContact); err != nil {
				return nil, err
			}
		}

		// After resolve, so an unknown key or a refused origin costs no limiter
		// entry; before any write, so a refused request writes nothing.
		ip := clientip.From(ctx)
		if ip == "" {
			ip = "unknown" // only outside the server's middleware (a test harness)
		}
		if err := allowChat(widgetID, clientip.Bucket(ip), visitor, visitor == ""); err != nil {
			return nil, err
		}
		sctx := tenant.WithWorkspace(ctx, wsID)

		convID := ""
		if visitor != "" {
			// Their own thread, or nothing — the token is scoped to one conversation.
			err := a.SQLDB.QueryRowContext(sctx, `SELECT id FROM conversations WHERE workspace_id = $1 AND visitor_token = $2`, wsID, visitor).Scan(&convID)
			if notFound(err) {
				return nil, huma.Error404NotFound("that conversation is not available")
			} else if err != nil {
				return nil, huma.Error500InternalServerError("could not read the conversation")
			}
		} else {
			visitor, err = token("cvt_")
			if err != nil {
				return nil, huma.Error500InternalServerError("could not start the conversation")
			}
			// Where they were, in what, and in which language: the first things
			// worth knowing when answering, recorded once, when the thread opens.
			vc := visitorContextOf(in.Origin, in.Body.PageURL, in.Referer, in.UserAgent, in.Body.Locale, in.AcceptLanguage)
			if err := a.SQLDB.QueryRowContext(sctx, `
				INSERT INTO conversations (workspace_id, channel, widget_id, customer_name, customer_email, customer_phone, visitor_token, subject,
				                           page_url, user_agent, locale, last_message_at, unread)
				VALUES ($1, 'chat', $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), true) RETURNING id`,
				wsID, widgetID, who.Name, who.Email, who.Phone, visitor, trim(text, 80),
				vc.PageURL, vc.UserAgent, vc.Locale,
			).Scan(&convID); err != nil {
				return nil, huma.Error500InternalServerError("could not start the conversation")
			}
			if w.Settings.RequireContact {
				if _, err := linkChatContact(sctx, a.SQLDB, wsID, convID, who, vc.Locale); err != nil {
					slog.Warn("chat: could not file the visitor in the CRM", "workspace", wsID, "conversation", convID, "err", err)
				}
			}
		}
		// A blocked visitor is told no more than a closed door (MH-657).
		if isBlocked(sctx, a.SQLDB, wsID, convID) {
			return nil, huma.Error403Forbidden("this conversation is not accepting messages")
		}
		if _, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO messages (workspace_id, conversation_id, direction, body) VALUES ($1, $2, 'in', $3)`,
			wsID, convID, text); err != nil {
			return nil, huma.Error500InternalServerError("could not send the message")
		}
		// An inbound message makes the thread unread for the team again.
		_, _ = a.SQLDB.ExecContext(sctx, `UPDATE conversations SET last_message_at = now(), unread = true, updated_at = now(), `+reopenOnInbound+`
			WHERE workspace_id = $1 AND id = $2`, wsID, convID)

		// The team is told the moment a visitor writes, rather than up to ten
		// seconds later when a poll happens to come round (fadymondy.com 01b5345):
		// the open tabs at once, and the devices of whoever it alerts (MH-378).
		announceInbound(ctx, a, wsID, convID, text, time.Time{})

		return out(struct {
			VisitorToken string `json:"visitor_token" doc:"Keep this to continue the conversation."`
			Sent         bool   `json:"sent"`
		}{visitor, true}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "chat-poll", Method: http.MethodGet, Path: "/api/chat/{key}/messages",
		Summary: "The visitor's own thread (public: the widget key, the Origin allow-list and the visitor token)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Key          string `path:"key"`
		Origin       string `header:"Origin"`
		VisitorToken string `query:"visitor_token"`
	}) (*body[ThreadOut], error) {
		w, err := resolveWidget(ctx, a, in.Key, in.Origin)
		if err != nil {
			return nil, err
		}
		wsID := w.WorkspaceID
		visitor := strings.TrimSpace(in.VisitorToken)
		if visitor == "" {
			return nil, huma.Error422UnprocessableEntity("visitor_token is required")
		}
		sctx := tenant.WithWorkspace(ctx, wsID)
		var convID, status string
		var unread int
		if err := a.SQLDB.QueryRowContext(sctx, `SELECT id, status, unread_visitor FROM conversations WHERE workspace_id = $1 AND visitor_token = $2`, wsID, visitor).Scan(&convID, &status, &unread); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("that conversation is not available")
			}
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		msgs, err := thread(sctx, a.SQLDB, wsID, convID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the thread")
		}
		// The visitor sees who wrote, not who the agent is: emails of staff are
		// not the visitor's business.
		for i := range msgs {
			msgs[i].AuthorEmail = ""
			msgs[i].AuthorUserID = nil
		}
		cfg := publicConfig(w.Settings, wsID)
		return out(ThreadOut{Messages: msgs, Status: status, Unread: unread,
			Greeting: cfg.Greeting, GreetingAr: cfg.GreetingAr, Presence: cfg.Presence}), nil
	})

	registerWidgetPublic(api, a)
	registerVisitorTyping(api, a)
}

// ThreadOut is the visitor's view of their thread. The greeting and presence
// ride along (MH-382), so a client that only ever polls this still shows them.
type ThreadOut struct {
	Messages   []MessageOut `json:"messages"`
	Status     string       `json:"status"`
	Unread     int          `json:"unread" doc:"Team replies this visitor has not seen yet (cleared by POST /api/chat/{key}/read)."`
	Greeting   string       `json:"greeting"`
	GreetingAr string       `json:"greeting_ar"`
	Presence   string       `json:"presence" enum:"online,away,offline"`
}
