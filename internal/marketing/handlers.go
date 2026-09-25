package marketing

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/contacts"
	"github.com/fadymondy/moharrik/internal/icp"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Subscription statuses.
const (
	StatusPending      = "pending"
	StatusSubscribed   = "subscribed"
	StatusUnsubscribed = "unsubscribed"
	StatusBounced      = "bounced"
)

// Campaign statuses.
const (
	CampaignDraft   = "draft"
	CampaignSending = "sending"
	CampaignSent    = "sent"
	CampaignFailed  = "failed"
)

// Pace between messages: providers rate-limit, and a burst that trips a limit
// gets the whole domain throttled.
const sendInterval = 150 * time.Millisecond

// ---- wire types ----------------------------------------------------------

type ListOut struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	DoubleOptIn bool      `json:"double_opt_in"`
	Subscribed  int64     `json:"subscribed"`
	Pending     int64     `json:"pending"`
	CreatedAt   time.Time `json:"created_at"`
}

type SubscriptionOut struct {
	ID       string `json:"id"`
	ListID   string `json:"list_id"`
	ListName string `json:"list_name"`
	Status   string `json:"status"`
	// When the address confirmed (or was subscribed directly on a list
	// without double opt-in), and when it left (MH-408).
	ConfirmedAt    *time.Time `json:"confirmed_at"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at"`
	// True when a member took the address off this list from the workspace,
	// rather than the person using the link in an email. Who it was is on the
	// contact's timeline.
	ByWorkspace bool `json:"unsubscribed_by_workspace"`
}

type ContactOut struct {
	ID            string            `json:"id"`
	Email         string            `json:"email"`
	Name          string            `json:"name"`
	Locale        string            `json:"locale"`
	Source        string            `json:"source"`
	Phone         string            `json:"phone"`
	// MH-561: a messaging handle/username, the third identity a contact may
	// carry instead of (or with) email/phone.
	Handle        string            `json:"handle"`
	Title         string            `json:"title"`
	CompanyID     *string           `json:"company_id"`
	CompanyName   string            `json:"company_name"`
	CreatedAt     time.Time         `json:"created_at"`
	Subscriptions []SubscriptionOut `json:"subscriptions"`
	// WhatsApp consent (MH-320), its own thing and never implied by a list.
	// Both kept: "never agreed" and "asked us to stop" are different facts.
	WhatsAppOptInAt  *time.Time `json:"whatsapp_opt_in_at"`
	WhatsAppOptOutAt *time.Time `json:"whatsapp_opt_out_at"`
	// MH-395: the lifecycle stage and the ideal-customer score with its reasons.
	Lifecycle    string       `json:"lifecycle" enum:",subscriber,lead,qualified,customer"`
	Score        *int         `json:"score" doc:"0-100 against the workspace's ideal-customer profile; null when the profile weighs nothing."`
	ScoreReasons []icp.Reason `json:"score_reasons"`
}

type CampaignOut struct {
	ID       string `json:"id"`
	ListID   string `json:"list_id"`
	ListName string `json:"list_name"`
	// email | whatsapp (MH-320). Defaulted in the database, so every campaign
	// that existed before broadcasts still reads as the email one it was.
	Channel        string     `json:"channel"`
	Subject        string     `json:"subject"`
	Body           string     `json:"body"`
	Locale         string     `json:"locale"`
	Status         string     `json:"status"`
	RecipientCount int64      `json:"recipient_count"`
	SentCount      int64      `json:"sent_count"`
	FailedCount    int64      `json:"failed_count"`
	// SkippedCount is subscribed contacts an email send left out for having no
	// email (MH-561) — always 0 for a WhatsApp campaign, which is gated on phone.
	SkippedCount int64 `json:"skipped_count"`
	LastError      string     `json:"last_error"`
	SentAt         *time.Time `json:"sent_at"`
	CreatedAt      time.Time  `json:"created_at"`
	// The SMTP service an email campaign goes out through (MH-412). Null =
	// the campaign default; SMTPServiceName is the service that resolves to
	// now, so the list can say which provider a send will use.
	SMTPServiceID   *string `json:"smtp_service_id"`
	SMTPServiceName string  `json:"smtp_service_name"`
	// Only contacts in this language receive it (MH-413); "" = everyone.
	AudienceLocale string `json:"audience_locale"`
}

type SMTPOut struct {
	Configured  bool       `json:"configured"`
	Host        string     `json:"host"`
	Port        int64      `json:"port"`
	Username    string     `json:"username"`
	HasPassword bool       `json:"has_password"`
	FromName    string     `json:"from_name"`
	FromEmail   string     `json:"from_email"`
	TLSMode     string     `json:"tls_mode"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// ---- helpers ------------------------------------------------------------

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func isFK(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23503"
}

// normChannel defaults to email: an unspecified channel must mean the thing
// that was always true, never a broadcast somebody did not ask for.
func normChannel(v string) string {
	if strings.ToLower(strings.TrimSpace(v)) == "whatsapp" {
		return "whatsapp"
	}
	return "email"
}

func normLocale(l string) string {
	if l == "ar" {
		return "ar"
	}
	return "en"
}

// publicBase is the origin links in an email point at (the web app, which
// proxies /api). Links must be absolute — a relative path in an inbox resolves
// against nothing.
func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

// sendConfirmation delivers the double opt-in email. The row is written first:
// a mail server being down must not lose the subscription.
func sendConfirmation(ctx context.Context, db *sql.DB, wsID, email, locale, listName, token string) error {
	svc, err := LoadSMTP(ctx, db, wsID)
	if err != nil {
		return err
	}
	var wsName string
	_ = db.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = $1`, wsID).Scan(&wsName)
	base := publicBase()
	confirmURL := base + "/" + locale + "/mail/confirm?token=" + url.QueryEscape(token)
	subject, text, htmlBody := ConfirmEmail(locale, confirmURL, wsName, listName)
	return Send(ctx, svc, Message{
		To: email, Subject: subject, Text: text, HTML: htmlBody,
		Headers: UnsubscribeHeaders(base + "/api/mail/unsubscribe?token=" + url.QueryEscape(token)),
	})
}

// campaignServiceCols reads a campaign's own service and the name of the
// service it resolves to now (its own, else the campaign default, else the
// default) — the same order LoadCampaignSMTP sends through.
const campaignServiceCols = `c.smtp_service_id, coalesce(
		(SELECT x.name FROM smtp_services x WHERE x.workspace_id = c.workspace_id AND x.id = c.smtp_service_id),
		(SELECT x.name FROM smtp_services x WHERE x.workspace_id = c.workspace_id AND x.is_campaign_default),
		(SELECT x.name FROM smtp_services x WHERE x.workspace_id = c.workspace_id AND x.is_default), '')`

func getCampaign(ctx context.Context, db *sql.DB, wsID, id string) (*CampaignOut, error) {
	var c CampaignOut
	err := db.QueryRowContext(ctx, `
		SELECT c.id, c.list_id, l.name, c.channel, c.subject, c.body, c.locale, c.status, c.recipient_count,
		       c.sent_count, c.failed_count, c.skipped_count, c.last_error, c.sent_at, c.created_at, `+campaignServiceCols+`, c.audience_locale
		FROM campaigns c JOIN mail_lists l ON l.id = c.list_id AND l.workspace_id = c.workspace_id
		WHERE c.workspace_id = $1 AND c.id = $2`, wsID, id,
	).Scan(&c.ID, &c.ListID, &c.ListName, &c.Channel, &c.Subject, &c.Body, &c.Locale, &c.Status, &c.RecipientCount,
		&c.SentCount, &c.FailedCount, &c.SkippedCount, &c.LastError, &c.SentAt, &c.CreatedAt, &c.SMTPServiceID, &c.SMTPServiceName, &c.AudienceLocale)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ---- routes -------------------------------------------------------------

// Register mounts the email-marketing endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {

	// ---- lists ----

	huma.Register(api, huma.Operation{
		OperationID: "w-mail-lists", Method: http.MethodGet, Path: "/api/w/mail/lists",
		Summary: "Mailing lists with their subscriber counts", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Lists []ListOut `json:"lists"`
	}], error) {
		s, err := scope(ctx, authz.ContactsRead)
		if err != nil {
			return nil, err
		}
		rows, err := a.SQLDB.QueryContext(ctx, `
			SELECT l.id, l.name, l.description, l.double_opt_in, l.created_at,
			       count(s.id) FILTER (WHERE s.status = 'subscribed'),
			       count(s.id) FILTER (WHERE s.status = 'pending')
			FROM mail_lists l LEFT JOIN list_subscriptions s ON s.list_id = l.id AND s.workspace_id = l.workspace_id
			WHERE l.workspace_id = $1
			GROUP BY l.id ORDER BY lower(l.name)`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list mailing lists")
		}
		defer rows.Close()
		res := out(struct {
			Lists []ListOut `json:"lists"`
		}{Lists: []ListOut{}})
		for rows.Next() {
			var l ListOut
			if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.DoubleOptIn, &l.CreatedAt, &l.Subscribed, &l.Pending); err != nil {
				return nil, huma.Error500InternalServerError("could not list mailing lists")
			}
			res.Body.Lists = append(res.Body.Lists, l)
		}
		return res, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-mail-list", Method: http.MethodPost, Path: "/api/w/mail/lists",
		DefaultStatus: http.StatusCreated, Summary: "Create a mailing list", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name        string `json:"name" minLength:"1" maxLength:"80"`
			Description string `json:"description,omitempty" maxLength:"500"`
			DoubleOptIn *bool  `json:"double_opt_in,omitempty"`
		}
	}) (*body[struct {
		List ListOut `json:"list"`
	}], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		doi := in.Body.DoubleOptIn == nil || *in.Body.DoubleOptIn
		var l ListOut
		err = a.SQLDB.QueryRowContext(ctx, `
			INSERT INTO mail_lists (workspace_id, name, description, double_opt_in) VALUES ($1, $2, $3, $4)
			RETURNING id, name, description, double_opt_in, created_at`,
			s.WorkspaceID, name, strings.TrimSpace(in.Body.Description), doi,
		).Scan(&l.ID, &l.Name, &l.Description, &l.DoubleOptIn, &l.CreatedAt)
		if isUnique(err) {
			return nil, huma.Error409Conflict("a list with that name already exists")
		}
		if err != nil {
			slog.ErrorContext(ctx, "create list", "err", err)
			return nil, huma.Error500InternalServerError("could not create the list")
		}
		return out(struct {
			List ListOut `json:"list"`
		}{l}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-mail-list", Method: http.MethodDelete, Path: "/api/w/mail/lists/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a mailing list (not while campaigns use it)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}
		res, err := a.SQLDB.ExecContext(ctx, `DELETE FROM mail_lists WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if isFK(err) {
			return nil, huma.Error409Conflict("campaigns use this list; delete them first")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the list")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("list not found")
		}
		return &struct{}{}, nil
	})

	// ---- contacts ----

	huma.Register(api, huma.Operation{
		OperationID: "w-contacts", Method: http.MethodGet, Path: "/api/w/contacts",
		Summary: "Contacts, one page at a time, with their per-list subscription status",
		Description: "Newest first. `total` is every contact matching the filter; follow `next_cursor` for the next page " +
			"(empty on the last one). With `list_id`, `counts` breaks that list down by status under the same search.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ListID    string `query:"list_id" doc:"Only contacts on this list"`
		Status    string `query:"status" doc:"subscribed, pending, unsubscribed or bounced. With list_id: only this status on that list"`
		Q         string `query:"q" maxLength:"200" doc:"Search name, email, phone and company"`
		Lifecycle string `query:"lifecycle" enum:"subscriber,lead,qualified,customer,none," doc:"Only contacts at this lifecycle stage (none: without one)"`
		MinScore  int    `query:"min_score" minimum:"0" maximum:"100" doc:"Only contacts scoring at least this against the ideal-customer profile (0 = all)"`
		Sort      string `query:"sort" enum:"newest,score," doc:"newest (default) or score (highest first, unscored last)"`
		Cursor    string `query:"cursor" doc:"next_cursor from the previous page"`
		Limit     int    `query:"limit" minimum:"0" maximum:"200" doc:"Page size (default 50, at most 200)"`
	}) (*body[ContactPage], error) {
		s, err := scope(ctx, authz.ContactsRead)
		if err != nil {
			return nil, err
		}
		f := ContactFilter{ListID: in.ListID, Status: in.Status, Q: in.Q, Lifecycle: in.Lifecycle, Sort: in.Sort}
		if in.MinScore > 0 {
			f.MinScore = &in.MinScore
		}
		page, err := ListContactsPage(ctx, a.SQLDB, s.WorkspaceID, f, in.Cursor, in.Limit)
		switch {
		case errors.Is(err, ErrBadCursor), errors.Is(err, ErrStatusNeedsList), errors.Is(err, ErrBadStatus), errors.Is(err, ErrBadLifecycle):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		case err != nil:
			slog.ErrorContext(ctx, "list contacts", "err", err)
			return nil, huma.Error500InternalServerError("could not list contacts")
		}
		return out(*page), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-add-contact", Method: http.MethodPost, Path: "/api/w/contacts",
		DefaultStatus: http.StatusCreated, Summary: "Add a contact, optionally to a list (double opt-in when the list asks for it)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body AddContactInput
	}) (*body[AddContactResult], error) {
		res, err := AddContact(ctx, a, in.Body)
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-resend-confirmation", Method: http.MethodPost, Path: "/api/w/subscriptions/{id}/resend",
		Summary: "Send the double opt-in email again", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Sent bool `json:"sent"`
	}], error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		var email, locale, listName, status, token string
		err = a.SQLDB.QueryRowContext(ctx, `
			SELECT c.email, c.locale, l.name, s.status, s.token
			FROM list_subscriptions s
			JOIN contacts c ON c.id = s.contact_id AND c.workspace_id = s.workspace_id
			JOIN mail_lists l ON l.id = s.list_id AND l.workspace_id = s.workspace_id
			WHERE s.workspace_id = $1 AND s.id = $2`, s.WorkspaceID, in.ID,
		).Scan(&email, &locale, &listName, &status, &token)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("subscription not found")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the subscription")
		}
		if status != StatusPending {
			return nil, huma.Error409Conflict("only a pending subscription needs confirming")
		}
		if err := sendConfirmation(ctx, a.SQLDB, s.WorkspaceID, email, locale, listName, token); err != nil {
			return nil, huma.Error502BadGateway(err.Error())
		}
		return out(struct {
			Sent bool `json:"sent"`
		}{true}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-contact", Method: http.MethodDelete, Path: "/api/w/contacts/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a contact and all its subscriptions", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.ContactsWrite)
		if err != nil {
			return nil, err
		}
		res, err := a.SQLDB.ExecContext(ctx, `DELETE FROM contacts WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the contact")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("contact not found")
		}
		PublishContacts(a, s.WorkspaceID)
		return &struct{}{}, nil
	})

	// ---- campaigns ----

	type campaignsOut struct {
		CampaignPage
		SMTPConfigured bool `json:"smtp_configured"`
		// Whether a WhatsApp number is linked (MH-320). Carried here so the
		// campaigns page can say "link one first" instead of offering a send
		// that would 409 — without needing the inbox permission to ask.
		WhatsAppLinked bool `json:"whatsapp_linked"`
		// The most recipients one send reaches (MH-467). A larger audience is
		// refused before sending, never cut short.
		MaxAudience int `json:"max_audience"`
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-campaigns", Method: http.MethodGet, Path: "/api/w/campaigns",
		Summary: "Campaigns and their delivery progress, a page at a time",
		Description: "Newest first. `total` is every campaign; follow `next_cursor` for the next page. " +
			"A limit over 200 is refused, not clamped (MH-467).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Cursor string `query:"cursor" doc:"next_cursor from the previous page"`
		Limit  int    `query:"limit" minimum:"0" maximum:"200" doc:"Page size (default 50, at most 200)"`
	}) (*body[campaignsOut], error) {
		s, err := scope(ctx, authz.CampaignsRead)
		if err != nil {
			return nil, err
		}
		limit, err := paging.Limit(in.Limit, DefaultCampaignPage, MaxCampaignPage)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		page, err := ListCampaignsPage(ctx, a.SQLDB, s.WorkspaceID, in.Cursor, limit)
		if errors.Is(err, paging.ErrBadCursor) {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not list campaigns")
		}
		res := out(campaignsOut{CampaignPage: *page, MaxAudience: MaxSendAudience})
		res.Body.SMTPConfigured = HasSMTP(ctx, a.SQLDB, s.WorkspaceID)
		_ = a.SQLDB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $1 AND state = 'open')`, s.WorkspaceID).Scan(&res.Body.WhatsAppLinked)
		return res, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-campaign", Method: http.MethodPost, Path: "/api/w/campaigns",
		DefaultStatus: http.StatusCreated, Summary: "Draft a campaign to one list (markdown body)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			ListID  string `json:"list_id" minLength:"1"`
			Channel string `json:"channel,omitempty" enum:"email,whatsapp" doc:"Defaults to email. A WhatsApp campaign reaches only contacts who opted in to WhatsApp specifically."`
			Subject string `json:"subject" minLength:"1" maxLength:"200" doc:"The email subject. For a WhatsApp campaign it is the internal name; it is not sent."`
			Body    string `json:"body" minLength:"1" maxLength:"100000"`
			Locale  string `json:"locale,omitempty" enum:"en,ar"`
			// MH-412: which SMTP service an email campaign goes out through.
			SMTPServiceID string `json:"smtp_service_id,omitempty" doc:"Email only. Omit for the workspace's campaign default."`
			// MH-413: only contacts in this language receive it.
			AudienceLocale string `json:"audience_locale,omitempty" enum:",en,ar" doc:"en or ar sends only to contacts in that language; omit for everyone."`
		}
	}) (*body[struct {
		Campaign CampaignOut `json:"campaign"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}
		svcID, err := campaignServiceID(ctx, a.SQLDB, s.WorkspaceID, in.Body.SMTPServiceID)
		if err != nil {
			return nil, err
		}
		var id string
		err = a.SQLDB.QueryRowContext(ctx, `
			INSERT INTO campaigns (workspace_id, list_id, channel, subject, body, locale, created_by, smtp_service_id, audience_locale)
			SELECT $1, l.id, $3, $4, $5, $6, $7, $8, $9 FROM mail_lists l WHERE l.workspace_id = $1 AND l.id = $2
			RETURNING id`, s.WorkspaceID, in.Body.ListID, normChannel(in.Body.Channel),
			strings.TrimSpace(in.Body.Subject), in.Body.Body,
			normLocale(in.Body.Locale), s.UserID, svcID, AudienceLocale(in.Body.AudienceLocale)).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("list not found")
		}
		if err != nil {
			slog.ErrorContext(ctx, "create campaign", "err", err)
			return nil, huma.Error500InternalServerError("could not create the campaign")
		}
		c, err := getCampaign(ctx, a.SQLDB, s.WorkspaceID, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the campaign")
		}
		PublishCampaigns(a, s.WorkspaceID, id)
		return out(struct {
			Campaign CampaignOut `json:"campaign"`
		}{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-campaign", Method: http.MethodDelete, Path: "/api/w/campaigns/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a campaign that is not sending", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}
		res, err := a.SQLDB.ExecContext(ctx,
			`DELETE FROM campaigns WHERE workspace_id = $1 AND id = $2 AND status <> 'sending'`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the campaign")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("campaign not found (or still sending)")
		}
		PublishCampaigns(a, s.WorkspaceID, in.ID)
		return &struct{}{}, nil
	})

	registerCampaignEdit(api, a)
	registerSubscriberAdmin(api, a)
	registerSMTPServices(api, a)

	huma.Register(api, huma.Operation{
		OperationID: "w-send-campaign", Method: http.MethodPost, Path: "/api/w/campaigns/{id}/send",
		DefaultStatus: http.StatusAccepted, Summary: "Send a campaign to every confirmed subscriber of its list (background)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Campaign CampaignOut `json:"campaign"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsSend)
		if err != nil {
			return nil, err
		}
		// Which channel this campaign goes out on decides everything below, so
		// it is read before anything is resolved (MH-320).
		var channel string
		if err := a.SQLDB.QueryRowContext(ctx,
			`SELECT channel FROM campaigns WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID).Scan(&channel); errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("campaign not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the campaign")
		}

		if channel == "whatsapp" {
			if err := StartWhatsAppSend(ctx, a, s.WorkspaceID, in.ID); err != nil {
				switch {
				case errors.Is(err, ErrNoWhatsApp):
					return nil, huma.Error409Conflict(err.Error())
				case errors.Is(err, ErrNotSendable):
					return nil, huma.Error409Conflict("this campaign has already been sent")
				}
				var tooLarge *AudienceTooLargeError
				if errors.As(err, &tooLarge) {
					return nil, huma.Error409Conflict(tooLarge.Error())
				}
				return nil, huma.Error500InternalServerError("could not start the broadcast")
			}
			c, err := getCampaign(ctx, a.SQLDB, s.WorkspaceID, in.ID)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read the campaign")
			}
			return out(struct {
				Campaign CampaignOut `json:"campaign"`
			}{*c}), nil
		}

		// Resolve the service before accepting, so a misconfiguration is reported
		// to the caller rather than discovered halfway through a list. The
		// campaign's own service, else the campaign default (MH-412).
		svc, err := LoadCampaignSMTP(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if errors.Is(err, ErrNoService) {
			return nil, huma.Error409Conflict("configure SMTP for this workspace before sending")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError(err.Error())
		}
		if err := StartSend(ctx, a, s.WorkspaceID, in.ID, svc); err != nil {
			return nil, err
		}
		c, err := getCampaign(ctx, a.SQLDB, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the campaign")
		}
		return out(struct {
			Campaign CampaignOut `json:"campaign"`
		}{*c}), nil
	})

	// ---- SMTP settings ----
	//
	// The single-service endpoints predate MH-412 and keep working: they read
	// and write the workspace's DEFAULT service. The list of services is
	// registerSMTPServices (smtp_service_routes.go).

	huma.Register(api, huma.Operation{
		OperationID: "w-smtp", Method: http.MethodGet, Path: "/api/w/mail/smtp",
		Summary: "This workspace's default SMTP service (the password is never returned)", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[SMTPOut], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		o := SMTPOut{Port: 587, TLSMode: TLSStartTLS}
		d, err := DefaultSMTPService(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil && !errors.Is(err, ErrNoService) {
			return nil, huma.Error500InternalServerError("could not read SMTP settings")
		}
		if d != nil {
			o = legacyOut(d)
		}
		return out(o), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-put-smtp", Method: http.MethodPut, Path: "/api/w/mail/smtp",
		Summary: "Save this workspace's default SMTP service (creates it when there is none)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Host      string  `json:"host" minLength:"1" maxLength:"253"`
			Port      int64   `json:"port" minimum:"1" maximum:"65535"`
			Username  string  `json:"username,omitempty" maxLength:"254"`
			Password  *string `json:"password,omitempty" doc:"Omit to keep the stored password; empty string clears it"`
			FromName  string  `json:"from_name,omitempty" maxLength:"120"`
			FromEmail string  `json:"from_email" minLength:"3" maxLength:"254"`
			TLSMode   string  `json:"tls_mode" enum:"none,starttls,tls"`
		}
	}) (*body[SMTPOut], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		b := in.Body
		input := SMTPServiceInput{Host: b.Host, Port: b.Port, Username: b.Username, Password: b.Password,
			FromName: b.FromName, FromEmail: b.FromEmail, TLSMode: b.TLSMode}
		d, err := DefaultSMTPService(ctx, a.SQLDB, s.WorkspaceID)
		var saved *SMTPService
		switch {
		case errors.Is(err, ErrNoService):
			input.Name = "Default"
			saved, err = CreateSMTPService(ctx, a.SQLDB, s.WorkspaceID, s.UserID, input)
		case err != nil:
			return nil, huma.Error500InternalServerError("could not read SMTP settings")
		default:
			input.Name = d.Name
			saved, err = UpdateSMTPService(ctx, a.SQLDB, s.WorkspaceID, d.ID, input)
		}
		if err != nil {
			return nil, serviceError(ctx, err)
		}
		return out(legacyOut(saved)), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-smtp", Method: http.MethodDelete, Path: "/api/w/mail/smtp",
		DefaultStatus: http.StatusNoContent, Summary: "Remove this workspace's default SMTP service (only when it is the last one)", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*struct{}, error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		d, err := DefaultSMTPService(ctx, a.SQLDB, s.WorkspaceID)
		if errors.Is(err, ErrNoService) {
			return &struct{}{}, nil
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not remove SMTP settings")
		}
		if err := DeleteSMTPService(ctx, a.SQLDB, s.WorkspaceID, d.ID); err != nil {
			return nil, serviceError(ctx, err)
		}
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-smtp", Method: http.MethodPost, Path: "/api/w/mail/smtp/test",
		Summary: "Send a test email through the default SMTP service", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			To string `json:"to" minLength:"3" maxLength:"254"`
		}
	}) (*body[struct {
		Sent bool `json:"sent"`
	}], error) {
		s, err := scope(ctx, authz.MailSettings)
		if err != nil {
			return nil, err
		}
		to, err := mail.ParseAddress(strings.TrimSpace(in.Body.To))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("that is not a valid email address")
		}
		svc, err := LoadSMTP(ctx, a.SQLDB, s.WorkspaceID)
		if errors.Is(err, ErrNoService) {
			return nil, huma.Error409Conflict("save the SMTP settings first")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the SMTP service")
		}
		sendErr := Send(ctx, svc, SMTPTestMessage(to.Address, ""))
		RecordSMTPTest(ctx, a.SQLDB, s.WorkspaceID, svc.ServiceID, sendErr)
		if sendErr != nil {
			return nil, huma.Error502BadGateway(sendErr.Error())
		}
		return out(struct {
			Sent bool `json:"sent"`
		}{true}), nil
	})

	// ---- public: double opt-in + unsubscribe (the token is the authorization) ----

	type tokenIn struct {
		Token   string `query:"token" doc:"Token from the email (or send {\"token\": ...} in the body)"`
		RawBody []byte
	}
	type MailLinkResult struct {
		Status    string `json:"status"`
		List      string `json:"list"`
		Workspace string `json:"workspace"`
	}
	tokenOf := func(in *tokenIn) string {
		if t := strings.TrimSpace(in.Token); t != "" {
			return t
		}
		var b struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(in.RawBody, &b)
		return strings.TrimSpace(b.Token)
	}
	// resolve finds which workspace a token belongs to — the one owner-pool
	// read, exactly like tenant resolution, with the token as the predicate.
	// Everything after it runs on the app pool scoped to that workspace.
	resolve := func(ctx context.Context, token string) (context.Context, string, error) {
		if len(token) < 32 {
			return nil, "", huma.Error404NotFound("this link is not valid")
		}
		var wsID string
		err := a.SysDB.QueryRowContext(ctx, `SELECT workspace_id FROM list_subscriptions WHERE token = $1`, token).Scan(&wsID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", huma.Error404NotFound("this link is not valid")
		}
		if err != nil {
			return nil, "", huma.Error500InternalServerError("could not read the link")
		}
		return tenant.WithWorkspace(ctx, wsID), wsID, nil
	}
	transition := func(ctx context.Context, token, to string) (*body[MailLinkResult], error) {
		sctx, wsID, err := resolve(ctx, token)
		if err != nil {
			return nil, err
		}
		o := MailLinkResult{}
		var q string
		if to == StatusSubscribed {
			// Confirming never revives an unsubscribed or bounced address.
			q = `UPDATE list_subscriptions SET status = 'subscribed', confirmed_at = coalesce(confirmed_at, now()), updated_at = now()
			     WHERE workspace_id = $1 AND token = $2 AND status IN ('pending', 'subscribed') RETURNING status, list_id`
		} else {
			// The same columns an admin unsubscribe writes (unsubscribeOne), so
			// the two are indistinguishable to everything downstream except the
			// record of who did it — which stays with whoever did it FIRST.
			q = `UPDATE list_subscriptions SET status = 'unsubscribed', unsubscribed_at = coalesce(unsubscribed_at, now()),
			       unsubscribed_by = CASE WHEN status = 'unsubscribed' THEN unsubscribed_by END, updated_at = now()
			     WHERE workspace_id = $1 AND token = $2 RETURNING status, list_id`
		}
		var listID string
		err = a.SQLDB.QueryRowContext(sctx, q, wsID, token).Scan(&o.Status, &listID)
		if errors.Is(err, sql.ErrNoRows) {
			_ = a.SQLDB.QueryRowContext(sctx, `SELECT status, list_id FROM list_subscriptions WHERE workspace_id = $1 AND token = $2`, wsID, token).Scan(&o.Status, &listID)
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not update the subscription")
		}
		_ = a.SQLDB.QueryRowContext(sctx, `
			SELECT l.name, w.name FROM mail_lists l JOIN workspaces w ON w.id = l.workspace_id
			WHERE l.workspace_id = $1 AND l.id = $2`, wsID, listID).Scan(&o.List, &o.Workspace)
		PublishContacts(a, wsID)
		return out(o), nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "mail-confirm", Method: http.MethodPost, Path: "/api/mail/confirm",
		Summary: "Confirm a subscription (double opt-in link)", Tags: tags,
	}, func(ctx context.Context, in *tokenIn) (*body[MailLinkResult], error) {
		return transition(ctx, tokenOf(in), StatusSubscribed)
	})

	huma.Register(api, huma.Operation{
		OperationID: "mail-unsubscribe", Method: http.MethodPost, Path: "/api/mail/unsubscribe",
		Summary: "Unsubscribe (email link and RFC 8058 one-click)", Tags: tags,
	}, func(ctx context.Context, in *tokenIn) (*body[MailLinkResult], error) {
		return transition(ctx, tokenOf(in), StatusUnsubscribed)
	})
}

// StartSend claims the campaign (draft/failed -> sending, atomically, so two
// clicks — or a click and an agent — cannot double-send) and delivers in the
// background. Exported for internal/mcp's send_campaign: one send worker, not
// a second thinner copy that drifts from this one. Progress lands on
// the row, so "how far has it got" is a read, not a held connection.
func StartSend(ctx context.Context, a *app.App, wsID, campaignID string, svc *SMTP) error {
	// Over the stated limit is refused before the claim (MH-467), so the
	// campaign stays a draft and nobody is half-sent to.
	var tooLarge *AudienceTooLargeError
	if err := checkAudience(ctx, a.SQLDB, wsID, campaignID); errors.As(err, &tooLarge) {
		return huma.Error409Conflict(tooLarge.Error())
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return huma.Error500InternalServerError("could not count the audience")
	}
	var listID, subject, bodyMD, locale, audience string
	err := a.SQLDB.QueryRowContext(ctx, `
		UPDATE campaigns SET status = 'sending', sent_count = 0, failed_count = 0, last_error = '', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND `+claimable+`
		RETURNING list_id, subject, body, locale, audience_locale`, wsID, campaignID,
	).Scan(&listID, &subject, &bodyMD, &locale, &audience)
	if errors.Is(err, sql.ErrNoRows) {
		var st string
		if a.SQLDB.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE workspace_id = $1 AND id = $2`, wsID, campaignID).Scan(&st) != nil {
			return huma.Error404NotFound("campaign not found")
		}
		return huma.Error409Conflict("this campaign is already " + st)
	}
	if err != nil {
		return huma.Error500InternalServerError("could not start the campaign")
	}

	type rcpt struct{ subID, email, token string }
	// The same rule the count before sending used (emailAudienceFrom).
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT s.id, c.email, s.token `+emailAudienceFrom+`
		ORDER BY s.created_at, s.id LIMIT $4`, wsID, listID, audience, MaxSendAudience+1)
	if err != nil {
		_, _ = a.SQLDB.ExecContext(ctx, `UPDATE campaigns SET status = 'failed', last_error = 'could not read recipients' WHERE workspace_id = $1 AND id = $2`, wsID, campaignID)
		return huma.Error500InternalServerError("could not read recipients")
	}
	var list []rcpt
	for rows.Next() {
		var r rcpt
		if rows.Scan(&r.subID, &r.email, &r.token) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	// The list grew past the limit between the check and the claim: fail the
	// campaign rather than send to the first MaxSendAudience of it.
	if len(list) > MaxSendAudience {
		tooLarge := &AudienceTooLargeError{Audience: int64(len(list)), Max: MaxSendAudience}
		_, _ = a.SQLDB.ExecContext(ctx, `UPDATE campaigns SET status = 'failed', last_error = $3 WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, tooLarge.Error())
		return huma.Error409Conflict(tooLarge.Error())
	}
	// MH-561: subscribed contacts this send reaches nobody, because they have
	// no email — reported next to recipient_count, not silently dropped.
	skipped, err := CountSkippedEmailAudienceFor(ctx, a.SQLDB, wsID, listID, audience)
	if err != nil {
		skipped = 0
	}
	_, _ = a.SQLDB.ExecContext(ctx, `UPDATE campaigns SET recipient_count = $3, skipped_count = $4 WHERE workspace_id = $1 AND id = $2`,
		wsID, campaignID, int64(len(list)), skipped)
	PublishCampaigns(a, wsID, campaignID)

	go func() {
		// A system context scoped to the one workspace: the RLS pool still applies.
		bg := tenant.WithWorkspace(context.Background(), wsID)
		base := publicBase()
		var sent, failed int64
		lastErr := ""
		for i, r := range list {
			// campaignEmail is also what the preview and the test send render
			// (campaign_edit.go), so what was checked is what is delivered.
			msg := campaignEmail(base, locale, subject, bodyMD, r.token)
			msg.To = r.email
			err := Send(bg, svc, msg)
			if err != nil {
				failed++
				lastErr = err.Error()
				a.Log.Warn("campaign send failed", "workspace", wsID, "campaign", campaignID, "err", err)
				// A permanent rejection means the mailbox is gone; stop paying for it.
				if IsHardBounce(err) {
					_, _ = a.SQLDB.ExecContext(bg, `UPDATE list_subscriptions SET status = 'bounced', updated_at = now() WHERE workspace_id = $1 AND id = $2`, wsID, r.subID)
					PublishContacts(a, wsID)
				}
			} else {
				sent++
			}
			if i%25 == 0 || i == len(list)-1 {
				_, _ = a.SQLDB.ExecContext(bg, `UPDATE campaigns SET sent_count = $3, failed_count = $4, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
					wsID, campaignID, sent, failed)
				PublishCampaigns(a, wsID, campaignID)
			}
			time.Sleep(sendInterval)
		}
		status := CampaignSent
		// Every message failing is a configuration problem, not a delivery result.
		if sent == 0 && failed > 0 {
			status = CampaignFailed
		}
		_, _ = a.SQLDB.ExecContext(bg, `
			UPDATE campaigns SET status = $3, sent_count = $4, failed_count = $5, last_error = $6, sent_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, status, sent, failed, lastErr)
		PublishCampaigns(a, wsID, campaignID)
	}()
	return nil
}

// AddContactInput is a new contact, optionally subscribed to one list. A
// contact needs at least one of email, phone or handle (MH-561); subscribing
// to a list additionally needs an email, because a list is an email audience.
type AddContactInput struct {
	Email  string `json:"email,omitempty" maxLength:"254"`
	Phone  string `json:"phone,omitempty" maxLength:"40" doc:"With country code, e.g. +20 101 234 5678."`
	Handle string `json:"handle,omitempty" maxLength:"120" doc:"A messaging handle/username on a channel with neither a phone nor an email."`
	Name   string `json:"name,omitempty" maxLength:"120"`
	Locale string `json:"locale,omitempty" enum:"en,ar"`
	ListID string `json:"list_id,omitempty" doc:"Omit to add the contact to the CRM without subscribing it anywhere. Requires an email — a list is an email audience."`
}

// UpsertContactIdentity finds or creates a contact by whichever identity is
// given (MH-583: ANY of email, phone or handle that already belongs to a
// contact counts as a match, not just the first given in priority order —
// this is thinner than it looks because internal/contacts.Upsert now does the
// matching, merging and identity bookkeeping). Blank fields never overwrite
// an existing value. Used by internal/imports (MH-562) to turn an imported
// chat/sender into a contact the same way every other path in does.
// email/phone/handle are taken as already normalised — callers outside this
// package should run them through mail.ParseAddress / NormalizePhone first,
// the way AddContact itself does.
func UpsertContactIdentity(ctx context.Context, db *sql.DB, wsID, email, phone, handle, name, locale, source string) (id string, created bool, err error) {
	if email == "" && phone == "" && handle == "" {
		return "", false, huma.Error422UnprocessableEntity("a contact needs an email, a phone number or a handle")
	}
	res, err := contacts.Upsert(ctx, db, wsID, contacts.Candidate{
		Email: email, Phone: phone, Handle: handle, Name: name, Locale: locale, Source: source,
	})
	if err != nil {
		return "", false, huma.Error500InternalServerError("could not add the contact")
	}
	return res.ContactID, res.Created, nil
}

type AddContactResult struct {
	Contact      ContactOut `json:"contact"`
	Status       string     `json:"status"`
	Confirmation string     `json:"confirmation" doc:"sent | not_needed | not_sent"`
	Error        string     `json:"error,omitempty"`
}

// AddContact adds a contact — or finds the one with that email — and, given a
// list, subscribes it, with the double opt-in email when the list asks for
// one. The REST route and internal/mcp's create_contact both call this.
func AddContact(ctx context.Context, a *app.App, in AddContactInput) (*AddContactResult, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	email := ""
	if raw := strings.TrimSpace(in.Email); raw != "" {
		addr, err := mail.ParseAddress(raw)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("that is not a valid email address")
		}
		email = strings.ToLower(addr.Address)
	}
	phone, err := NormalizePhone(in.Phone)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	handle := strings.ToLower(strings.TrimSpace(in.Handle))
	if email == "" && phone == "" && handle == "" {
		return nil, huma.Error422UnprocessableEntity("a contact needs an email, a phone number or a handle")
	}
	if in.ListID != "" && email == "" {
		return nil, huma.Error422UnprocessableEntity("subscribing to a mailing list needs an email")
	}
	locale := normLocale(in.Locale)

	// A CRM contact with no list: no subscription, no consent email.
	if in.ListID == "" {
		cres, err := contacts.Upsert(ctx, a.SQLDB, s.WorkspaceID, contacts.Candidate{
			Email: email, Phone: phone, Handle: handle, Name: strings.TrimSpace(in.Name), Locale: locale, Source: "crm",
		})
		if err != nil {
			return nil, huma.Error500InternalServerError("could not add the contact")
		}
		contactID := cres.ContactID
		res := AddContactResult{Status: "crm", Confirmation: "not_needed"}
		if c, err := getContactOut(ctx, a.SQLDB, s.WorkspaceID, contactID); err == nil {
			res.Contact = *c
		}
		return &res, nil
	}

	var listName string
	var doi bool
	err = a.SQLDB.QueryRowContext(ctx,
		`SELECT name, double_opt_in FROM mail_lists WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ListID,
	).Scan(&listName, &doi)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("list not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the list")
	}

	// The contact resolves through the shared internal/contacts.Upsert (MH-583)
	// before the list-subscription transaction opens: it runs its own
	// transaction (with the workspace's advisory lock), so it cannot be nested
	// inside this one.
	cres, err := contacts.Upsert(ctx, a.SQLDB, s.WorkspaceID, contacts.Candidate{
		Email: email, Phone: phone, Handle: handle, Name: strings.TrimSpace(in.Name), Locale: locale, Source: "manual",
	})
	if err != nil {
		slog.ErrorContext(ctx, "upsert contact", "err", err)
		return nil, huma.Error500InternalServerError("could not add the contact")
	}
	contactID := cres.ContactID

	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not add the contact")
	}
	defer tx.Rollback() //nolint:errcheck

	var subID, status, token string
	err = tx.QueryRowContext(ctx, `
		SELECT id, status, token FROM list_subscriptions WHERE workspace_id = $1 AND list_id = $2 AND contact_id = $3`,
		s.WorkspaceID, in.ListID, contactID).Scan(&subID, &status, &token)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		status = StatusPending
		if !doi {
			status = StatusSubscribed
		}
		token = newToken()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token, confirmed_at)
			VALUES ($1, $2, $3, $4, $5, CASE WHEN $4 = 'subscribed' THEN now() END)`,
			s.WorkspaceID, in.ListID, contactID, status, token); err != nil {
			slog.ErrorContext(ctx, "subscribe", "err", err)
			return nil, huma.Error500InternalServerError("could not add the contact")
		}
	case err != nil:
		return nil, huma.Error500InternalServerError("could not add the contact")
	case status == StatusSubscribed:
		// Already in; nothing to do.
	default:
		// Pending, unsubscribed or bounced: a re-add must not skip consent.
		status = StatusPending
		if !doi {
			status = StatusSubscribed
		}
		token = newToken()
		if _, err := tx.ExecContext(ctx, `
			UPDATE list_subscriptions SET status = $3, token = $4, unsubscribed_at = NULL, unsubscribed_by = NULL,
			  confirmed_at = CASE WHEN $3 = 'subscribed' THEN now() ELSE confirmed_at END, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, subID, status, token); err != nil {
			return nil, huma.Error500InternalServerError("could not add the contact")
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, huma.Error500InternalServerError("could not add the contact")
	}

	res := AddContactResult{Status: status, Confirmation: "not_needed"}
	if status == StatusPending {
		if err := sendConfirmation(ctx, a.SQLDB, s.WorkspaceID, email, locale, listName, token); err != nil {
			res.Confirmation, res.Error = "not_sent", err.Error()
			a.Log.Warn("confirmation email not sent", "workspace", s.WorkspaceID, "err", err)
		} else {
			res.Confirmation = "sent"
		}
	}
	if c, err := getContactOut(ctx, a.SQLDB, s.WorkspaceID, contactID); err == nil {
		res.Contact = *c
	}
	PublishContacts(a, s.WorkspaceID)
	return &res, nil
}
