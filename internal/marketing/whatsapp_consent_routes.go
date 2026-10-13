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

// OptInEvidenceBody is how a contact agreed and the proof, for an opt-in
// recorded with evidence (the record_whatsapp_opt_in MCP tool takes the same).
type OptInEvidenceBody struct {
	Source      string `json:"source" enum:"public_request,inbound_message,verbal,form" doc:"public_request: they asked publicly to be contacted on WhatsApp (a post with their number); inbound_message: they messaged first; verbal: they said so; form: a form they filled in."`
	EvidenceURL string `json:"evidence_url,omitempty" maxLength:"2000" doc:"A link to the post or message where they agreed. Required for public_request."`
	Note        string `json:"note,omitempty" maxLength:"1000" doc:"What they wrote or said, and where. evidence_url, note or both."`
}

// recordOptIn records an opt-in, with its evidence when there is a body.
func recordOptIn(ctx context.Context, db *sql.DB, wsID, userID, contactID string, ev *OptInEvidenceBody) error {
	if ev == nil {
		return RecordWhatsAppOptIn(ctx, db, wsID, contactID)
	}
	return RecordWhatsAppOptInWithEvidence(ctx, db, wsID, contactID, WhatsAppOptInEvidence{
		Source: ev.Source, URL: ev.EvidenceURL, Note: ev.Note, UserID: userID,
	})
}

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
		// MH-561: subscribed contacts this send would skip for having no email
		// (a phone/handle-only contact that got subscribed some other way, e.g.
		// imported) — said before the send is pressed, not discovered after.
		Skipped int64 `json:"skipped" doc:"Confirmed subscribers with no email, who the send would skip."`
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
		skipped, err := CountSkippedEmailAudienceFor(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Locale)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not count the audience")
		}
		return out(struct {
			Reachable int64 `json:"reachable" doc:"Confirmed subscribers of the list (in that language, when one is given)."`
			Skipped   int64 `json:"skipped" doc:"Confirmed subscribers with no email, who the send would skip."`
		}{n, skipped}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-whatsapp-opt-in", Method: http.MethodPost, Path: "/api/w/contacts/{id}/whatsapp/opt-in",
		Summary: "Record that a contact agreed to WhatsApp messages, optionally with how and the evidence (409 if they said STOP)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string             `path:"id"`
		Body *OptInEvidenceBody `required:"false"`
	}) (*body[struct {
		OK bool `json:"ok"`
	}], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		var evErr ErrOptInEvidence
		switch err := recordOptIn(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.ID, in.Body); {
		case errors.As(err, &evErr):
			return nil, huma.Error422UnprocessableEntity(evErr.Msg)
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
