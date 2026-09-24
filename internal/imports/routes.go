package imports

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Import"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// Register mounts the REST side of "Contacts → Import from a connection"
// (MH-562): list sources, preview one connection, start an import, and poll
// its job. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-import-sources", Method: http.MethodGet, Path: "/api/w/import/sources",
		Summary: "Every import source this app knows, with the workspace's connections for each", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Sources []SourceInfo `json:"sources"`
	}], error) {
		sources, err := ListSources(ctx, a, wsID(ctx))
		if err != nil {
			return nil, err
		}
		return out(struct {
			Sources []SourceInfo `json:"sources"`
		}{sources}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-import-preview", Method: http.MethodGet, Path: "/api/w/import/{source}/preview",
		Summary: "Preview what one connection has to import, filtered before anything is written", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Source       string `path:"source"`
		ConnectionID string `query:"connection_id"`
		Q            string `query:"q"`
		Kind         string `query:"kind" enum:",direct,group"`
		IncludeFlagged bool `query:"include_flagged"`
	}) (*body[struct {
		Items []Item `json:"items"`
	}], error) {
		items, err := Preview(ctx, a, wsID(ctx), in.Source, in.ConnectionID, Filters{Q: in.Q, Kind: in.Kind, IncludeFlagged: in.IncludeFlagged})
		if err != nil {
			return nil, err
		}
		return out(struct {
			Items []Item `json:"items"`
		}{items}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-import-start", Method: http.MethodPost, Path: "/api/w/import/{source}",
		Summary: "Import the selected items from one connection, in the background", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Source string `path:"source"`
		Body   ImportRequest
	}) (*body[struct {
		Job Job `json:"job"`
	}], error) {
		job, err := StartImport(ctx, a, wsID(ctx), in.Source, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Job Job `json:"job"`
		}{*job}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-import-job", Method: http.MethodGet, Path: "/api/w/import/jobs/{id}",
		Summary: "One import run's progress", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Job Job `json:"job"`
	}], error) {
		job, err := JobStatus(ctx, a, wsID(ctx), in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Job Job `json:"job"`
		}{*job}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-summary", Method: http.MethodGet, Path: "/api/w/conversations/{id}/summary",
		Summary: "One conversation's analytics — message counts, participants, first/last message", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Summary Summary `json:"summary"`
	}], error) {
		sum, err := ChatSummary(ctx, a, wsID(ctx), in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Summary Summary `json:"summary"`
		}{*sum}), nil
	})
}

// wsID reads the workspace id the tenant middleware already resolved. Every
// function in this package also re-checks role/membership itself (scope()),
// the same belt-and-suspenders every other package here uses.
func wsID(ctx context.Context) string {
	if s, ok := tenant.From(ctx); ok {
		return s.WorkspaceID
	}
	return ""
}
