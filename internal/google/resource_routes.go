package google

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ResourcesOut is the picker (integrations:manage) or, for a reader, the
// enabled resources the data pages switch between.
type ResourcesOut struct {
	Resources []Resource `json:"resources"`
	// Per connection and product: when discovery last ran and why it failed.
	// Empty for a caller without integrations:manage.
	Discoveries []Discovery `json:"discoveries"`
	// Kinds with several resources and none enabled: the page asks.
	NeedsChoice []Kind `json:"needs_choice"`
	// Kinds the workspace has at least one resource of, enabled or not.
	Available []Kind `json:"available"`
	// Whether the caller may switch resources on and off and sync them.
	CanManage bool `json:"can_manage"`
}

// readScope admits analytics:read; manage reports integrations:manage too.
func readScope(ctx context.Context) (*tenant.Scope, bool, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, false, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, authz.AnalyticsRead) {
		return nil, false, huma.Error403Forbidden("your role cannot read this workspace's analytics")
	}
	return s, authz.Can(s.Role, authz.IntegrationsManage), nil
}

// ReadScope is readScope for the data packages.
func ReadScope(ctx context.Context) (*tenant.Scope, bool, error) { return readScope(ctx) }

// discoverBudget bounds the discovery a page load may wait for.
const discoverBudget = 15 * time.Second

// RegisterResources mounts the picker routes. `a` may be nil (OpenAPI export).
func RegisterResources(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-google-resources", Method: http.MethodGet, Path: "/api/w/google/resources",
		Summary: "The GA4 properties, Search Console sites and YouTube channels this workspace's Google accounts can see",
		Description: "An admin gets every resource, the account it is read through and the discovery log; any other " +
			"analytics:read member gets the enabled ones only. A connection that has never been discovered is " +
			"discovered first (bounded to 15 s), so a freshly connected account lists its resources on first load.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Kind string `query:"kind" enum:",ga4_property,gsc_site,yt_channel"`
	}) (*body[ResourcesOut], error) {
		s, manage, err := readScope(ctx)
		if err != nil {
			return nil, err
		}
		dctx, cancel := context.WithTimeout(ctx, discoverBudget)
		derr := DiscoverWorkspace(dctx, a, s.WorkspaceID, true)
		cancel()
		if derr != nil {
			logWarn(a, "google resources: discovery on load failed", derr)
		}
		return resourcesOut(ctx, a, s.WorkspaceID, Kind(in.Kind), manage)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-resources-discover", Method: http.MethodPost, Path: "/api/w/google/resources/discover",
		Summary: "Ask Google again what every connected account can see", Tags: tags,
	}, func(ctx context.Context, in *wsHeader) (*body[ResourcesOut], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if err := DiscoverWorkspace(ctx, a, s.WorkspaceID, false); err != nil {
			logWarn(a, "google resources: discovery failed", err)
		}
		return resourcesOut(ctx, a, s.WorkspaceID, "", true)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-resource-update", Method: http.MethodPatch, Path: "/api/w/google/resources/{id}",
		Summary: "Switch a property, site or channel on or off for this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}) (*body[Resource], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := SetEnabled(ctx, a, s.WorkspaceID, in.ID, in.Body.Enabled)
		if errors.Is(err, ErrResourceNotFound) {
			return nil, huma.Error404NotFound(err.Error())
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not change that resource")
		}
		return &body[Resource]{*r}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-resource-sync", Method: http.MethodPost, Path: "/api/w/google/resources/{id}/sync",
		Summary:     "Sync an enabled property, site or channel now",
		Description: "Runs in the background; the resource's syncing flag, last_synced_at and last_error say how it went.",
		Tags:        tags, DefaultStatus: http.StatusAccepted,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[Resource], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := GetResource(ctx, a, s.WorkspaceID, in.ID)
		if errors.Is(err, ErrResourceNotFound) {
			return nil, huma.Error404NotFound(err.Error())
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read that resource")
		}
		if !r.Enabled {
			return nil, huma.Error409Conflict("switch it on first")
		}
		if r.BackoffUntil != nil {
			return nil, huma.Error429TooManyRequests("Google asked Moharrik to slow down; the next sync is at " + r.BackoffUntil.UTC().Format(time.RFC3339))
		}
		SyncSoon(a, s.WorkspaceID, r.ID)
		r.Syncing = true
		return &body[Resource]{*r}, nil
	})
}

func resourcesOut(ctx context.Context, a *app.App, workspaceID string, kind Kind, manage bool) (*body[ResourcesOut], error) {
	all, err := ListResources(ctx, a, workspaceID, Filter{Kind: kind})
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the Google resources")
	}
	out := ResourcesOut{Resources: []Resource{}, Discoveries: []Discovery{}, NeedsChoice: NeedsChoice(all), Available: []Kind{}, CanManage: manage}
	seen := map[Kind]bool{}
	for _, r := range all {
		if !seen[r.Kind] {
			seen[r.Kind] = true
			out.Available = append(out.Available, r.Kind)
		}
		if !manage {
			if !r.Enabled {
				continue
			}
			r.ConnectionEmail = ""
		}
		out.Resources = append(out.Resources, r)
	}
	if manage {
		if out.Discoveries, err = ListDiscoveries(ctx, a, workspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the discovery log")
		}
	}
	return &body[ResourcesOut]{out}, nil
}
