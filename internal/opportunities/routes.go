package opportunities

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

// Register mounts GET /api/w/opportunities/signals. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-opportunity-signals", Method: http.MethodGet, Path: "/api/w/opportunities/signals",
		Summary: "Opportunity signals: open deals gone quiet, new leads and high-fit prospects, each with the real reason it is listed (MH-1065)",
		Description: "Derived from rows that exist, never scored: an open deal with no activity, stage move or message for `thresholds.quiet_days`; " +
			"a lead created within `thresholds.new_lead_days` that is not converted or spam; a prospect waiting for a decision scoring at least " +
			"`thresholds.high_fit_score`. A kind the caller's role cannot read is absent from `sources` and null in `counts`.",
		Tags: []string{"Opportunities"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		Kind  string `query:"kind" enum:"at_risk_deal,new_lead,high_fit_prospect," doc:"Only this kind of signal."`
		Limit int    `query:"limit" minimum:"0" maximum:"100" doc:"OpportunitySignals per kind (default 25, at most 100)."`
	}) (*body[OpportunitySignals], error) {
		res, err := Read(ctx, a.SQLDB, in.Kind, in.Limit, time.Now())
		if err != nil {
			return nil, err
		}
		return &body[OpportunitySignals]{*res}, nil
	})
}
