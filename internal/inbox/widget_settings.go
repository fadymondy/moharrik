package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/presence"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
What a chat widget does on the customer's page (MH-382), per widget.

fadymondy.com keeps one set of these for its one site (chat_module_api.go's
ChatWidgetSettings: enabled, invite, invite_after_seconds) and hard-codes the
rest in chat-widget.tsx: the greeting, the look, the page-dependent openers
(lib/chat/quick-actions.ts) and the generate_lead event. Moharrik has many
widgets on many sites, so every one of those is a column on chat_widgets
(internal/db/post/100_chat_widget_parity.sql) and the widget reads them from
GET /api/chat/{key}/config.

The copied snippet carries the same values as data-* attributes, so the first
paint is already right before the config arrives. The config is what keeps an
old snippet current: change the greeting here and every page that embeds the
key shows the new one without anybody re-pasting.
*/

// QuickAction is an opener shown in an empty panel. Picking it sends Message
// as the visitor's first message (fadymondy.com FM-137: a suggestion you then
// have to send yourself is two steps for one intention).
type QuickAction struct {
	Label   string `json:"label" maxLength:"40"`
	Message string `json:"message" maxLength:"500"`
	// Path limits it to pages whose path starts with this ("/pricing"). Empty = every page.
	Path string `json:"path,omitempty" maxLength:"200" doc:"Only on pages whose path starts with this, e.g. /pricing. Empty: every page."`
	// Locale limits it to the widget in one language. Empty = both.
	Locale string `json:"locale,omitempty" enum:",en,ar" doc:"Only when the widget runs in this language. Empty: both."`
}

// WidgetSettings is everything about a widget's behaviour on the page. Used as
// the wire shape for reading AND for create/patch (every field optional on a
// patch — see WidgetPatch).
type WidgetSettings struct {
	Greeting           string        `json:"greeting" maxLength:"500"`
	GreetingAr         string        `json:"greeting_ar" maxLength:"500" doc:"Shown instead of greeting when the widget runs in Arabic. Empty: greeting."`
	Title              string        `json:"title" maxLength:"60" doc:"The panel title. Empty: the widget's own (Chat with us)."`
	TitleAr            string        `json:"title_ar" maxLength:"60"`
	Accent             string        `json:"accent" doc:"#RGB or #RRGGBB. Empty: the widget's default."`
	Theme              string        `json:"theme" enum:"auto,light,dark" doc:"auto follows the visitor's system setting."`
	Position           string        `json:"position" enum:"start,end" doc:"end = bottom right in LTR, bottom left in RTL."`
	Invite             bool          `json:"invite" doc:"Open the panel by itself, once per visit, for a visitor who stays."`
	InviteAfterSeconds int           `json:"invite_after_seconds" minimum:"5" maximum:"600"`
	QuickActions       []QuickAction `json:"quick_actions" maxItems:"8"`
	LeadEvent          bool          `json:"lead_event" doc:"Report a visitor's first message as a generate_lead analytics event on the host page (gtag / dataLayer)."`
}

func defaultSettings() WidgetSettings {
	return WidgetSettings{Theme: "auto", Position: "end", InviteAfterSeconds: 25, QuickActions: []QuickAction{}, LeadEvent: true}
}

// WidgetPatch is a partial WidgetSettings: nil = unchanged.
type WidgetPatch struct {
	Greeting           *string        `json:"greeting,omitempty" maxLength:"500"`
	GreetingAr         *string        `json:"greeting_ar,omitempty" maxLength:"500"`
	Title              *string        `json:"title,omitempty" maxLength:"60"`
	TitleAr            *string        `json:"title_ar,omitempty" maxLength:"60"`
	Accent             *string        `json:"accent,omitempty" doc:"#RGB or #RRGGBB; empty resets to the widget's default."`
	Theme              *string        `json:"theme,omitempty" enum:"auto,light,dark"`
	Position           *string        `json:"position,omitempty" enum:"start,end"`
	Invite             *bool          `json:"invite,omitempty"`
	InviteAfterSeconds *int           `json:"invite_after_seconds,omitempty" minimum:"5" maximum:"600"`
	QuickActions       *[]QuickAction `json:"quick_actions,omitempty" maxItems:"8"`
	LeadEvent          *bool          `json:"lead_event,omitempty"`
}

var accentRE = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// apply writes the patch onto s and validates the result. The accent ends up
// inside a stylesheet on somebody else's page, so it is a strict colour or
// nothing; the rest is bounded here as well as by the schema, because the MCP
// and any future caller come through this too.
func (p WidgetPatch) apply(s *WidgetSettings) error {
	str := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	str(&s.Greeting, p.Greeting)
	str(&s.GreetingAr, p.GreetingAr)
	str(&s.Title, p.Title)
	str(&s.TitleAr, p.TitleAr)
	str(&s.Accent, p.Accent)
	str(&s.Theme, p.Theme)
	str(&s.Position, p.Position)
	if p.Invite != nil {
		s.Invite = *p.Invite
	}
	if p.InviteAfterSeconds != nil {
		s.InviteAfterSeconds = *p.InviteAfterSeconds
	}
	if p.LeadEvent != nil {
		s.LeadEvent = *p.LeadEvent
	}
	if p.QuickActions != nil {
		s.QuickActions = *p.QuickActions
	}
	return s.validate()
}

func (s *WidgetSettings) validate() error {
	if s.Accent != "" && !accentRE.MatchString(s.Accent) {
		return huma.Error422UnprocessableEntity("the accent must be a colour like #4B00B6")
	}
	if s.Theme == "" {
		s.Theme = "auto"
	}
	if s.Theme != "auto" && s.Theme != "light" && s.Theme != "dark" {
		return huma.Error422UnprocessableEntity("theme must be auto, light or dark")
	}
	if s.Position == "" {
		s.Position = "end"
	}
	if s.Position != "start" && s.Position != "end" {
		return huma.Error422UnprocessableEntity("position must be start or end")
	}
	if s.InviteAfterSeconds < 5 || s.InviteAfterSeconds > 600 {
		return huma.Error422UnprocessableEntity("the invitation opens after 5 to 600 seconds")
	}
	if len([]rune(s.Greeting)) > 500 || len([]rune(s.GreetingAr)) > 500 {
		return huma.Error422UnprocessableEntity("the greeting is at most 500 characters")
	}
	if len([]rune(s.Title)) > 60 || len([]rune(s.TitleAr)) > 60 {
		return huma.Error422UnprocessableEntity("the title is at most 60 characters")
	}
	if s.QuickActions == nil {
		s.QuickActions = []QuickAction{}
	}
	if len(s.QuickActions) > 8 {
		return huma.Error422UnprocessableEntity("at most 8 quick actions")
	}
	for i := range s.QuickActions {
		q := &s.QuickActions[i]
		q.Label, q.Message = strings.TrimSpace(q.Label), strings.TrimSpace(q.Message)
		q.Path, q.Locale = strings.TrimSpace(q.Path), strings.TrimSpace(q.Locale)
		if q.Label == "" || q.Message == "" {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("quick action %d needs a label and a message", i+1))
		}
		if len([]rune(q.Label)) > 40 || len([]rune(q.Message)) > 500 {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("quick action %d: the label is at most 40 characters and the message 500", i+1))
		}
		if q.Path != "" && (!strings.HasPrefix(q.Path, "/") || len(q.Path) > 200 || strings.ContainsAny(q.Path, " \t\r\n\"<>")) {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("quick action %d: the page path starts with / (for example /pricing)", i+1))
		}
		if q.Locale != "" && q.Locale != "en" && q.Locale != "ar" {
			return huma.Error422UnprocessableEntity(fmt.Sprintf("quick action %d: the language is en, ar or empty", i+1))
		}
	}
	return nil
}

// ---- reading and writing the row ----------------------------------------------

const widgetColumns = `id, name, public_key, allowed_origins, enabled, created_at,
	greeting, greeting_ar, title, title_ar, accent, theme, position, invite, invite_after_seconds, quick_actions::text, lead_event`

func scanWidget(row interface{ Scan(...any) error }) (WidgetOut, error) {
	var w WidgetOut
	var origins, qa string
	s := &w.WidgetSettings
	if err := row.Scan(&w.ID, &w.Name, &w.PublicKey, &origins, &w.Enabled, &w.CreatedAt,
		&s.Greeting, &s.GreetingAr, &s.Title, &s.TitleAr, &s.Accent, &s.Theme, &s.Position, &s.Invite, &s.InviteAfterSeconds, &qa, &s.LeadEvent); err != nil {
		return w, err
	}
	s.QuickActions = []QuickAction{}
	_ = json.Unmarshal([]byte(qa), &s.QuickActions)
	w.AllowedOrigins = splitOrigins(origins)
	w.Embed = embedSnippet(w.PublicKey, *s)
	return w, nil
}

func readWidget(ctx context.Context, db *sql.DB, wsID, id string) (WidgetOut, error) {
	return scanWidget(db.QueryRowContext(ctx, `SELECT `+widgetColumns+` FROM chat_widgets WHERE workspace_id = $1 AND id = $2`, wsID, id))
}

// settingsArgs are the settings columns in widgetColumns' order, for a write.
func settingsArgs(s WidgetSettings) []any {
	qa, _ := json.Marshal(s.QuickActions)
	return []any{s.Greeting, s.GreetingAr, s.Title, s.TitleAr, s.Accent, s.Theme, s.Position, s.Invite, s.InviteAfterSeconds, string(qa), s.LeadEvent}
}

const settingsSet = `greeting = $3, greeting_ar = $4, title = $5, title_ar = $6, accent = $7, theme = $8, position = $9,
	invite = $10, invite_after_seconds = $11, quick_actions = $12::jsonb, lead_event = $13`

// embedSnippet is the tag to paste. It names the settings that change the
// first paint (the SDK reads the rest from the config), each escaped for an
// HTML attribute — a greeting is the workspace's own text, but it is still
// text going into markup on somebody's site.
func embedSnippet(key string, s WidgetSettings) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<script src="%s/chat/v1.js" data-key="%s"`, publicBase(), html.EscapeString(key))
	attr := func(name, v string) {
		if v != "" {
			fmt.Fprintf(&b, ` %s="%s"`, name, html.EscapeString(v))
		}
	}
	attr("data-greeting", s.Greeting)
	attr("data-greeting-ar", s.GreetingAr)
	attr("data-title", s.Title)
	attr("data-title-ar", s.TitleAr)
	attr("data-accent", s.Accent)
	if s.Theme != "auto" {
		attr("data-theme", s.Theme)
	}
	if s.Position != "end" {
		attr("data-position", s.Position)
	}
	b.WriteString(` defer></script>`)
	return b.String()
}

// ---- public: what the widget reads ---------------------------------------------

// PublicConfig is GET /api/chat/{key}/config. Nothing here is private: it is
// what the widget shows on a public page anyway, plus a presence state that is
// deliberately a word, never a time and never a name (internal/presence).
type PublicConfig struct {
	WidgetSettings
	Presence string `json:"presence" enum:"online,away,offline" doc:"Whether anybody who can answer is around: online (active in the last 5 minutes), away (45) or offline."`
}

func publicConfig(s WidgetSettings, wsID string) PublicConfig {
	return PublicConfig{WidgetSettings: s, Presence: string(presence.Current(wsID))}
}

// resolvedWidget is what the public routes know once a key and an Origin
// check out.
type resolvedWidget struct {
	WorkspaceID string
	ID          string
	Settings    WidgetSettings
}

// resolveWidget finds the widget's workspace on the owner pool (the one
// cross-tenant read, as in registerPublicChat) and checks the browser Origin
// against its allow-list. The answers are the ones the message routes give.
func resolveWidget(ctx context.Context, a *app.App, key, origin string) (*resolvedWidget, error) {
	var wsID, origins string
	var enabled bool
	w, err := scanWidgetFull(a.SysDB.QueryRowContext(ctx, `SELECT workspace_id, `+widgetColumns+` FROM chat_widgets WHERE public_key = $1`, key), &wsID, &origins, &enabled)
	if notFound(err) {
		return nil, huma.Error404NotFound("this chat widget does not exist")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not resolve the chat widget")
	}
	if !enabled {
		return nil, huma.Error403Forbidden("this chat widget is disabled")
	}
	if !originAllowed(origins, origin) {
		return nil, huma.Error403Forbidden("this origin is not allowed for this chat widget")
	}
	return &resolvedWidget{WorkspaceID: wsID, ID: w.ID, Settings: w.WidgetSettings}, nil
}

func scanWidgetFull(row *sql.Row, wsID, origins *string, enabled *bool) (WidgetOut, error) {
	var w WidgetOut
	var qa string
	s := &w.WidgetSettings
	if err := row.Scan(wsID, &w.ID, &w.Name, &w.PublicKey, origins, enabled, &w.CreatedAt,
		&s.Greeting, &s.GreetingAr, &s.Title, &s.TitleAr, &s.Accent, &s.Theme, &s.Position, &s.Invite, &s.InviteAfterSeconds, &qa, &s.LeadEvent); err != nil {
		return w, err
	}
	s.QuickActions = []QuickAction{}
	_ = json.Unmarshal([]byte(qa), &s.QuickActions)
	return w, nil
}

func registerWidgetPublic(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "chat-config", Method: http.MethodGet, Path: "/api/chat/{key}/config",
		Summary: "What the widget should show and do: greeting, look, invitation, quick actions, presence (public: the key plus the Origin allow-list)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Key    string `path:"key"`
		Origin string `header:"Origin"`
	}) (*body[PublicConfig], error) {
		w, err := resolveWidget(ctx, a, in.Key, in.Origin)
		if err != nil {
			return nil, err
		}
		return out(publicConfig(w.Settings, w.WorkspaceID)), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "chat-read", Method: http.MethodPost, Path: "/api/chat/{key}/read",
		Summary: "The visitor has seen the team's replies: clears their unread count (public: key, Origin and visitor token)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Key    string `path:"key"`
		Origin string `header:"Origin"`
		Body   struct {
			VisitorToken string `json:"visitor_token" minLength:"1" maxLength:"200"`
		}
	}) (*body[struct {
		Unread int `json:"unread"`
	}], error) {
		w, err := resolveWidget(ctx, a, in.Key, in.Origin)
		if err != nil {
			return nil, err
		}
		sctx := tenant.WithWorkspace(ctx, w.WorkspaceID)
		// Scoped to the token's own conversation, and the same 404 whether the
		// token never existed or belongs to another widget: an answer that
		// differed would say which tokens are real.
		res, err := a.SQLDB.ExecContext(sctx, `UPDATE conversations SET unread_visitor = 0
			WHERE workspace_id = $1 AND visitor_token = $2 AND widget_id = $3`, w.WorkspaceID, strings.TrimSpace(in.Body.VisitorToken), w.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not mark the conversation read")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("that conversation is not available")
		}
		return out(struct {
			Unread int `json:"unread"`
		}{0}), nil
	})
}

// ---- public: the visitor's live stream ------------------------------------------

/*
VisitorEvents is GET /api/chat/{key}/events?visitor_token=… — Server-Sent
Events for ONE visitor's ONE conversation (MH-382).

fadymondy.com uses a WebSocket for this and polls when it cannot connect;
Moharrik already streams to its members over SSE (internal/realtime), and SSE
is the simpler fit for a one-way "something changed": EventSource reconnects
by itself, needs no upgrade a proxy can block, and the widget keeps polling as
the fallback either way. The rule from fadymondy.com's chat.go stands: the
database is how a message arrives at all, this is how it arrives quickly. An
event carries no message text, only "refetch", so nothing reaches the page
that the ordinary GET would not have given it.

Authorization is exactly the GET thread's: the key, the Origin allow-list, and
a visitor token that belongs to a conversation of THIS widget. Every refusal
happens before the stream opens, with the same statuses as the JSON routes.
*/
func VisitorEvents(a *app.App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/chat/"), "/events")
		rw, err := resolveWidget(ctx, a, key, r.Header.Get("Origin"))
		if err != nil {
			writeHumaError(w, err)
			return
		}
		visitor := strings.TrimSpace(r.URL.Query().Get("visitor_token"))
		if visitor == "" {
			writeJSONError(w, http.StatusUnprocessableEntity, "visitor_token is required")
			return
		}
		var convID string
		err = a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, rw.WorkspaceID),
			`SELECT id FROM conversations WHERE workspace_id = $1 AND visitor_token = $2 AND widget_id = $3`,
			rw.WorkspaceID, visitor, rw.ID).Scan(&convID)
		if notFound(err) {
			writeJSONError(w, http.StatusNotFound, "that conversation is not available")
			return
		} else if err != nil {
			writeJSONError(w, http.StatusInternalServerError, "could not read the conversation")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, "streaming unsupported")
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache, no-transform")
		h.Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ": open\n\nretry: 5000\n\n")
		flusher.Flush()

		events, stop := visitors.subscribe(convID)
		defer stop()
		ticker := time.NewTicker(visitorHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case kind, open := <-events:
				if !open {
					return
				}
				fmt.Fprintf(w, "data: {\"kind\":%q}\n\n", kind)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			}
		}
	}
}

// visitorHeartbeat keeps proxies (NPM, Cloudflare) from closing a quiet stream.
const visitorHeartbeat = 25 * time.Second

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]any{"status": status, "title": http.StatusText(status), "detail": msg})
	_, _ = w.Write(b)
}

func writeHumaError(w http.ResponseWriter, err error) {
	if se, ok := err.(huma.StatusError); ok {
		msg := err.Error()
		if m, ok := err.(*huma.ErrorModel); ok {
			msg = m.Detail
		}
		writeJSONError(w, se.GetStatus(), msg)
		return
	}
	writeJSONError(w, http.StatusInternalServerError, "could not resolve the chat widget")
}
