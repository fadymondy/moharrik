package inbox

import (
	"context"
	"database/sql"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/clientip"
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
	       c.snoozed_until, c.closed_at
	FROM conversations c
	LEFT JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
	LEFT JOIN companies co ON co.workspace_id = ct.workspace_id AND co.id = ct.company_id
	LEFT JOIN deals d ON d.workspace_id = c.workspace_id AND d.id = c.deal_id
	WHERE c.workspace_id = $1`

func scanConversation(row interface{ Scan(...any) error }) (ConversationOut, error) {
	var c ConversationOut
	var contactID, assignee, ctName, ctEmail, companyID, coName, dealID, dealTitle sql.NullString
	var snoozed, closed sql.NullTime
	var preview string
	if err := row.Scan(&c.ID, &c.Channel, &c.Subject, &c.Status, &c.CustomerName, &c.CustomerEmail, &contactID,
		&assignee, &c.Unread, &c.LastMessageAt, &c.CreatedAt, &c.PageURL, &c.UserAgent, &c.Locale, &preview, &c.MessageCount,
		&c.CustomerPhone, &c.LinkedBy, &ctName, &ctEmail, &companyID, &coName, &dealID, &dealTitle, &snoozed, &closed); err != nil {
		return c, err
	}
	c.ContactID = strp(contactID)
	c.AssigneeUserID = strp(assignee)
	c.Preview = trim(preview, 140)
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
	return c, nil
}

// readConversation is one conversation by id, in the list's shape.
func readConversation(ctx context.Context, db *sql.DB, wsID, id string) (ConversationOut, error) {
	return scanConversation(db.QueryRowContext(ctx, conversationSelect+` AND c.id = $2`, wsID, id))
}

// Register mounts the inbox endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }
	registerContactWhatsApp(api, a) // MH-410

	// ---- conversations -----------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-conversations", Method: http.MethodGet, Path: "/api/w/conversations",
		Summary: "The inbox: conversations, most recent first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Status    string `query:"status" enum:",open,snoozed,closed"`
		Unread    bool   `query:"unread" doc:"Only conversations the team has not opened since the last inbound message."`
		Channel   string `query:"channel" enum:",chat,email,whatsapp"`
		ContactID string `query:"contact_id" doc:"Only conversations linked to this CRM contact."`
		Q         string `query:"q" maxLength:"200" doc:"Search the customer's name, email and phone and the subject."`
		Limit     int    `query:"limit" minimum:"1" maximum:"200"`
	}) (*body[ConversationList], error) {
		res, err := ListConversations(ctx, a, ConversationFilter{
			Status: in.Status, Channel: in.Channel, Unread: in.Unread, ContactID: in.ContactID, Q: in.Q, Limit: in.Limit,
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
			Body string `json:"body" minLength:"1" maxLength:"8000"`
		}
	}) (*body[struct {
		Message MessageOut `json:"message"`
	}], error) {
		m, err := Reply(ctx, a, in.ID, in.Body.Body)
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
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `
			SELECT `+widgetColumns+`
			FROM chat_widgets WHERE workspace_id = $1 ORDER BY lower(name)`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list chat widgets")
		}
		defer rows.Close()
		list := []WidgetOut{}
		for rows.Next() {
			w, err := scanWidget(rows)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not list chat widgets")
			}
			list = append(list, w)
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
		Body struct {
			Name           *string   `json:"name,omitempty" maxLength:"80"`
			AllowedOrigins *[]string `json:"allowed_origins,omitempty"`
			Enabled        *bool     `json:"enabled,omitempty"`
			WidgetPatch
		}
	}) (*body[struct {
		Widget WidgetOut `json:"widget"`
	}], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		u := &update{}
		if in.Body.Name != nil {
			n := strings.TrimSpace(*in.Body.Name)
			if n == "" {
				return nil, huma.Error422UnprocessableEntity("a name is required")
			}
			u.set("name", n)
		}
		if in.Body.AllowedOrigins != nil {
			u.set("allowed_origins", strings.Join(splitOrigins(strings.Join(*in.Body.AllowedOrigins, ",")), ","))
		}
		// The settings are read, patched and validated as a whole, so a patch
		// can never leave a widget in a state validate() would refuse.
		current, err := readWidget(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("chat widget not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the chat widget")
		}
		settings := current.WidgetSettings
		if err := in.Body.WidgetPatch.apply(&settings); err != nil {
			return nil, err
		}
		for i, v := range settingsArgs(settings) {
			u.set([]string{"greeting", "greeting_ar", "title", "title_ar", "accent", "theme", "position",
				"invite", "invite_after_seconds", "quick_actions", "lead_event"}[i], v)
		}
		for i, set := range u.sets {
			if strings.HasPrefix(set, "quick_actions = ") {
				u.sets[i] = set + "::jsonb"
			}
		}
		if in.Body.Enabled != nil {
			u.set("enabled", *in.Body.Enabled)
		}
		if len(u.sets) > 0 {
			res, err := db().ExecContext(ctx, `UPDATE chat_widgets SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, in.ID}, u.args...)...)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not update the chat widget")
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("chat widget not found")
			}
		}
		w, err := readWidget(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("chat widget not found")
			}
			return nil, huma.Error500InternalServerError("could not read the chat widget")
		}
		return out(struct {
			Widget WidgetOut `json:"widget"`
		}{w}), nil
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
	registerStats(api, a)
	registerPublicChat(api, a)
	registerThread(api, a)
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
	page, err := threadPage(ctx, db, wsID, convID, "", threadPageMax)
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
		PageURL      string `json:"page_url,omitempty" maxLength:"2000" doc:"The page the visitor is on. Kept only when it is on the request's own (allowed) origin; otherwise the Referer is used."`
		Locale       string `json:"locale,omitempty" maxLength:"35" doc:"The widget's language, e.g. ar or en-GB. Falls back to Accept-Language."`
	}
}

func registerPublicChat(api huma.API, a *app.App) {
	// resolve finds the widget's workspace on the owner pool and checks the
	// browser Origin against its allow-list.
	resolve := func(ctx context.Context, key, origin string) (string, string, error) {
		var wsID, id, origins string
		var enabled bool
		err := a.SysDB.QueryRowContext(ctx, `SELECT workspace_id, id, allowed_origins, enabled FROM chat_widgets WHERE public_key = $1`, key).
			Scan(&wsID, &id, &origins, &enabled)
		if notFound(err) {
			return "", "", huma.Error404NotFound("this chat widget does not exist")
		} else if err != nil {
			return "", "", huma.Error500InternalServerError("could not resolve the chat widget")
		}
		if !enabled {
			return "", "", huma.Error403Forbidden("this chat widget is disabled")
		}
		if !originAllowed(origins, origin) {
			return "", "", huma.Error403Forbidden("this origin is not allowed for this chat widget")
		}
		return wsID, id, nil
	}

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
		wsID, widgetID, err := resolve(ctx, in.Key, in.Origin)
		if err != nil {
			return nil, err
		}
		visitor := strings.TrimSpace(in.Body.VisitorToken)

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
				INSERT INTO conversations (workspace_id, channel, widget_id, customer_name, customer_email, visitor_token, subject,
				                           page_url, user_agent, locale, last_message_at, unread)
				VALUES ($1, 'chat', $2, $3, $4, $5, $6, $7, $8, $9, now(), true) RETURNING id`,
				wsID, widgetID, strings.TrimSpace(in.Body.Name), strings.TrimSpace(in.Body.Email), visitor, trim(text, 80),
				vc.PageURL, vc.UserAgent, vc.Locale,
			).Scan(&convID); err != nil {
				return nil, huma.Error500InternalServerError("could not start the conversation")
			}
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
