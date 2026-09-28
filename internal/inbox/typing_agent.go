package inbox

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Outbound typing (MH-660): while an agent is composing a reply, the customer
should see it the same way they would on a phone or a normal chat widget.

Two destinations, from the same POST /api/w/conversations/{id}/typing:

  - WhatsApp: Evolution's sendPresence. Throttled to at most one "composing"
    every agentPresenceThrottle per conversation — the composer fires this on
    every keystroke-ish tick, and WhatsApp's own presence protocol is not
    meant to be hammered. "paused" (stop typing, or the message was sent) is
    never throttled: a customer seeing a stale "typing…" after a reply landed
    would be more confusing than one extra API call.
  - The chat widget: the visitor's own SSE stream (visitorHub), same as a
    reply or a status change already reaches it.

Never logged, never a whatsapp_sends row: this is not a message, and losing
one composing signal to a dropped presence call is not worth retrying.
*/

const agentPresenceThrottle = 5 * time.Second

var agentPresence = struct {
	sync.Mutex
	last map[string]time.Time // conversation id -> last "composing" sent
}{last: map[string]time.Time{}}

func shouldSendComposing(convID string) bool {
	agentPresence.Lock()
	defer agentPresence.Unlock()
	now := time.Now()
	if last, ok := agentPresence.last[convID]; ok && now.Sub(last) < agentPresenceThrottle {
		return false
	}
	agentPresence.last[convID] = now
	return true
}

func clearPresenceThrottle(convID string) {
	agentPresence.Lock()
	delete(agentPresence.last, convID)
	agentPresence.Unlock()
}

// AgentTyping is what the composer calls: "composing" while the agent types,
// "" (paused) on stop or on send. It never returns an error the composer
// needs to react to — presence is best-effort by nature.
func AgentTyping(ctx context.Context, a *app.App, id, presence string) error {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return err
	}
	presence = presenceOf(presence)
	sctx := tenant.WithWorkspace(ctx, s.WorkspaceID)
	var channel, customerPhone string
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT channel, coalesce(customer_phone, '') FROM conversations
		WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id).Scan(&channel, &customerPhone); err != nil {
		if notFound(err) {
			return huma.Error404NotFound("conversation not found")
		}
		return huma.Error500InternalServerError("could not read the conversation")
	}

	switch channel {
	case "whatsapp":
		if customerPhone == "" {
			break
		}
		if presence == "" {
			clearPresenceThrottle(id)
			_ = whatsapp.SendPresenceFor(sctx, a, s.WorkspaceID, customerPhone, "paused")
		} else if shouldSendComposing(id) {
			_ = whatsapp.SendPresenceFor(sctx, a, s.WorkspaceID, customerPhone, presence)
		}
	case "chat":
		visitors.publishTyping(id, presence)
	}
	return nil
}

func registerAgentTyping(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-typing", Method: http.MethodPost, Path: "/api/w/conversations/{id}/typing",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Tell the customer the agent is typing (WhatsApp presence, or the chat widget visitor stream)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Presence string `json:"presence,omitempty" doc:"composing, recording, or empty/paused to clear it."`
		}
	}) (*struct{}, error) {
		if err := AgentTyping(ctx, a, in.ID, strings.TrimSpace(in.Body.Presence)); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

// registerVisitorTyping mounts the public side: a widget visitor typing
// reaches the inbox the same way their message would (MH-660).
func registerVisitorTyping(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "chat-typing", Method: http.MethodPost, Path: "/api/chat/{key}/typing",
		DefaultStatus: http.StatusNoContent,
		Summary:       "The visitor is typing: shows in the inbox thread and list row (public: key, Origin, visitor token)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Key    string `path:"key"`
		Origin string `header:"Origin"`
		Body   struct {
			VisitorToken string `json:"visitor_token" minLength:"1" maxLength:"200"`
			Presence     string `json:"presence,omitempty" doc:"composing, or empty to clear it."`
		}
	}) (*struct{}, error) {
		w, err := resolveWidget(ctx, a, in.Key, in.Origin)
		if err != nil {
			return nil, err
		}
		sctx := tenant.WithWorkspace(ctx, w.WorkspaceID)
		var convID string
		err = a.SQLDB.QueryRowContext(sctx,
			`SELECT id FROM conversations WHERE workspace_id = $1 AND visitor_token = $2 AND widget_id = $3`,
			w.WorkspaceID, strings.TrimSpace(in.Body.VisitorToken), w.ID).Scan(&convID)
		if notFound(err) {
			return nil, huma.Error404NotFound("that conversation is not available")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		changed(ctx, a, w.WorkspaceID, realtime.Event{
			Kind: "typing", ConversationID: convID, Direction: "in",
			Presence: presenceOf(in.Body.Presence),
		})
		return nil, nil
	})
}
