package marketing

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

// The list of SMTP services (MH-412). Every route is mail:settings, like the
// single-service one it grew out of. See smtp_services.go for the rules.

type serviceBody struct {
	Name      string  `json:"name" minLength:"1" maxLength:"80"`
	Host      string  `json:"host" minLength:"1" maxLength:"253"`
	Port      int64   `json:"port" minimum:"1" maximum:"65535"`
	Username  string  `json:"username,omitempty" maxLength:"254"`
	Password  *string `json:"password,omitempty" doc:"Write-only. Omit to keep the stored password; an empty string clears it."`
	FromName  string  `json:"from_name,omitempty" maxLength:"120"`
	FromEmail string  `json:"from_email" minLength:"3" maxLength:"254"`
	TLSMode   string  `json:"tls_mode" enum:"none,starttls,tls"`
}

func (b serviceBody) input() SMTPServiceInput {
	return SMTPServiceInput{Name: b.Name, Host: b.Host, Port: b.Port, Username: b.Username, Password: b.Password,
		FromName: b.FromName, FromEmail: b.FromEmail, TLSMode: b.TLSMode}
}

type smtpServiceOne struct {
	Service SMTPService `json:"service"`
}

// legacyOut is the single-service shape of GET /api/w/mail/smtp.
func legacyOut(d *SMTPService) SMTPOut {
	u := d.UpdatedAt
	return SMTPOut{Configured: true, Host: d.Host, Port: d.Port, Username: d.Username, HasPassword: d.HasPassword,
		FromName: d.FromName, FromEmail: d.FromEmail, TLSMode: d.TLSMode, UpdatedAt: &u}
}

// serviceError maps the service functions' errors to statuses. Anything
// unexpected is logged and answered generically: a database error can carry
// the statement, and nothing about a service's row belongs in a reply.
func serviceError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrServiceNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, ErrServiceName), errors.Is(err, ErrDefaultInUse):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrBadSender):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "a host") || strings.HasPrefix(msg, "the port") || strings.HasPrefix(msg, "tls_mode") {
		return huma.Error422UnprocessableEntity(msg)
	}
	slog.ErrorContext(ctx, "smtp service", "err", err)
	return huma.Error500InternalServerError("could not save the SMTP service")
}

// campaignServiceID validates the service a campaign names: "" is the
// campaign default (NULL); anything else must be one of this workspace's.
func campaignServiceID(ctx context.Context, db *sql.DB, wsID, id string) (*string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}
	var ok bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1 AND id = $2)`,
		wsID, id).Scan(&ok); err != nil {
		return nil, huma.Error500InternalServerError("could not read the SMTP service")
	}
	if !ok {
		return nil, huma.Error422UnprocessableEntity("that SMTP service does not exist in this workspace")
	}
	return &id, nil
}

// SetCampaignService points a draft (or failed) campaign at a service; "" is
// the campaign default. Refused once the campaign is sending or sent.
func SetCampaignService(ctx context.Context, db *sql.DB, wsID, campaignID string, serviceID *string) error {
	res, err := db.ExecContext(ctx, `UPDATE campaigns SET smtp_service_id = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND `+claimable, wsID, campaignID, serviceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		if db.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE workspace_id = $1 AND id = $2`, wsID, campaignID).Scan(&status) != nil {
			return ErrCampaignNotFound
		}
		return &LockedError{Status: status}
	}
	return nil
}

func registerSMTPServices(api huma.API, a *app.App) {
	type smtpServiceList struct {
		Services []SMTPService `json:"services"`
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-smtp-services", Method: http.MethodGet, Path: "/api/w/smtp-services",
		Summary: "This workspace's SMTP services: which is the default for confirmations and replies, which for campaigns, and how each last tested. Passwords are never returned.",
		Tags:    tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[smtpServiceList], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		list, err := ListSMTPServices(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list SMTP services")
		}
		return out(smtpServiceList{list}), nil
	})

	// The picker on a campaign needs the names, and a member who may write
	// campaigns may not be allowed into the mail settings. Names and roles
	// only: no host, no sender, no username.
	type smtpServiceOption struct {
		ID                string `json:"id"`
		Name              string `json:"name"`
		IsDefault         bool   `json:"is_default"`
		IsCampaignDefault bool   `json:"is_campaign_default"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "w-smtp-service-options", Method: http.MethodGet, Path: "/api/w/smtp-services/options",
		Summary: "The SMTP services a campaign can be sent through: names and default roles only", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Services []smtpServiceOption `json:"services"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsRead)
		if err != nil {
			return nil, err
		}
		list, err := ListSMTPServices(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list SMTP services")
		}
		res := out(struct {
			Services []smtpServiceOption `json:"services"`
		}{Services: []smtpServiceOption{}})
		for _, o := range list {
			res.Body.Services = append(res.Body.Services, smtpServiceOption{o.ID, o.Name, o.IsDefault, o.IsCampaignDefault})
		}
		return res, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-smtp-service", Method: http.MethodPost, Path: "/api/w/smtp-services",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add an SMTP service. The first one becomes the default for confirmations and for campaigns.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body serviceBody
	}) (*body[smtpServiceOne], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		o, err := CreateSMTPService(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in.Body.input())
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		return out(smtpServiceOne{*o}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-smtp-service", Method: http.MethodPut, Path: "/api/w/smtp-services/{id}",
		Summary: "Edit an SMTP service (omit the password to keep it)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body serviceBody
	}) (*body[smtpServiceOne], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		o, err := UpdateSMTPService(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Body.input())
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		return out(smtpServiceOne{*o}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-smtp-service", Method: http.MethodDelete, Path: "/api/w/smtp-services/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove an SMTP service. Campaigns that named it go back to the campaign default. The default itself can only go last.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		if err := DeleteSMTPService(ctx, a.SQLDB, s.WorkspaceID, in.ID); err != nil {
			return nil, serviceError(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-smtp-service-default", Method: http.MethodPost, Path: "/api/w/smtp-services/{id}/default",
		Summary: "Make a service the default for confirmations and replies (role=transactional) or for campaigns (role=campaign)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Role string `json:"role" enum:"transactional,campaign"`
		}
	}) (*body[struct {
		Services []SMTPService `json:"services"`
	}], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		if err := SetSMTPDefault(ctx, a.SQLDB, s.WorkspaceID, in.ID, in.Body.Role == "campaign"); err != nil {
			return nil, serviceError(ctx, err)
		}
		list, err := ListSMTPServices(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list SMTP services")
		}
		return out(struct {
			Services []SMTPService `json:"services"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-smtp-service", Method: http.MethodPost, Path: "/api/w/smtp-services/{id}/test",
		Summary: "Send a test email through one SMTP service; the outcome is kept on the service", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			To string `json:"to" minLength:"3" maxLength:"254"`
		}
	}) (*body[smtpServiceOne], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		to, err := mail.ParseAddress(strings.TrimSpace(in.Body.To))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("that is not a valid email address")
		}
		svc, err := LoadSMTPService(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		sendErr := Send(ctx, svc, SMTPTestMessage(to.Address, svc.ServiceName))
		RecordSMTPTest(ctx, a.SQLDB, s.WorkspaceID, in.ID, sendErr)
		if sendErr != nil {
			return nil, huma.NewError(http.StatusFailedDependency, sendErr.Error())
		}
		o, err := GetSMTPService(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		return out(smtpServiceOne{*o}), nil
	})
}
