package marketing

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

// registerCampaignEdit mounts edit, preview and test send (MH-407). The rules
// they follow are in campaign_edit.go.
func registerCampaignEdit(api huma.API, a *app.App) {
	type campaignBody struct {
		ListID  string `json:"list_id" minLength:"1"`
		Channel string `json:"channel,omitempty" enum:"email,whatsapp" doc:"Defaults to email."`
		Subject string `json:"subject" minLength:"1" maxLength:"200" doc:"The email subject. For a WhatsApp campaign it is the internal name; it is not sent."`
		Body    string `json:"body" minLength:"1" maxLength:"100000"`
		Locale  string `json:"locale,omitempty" enum:"en,ar"`
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-update-campaign", Method: http.MethodPut, Path: "/api/w/campaigns/{id}",
		Summary:     "Edit a campaign that has not gone out (draft, or failed and retryable). 409 once sending has started.",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body campaignBody
	}) (*body[struct {
		Campaign CampaignOut `json:"campaign"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}
		b := in.Body
		err = UpdateCampaign(ctx, a.SQLDB, s.WorkspaceID, in.ID, CampaignInput{
			ListID: b.ListID, Channel: b.Channel, Subject: b.Subject, Body: b.Body, Locale: b.Locale,
		})
		var locked *LockedError
		switch {
		case errors.As(err, &locked):
			return nil, huma.Error409Conflict(locked.Error())
		case errors.Is(err, ErrCampaignNotFound), errors.Is(err, ErrListNotFound):
			return nil, huma.Error404NotFound(err.Error())
		case err != nil:
			slog.ErrorContext(ctx, "update campaign", "err", err)
			return nil, huma.Error500InternalServerError("could not save the campaign")
		}
		c, err := getCampaign(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the campaign")
		}
		return out(struct {
			Campaign CampaignOut `json:"campaign"`
		}{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-preview-campaign", Method: http.MethodPost, Path: "/api/w/campaigns/preview",
		Summary: "Render a campaign (saved or not) exactly as recipients get it: the email HTML and text, or the WhatsApp message with its stop line",
		Tags:    tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Channel string `json:"channel,omitempty" enum:"email,whatsapp"`
			Subject string `json:"subject,omitempty" maxLength:"200"`
			Body    string `json:"body" maxLength:"100000"`
			Locale  string `json:"locale,omitempty" enum:"en,ar"`
		}
	}) (*body[Preview], error) {
		if _, err := scope(ctx, authz.CampaignsRead); err != nil {
			return nil, err
		}
		b := in.Body
		return out(RenderPreview(b.Channel, b.Locale, b.Subject, b.Body)), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-campaign", Method: http.MethodPost, Path: "/api/w/campaigns/{id}/test",
		Summary: "Send one test copy of a saved campaign to yourself (email: your address or one you type, subject prefixed [TEST]; WhatsApp: the workspace's own linked number). Not a send: status and counters are untouched. At most 10 an hour per campaign.",
		Tags:    tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			To string `json:"to,omitempty" maxLength:"254" doc:"Email campaigns only. Omit to send to your own address."`
		}
	}) (*body[TestResult], error) {
		s, err := scope(ctx, authz.CampaignsSend)
		if err != nil {
			return nil, err
		}
		res, err := SendTest(ctx, a, s.WorkspaceID, in.ID, s.Email, in.Body.To)
		if err != nil {
			return nil, testError(err)
		}
		return out(*res), nil
	})
}

// testError maps a SendTest failure to what the caller can act on.
func testError(err error) error {
	var locked *LockedError
	var limited *RateLimitError
	var evo *whatsapp.Error
	switch {
	case errors.As(err, &limited):
		secs := int(limited.RetryAfter.Seconds()) + 1
		return huma.ErrorWithHeaders(huma.Error429TooManyRequests(limited.Error()),
			map[string][]string{"Retry-After": {strconv.Itoa(secs)}})
	case errors.As(err, &locked):
		return huma.Error409Conflict(locked.Error())
	case errors.Is(err, ErrCampaignNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrBadAddress), errors.Is(err, ErrWhatsAppTestTarget):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, ErrNoService):
		return huma.Error409Conflict("configure SMTP for this workspace before sending a test")
	case errors.Is(err, ErrNoWhatsApp):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, whatsapp.ErrNotConfigured):
		return huma.Error503ServiceUnavailable(err.Error())
	case errors.As(err, &evo):
		return huma.Error502BadGateway(evo.Error())
	}
	// The mail server's own refusal is the part that says what to change.
	return huma.Error502BadGateway(err.Error())
}
