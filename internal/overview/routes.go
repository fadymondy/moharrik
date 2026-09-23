package overview

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

// Register mounts GET /api/w/overview. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-overview", Method: http.MethodGet, Path: "/api/w/overview",
		Summary: "The workspace's front page: today's numbers, the pipeline, the latest leads, conversations and " +
			"notifications, marketing, trends and what is still to set up — each section only for a role that can read its area",
		Tags: []string{"Overview"},
	}, func(ctx context.Context, _ *wsHeader) (*body[Overview], error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if !authz.Can(s.Role, authz.WorkspaceRead) {
			return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
		}
		env := Env{
			WhatsAppAvailable: whatsapp.LoadConfig().Configured(),
			GoogleAvailable:   google.LoadConfig().Ready(),
		}
		o, err := Build(ctx, a.SQLDB, s.WorkspaceID, s.Role, env, time.Now())
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if err != nil {
			a.Log.Error("overview failed", "workspace", s.WorkspaceID, "err", err)
			return nil, huma.Error500InternalServerError("could not read the overview")
		}
		return &body[Overview]{*o}, nil
	})
}
