package home

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/circlexo"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

// Register mounts GET /api/w/home. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-home", Method: http.MethodGet, Path: "/api/w/home",
		Summary: "The Home page: today's signals, opportunities, actions and attention items, what Moharrik is working on, " +
			"the latest activity, open deals as journeys and the connected business — each section only for a role that can read its sources",
		Tags: []string{"Home"},
	}, func(ctx context.Context, _ *wsHeader) (*body[Home], error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if !authz.Can(s.Role, authz.WorkspaceRead) {
			return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
		}
		env := Env{
			Channels: func(ctx context.Context) ([]HomeChannel, error) {
				l, err := channels.ListChannels(ctx, a, s.WorkspaceID, channels.ChannelFilter{Limit: 200})
				if err != nil {
					return nil, err
				}
				out := make([]HomeChannel, 0, len(l.Channels))
				for _, c := range l.Channels {
					out = append(out, HomeChannel{ID: c.ID, Type: c.Type, Network: c.Network, Name: c.Name, Status: c.Status,
						HasError: c.LastError != "", LastMessageAt: c.LastMessageAt, Conversations30d: c.Conversations30d})
				}
				return out, nil
			},
			CircleXO: func(ctx context.Context) (*HomeCircleXO, error) {
				svc := circlexo.Current()
				if svc == nil {
					return nil, nil
				}
				st, err := svc.StateOf(ctx, s.WorkspaceID)
				if err != nil || !st.Governed() {
					return nil, err
				}
				cx := &HomeCircleXO{Linked: true}
				if st.Set != nil {
					cx.Plan, cx.Status, cx.Active = st.Set.Plan, st.Set.Status, st.Set.Active
				}
				return cx, nil
			},
		}
		h, err := Build(ctx, a.SQLDB, s.WorkspaceID, s.Role, env, time.Now())
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if err != nil {
			a.Log.Error("home failed", "workspace", s.WorkspaceID, "err", err)
			return nil, huma.Error500InternalServerError("could not read the home page")
		}
		return &body[Home]{*h}, nil
	})
}
