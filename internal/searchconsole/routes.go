package searchconsole

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type body[T any] struct{ Body T }

// PerformanceOut is the Search Console page.
type PerformanceOut struct {
	// The site this answers for; null when the workspace has none enabled.
	Resource    *google.Resource `json:"resource"`
	Performance *Performance     `json:"performance"`
}

// Register mounts the Search Console routes. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-search-console-performance", Method: http.MethodGet, Path: "/api/w/search-console/performance",
		Summary: "Search Console clicks, impressions, CTR and position for one enabled site, with top queries and pages",
		Description: "Answered from the daily import (never from Google on the request). The window ends on the newest day " +
			"Search Console has data for. With a query or page filter, totals are sums of query-level rows (basis=queries), " +
			"which leave out anonymised queries; otherwise they are the site's own totals (basis=site).",
		Tags: []string{"Search Console"},
	}, func(ctx context.Context, in *struct {
		WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
		Resource    string `query:"resource" doc:"An enabled site's resource id; default the first enabled one."`
		Days        int    `query:"days" default:"28" doc:"7, 28 or 90."`
		Compare     bool   `query:"compare" doc:"Also answer the same length of time just before."`
		Query       string `query:"query" maxLength:"200"`
		Page        string `query:"page" maxLength:"500"`
		Country     string `query:"country" maxLength:"3" doc:"ISO 3166-1 alpha-3, as Search Console writes it (egy, usa)."`
		Device      string `query:"device" enum:",DESKTOP,MOBILE,TABLET"`
	}) (*body[PerformanceOut], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		if !ValidDays(in.Days) {
			return nil, huma.Error422UnprocessableEntity("days must be 7, 28 or 90")
		}
		r, err := google.ResolveEnabled(ctx, a, s.WorkspaceID, google.KindGSC, in.Resource)
		if errors.Is(err, google.ErrResourceNotFound) {
			return nil, huma.Error404NotFound("that Search Console site is not enabled in this workspace")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the Search Console sites")
		}
		out := PerformanceOut{Resource: r}
		if r == nil {
			return &body[PerformanceOut]{out}, nil
		}
		r.ConnectionEmail = ""
		p, err := Report(tenant.WithWorkspace(ctx, s.WorkspaceID), a, s.WorkspaceID, r.ID, in.Days, in.Compare,
			Filters{Query: in.Query, Page: in.Page, Country: in.Country, Device: in.Device})
		if err != nil {
			a.Log.Error("search console report failed", "err", err)
			return nil, huma.Error500InternalServerError("could not read the Search Console report")
		}
		out.Performance = p
		return &body[PerformanceOut]{out}, nil
	})
}
