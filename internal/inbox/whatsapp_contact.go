package inbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Messaging one contact on WhatsApp from their page (MH-410).

The rules are SendWhatsAppToContact's (MH-409), so a member and an agent are
held to exactly the same ones:

  - CONSENT. The contact opted in to WhatsApp (whatsapp_opt_in_at) and has not
    said STOP (whatsapp_opt_out_at). Email consent is not WhatsApp consent.
  - CONFIRMATION. A message that opens a new thread takes two steps: the first
    call returns a one-time code bound to that contact and that exact text, and
    only the same call repeated with it sends. On the page the second step is
    the confirm dialog; over MCP it is the agent repeating the call.
  - PACING AND LIMITS. Every send goes through whatsapp.SendAs (one paced lane
    per workspace, shared with replies and broadcasts, and a whatsapp_sends row
    each). On top of that, fadymondy.com's contact limits: at most DirectPerHour
    messages to one contact in an hour, DirectPerDay in 24 hours, and
    DirectDailyCap direct messages from the workspace per day. A number that
    messages people first in bulk is what WhatsApp's abuse detection bans, and
    it is the workspace's only number.

The 24-hour customer-service window is a rule of Meta's Cloud API. Moharrik's
connection is an Evolution (WhatsApp Web) session, where it does not apply;
the limits above are what stands in for it, as they do on fadymondy.com.
*/

// The direct-message limits. Variables so the tests can lower them.
var (
	DirectPerHour  = 5
	DirectPerDay   = 20
	DirectDailyCap = 50
)

// directLimit checks the limits for one contact's number. It counts every
// direct attempt in the log, failed ones included: each one reached WhatsApp.
func directLimit(ctx context.Context, db *sql.DB, wsID, digits string) (*DirectLimits, error) {
	now := time.Now()
	l := &DirectLimits{HourMax: DirectPerHour, DayMax: DirectPerDay, WorkspaceMax: DirectDailyCap}
	err := db.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE phone = $2 AND created_at > $3),
		       count(*) FILTER (WHERE phone = $2 AND created_at > $4),
		       count(*) FILTER (WHERE created_at > $4)
		FROM whatsapp_sends
		WHERE workspace_id = $1 AND kind = 'direct' AND created_at > $4`,
		wsID, digits, now.Add(-time.Hour), now.Add(-24*time.Hour)).Scan(&l.HourUsed, &l.DayUsed, &l.WorkspaceUsed)
	if err != nil {
		return nil, err
	}
	switch {
	case l.HourUsed >= l.HourMax:
		l.Blocked = fmt.Sprintf("this contact already had %d WhatsApp messages from the workspace in the last hour", l.HourMax)
	case l.DayUsed >= l.DayMax:
		l.Blocked = fmt.Sprintf("this contact already had %d WhatsApp messages from the workspace in the last 24 hours", l.DayMax)
	case l.WorkspaceUsed >= l.WorkspaceMax:
		l.Blocked = fmt.Sprintf("the workspace has sent its %d direct WhatsApp messages for the last 24 hours", l.WorkspaceMax)
	}
	return l, nil
}

// DirectLimits is where one contact stands against the limits.
type DirectLimits struct {
	HourUsed      int    `json:"hour_used"`
	HourMax       int    `json:"hour_max"`
	DayUsed       int    `json:"day_used"`
	DayMax        int    `json:"day_max"`
	WorkspaceUsed int    `json:"workspace_used"`
	WorkspaceMax  int    `json:"workspace_max"`
	Blocked       string `json:"blocked,omitempty" doc:"Why the next message would be refused; empty when it would not be."`
}

// SendLogRow is one attempt in the send log.
type SendLogRow struct {
	At    time.Time `json:"at"`
	Kind  string    `json:"kind" doc:"reply | broadcast | direct"`
	OK    bool      `json:"ok"`
	Error string    `json:"error"`
}

// ContactWhatsAppStatus is what the contact page needs to offer (or explain
// why it cannot offer) "Message on WhatsApp".
type ContactWhatsAppStatus struct {
	// CanSend: a message would be accepted now (the confirm step aside).
	CanSend bool `json:"can_send"`
	// Reason when CanSend is false: unavailable | not_linked | no_phone |
	// not_opted_in | opted_out | limit.
	Reason string `json:"reason,omitempty" doc:"unavailable | not_linked | no_phone | not_opted_in | opted_out | limit"`
	// Detail says the reason in a sentence (English; the page translates Reason).
	Detail string `json:"detail,omitempty"`
	Phone  string `json:"phone" doc:"The contact's number as WhatsApp compares it: digits, country code first."`
	// The consent itself (MH-320): when they agreed, and when they said STOP.
	OptInAt  *time.Time `json:"whatsapp_opt_in_at"`
	OptOutAt *time.Time `json:"whatsapp_opt_out_at"`
	// How they agreed and the proof, when the opt-in was recorded with it.
	OptIn *marketing.WhatsAppOptInRecord `json:"whatsapp_opt_in,omitempty"`
	// ConversationID is the contact's WhatsApp thread, if there is one: a
	// message continues it; without one, the first message opens it.
	ConversationID string        `json:"conversation_id"`
	Limits         *DirectLimits `json:"limits,omitempty"`
	// Recent is this number's last sends from the workspace, newest first:
	// replies, broadcasts and direct messages alike.
	Recent []SendLogRow `json:"recent"`
}

// ContactWhatsApp reads where one contact stands.
func ContactWhatsApp(ctx context.Context, a *app.App, contactID string) (*ContactWhatsAppStatus, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	var phone string
	var optIn, optOut sql.NullTime
	err = a.SQLDB.QueryRowContext(ctx, `
		SELECT coalesce(phone, ''), whatsapp_opt_in_at, whatsapp_opt_out_at
		FROM contacts WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, contactID).Scan(&phone, &optIn, &optOut)
	if notFound(err) {
		return nil, huma.Error404NotFound("contact not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the contact")
	}
	st := &ContactWhatsAppStatus{Phone: whatsapp.Number(phone), Recent: []SendLogRow{}}
	if optIn.Valid {
		st.OptInAt = &optIn.Time
		if st.OptIn, err = marketing.WhatsAppOptInOf(ctx, a, s.WorkspaceID, contactID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the opt-in")
		}
	}
	if optOut.Valid {
		st.OptOutAt = &optOut.Time
	}
	if len(st.Phone) >= 8 {
		if err := a.SQLDB.QueryRowContext(ctx, `
			SELECT id FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
			s.WorkspaceID, st.Phone).Scan(&st.ConversationID); err != nil && !notFound(err) {
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		rows, err := a.SQLDB.QueryContext(ctx, `
			SELECT created_at, kind, ok, error FROM whatsapp_sends
			WHERE workspace_id = $1 AND phone = $2 ORDER BY created_at DESC LIMIT 10`, s.WorkspaceID, st.Phone)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the send log")
		}
		for rows.Next() {
			var r SendLogRow
			if err := rows.Scan(&r.At, &r.Kind, &r.OK, &r.Error); err != nil {
				rows.Close()
				return nil, huma.Error500InternalServerError("could not read the send log")
			}
			st.Recent = append(st.Recent, r)
		}
		rows.Close()
		if st.Limits, err = directLimit(ctx, a.SQLDB, s.WorkspaceID, st.Phone); err != nil {
			return nil, huma.Error500InternalServerError("could not read the send log")
		}
	}

	// The order is the order a member can do something about it: consent is
	// the contact's to give, the number is the workspace's to link.
	switch {
	case optOut.Valid:
		st.Reason, st.Detail = "opted_out", "this contact asked to stop WhatsApp messages"
	case !optIn.Valid:
		st.Reason, st.Detail = "not_opted_in", "this contact has not opted in to WhatsApp messages; email consent is not WhatsApp consent"
	case len(st.Phone) < 8:
		st.Reason, st.Detail = "no_phone", "this contact has no usable phone number"
	case !whatsapp.LoadConfig().Configured():
		st.Reason, st.Detail = "unavailable", whatsapp.ErrNotConfigured.Error()
	default:
		l, err := whatsapp.Load(ctx, a, s.WorkspaceID)
		switch {
		case errors.Is(err, whatsapp.ErrNoLink):
			st.Reason, st.Detail = "not_linked", "this workspace has no WhatsApp number linked"
		case err != nil:
			return nil, huma.Error500InternalServerError("could not read the WhatsApp link")
		case l.State != "open":
			st.Reason, st.Detail = "not_linked", "the workspace's WhatsApp number is not connected"
		case st.Limits != nil && st.Limits.Blocked != "":
			st.Reason, st.Detail = "limit", st.Limits.Blocked
		default:
			st.CanSend = true
		}
	}
	return st, nil
}

// registerContactWhatsApp mounts the contact page's WhatsApp endpoints.
func registerContactWhatsApp(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-contact-whatsapp", Method: http.MethodGet, Path: "/api/w/contacts/{id}/whatsapp",
		Summary: "Whether this contact can be messaged on WhatsApp now, and why not: consent, number, link and limits; plus their thread and the send log",
		Tags:    []string{"WhatsApp"},
	}, func(ctx context.Context, in *struct {
		WorkspaceID string `header:"X-Workspace-ID"`
		ID          string `path:"id"`
	}) (*struct{ Body ContactWhatsAppStatus }, error) {
		st, err := ContactWhatsApp(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body ContactWhatsAppStatus }{*st}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-contact-whatsapp-send", Method: http.MethodPost, Path: "/api/w/contacts/{id}/whatsapp/send",
		Summary: "Message one opted-in contact on WhatsApp. A new thread takes two calls: the first returns confirmation_required and a code; repeat it with the code to send.",
		Tags:    []string{"WhatsApp"},
	}, func(ctx context.Context, in *struct {
		WorkspaceID string `header:"X-Workspace-ID"`
		ID          string `path:"id"`
		Body        struct {
			Text string `json:"text" minLength:"1" maxLength:"4096"`
			Code string `json:"code,omitempty" maxLength:"6" doc:"The code from the first call, for a contact with no thread yet."`
		}
	}) (*struct{ Body DirectSend }, error) {
		res, err := SendWhatsAppToContact(ctx, a, in.ID, in.Body.Text, in.Body.Code)
		if err != nil {
			return nil, err
		}
		return &struct{ Body DirectSend }{*res}, nil
	})
}

// limitError is the refusal the API answers for a LimitError.
func limitError(reason string) error {
	return huma.Error429TooManyRequests(strings.TrimSpace(reason))
}
