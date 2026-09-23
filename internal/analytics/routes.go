package analytics

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

// ReportOut is the Analytics page.
type ReportOut struct {
	// The property this answers for; null when the workspace has none enabled.
	Resource *google.Resource `json:"resource"`
	// Null when GA4 could not be asked and nothing is stored yet (Error says why).
	Report *Report `json:"report"`
	Error  string  `json:"error,omitempty"`
}

// Register mounts the Analytics routes. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-analytics-report", Method: http.MethodGet, Path: "/api/w/analytics/report",
		Summary: "GA4 totals, daily trend, top pages and source / medium for one enabled property",
		Description: "Served from a one-hour cache; after that GA4 is asked again. When GA4 fails the last answer is " +
			"served with stale=true and the reason in note; with nothing stored, report is null and error says why.",
		Tags: []string{"Analytics"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		Resource string `query:"resource" doc:"An enabled GA4 property's resource id; default the first enabled one."`
		Days     int    `query:"days" default:"28" doc:"7, 28 or 90."`
		Refresh  bool   `query:"refresh" doc:"Ask GA4 now rather than serve the hour-old cache."`
	}) (*body[ReportOut], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		if !ValidDays(in.Days) {
			return nil, huma.Error422UnprocessableEntity("days must be 7, 28 or 90")
		}
		r, err := google.ResolveEnabled(ctx, a, s.WorkspaceID, google.KindGA4, in.Resource)
		if errors.Is(err, google.ErrResourceNotFound) {
			return nil, huma.Error404NotFound("that GA4 property is not enabled in this workspace")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the GA4 properties")
		}
		out := ReportOut{Resource: r}
		if r == nil {
			return &body[ReportOut]{out}, nil
		}
		if r.Reauth {
			out.Error = google.Describe(google.ErrReauth)
		}
		rep, err := Get(ctx, a, s.WorkspaceID, r, in.Days, in.Refresh)
		var nd *ErrNoData
		switch {
		case errors.As(err, &nd):
			out.Error = nd.Error()
		case err != nil:
			return nil, huma.Error500InternalServerError("could not read the GA4 report")
		default:
			out.Report = rep
		}
		// The resource as it is now (last_error may have just changed).
		if fresh, ferr := google.GetResource(ctx, a, s.WorkspaceID, r.ID); ferr == nil {
			fresh.ConnectionEmail = ""
			out.Resource = fresh
		}
		return &body[ReportOut]{out}, nil
	})
}
