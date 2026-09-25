// REST routes for MH-586's contact context: upload/preview/commit a WhatsApp
// export, remove an import, browse the context summary, add a free-text
// note, ask a grounded question, and generate/list versioned reports.
//
// Follows the same shape as internal/vcard/routes.go: base64 bytes in a JSON
// body (no multipart anywhere else in this codebase — see internal/crm/
// avatar.go's doc comment), a workspace header resolved through
// internal/tenant, and a small in-memory rate limiter shared between REST
// and the MCP tools so an agent can't out-pace a human member.
package contactcontext

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// required trims a required string field and refuses it when empty.
func required(name, v string) (string, error) {
	if v = strings.TrimSpace(v); v == "" {
		return "", errors.New(name + " is required")
	}
	return v, nil
}

var restTags = []string{"Contacts"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// importLimiter caps how often one workspace can preview/commit a WhatsApp
// export — generous enough for real bulk work, tight enough to stop a
// scripted loop. MCP tools share it via CheckRateLimit.
var importLimiter inbox.Limiter = inbox.NewMemoryLimiter()

// CheckRateLimit reports whether wsID may run another preview/commit right
// now.
func CheckRateLimit(wsID string) bool {
	ok, _ := importLimiter.Take(time.Now(), inbox.Rule{Key: "contact-context-import:" + wsID, Limit: 20, Window: time.Minute})
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

// Register mounts the contact-context REST routes. `a` may be nil during
// OpenAPI export. aiClient is resolved once at startup (nil is a completely
// supported state — see ask.go/report.go's doc comments: MH-586 requires
// every one of these to work with zero AI configured).
func Register(api huma.API, a *app.App, aiClient *ai.Client) {
	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-preview", Method: http.MethodPost, Path: "/api/w/contacts/{contact_id}/context/whatsapp/preview",
		Summary: "Preview a WhatsApp export (.txt or .zip) for a contact: message/media counts, senders, a sample — writes nothing", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
		Body      struct {
			Filename string `json:"filename" doc:"Original filename, used to tell a .txt export from a .zip export."`
			Data     string `json:"data" doc:"Base64-encoded file bytes."`
		}
	}) (*body[WhatsAppExportPreview], error) {
		ws := wsID(ctx)
		if !CheckRateLimit(ws) {
			return nil, huma.Error429TooManyRequests("too many WhatsApp import previews for this workspace in the last minute — try again shortly")
		}
		data, err := base64.StdEncoding.DecodeString(in.Body.Data)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("data must be base64-encoded: " + err.Error())
		}
		brief, err := loadContactBrief(ctx, a, ws, in.ContactID)
		if err != nil {
			return nil, huma.Error404NotFound("contact not found")
		}
		pv, err := BuildPreview(ctx, a, ws, in.ContactID, in.Body.Filename, data, brief.Name)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*pv), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-commit", Method: http.MethodPost, Path: "/api/w/contacts/{contact_id}/context/whatsapp/commit",
		Summary: "Import a previewed WhatsApp export into a contact's conversation. Re-committing the same file adds nothing (deduped). Never triggers automations or notifications.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
		Body      struct {
			Filename      string `json:"filename"`
			Data          string `json:"data" doc:"Base64-encoded file bytes — the same bytes passed to preview."`
			ContactSender string `json:"contact_sender" doc:"Which sender in the export is this contact (from preview's senders list)."`
		}
	}) (*body[WhatsAppImportResult], error) {
		ws := wsID(ctx)
		if !CheckRateLimit(ws) {
			return nil, huma.Error429TooManyRequests("too many WhatsApp imports for this workspace in the last minute — try again shortly")
		}
		data, err := base64.StdEncoding.DecodeString(in.Body.Data)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("data must be base64-encoded: " + err.Error())
		}
		res, err := Commit(ctx, a, ws, in.ContactID, userID(ctx), in.Body.Filename, data, in.Body.ContactSender)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-remove-import", Method: http.MethodDelete, Path: "/api/w/contacts/{contact_id}/context/imports/{import_id}",
		Summary: "Remove one WhatsApp import: deletes exactly its messages, attachments and any notes tagged with it. Nothing else on the conversation is touched.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
		ImportID  string `path:"import_id"`
	}) (*struct{}, error) {
		ws := wsID(ctx)
		if err := RemoveImport(ctx, a, ws, in.ContactID, in.ImportID); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-summary", Method: http.MethodGet, Path: "/api/w/contacts/{contact_id}/context",
		Summary: "A contact's context at a glance: recent notes, recent messages across every channel, imports on file, open deals.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
	}) (*body[ContextSummary], error) {
		ws := wsID(ctx)
		s, err := GetContextSummary(ctx, a, ws, in.ContactID)
		if err != nil {
			return nil, huma.Error404NotFound("contact not found")
		}
		return out(*s), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-add-note", Method: http.MethodPost, Path: "/api/w/contacts/{contact_id}/context/notes",
		Summary: "Add free-text context to a contact: a note, or a small text/markdown file's content.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
		Body      struct {
			Kind     string `json:"kind,omitempty" doc:"\"note\" (default) or \"file\"."`
			Title    string `json:"title,omitempty"`
			Content  string `json:"content" doc:"The note text, or the uploaded file's decoded text content."`
			Filename string `json:"filename,omitempty" doc:"Set when kind=\"file\"."`
		}
	}) (*body[struct {
		ID string `json:"id"`
	}], error) {
		ws := wsID(ctx)
		if _, err := required("content", in.Body.Content); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		id, err := AddNote(ctx, a, ws, in.ContactID, in.Body.Kind, in.Body.Title, in.Body.Content, in.Body.Filename, userID(ctx))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(struct {
			ID string `json:"id"`
		}{ID: id}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-ask", Method: http.MethodPost, Path: "/api/w/contacts/{contact_id}/context/ask",
		Summary: "Ask a free-text question about a contact, grounded ONLY in their retrieved context, with citations. Works even with no AI provider configured.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
		Body      struct {
			Question string `json:"question"`
		}
	}) (*body[Answer], error) {
		ws := wsID(ctx)
		if _, err := required("question", in.Body.Question); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		ans, err := AskAboutContact(ctx, a, aiClient, ws, in.ContactID, in.Body.Question)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*ans), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-generate-report", Method: http.MethodPost, Path: "/api/w/contacts/{contact_id}/context/reports",
		Summary: "Generate a new, versioned relationship report for a contact. Every claim is cited against a timeline entry; works even with no AI provider configured.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
	}) (*body[ContactReport], error) {
		ws := wsID(ctx)
		rep, err := GenerateReport(ctx, a, aiClient, ws, in.ContactID, userID(ctx))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(*rep), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-context-list-reports", Method: http.MethodGet, Path: "/api/w/contacts/{contact_id}/context/reports",
		Summary: "List a contact's previously generated reports, newest version first.", Tags: restTags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ContactID string `path:"contact_id"`
	}) (*body[[]ContactReport], error) {
		ws := wsID(ctx)
		if _, err := loadContactBrief(ctx, a, ws, in.ContactID); err != nil {
			return nil, huma.Error404NotFound("contact not found")
		}
		list, err := ListReports(ctx, a, ws, in.ContactID)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return out(list), nil
	})
}
