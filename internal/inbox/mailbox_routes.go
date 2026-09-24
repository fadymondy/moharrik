package inbox

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// MailboxOut is what the settings page and the MCP tools read back. The
// password is never in it — only whether one is stored (MH-555).
type MailboxOut struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	IsPrimary       bool       `json:"is_primary"`
	Host            string     `json:"host"`
	Port            int        `json:"port"`
	Username        string     `json:"username"`
	HasPassword     bool       `json:"has_password"`
	TLSMode         string     `json:"tls_mode"`
	Folder          string     `json:"folder"`
	SMTPServiceID   string     `json:"smtp_service_id" doc:"Which of the workspace's SMTP services a reply on this mailbox goes out through. Empty = the workspace default."`
	SMTPServiceName string     `json:"smtp_service_name,omitempty"`
	Enabled         bool       `json:"enabled"`
	LastPolledAt    *time.Time `json:"last_polled_at"`
	LastError       string     `json:"last_error"`
	LastTestAt      *time.Time `json:"last_test_at"`
	LastTestOK      *bool      `json:"last_test_ok"`
	LastTestError   string     `json:"last_test_error"`
	MessageCount    int        `json:"message_count"`
	CreatedAt       time.Time  `json:"created_at"`
}

// MailboxIn is a mailbox created or updated. An empty Password on an update
// keeps the one already stored — the form cannot show it, so saving a changed
// folder must not wipe it.
type MailboxIn struct {
	Name          string `json:"name,omitempty" maxLength:"120" doc:"Defaults to \"Mailbox N\"."`
	Host          string `json:"host" minLength:"1" maxLength:"200"`
	Port          int    `json:"port" minimum:"1" maximum:"65535"`
	Username      string `json:"username" minLength:"1" maxLength:"200"`
	Password      string `json:"password,omitempty" maxLength:"400"`
	TLSMode       string `json:"tls_mode" enum:"tls,starttls,none"`
	Folder        string `json:"folder,omitempty" maxLength:"100"`
	SMTPServiceID string `json:"smtp_service_id,omitempty" doc:"One of the workspace's SMTP services (MH-412); empty = the workspace default."`
	Enabled       *bool  `json:"enabled,omitempty"`
	IsPrimary     *bool  `json:"is_primary,omitempty"`
}

// RegisterMailbox mounts the several-mailboxes-per-workspace settings (MH-555)
// and each mailbox's "test connection" and "check now" actions. Configuring a
// mailbox is inbox:manage — the same right as creating a chat widget, and for
// the same reason: it decides what can write into the workspace's inbox.
func RegisterMailbox(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-mailboxes", Method: http.MethodGet, Path: "/api/w/mailboxes",
		Summary: "This workspace's mailboxes (MH-555)", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Mailboxes []MailboxOut `json:"mailboxes"`
	}], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		list, err := listMailboxes(ctx, db(), s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the mailboxes")
		}
		return out(struct {
			Mailboxes []MailboxOut `json:"mailboxes"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-mailbox", Method: http.MethodPost, Path: "/api/w/mailboxes",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a mailbox to collect email from (MH-555)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body MailboxIn
	}) (*body[mailboxResp], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		id, err := createMailbox(ctx, db(), s.WorkspaceID, s.UserID, in.Body)
		if err != nil {
			return nil, err
		}
		got, err := readMailboxByID(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox back")
		}
		return mbOut(got), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-save-mailbox", Method: http.MethodPut, Path: "/api/w/mailboxes/{id}",
		Summary: "Update a mailbox (MH-555)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		idPath
		Body MailboxIn
	}) (*body[mailboxResp], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		if err := updateMailbox(ctx, db(), s.WorkspaceID, in.ID, in.Body); err != nil {
			return nil, err
		}
		got, err := readMailboxByID(ctx, db(), s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("mailbox not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox back")
		}
		return mbOut(got), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-mailbox", Method: http.MethodDelete, Path: "/api/w/mailboxes/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Stop collecting email on this mailbox (MH-555)", Tags: tags,
	}, func(ctx context.Context, in *struct{ idPath }) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		// The conversations already collected stay: they are the customer
		// history, not part of the connection (conversations/messages.mailbox_id
		// goes NULL, ON DELETE SET NULL — post/150_mailboxes.sql).
		res, err := db().ExecContext(ctx, `DELETE FROM mailboxes WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not remove the mailbox")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("mailbox not found")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-mailbox", Method: http.MethodPost, Path: "/api/w/mailboxes/{id}/test",
		Summary: "Check the mailbox's credentials without collecting anything (MH-555)", Tags: tags,
	}, func(ctx context.Context, in *struct{ idPath }) (*body[mailboxResp], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		mb, err := loadMailboxByID(ctx, a, s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("mailbox not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox")
		}
		terr := TestConnection(ctx, *mb)
		recordTest(ctx, a, s.WorkspaceID, in.ID, terr)
		got, err := readMailboxByID(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox back")
		}
		return mbOut(got), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-check-mailbox", Method: http.MethodPost, Path: "/api/w/mailboxes/{id}/check",
		Summary: "Collect from this mailbox now (the same code path as the collector)", Tags: tags,
	}, func(ctx context.Context, in *struct{ idPath }) (*body[struct {
		Filed   int        `json:"filed"`
		Error   string     `json:"error,omitempty"`
		Mailbox MailboxOut `json:"mailbox"`
	}], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		mb, err := loadMailboxByID(ctx, a, s.WorkspaceID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("mailbox not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox")
		}

		filed, perr := Poll(ctx, a, *mb)
		recordPoll(ctx, a, s.WorkspaceID, in.ID, filed, perr)

		got, err := readMailboxByID(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the mailbox back")
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

// mailboxResp wraps one mailbox under a named key, the same convention every
// other single-object response here follows (w-create-company's Company
// CompanyOut `json:"company"`, and so on) — never a bare object at the top
// level.
type mailboxResp struct {
	Mailbox MailboxOut `json:"mailbox"`
}

func mbOut(m MailboxOut) *body[mailboxResp] { return out(mailboxResp{m}) }

// ErrNoMailbox: this workspace has not configured one.
var ErrNoMailbox = errors.New("this workspace has no mailbox configured")

// loadMailboxByID reads one mailbox's settings and unseals the password. It is
// the only place the plaintext exists, and it never leaves this package.
func loadMailboxByID(ctx context.Context, a *app.App, workspaceID, id string) (*Mailbox, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var mb Mailbox
	var sealed string
	var smtpID sql.NullString
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT id, name, imap_host, imap_port, username, password_sealed, tls_mode, folder, smtp_service_id, created_at
		FROM mailboxes WHERE workspace_id = $1 AND id = $2`, workspaceID, id,
	).Scan(&mb.ID, &mb.Name, &mb.Host, &mb.Port, &mb.Username, &sealed, &mb.TLSMode, &mb.Folder, &smtpID, &mb.ConnectedAt)
	if err != nil {
		return nil, err
	}
	if mb.Password, err = unseal(sealed); err != nil {
		return nil, err
	}
	mb.WorkspaceID = workspaceID
	mb.SMTPServiceID = smtpID.String
	return &mb, nil
}

// LoadPrimaryMailbox reads the workspace's primary mailbox, for a caller that
// has no particular mailbox to hand (a manually started email conversation).
// Kept for the one caller that predates MH-555's several mailboxes.
func LoadPrimaryMailbox(ctx context.Context, a *app.App, workspaceID string) (*Mailbox, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var id string
	err := a.SQLDB.QueryRowContext(sctx, `SELECT id FROM mailboxes WHERE workspace_id = $1 AND is_primary LIMIT 1`, workspaceID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoMailbox
	}
	if err != nil {
		return nil, err
	}
	return loadMailboxByID(ctx, a, workspaceID, id)
}

// LoadMailboxForBrowsing reads one mailbox with its password unsealed, for a
// caller OUTSIDE this package that wants to read its mail without filing it
// into the inbox — ListMessages (imap.go) is what such a caller then calls.
// The canonical use is a contact/history importer (MH-562): it needs to know
// what a mailbox has said to whom, and that is a browsing right, not a full
// inbox:manage one — inbox:read is enough, the same as reading a
// conversation.
//
// This — plus ListMessages taking a Mailbox rather than a workspace/mailbox
// id pair — is deliberately the ENTIRE surface a caller outside this package
// gets: it can read what a mailbox holds, on its own schedule, and nothing
// about mailbox credentials or the connection lives outside internal/inbox.
func LoadMailboxForBrowsing(ctx context.Context, a *app.App, workspaceID, mailboxID string) (*Mailbox, error) {
	if _, err := scope(ctx, authz.InboxRead); err != nil {
		return nil, err
	}
	mb, err := loadMailboxByID(ctx, a, workspaceID, mailboxID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoMailbox
	}
	return mb, err
}

func listMailboxes(ctx context.Context, db *sql.DB, workspaceID string) ([]MailboxOut, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT m.id, m.name, m.is_primary, m.imap_host, m.imap_port, m.username, m.password_sealed,
		       m.tls_mode, m.folder, m.smtp_service_id, coalesce(sv.name, ''), m.enabled,
		       m.last_polled_at, m.last_error, m.last_test_at, m.last_test_ok, m.last_test_error,
		       m.message_count, m.created_at
		FROM mailboxes m LEFT JOIN smtp_services sv ON sv.workspace_id = m.workspace_id AND sv.id = m.smtp_service_id
		WHERE m.workspace_id = $1 ORDER BY m.is_primary DESC, lower(m.name), m.created_at`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []MailboxOut{}
	for rows.Next() {
		o, err := scanMailbox(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

func readMailboxByID(ctx context.Context, db *sql.DB, workspaceID, id string) (MailboxOut, error) {
	row := db.QueryRowContext(ctx, `
		SELECT m.id, m.name, m.is_primary, m.imap_host, m.imap_port, m.username, m.password_sealed,
		       m.tls_mode, m.folder, m.smtp_service_id, coalesce(sv.name, ''), m.enabled,
		       m.last_polled_at, m.last_error, m.last_test_at, m.last_test_ok, m.last_test_error,
		       m.message_count, m.created_at
		FROM mailboxes m LEFT JOIN smtp_services sv ON sv.workspace_id = m.workspace_id AND sv.id = m.smtp_service_id
		WHERE m.workspace_id = $1 AND m.id = $2`, workspaceID, id)
	return scanMailbox(row)
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanMailbox(r rowScanner) (MailboxOut, error) {
	var o MailboxOut
	var sealed string
	var smtpID sql.NullString
	var polled, testAt sql.NullTime
	var testOK sql.NullBool
	if err := r.Scan(&o.ID, &o.Name, &o.IsPrimary, &o.Host, &o.Port, &o.Username, &sealed, &o.TLSMode, &o.Folder,
		&smtpID, &o.SMTPServiceName, &o.Enabled, &polled, &o.LastError, &testAt, &testOK, &o.LastTestError,
		&o.MessageCount, &o.CreatedAt); err != nil {
		return o, err
	}
	o.HasPassword = sealed != ""
	o.SMTPServiceID = smtpID.String
	o.LastPolledAt = timeptr(polled)
	o.LastTestAt = timeptr(testAt)
	if testOK.Valid {
		v := testOK.Bool
		o.LastTestOK = &v
	}
	return o, nil
}

// createMailbox validates and inserts one mailbox. The first mailbox a
// workspace creates is always primary, whatever IsPrimary says — a workspace
// with mail configured must always have exactly one primary.
func createMailbox(ctx context.Context, db *sql.DB, workspaceID, userID string, in MailboxIn) (string, error) {
	if err := validateMailboxIn(ctx, db, workspaceID, in); err != nil {
		return "", err
	}
	port, folder, tlsMode := normalizeMailboxIn(in)
	name := strings.TrimSpace(in.Name)
	if name == "" {
		var n int
		_ = db.QueryRowContext(ctx, `SELECT count(*) FROM mailboxes WHERE workspace_id = $1`, workspaceID).Scan(&n)
		name = "Mailbox " + strconv.Itoa(n+1)
	}
	sealed := ""
	if in.Password != "" {
		var err error
		if sealed, err = seal(in.Password); err != nil {
			return "", huma.Error500InternalServerError("could not store the password")
		}
	}
	primary := in.IsPrimary != nil && *in.IsPrimary
	var hasAny bool
	_ = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1)`, workspaceID).Scan(&hasAny)
	if !hasAny {
		primary = true
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", huma.Error500InternalServerError("could not create the mailbox")
	}
	defer tx.Rollback() //nolint:errcheck
	if primary {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET is_primary = false WHERE workspace_id = $1`, workspaceID); err != nil {
			return "", huma.Error500InternalServerError("could not create the mailbox")
		}
	}
	var id string
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO mailboxes (workspace_id, name, is_primary, imap_host, imap_port, username, password_sealed,
		                        tls_mode, folder, smtp_service_id, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id`,
		workspaceID, name, primary, strings.TrimSpace(in.Host), port, strings.TrimSpace(in.Username), sealed,
		tlsMode, folder, nullable(in.SMTPServiceID), enabled, userID,
	).Scan(&id); err != nil {
		if pgCode(err) == "23505" {
			return "", huma.Error409Conflict("this workspace already has a mailbox named " + name)
		}
		if pgCode(err) == "23503" {
			return "", huma.Error422UnprocessableEntity("that SMTP service is not in this workspace")
		}
		return "", huma.Error500InternalServerError("could not create the mailbox")
	}
	if err := tx.Commit(); err != nil {
		return "", huma.Error500InternalServerError("could not create the mailbox")
	}
	return id, nil
}

func updateMailbox(ctx context.Context, db *sql.DB, workspaceID, id string, in MailboxIn) error {
	if err := validateMailboxIn(ctx, db, workspaceID, in); err != nil {
		return err
	}
	port, folder, tlsMode := normalizeMailboxIn(in)
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	sealed := ""
	if in.Password != "" {
		var err error
		if sealed, err = seal(in.Password); err != nil {
			return huma.Error500InternalServerError("could not store the password")
		}
	}
	name := strings.TrimSpace(in.Name)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return huma.Error500InternalServerError("could not update the mailbox")
	}
	defer tx.Rollback() //nolint:errcheck
	if in.IsPrimary != nil && *in.IsPrimary {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET is_primary = false WHERE workspace_id = $1`, workspaceID); err != nil {
			return huma.Error500InternalServerError("could not update the mailbox")
		}
	}
	q := `UPDATE mailboxes SET imap_host = $3, imap_port = $4, username = $5,
			password_sealed = CASE WHEN $6 = '' THEN password_sealed ELSE $6 END,
			tls_mode = $7, folder = $8, smtp_service_id = $9, enabled = $10,
			last_error = '', last_error_at = NULL, updated_at = now()`
	args := []any{workspaceID, id, strings.TrimSpace(in.Host), port, strings.TrimSpace(in.Username), sealed, tlsMode, folder, nullable(in.SMTPServiceID), enabled}
	if name != "" {
		q += `, name = $11`
		args = append(args, name)
	}
	if in.IsPrimary != nil {
		q += `, is_primary = $` + strconv.Itoa(len(args)+1)
		args = append(args, *in.IsPrimary)
	}
	q += ` WHERE workspace_id = $1 AND id = $2`
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		if pgCode(err) == "23505" {
			return huma.Error409Conflict("this workspace already has a mailbox with that name")
		}
		if pgCode(err) == "23503" {
			return huma.Error422UnprocessableEntity("that SMTP service is not in this workspace")
		}
		return huma.Error500InternalServerError("could not update the mailbox")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("mailbox not found")
	}
	// A mailbox unset as primary with nothing else made primary would leave the
	// workspace with none; the FIRST enabled mailbox (oldest) becomes primary
	// instead, so there is always exactly one.
	if in.IsPrimary != nil && !*in.IsPrimary {
		var still bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM mailboxes WHERE workspace_id = $1 AND is_primary)`, workspaceID).Scan(&still)
		if !still {
			_, _ = tx.ExecContext(ctx, `
				UPDATE mailboxes SET is_primary = true WHERE workspace_id = $1 AND id = (
					SELECT id FROM mailboxes WHERE workspace_id = $1 ORDER BY created_at LIMIT 1)`, workspaceID)
		}
	}
	return tx.Commit()
}

func validateMailboxIn(ctx context.Context, db *sql.DB, workspaceID string, in MailboxIn) error {
	if strings.TrimSpace(in.SMTPServiceID) != "" {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1 AND id = $2)`,
			workspaceID, in.SMTPServiceID).Scan(&exists); err != nil {
			return huma.Error500InternalServerError("could not check the SMTP service")
		}
		if !exists {
			return huma.Error422UnprocessableEntity("that SMTP service is not in this workspace")
		}
	}
	return nil
}

func normalizeMailboxIn(in MailboxIn) (port int, folder, tlsMode string) {
	port = in.Port
	if port == 0 {
		port = 993
	}
	folder = strings.TrimSpace(in.Folder)
	if folder == "" {
		folder = "INBOX"
	}
	tlsMode = in.TLSMode
	if tlsMode == "" {
		tlsMode = "tls"
	}
	return
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
func recordPoll(ctx context.Context, a *app.App, workspaceID, mailboxID string, filed int, err error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	if err != nil {
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE mailboxes SET last_polled_at = now(), last_error = $3, last_error_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, workspaceID, mailboxID, trimTo(err.Error(), 300))
		return
	}
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE mailboxes SET last_polled_at = now(), last_error = '', last_error_at = NULL,
		    message_count = message_count + $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, mailboxID, filed)
}

// recordTest writes a "test connection" result (MH-555), kept apart from
// last_error/last_polled_at: testing a mailbox must not look like a poll that
// found zero mail, and a poll's own failure must not be overwritten by an old
// test result.
func recordTest(ctx context.Context, a *app.App, workspaceID, mailboxID string, err error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	ok := err == nil
	msg := ""
	if err != nil {
		msg = trimTo(err.Error(), 300)
	}
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE mailboxes SET last_test_at = now(), last_test_ok = $3, last_test_error = $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, mailboxID, ok, msg)
}

// resolveSMTP picks the SMTP service a reply on mailboxID goes out through
// (MH-412 + MH-555): the mailbox's own choice, or the workspace's default.
func resolveSMTP(ctx context.Context, db *sql.DB, workspaceID, mailboxID string) (*marketing.SMTP, error) {
	if mailboxID != "" {
		var svcID sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT smtp_service_id FROM mailboxes WHERE workspace_id = $1 AND id = $2`,
			workspaceID, mailboxID).Scan(&svcID); err != nil && !notFound(err) {
			return nil, err
		} else if svcID.Valid && svcID.String != "" {
			return marketing.LoadSMTPService(ctx, db, workspaceID, svcID.String)
		}
	}
	return marketing.LoadSMTP(ctx, db, workspaceID)
}

// ---- MCP tool entry points (MH-555) -----------------------------------------
//
// Thin wrappers that do their own scope() check, the same shape as every
// other function in this package a tool calls (service.go's doc explains
// why): the REST handlers above and internal/mcp/user_tools_mailbox.go both
// end up here, so a mailbox managed by an agent follows exactly the same
// rules as one managed from the settings page.

func ListMailboxesTool(ctx context.Context, a *app.App) ([]MailboxOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	list, err := listMailboxes(ctx, a.SQLDB, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the mailboxes")
	}
	return list, nil
}

func CreateMailboxTool(ctx context.Context, a *app.App, in MailboxIn) (MailboxOut, error) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return MailboxOut{}, err
	}
	id, err := createMailbox(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in)
	if err != nil {
		return MailboxOut{}, err
	}
	return readMailboxByID(ctx, a.SQLDB, s.WorkspaceID, id)
}

func UpdateMailboxTool(ctx context.Context, a *app.App, id string, in MailboxIn) (MailboxOut, error) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return MailboxOut{}, err
	}
	if err := updateMailbox(ctx, a.SQLDB, s.WorkspaceID, id, in); err != nil {
		return MailboxOut{}, err
	}
	got, err := readMailboxByID(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return MailboxOut{}, huma.Error404NotFound("mailbox not found")
	}
	return got, err
}

func DeleteMailboxTool(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return err
	}
	res, err := a.SQLDB.ExecContext(ctx, `DELETE FROM mailboxes WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
	if err != nil {
		return huma.Error500InternalServerError("could not remove the mailbox")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("mailbox not found")
	}
	return nil
}

func TestMailboxTool(ctx context.Context, a *app.App, id string) (MailboxOut, error) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return MailboxOut{}, err
	}
	mb, err := loadMailboxByID(ctx, a, s.WorkspaceID, id)
	if notFound(err) {
		return MailboxOut{}, huma.Error404NotFound("mailbox not found")
	} else if err != nil {
		return MailboxOut{}, huma.Error500InternalServerError("could not read the mailbox")
	}
	recordTest(ctx, a, s.WorkspaceID, id, TestConnection(ctx, *mb))
	return readMailboxByID(ctx, a.SQLDB, s.WorkspaceID, id)
}

// CheckMailboxTool polls one mailbox now. A poll failure (a bad password, an
// unreachable host) is recorded on the row and returned as pollErr rather than
// as the function's own error — the mailbox WAS found and read, so the tool
// still has something to report, the same shape as POST .../check over REST.
func CheckMailboxTool(ctx context.Context, a *app.App, id string) (filed int, pollErr error, got MailboxOut) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return 0, err, MailboxOut{}
	}
	mb, err := loadMailboxByID(ctx, a, s.WorkspaceID, id)
	if notFound(err) {
		return 0, huma.Error404NotFound("mailbox not found"), MailboxOut{}
	} else if err != nil {
		return 0, huma.Error500InternalServerError("could not read the mailbox"), MailboxOut{}
	}
	n, perr := Poll(ctx, a, *mb)
	recordPoll(ctx, a, s.WorkspaceID, id, n, perr)
	got, rerr := readMailboxByID(ctx, a.SQLDB, s.WorkspaceID, id)
	if rerr != nil {
		return n, perr, MailboxOut{}
	}
	return n, perr, got
}
