package channels

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

// RegisterAggregate mounts the One Channels page's read-only endpoint
// (MH-571): every inbox channel connection of every kind, in one list.
func RegisterAggregate(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-channels", Method: http.MethodGet, Path: "/api/w/channels",
		Summary: "Every inbox channel connection in this workspace — email, chat, WhatsApp, Telegram, Slack, Discord and workflow — in one list",
		Tags:    tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Type   []string `query:"type" explode:"false" doc:"Repeatable or comma-separated: email, chat, whatsapp, telegram, slack, discord, workflow. Empty means every type."`
		Status string   `query:"status" enum:",connected,error,paused"`
		Q      string   `query:"q" maxLength:"200" doc:"Case-insensitive substring match on the connection's name (and, for WhatsApp, its phone)."`
		Cursor string   `query:"cursor" doc:"next_cursor from the page before this one; empty for the first page."`
		Limit  int      `query:"limit" minimum:"0" maximum:"200"`
	}) (*body[ChannelList], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		types := splitTypes(in.Type)
		for _, t := range types {
			if !validChannelTypes[t] {
				return nil, huma.Error422UnprocessableEntity("unknown channel type: " + t)
			}
		}
		res, err := ListChannels(ctx, a, s.WorkspaceID, ChannelFilter{
			Types: types, Status: in.Status, Q: in.Q, Cursor: in.Cursor, Limit: in.Limit,
		})
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*res), nil
	})
}

// splitTypes flattens type=a&type=b and type=a,b into one list, since a
// query param can arrive either way depending on the client.
func splitTypes(vs []string) []string {
	var out []string
	for _, v := range vs {
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(strings.ToLower(p))
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

