package searchconsole

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
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

// InspectionsOut is the URL-inspection tab.
type InspectionsOut struct {
	Resource    *google.Resource `json:"resource"`
	Inspections []Inspection     `json:"inspections"`
	Quota       InspectionQuota  `json:"quota"`
	// Whether the caller may inspect and submit (search_console:write).
	CanWrite bool `json:"can_write"`
}

// SitemapsOut is the sitemaps tab.
type SitemapsOut struct {
	Resource *google.Resource `json:"resource"`
	Sitemaps []Sitemap        `json:"sitemaps"`
	// Why the list could not be read from Google ('' = it was).
	Error    string `json:"error,omitempty"`
	CanWrite bool   `json:"can_write"`
}

type siteRef struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
	Resource    string `query:"resource" doc:"An enabled site's resource id; default the first enabled one."`
}

func site(ctx context.Context, a *app.App, ws, id string) (*google.Resource, error) {
	r, err := google.ResolveEnabled(ctx, a, ws, google.KindGSC, id)
	if errors.Is(err, google.ErrResourceNotFound) {
		return nil, huma.Error404NotFound("that Search Console site is not enabled in this workspace")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the Search Console sites")
	}
	if r == nil {
		return nil, huma.Error404NotFound("no Search Console site is enabled in this workspace")
	}
	r.ConnectionEmail = ""
	return r, nil
}

func writeScope(ctx context.Context) (*tenant.Scope, error) {
	s, _, err := google.ReadScope(ctx)
	if err != nil {
		return nil, err
	}
	if !authz.Can(s.Role, authz.SearchConsoleWrite) {
		return nil, huma.Error403Forbidden("inspecting URLs and submitting sitemaps needs the manager role in this workspace")
	}
	return s, nil
}

func toolError(err error) error {
	var q *ErrInspectionQuota
	switch {
	case errors.Is(err, ErrNotInProperty):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.As(err, &q):
		return huma.Error429TooManyRequests(q.Error())
	}
	// Google refused or failed: say what it said, in words (never a token).
	return huma.NewError(http.StatusFailedDependency, google.Describe(err))
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

	gscTags := []string{"Search Console"}
	huma.Register(api, huma.Operation{
		OperationID: "w-search-console-inspections", Method: http.MethodGet, Path: "/api/w/search-console/inspections",
		Summary: "The latest URL inspections of an enabled site, and today's inspection budget", Tags: gscTags,
	}, func(ctx context.Context, in *siteRef) (*body[InspectionsOut], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := site(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		out := InspectionsOut{Resource: r, CanWrite: authz.Can(s.Role, authz.SearchConsoleWrite)}
		if out.Inspections, err = ListInspections(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID, 50); err != nil {
			return nil, huma.Error500InternalServerError("could not list the inspections")
		}
		if out.Quota, err = QuotaFor(ctx, a.SysDB, r.ExternalID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the inspection budget")
		}
		return &body[InspectionsOut]{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-search-console-inspect", Method: http.MethodPost, Path: "/api/w/search-console/inspect",
		Summary:     "Inspect one URL of an enabled site with Google's URL Inspection",
		Description: "Spends one of the site's 2,000 daily inspections (shared by every workspace reading the site); refused with 429 when they are used up.",
		Tags:        gscTags,
	}, func(ctx context.Context, in *struct {
		siteRef
		Body struct {
			URL    string `json:"url" maxLength:"2000" doc:"An absolute URL under the site."`
			Locale string `json:"locale,omitempty" enum:",en,ar"`
		}
	}) (*body[Inspection], error) {
		s, err := writeScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := site(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		res, err := Inspect(ctx, a, s.WorkspaceID, *r, in.Body.URL, in.Body.Locale, s.UserID)
		if err != nil {
			return nil, toolError(err)
		}
		return &body[Inspection]{*res}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-search-console-sitemaps", Method: http.MethodGet, Path: "/api/w/search-console/sitemaps",
		Summary: "An enabled site's sitemaps, as Google lists them now", Tags: gscTags,
	}, func(ctx context.Context, in *siteRef) (*body[SitemapsOut], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := site(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		out := SitemapsOut{Resource: r, Sitemaps: []Sitemap{}, CanWrite: authz.Can(s.Role, authz.SearchConsoleWrite)}
		if list, err := Sitemaps(ctx, a, s.WorkspaceID, *r); err != nil {
			out.Error = google.Describe(err)
		} else {
			out.Sitemaps = list
		}
		return &body[SitemapsOut]{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-search-console-sitemap-submit", Method: http.MethodPost, Path: "/api/w/search-console/sitemaps",
		Summary: "Submit a sitemap of an enabled site to Google (or ask Google to read it again)", Tags: gscTags,
	}, func(ctx context.Context, in *struct {
		siteRef
		Body struct {
			URL string `json:"url" maxLength:"2000" doc:"The sitemap's absolute URL, under the site."`
		}
	}) (*body[SitemapsOut], error) {
		s, err := writeScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := site(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		if err := SubmitSitemap(ctx, a, s.WorkspaceID, *r, in.Body.URL); err != nil {
			return nil, toolError(err)
		}
		out := SitemapsOut{Resource: r, Sitemaps: []Sitemap{}, CanWrite: true}
		if list, err := Sitemaps(ctx, a, s.WorkspaceID, *r); err != nil {
			out.Error = google.Describe(err)
		} else {
			out.Sitemaps = list
		}
		return &body[SitemapsOut]{out}, nil
	})
}
