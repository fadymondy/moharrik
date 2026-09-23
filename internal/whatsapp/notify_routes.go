package whatsapp

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
)

// The member's own WhatsApp notification settings (MH-411). Every route acts
// on the caller's row only: nobody reads, verifies or changes another
// member's number.

// NotifyKindOut is one kind of event, and whether the caller's role may
// receive it at all.
type NotifyKindOut struct {
	Kind    string `json:"kind" enum:"automation_failed,lead,notification"`
	Allowed bool   `json:"allowed" doc:"false when the caller's role cannot read this kind of event, so it cannot be chosen."`
}

// NotifyMessageOut is one row of the member's send log.
type NotifyMessageOut struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind" doc:"batch | test"`
	Events int       `json:"events"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error"`
}

// NotifySettings is what the settings page reads.
type NotifySettings struct {
	Available       bool               `json:"available" doc:"false when this deployment has no WhatsApp server."`
	WorkspaceLinked bool               `json:"workspace_linked" doc:"The workspace's own number is connected; messages are sent from it."`
	Phone           string             `json:"phone"`
	Verified        bool               `json:"verified"`
	VerifiedAt      *time.Time         `json:"verified_at"`
	CodePending     bool               `json:"code_pending" doc:"A code was sent and has not expired."`
	Enabled         bool               `json:"enabled"`
	Kinds           []string           `json:"kinds"`
	AllKinds        []NotifyKindOut    `json:"all_kinds"`
	QuietStart      int                `json:"quiet_start" doc:"Minutes after local midnight."`
	QuietEnd        int                `json:"quiet_end"`
	Timezone        string             `json:"timezone" doc:"Empty = the workspace's."`
	EffectiveZone   string             `json:"effective_timezone"`
	DailyCap        int                `json:"daily_cap"`
	BatchMinutes    int                `json:"batch_minutes"`
	Locale          string             `json:"locale"`
	Pending         int                `json:"pending" doc:"Events waiting for the next message (quiet hours, the cap, or the batch window)."`
	SentToday       int                `json:"sent_today"`
	Recent          []NotifyMessageOut `json:"recent"`
}

type notifyWS struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

func readNotify(ctx context.Context, a *app.App, wsID, userID, role string) (*NotifySettings, error) {
	o := &NotifySettings{Available: LoadConfig().Configured(), Kinds: []string{}, Recent: []NotifyMessageOut{},
		QuietStart: 1320, QuietEnd: 420, DailyCap: 20, BatchMinutes: 5, Locale: "en", Enabled: true}
	o.WorkspaceLinked = sendable(ctx, a, wsID)
	for _, k := range NotifyKinds {
		o.AllKinds = append(o.AllKinds, NotifyKindOut{Kind: k.Kind, Allowed: authz.Can(role, k.Permission)})
	}
	m, err := loadMember(ctx, a.SQLDB, wsID, userID)
	if err != nil {
		return nil, err
	}
	loc := memberLocation(ctx, a.SQLDB, wsID, "")
	if m != nil {
		o.Phone, o.VerifiedAt, o.Verified = m.Phone, m.VerifiedAt, m.VerifiedAt != nil
		o.CodePending = m.CodeHash != "" && m.CodeExpires != nil && Clock().Before(*m.CodeExpires) && m.CodeAttempts < codeTries
		o.Enabled, o.Kinds = m.Enabled, append([]string{}, m.Kinds...)
		o.QuietStart, o.QuietEnd, o.Timezone = m.QuietStart, m.QuietEnd, m.Timezone
		o.DailyCap, o.BatchMinutes, o.Locale = m.DailyCap, m.BatchMinutes, m.Locale
		loc = memberLocation(ctx, a.SQLDB, wsID, m.Timezone)
		_ = a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM member_whatsapp_events WHERE workspace_id = $1 AND user_id = $2 AND sent_at IS NULL`,
			wsID, userID).Scan(&o.Pending)
		local := Clock().In(loc)
		dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		_ = a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM member_whatsapp_messages
			WHERE workspace_id = $1 AND user_id = $2 AND kind = 'batch' AND ok AND created_at >= $3`, wsID, userID, dayStart).Scan(&o.SentToday)
		rows, err := a.SQLDB.QueryContext(ctx, `SELECT created_at, kind, events, ok, error FROM member_whatsapp_messages
			WHERE workspace_id = $1 AND user_id = $2 ORDER BY created_at DESC LIMIT 10`, wsID, userID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var r NotifyMessageOut
			if rows.Scan(&r.At, &r.Kind, &r.Events, &r.OK, &r.Error) == nil {
				o.Recent = append(o.Recent, r)
			}
		}
		rows.Close()
	}
	o.EffectiveZone = loc.String()
	return o, nil
}

func notifyError(err error) error {
	switch {
	case errors.Is(err, ErrNotifyNoNumber), errors.Is(err, ErrNotifyBadCode):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, ErrNotifyTooSoon):
		return huma.Error429TooManyRequests(err.Error())
	case errors.Is(err, ErrNotifyNotLinked), errors.Is(err, ErrNotifyNotStarted), errors.Is(err, ErrNotifyUnverified):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrNotConfigured):
		return huma.Error503ServiceUnavailable(err.Error())
	}
	var e *Error
	if errors.As(err, &e) {
		return huma.Error502BadGateway(e.Error())
	}
	return huma.Error500InternalServerError("could not update WhatsApp notifications")
}

// RegisterNotify mounts the member's WhatsApp notification routes. `a` may be
// nil (OpenAPI export).
func RegisterNotify(api huma.API, a *app.App) {
	ntags := []string{"WhatsApp notifications"}
	read := func(ctx context.Context) (*body[NotifySettings], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		o, err := readNotify(ctx, a, s.WorkspaceID, s.UserID, s.Role)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read WhatsApp notifications")
		}
		return out(*o), nil
	}

	huma.Register(api, huma.Operation{
		OperationID: "w-notify-whatsapp", Method: http.MethodGet, Path: "/api/w/notify/whatsapp",
		Summary: "Your WhatsApp notifications in this workspace: your number, what reaches you, quiet hours, the cap and the send log", Tags: ntags,
	}, func(ctx context.Context, _ *notifyWS) (*body[NotifySettings], error) { return read(ctx) })

	huma.Register(api, huma.Operation{
		OperationID: "w-notify-whatsapp-verify", Method: http.MethodPost, Path: "/api/w/notify/whatsapp/verify",
		Summary: "Send a verification code to your own WhatsApp number from the workspace's number", Tags: ntags,
	}, func(ctx context.Context, in *struct {
		notifyWS
		Body struct {
			Phone  string `json:"phone" minLength:"8" maxLength:"32" doc:"Your number with its country code."`
			Locale string `json:"locale,omitempty" enum:"en,ar" doc:"The language of the code and of your notifications."`
		}
	}) (*body[NotifySettings], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		if err := StartVerify(ctx, a, s.WorkspaceID, s.UserID, in.Body.Phone, in.Body.Locale); err != nil {
			return nil, notifyError(err)
		}
		return read(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-notify-whatsapp-confirm", Method: http.MethodPost, Path: "/api/w/notify/whatsapp/confirm",
		Summary: "Confirm your number with the code WhatsApp delivered", Tags: ntags,
	}, func(ctx context.Context, in *struct {
		notifyWS
		Body struct {
			Code string `json:"code" minLength:"6" maxLength:"6"`
		}
	}) (*body[NotifySettings], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		if err := ConfirmVerify(ctx, a, s.WorkspaceID, s.UserID, in.Body.Code); err != nil {
			return nil, notifyError(err)
		}
		return read(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-notify-whatsapp-put", Method: http.MethodPut, Path: "/api/w/notify/whatsapp",
		Summary: "Choose what reaches you, quiet hours, the daily cap and the batch window", Tags: ntags,
	}, func(ctx context.Context, in *struct {
		notifyWS
		Body struct {
			Enabled      bool     `json:"enabled"`
			Kinds        []string `json:"kinds" maxItems:"3"`
			QuietStart   int      `json:"quiet_start" minimum:"0" maximum:"1439"`
			QuietEnd     int      `json:"quiet_end" minimum:"0" maximum:"1439"`
			Timezone     string   `json:"timezone,omitempty" maxLength:"64" doc:"IANA zone; empty for the workspace's."`
			DailyCap     int      `json:"daily_cap" minimum:"1" maximum:"200"`
			BatchMinutes int      `json:"batch_minutes" minimum:"0" maximum:"120"`
			Locale       string   `json:"locale,omitempty" enum:"en,ar"`
		}
	}) (*body[NotifySettings], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		b := in.Body
		kinds := []string{}
		for _, k := range b.Kinds {
			perm := kindPermission(k)
			if perm == "" {
				return nil, huma.Error422UnprocessableEntity("unknown kind: " + k)
			}
			if !authz.Can(s.Role, perm) {
				return nil, huma.Error422UnprocessableEntity("your role cannot receive " + k + " events")
			}
			if !slices.Contains(kinds, k) {
				kinds = append(kinds, k)
			}
		}
		tz := strings.TrimSpace(b.Timezone)
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				return nil, huma.Error422UnprocessableEntity("unknown time zone: " + tz)
			}
		}
		locale := b.Locale
		if locale != "ar" {
			locale = "en"
		}
		res, err := a.SQLDB.ExecContext(ctx, `
			UPDATE member_whatsapp SET enabled = $3, kinds = $4::text[], quiet_start = $5, quiet_end = $6, timezone = $7,
			  daily_cap = $8, batch_minutes = $9, locale = $10, updated_at = now()
			WHERE workspace_id = $1 AND user_id = $2 AND verified_at IS NOT NULL`,
			s.WorkspaceID, s.UserID, b.Enabled, pgtext.Literal(kinds), b.QuietStart, b.QuietEnd, tz, b.DailyCap, b.BatchMinutes, locale)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save WhatsApp notifications")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, notifyError(ErrNotifyUnverified)
		}
		return read(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-notify-whatsapp-test", Method: http.MethodPost, Path: "/api/w/notify/whatsapp/test",
		Summary: "Send yourself a test notification now (ignores quiet hours; not counted against the cap)", Tags: ntags,
	}, func(ctx context.Context, _ *notifyWS) (*body[NotifySettings], error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		if err := SendNotifyTest(ctx, a, s.WorkspaceID, s.UserID); err != nil {
			return nil, notifyError(err)
		}
		return read(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-notify-whatsapp-delete", Method: http.MethodDelete, Path: "/api/w/notify/whatsapp",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Stop WhatsApp notifications and forget your number, its pending events and its log", Tags: ntags,
	}, func(ctx context.Context, _ *notifyWS) (*struct{}, error) {
		s, err := scope(ctx, authz.WorkspaceRead)
		if err != nil {
			return nil, err
		}
		if _, err := a.SQLDB.ExecContext(ctx, `DELETE FROM member_whatsapp WHERE workspace_id = $1 AND user_id = $2`, s.WorkspaceID, s.UserID); err != nil {
			return nil, huma.Error500InternalServerError("could not remove your number")
		}
		return nil, nil
	})
}
