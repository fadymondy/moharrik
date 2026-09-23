package marketing

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
The workspace side of WhatsApp consent (MH-320): what the campaigns and
contacts pages need to show it and change it.

Two actions, deliberately separate:

	RECORD AN OPT-IN   — a member noting that a contact agreed. It will not
	                     touch somebody who said STOP; it answers 409 so the
	                     page can offer the reversal instead of pretending.

	REVERSE AN OPT-OUT — letting someone who said STOP back in. A manager's
	                     call, and the body must say so explicitly, so no
	                     client can reach it by accident with an empty POST.
*/

// RegisterWhatsAppConsent mounts the consent endpoints. `a` may be nil (OpenAPI export).
func RegisterWhatsAppConsent(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-list-whatsapp-audience", Method: http.MethodGet, Path: "/api/w/mail/lists/{id}/whatsapp-audience",
		Summary: "How many contacts a WhatsApp broadcast to this list would reach now", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		Locale string `query:"locale" enum:",en,ar" doc:"Only contacts in this language (MH-413)."`
	}) (*body[struct {
		Reachable int64 `json:"reachable" doc:"On the list, not unsubscribed, opted in to WhatsApp, not opted out, and with a phone."`
	}], error) {
		s, err := scope(ctx, authz.CampaignsRead)
		if err != nil {
			return nil, err
		}
		var exists bool
		if err := a.SQLDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM mail_lists WHERE workspace_id = $1 AND id = $2)`,
			s.WorkspaceID, in.ID).Scan(&exists); err != nil {
			return nil, huma.Error500InternalServerError("could not read the list")
		}
		if !exists {
			return nil, huma.Error404NotFound("list not found")
		}
		n, err := CountWhatsAppAudienceFor(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Locale)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not count the audience")
		}
		return out(struct {
			Reachable int64 `json:"reachable" doc:"On the list, not unsubscribed, opted in to WhatsApp, not opted out, and with a phone."`
		}{n}), nil
	})

	// The email audience, the same way (MH-413): who an email send to this
	// list would reach now, in one language or all.
	huma.Register(api, huma.Operation{
		OperationID: "w-list-email-audience", Method: http.MethodGet, Path: "/api/w/mail/lists/{id}/email-audience",
		Summary: "How many confirmed subscribers an email send to this list would reach now", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID     string `path:"id"`
		Locale string `query:"locale" enum:",en,ar" doc:"Only contacts in this language."`
	}) (*body[struct {
		Reachable int64 `json:"reachable" doc:"Confirmed subscribers of the list (in that language, when one is given)."`
	}], error) {
		s, err := scope(ctx, authz.CampaignsRead)
		if err != nil {
			return nil, err
		}
		var exists bool
		if err := a.SQLDB.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM mail_lists WHERE workspace_id = $1 AND id = $2)`,
			s.WorkspaceID, in.ID).Scan(&exists); err != nil {
			return nil, huma.Error500InternalServerError("could not read the list")
		}
		if !exists {
			return nil, huma.Error404NotFound("list not found")
		}
		n, err := CountEmailAudienceFor(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Locale)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not count the audience")
		}
		return out(struct {
			Reachable int64 `json:"reachable" doc:"Confirmed subscribers of the list (in that language, when one is given)."`
		}{n}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-whatsapp-opt-in", Method: http.MethodPost, Path: "/api/w/contacts/{id}/whatsapp/opt-in",
		Summary: "Record that a contact agreed to WhatsApp messages (409 if they said STOP)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		OK bool `json:"ok"`
	}], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		switch err := RecordWhatsAppOptIn(ctx, a.SQLDB, s.WorkspaceID, in.ID); {
		case errors.Is(err, ErrNoSuchContact):
			return nil, huma.Error404NotFound("contact not found")
		case errors.Is(err, ErrWhatsAppOptedOut):
			return nil, huma.Error409Conflict(err.Error())
		case err != nil:
			return nil, huma.Error500InternalServerError("could not record the opt-in")
		}
		PublishContacts(a, s.WorkspaceID)
		return out(struct {
			OK bool `json:"ok"`
		}{true}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-whatsapp-reverse-opt-out", Method: http.MethodPost, Path: "/api/w/contacts/{id}/whatsapp/reverse-opt-out",
		Summary: "Reverse a contact's WhatsApp STOP — explicit, and a manager's call", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Confirm bool `json:"confirm" doc:"Must be true: the contact asked to stop, and this lets them back in."`
		}
	}) (*body[struct {
		OK bool `json:"ok"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}
		if !in.Body.Confirm {
			return nil, huma.Error422UnprocessableEntity("confirm must be true to reverse an opt-out")
		}
		var optedOut bool
		err = a.SQLDB.QueryRowContext(ctx, `
			SELECT whatsapp_opt_out_at IS NOT NULL FROM contacts WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID).Scan(&optedOut)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("contact not found")
			}
			return nil, huma.Error500InternalServerError("could not read the contact")
		}
		if !optedOut {
			return nil, huma.Error409Conflict("this contact has not opted out")
		}
		if err := ClearWhatsAppOptOut(ctx, a.SQLDB, s.WorkspaceID, in.ID); err != nil {
			return nil, huma.Error500InternalServerError("could not reverse the opt-out")
		}
		PublishContacts(a, s.WorkspaceID)
		return out(struct {
			OK bool `json:"ok"`
		}{true}), nil
	})
}
