// Package vcard's REST routes: preview/commit an uploaded .vcf, and export a
// contact/selection/the whole workspace back out as one. Kept separate from
// internal/contacts (which cannot import internal/marketing — see that
// package's doc comment) since this package needs both contacts.Upsert and
// the marketing phone normaliser vcard's own ToE164 builds on.
package vcard

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var restTags = []string{"Contacts"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// importLimiter caps how often one workspace can preview/commit a vCard
// file — a generous ceiling meant to stop an accidental script loop, not a
// legitimate bulk import: MCP tools share it with the REST routes.
var importLimiter inbox.Limiter = inbox.NewMemoryLimiter()

// CheckRateLimit reports whether wsID may run another preview/commit right
// now. Exported so internal/mcp's tools enforce the exact same ceiling as
// the REST routes rather than a second, driftable copy of the number.
func CheckRateLimit(wsID string) bool {
	ok, _ := importLimiter.Take(time.Now(), inbox.Rule{Key: "vcard-import:" + wsID, Limit: 20, Window: time.Minute})
	return ok
}

func wsID(ctx context.Context) string {
	if s, ok := tenant.From(ctx); ok {
		return s.WorkspaceID
	}
	return ""
}

func userID(ctx context.Context) string {
	if s, ok := tenant.From(ctx); ok {
		return s.UserID
	}
	return ""
}

// Register mounts the vCard import/export REST routes. `a` may be nil during
// OpenAPI export.
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-vcard-preview", Method: http.MethodPost, Path: "/api/w/contacts/vcard/preview",
		Summary: "Preview a .vcf file: which cards are new, which merge into an existing contact, which are invalid", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			VCard          string `json:"vcard" doc:"Raw vCard 2.1/3.0/4.0 text, one or more cards."`
			DefaultCountry string `json:"default_country,omitempty" doc:"ISO 3166-1 alpha-2 country to resolve local phone numbers against, if the workspace has none set."`
		}
	}) (*body[VcardPreview], error) {
		ws := wsID(ctx)
		if !CheckRateLimit(ws) {
			return nil, huma.Error429TooManyRequests("too many vCard imports for this workspace in the last minute — try again shortly")
		}
		if len(in.Body.VCard) > MaxFileSize {
			return nil, huma.Error422UnprocessableEntity(ErrTooLarge.Error())
		}
		country, err := resolveCountry(ctx, a, ws, in.Body.DefaultCountry)
		if err != nil {
			return nil, err
		}
		pv, err := PreviewImport(ctx, a.SQLDB, ws, []byte(in.Body.VCard), country)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*pv), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-vcard-commit", Method: http.MethodPost, Path: "/api/w/contacts/vcard/commit",
		Summary: "Import a previewed .vcf file: create new contacts, merge into existing ones, never overwrite a filled field", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			VCard          string `json:"vcard" doc:"The same raw vCard text passed to preview."`
			DefaultCountry string `json:"default_country,omitempty" doc:"ISO 3166-1 alpha-2 country to resolve local phone numbers against, if the workspace has none set."`
		}
	}) (*body[CommitResult], error) {
		ws := wsID(ctx)
		if !CheckRateLimit(ws) {
			return nil, huma.Error429TooManyRequests("too many vCard imports for this workspace in the last minute — try again shortly")
		}
		if len(in.Body.VCard) > MaxFileSize {
			return nil, huma.Error422UnprocessableEntity(ErrTooLarge.Error())
		}
		country, err := resolveCountry(ctx, a, ws, in.Body.DefaultCountry)
		if err != nil {
			return nil, err
		}
		res, err := CommitImport(ctx, a.SQLDB, ws, userID(ctx), []byte(in.Body.VCard), country)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-vcard-export", Method: http.MethodGet, Path: "/api/w/contacts/vcard/export",
		Summary: "Export one contact, a selection, or every contact in the workspace as vCard 3.0", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		IDs string `query:"ids" doc:"Comma-separated contact ids. Omit to export the whole workspace."`
	}) (*huma.StreamResponse, error) {
		ws := wsID(ctx)
		var ids []string
		if in.IDs != "" {
			ids = splitCSV(in.IDs)
		}
		text, err := ExportVCard(ctx, a.SQLDB, ws, ids)
		if err != nil {
			return nil, err
		}
		return &huma.StreamResponse{
			Body: func(hctx huma.Context) {
				hctx.SetHeader("Content-Type", "text/vcard; charset=utf-8")
				hctx.SetHeader("Content-Disposition", `attachment; filename="contacts.vcf"`)
				hctx.SetStatus(http.StatusOK)
				_, _ = hctx.BodyWriter().Write([]byte(text))
			},
		}, nil
	})
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// resolveCountry prefers an explicit override, then the workspace's stored
// default_country, then "" (ToE164 then returns a per-number error the
// preview/commit surface instead of guessing).
func resolveCountry(ctx context.Context, a *app.App, wsID, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	var country string
	err := a.SQLDB.QueryRowContext(ctx, `SELECT default_country FROM workspaces WHERE id = $1`, wsID).Scan(&country)
	if err != nil {
		return "", nil //nolint:nilerr // no workspace row resolvable here is not this route's problem to report
	}
	return country, nil
}
