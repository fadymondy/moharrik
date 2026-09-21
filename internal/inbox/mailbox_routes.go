package inbox

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MailboxOut is what the settings page reads back. The password is never in it
// — only whether one is stored.
type MailboxOut struct {
	Configured   bool       `json:"configured"`
	Host         string     `json:"host"`
	Port         int        `json:"port"`
	Username     string     `json:"username"`
	HasPassword  bool       `json:"has_password"`
	TLSMode      string     `json:"tls_mode"`
	Folder       string     `json:"folder"`
	Enabled      bool       `json:"enabled"`
	LastPolledAt *time.Time `json:"last_polled_at"`
	LastError    string     `json:"last_error"`
	MessageCount int        `json:"message_count"`
}

// RegisterMailbox mounts the email channel's settings and its "check now"
// button. Configuring a mailbox is inbox:manage — the same right as creating a
// chat widget, and for the same reason: it decides what can write into the
// workspace's inbox.
func RegisterMailbox(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-mailbox", Method: http.MethodGet, Path: "/api/w/mailbox",
		Summary: "The mailbox this workspace collects email from", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[MailboxOut], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		out2, err := readMailbox(ctx, db(), s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox settings")
		}
		return out(out2), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-save-mailbox", Method: http.MethodPut, Path: "/api/w/mailbox",
		Summary: "Point this workspace at a mailbox (IMAP)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Host     string `json:"host" minLength:"1" maxLength:"200"`
			Port     int    `json:"port" minimum:"1" maximum:"65535"`
			Username string `json:"username" minLength:"1" maxLength:"200"`
			Password string `json:"password,omitempty" maxLength:"400" doc:"Leave empty to keep the stored one."`
			TLSMode  string `json:"tls_mode" enum:"tls,starttls,none"`
			Folder   string `json:"folder,omitempty" maxLength:"100"`
			Enabled  *bool  `json:"enabled,omitempty"`
		}
	}) (*body[MailboxOut], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		port := in.Body.Port
		if port == 0 {
			port = 993
		}
		folder := strings.TrimSpace(in.Body.Folder)
		if folder == "" {
			folder = "INBOX"
		}
		enabled := true
		if in.Body.Enabled != nil {
			enabled = *in.Body.Enabled
		}
		tlsMode := in.Body.TLSMode
		if tlsMode == "" {
			tlsMode = "tls"
		}

		sealed := ""
		if in.Body.Password != "" {
			if sealed, err = seal(in.Body.Password); err != nil {
				return nil, huma.Error500InternalServerError("could not store the password")
			}
		}

		// An empty password keeps the stored one: the form cannot show it, so
		// saving a changed folder must not wipe it.
		if _, err := db().ExecContext(ctx, `
			INSERT INTO mailboxes (workspace_id, imap_host, imap_port, username, password_sealed, tls_mode, folder, enabled, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (workspace_id) DO UPDATE SET
				imap_host = EXCLUDED.imap_host, imap_port = EXCLUDED.imap_port,
				username = EXCLUDED.username,
				password_sealed = CASE WHEN EXCLUDED.password_sealed = '' THEN mailboxes.password_sealed ELSE EXCLUDED.password_sealed END,
				tls_mode = EXCLUDED.tls_mode, folder = EXCLUDED.folder, enabled = EXCLUDED.enabled,
				last_error = '', last_error_at = NULL, updated_at = now()`,
			s.WorkspaceID, strings.TrimSpace(in.Body.Host), port, strings.TrimSpace(in.Body.Username),
			sealed, tlsMode, folder, enabled, s.UserID); err != nil {
			return nil, huma.Error500InternalServerError("could not save the mailbox settings")
		}

		got, err := readMailbox(ctx, db(), s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox settings")
		}
		return out(got), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-mailbox", Method: http.MethodDelete, Path: "/api/w/mailbox",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Stop collecting email for this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		// The conversations already collected stay: they are the customer
		// history, not part of the connection.
		if _, err := db().ExecContext(ctx, `DELETE FROM mailboxes WHERE workspace_id = $1`, s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not remove the mailbox")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-check-mailbox", Method: http.MethodPost, Path: "/api/w/mailbox/check",
		Summary: "Collect from the mailbox now (the same code path as the collector)", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Filed   int        `json:"filed"`
		Error   string     `json:"error,omitempty"`
		Mailbox MailboxOut `json:"mailbox"`
	}], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		mb, err := LoadMailbox(ctx, a, s.WorkspaceID)
		if errors.Is(err, ErrNoMailbox) {
			return nil, huma.Error404NotFound("this workspace has no mailbox configured")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox settings")
		}

		filed, perr := Poll(ctx, a, *mb)
		recordPoll(ctx, a, s.WorkspaceID, filed, perr)

		got, err := readMailbox(ctx, db(), s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox settings")
		}
		msg := ""
		if perr != nil {
			// Returned in the body rather than as an error status: the mailbox
			// row was still updated, and the page needs to show both.
			msg = perr.Error()
		}
		return out(struct {
			Filed   int        `json:"filed"`
			Error   string     `json:"error,omitempty"`
			Mailbox MailboxOut `json:"mailbox"`
		}{filed, msg, got}), nil
	})
}

// ErrNoMailbox: this workspace has not configured one.
var ErrNoMailbox = errors.New("this workspace has no mailbox configured")

// LoadMailbox reads the settings and unseals the password. It is the only place
// the plaintext exists, and it never leaves this package.
func LoadMailbox(ctx context.Context, a *app.App, workspaceID string) (*Mailbox, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var mb Mailbox
	var sealed string
	var enabled bool
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT imap_host, imap_port, username, password_sealed, tls_mode, folder, enabled
		FROM mailboxes WHERE workspace_id = $1`, workspaceID,
	).Scan(&mb.Host, &mb.Port, &mb.Username, &sealed, &mb.TLSMode, &mb.Folder, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoMailbox
	}
	if err != nil {
		return nil, err
	}
	if mb.Password, err = unseal(sealed); err != nil {
		return nil, err
	}
	mb.WorkspaceID = workspaceID
	return &mb, nil
}

func readMailbox(ctx context.Context, db *sql.DB, workspaceID string) (MailboxOut, error) {
	var o MailboxOut
	var sealed string
	var polled sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT imap_host, imap_port, username, password_sealed, tls_mode, folder, enabled,
		       last_polled_at, last_error, message_count
		FROM mailboxes WHERE workspace_id = $1`, workspaceID,
	).Scan(&o.Host, &o.Port, &o.Username, &sealed, &o.TLSMode, &o.Folder, &o.Enabled,
		&polled, &o.LastError, &o.MessageCount)
	if errors.Is(err, sql.ErrNoRows) {
		return MailboxOut{Port: 993, TLSMode: "tls", Folder: "INBOX"}, nil
	}
	if err != nil {
		return o, err
	}
	o.Configured = true
	o.HasPassword = sealed != ""
	o.LastPolledAt = timeptr(polled)
	return o, nil
}

func timeptr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

// recordPoll writes what happened on the mailbox row, so a mailbox that is
// quietly failing says so on the page instead of just collecting nothing.
func recordPoll(ctx context.Context, a *app.App, workspaceID string, filed int, err error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	if err != nil {
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE mailboxes SET last_polled_at = now(), last_error = $2, last_error_at = now(), updated_at = now()
			WHERE workspace_id = $1`, workspaceID, trimTo(err.Error(), 300))
		return
	}
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE mailboxes SET last_polled_at = now(), last_error = '', last_error_at = NULL,
		    message_count = message_count + $2, updated_at = now()
		WHERE workspace_id = $1`, workspaceID, filed)
}
