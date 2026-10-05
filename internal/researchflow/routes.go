package researchflow

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

// Register mounts GET /api/w/research-overview. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-research-overview", Method: http.MethodGet, Path: "/api/w/research-overview",
		Summary: "The research funnel (found, evidence, prospects scored, high-fit) for the workspace and for each recent run, with each run's stages as stored (MH-1066)",
		Description: "Counts of rows that exist, never estimates. `prospects` and `high_fit` are only ever non-zero for discovery runs: " +
			"only discovery runs score prospects. `high_fit` is a score of at least `thresholds.high_fit_score`.",
		Tags: []string{"Research"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		Runs int `query:"runs" minimum:"0" maximum:"25" doc:"Recent runs to list (default 8, at most 25)."`
	}) (*body[ResearchOverview], error) {
		res, err := Read(ctx, a.SQLDB, in.Runs)
		if err != nil {
			return nil, err
		}
		return &body[ResearchOverview]{*res}, nil
	})
}
