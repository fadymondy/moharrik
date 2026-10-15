package circlexo

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// CircleXOFeatureOut is one hub feature or meter as the hub reports it for a workspace.
type CircleXOFeatureOut struct {
	Key          string `json:"key"`
	Kind         string `json:"kind" doc:"flag | limit | meter"`
	Enabled      bool   `json:"enabled"`
	Limit        *int64 `json:"limit,omitempty" doc:"Absent when unlimited."`
	IncludedQty  int64  `json:"included_qty"`
	UsedInPeriod int64  `json:"used_in_period"`
	Enforced     bool   `json:"enforced" doc:"True when Moharrik itself refuses an action because of this feature (see internal/circlexo/entitlements.go)."`
}

// CircleXOOut is GET /api/w/circlexo.
type CircleXOOut struct {
	Enabled    bool         `json:"enabled" doc:"The CircleXO integration is switched on for this deployment."`
	Linked     bool         `json:"linked" doc:"The workspace is served for a CircleXO org (an active link)."`
	OrgSlug    string       `json:"org_slug,omitempty"`
	Active     bool         `json:"active" doc:"The hub confirms the org may use the app."`
	Plan       string       `json:"plan,omitempty"`
	Status     string       `json:"status,omitempty"`
	ReadOnly   bool         `json:"read_only" doc:"The hub could not be reached and a stale copy is in use. Nothing is refused because of it."`
	Unknown    bool         `json:"unknown" doc:"The hub could not confirm the entitlements at all."`
	PaygOn     bool         `json:"payg_enabled"`
	BillingURL string       `json:"billing_url,omitempty"`
	Features   []CircleXOFeatureOut `json:"features"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

// RegisterAPI mounts GET /api/w/circlexo: what the hub says about this workspace. With the
// integration off, or a workspace with no active link, it answers linked=false and no features.
// `a` may be nil (the OpenAPI export).
func RegisterAPI(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-circlexo", Method: http.MethodGet, Path: "/api/w/circlexo",
		Summary: "The workspace's CircleXO link and the hub's entitlements for it (nothing when the integration is off or the workspace is not linked)",
		Tags:    []string{"CircleXO"},
	}, func(ctx context.Context, _ *wsHeader) (*struct{ Body CircleXOOut }, error) {
		s, ok := tenant.From(ctx)
		if !ok {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if !authz.Can(s.Role, authz.WorkspaceRead) {
			return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
		}
		out := CircleXOOut{Features: []CircleXOFeatureOut{}}
		svc := Current()
		if svc == nil {
			return &struct{ Body CircleXOOut }{out}, nil
		}
		out.Enabled = true
		st, err := svc.StateOf(ctx, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the CircleXO link")
		}
		if !st.Governed() {
			return &struct{ Body CircleXOOut }{out}, nil
		}
		out.Linked, out.OrgSlug = true, st.Link.OrgSlug
		out.BillingURL = svc.billingURL(st.Link.OrgSlug)
		if st.Set == nil {
			out.Unknown = true
			return &struct{ Body CircleXOOut }{out}, nil
		}
		out.Active, out.Plan, out.Status = st.Set.Active, st.Set.Plan, st.Set.Status
		out.ReadOnly, out.PaygOn = st.Set.ReadOnly, st.Set.PaygEnabled
		for _, f := range st.Set.Features {
			out.Features = append(out.Features, CircleXOFeatureOut{
				Key: f.Key, Kind: f.Kind, Enabled: f.Enabled, Limit: f.Limit,
				IncludedQty: f.IncludedQty, UsedInPeriod: f.UsedInPeriod, Enforced: false, // free and open source: nothing is enforced
			})
		}
		return &struct{ Body CircleXOOut }{out}, nil
	})
}
