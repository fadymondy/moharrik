package marketing

import (
	"context"
	"database/sql"
	"errors"
	"html"
	"net/mail"
	"strings"
	"time"
)

/*
Several SMTP services per workspace (MH-412).

A workspace used to have ONE smtp_settings row, so a large campaign and the
double-opt-in confirmations shared one provider's rate limits and one sender
reputation. Now it names several services, and two of them carry a role:

	DEFAULT           transactional mail — confirmations, inbox replies, SMTP
	                  and campaign test sends. LoadSMTP resolves it.
	CAMPAIGN DEFAULT  a campaign that names no service of its own.
	                  LoadCampaignSMTP resolves: the campaign's own service,
	                  else the campaign default, else the default.

The first service a workspace adds takes both roles, so a workspace with one
service behaves exactly like the single row it replaces. Migration 115 copies
that row in as "Default" with both roles.

Credentials: the password is sealed at rest and NEVER returned — not by REST
(HasPassword says whether one is stored), not by MCP (list_smtp_services
returns names and status only), and not in an error.
*/

// SMTPService is one named service as the settings page reads it. The
// password is never in it.
type SMTPService struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Host              string     `json:"host"`
	Port              int64      `json:"port"`
	Username          string     `json:"username"`
	HasPassword       bool       `json:"has_password" doc:"The password is write-only; this only says whether one is stored."`
	FromName          string     `json:"from_name"`
	FromEmail         string     `json:"from_email"`
	TLSMode           string     `json:"tls_mode"`
	IsDefault         bool       `json:"is_default" doc:"Transactional mail (confirmations, inbox replies, tests) goes through this one."`
	IsCampaignDefault bool       `json:"is_campaign_default" doc:"A campaign that names no service goes through this one."`
	LastTestAt        *time.Time `json:"last_test_at"`
	LastTestOK        *bool      `json:"last_test_ok"`
	LastTestError     string     `json:"last_test_error"`
	// How many campaigns name this service explicitly.
	Campaigns int64     `json:"campaigns"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SMTPServiceInput is a service as it is saved. Password nil keeps the stored
// one; an empty string clears it.
type SMTPServiceInput struct {
	Name      string
	Host      string
	Port      int64
	Username  string
	Password  *string
	FromName  string
	FromEmail string
	TLSMode   string
}

// ErrServiceNotFound is a service id this workspace does not have.
var ErrServiceNotFound = errors.New("SMTP service not found")

// ErrServiceName is a second service with the same name.
var ErrServiceName = errors.New("a service with that name already exists")

// ErrDefaultInUse refuses deleting the default while others remain: the
// confirmations would otherwise switch provider without anyone choosing it.
var ErrDefaultInUse = errors.New("this is the default service for confirmation and reply emails; make another service the default first")

// ErrBadSender is a from address that is not an email address.
var ErrBadSender = errors.New("the sender is not a valid email address")

const serviceCols = `s.id, s.name, s.host, s.port, s.username, s.password_sealed <> '', s.from_name, s.from_email, s.tls_mode,
	s.is_default, s.is_campaign_default, s.last_test_at, s.last_test_ok, s.last_test_error,
	(SELECT count(*) FROM campaigns c WHERE c.workspace_id = s.workspace_id AND c.smtp_service_id = s.id),
	s.created_at, s.updated_at`

func scanService(sc interface{ Scan(...any) error }) (SMTPService, error) {
	var o SMTPService
	var ok sql.NullBool
	err := sc.Scan(&o.ID, &o.Name, &o.Host, &o.Port, &o.Username, &o.HasPassword, &o.FromName, &o.FromEmail, &o.TLSMode,
		&o.IsDefault, &o.IsCampaignDefault, &o.LastTestAt, &ok, &o.LastTestError, &o.Campaigns, &o.CreatedAt, &o.UpdatedAt)
	if ok.Valid {
		v := ok.Bool
		o.LastTestOK = &v
	}
	return o, err
}

// ListSMTPServices is every service of a workspace: the default first, then by name.
func ListSMTPServices(ctx context.Context, db *sql.DB, wsID string) ([]SMTPService, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+serviceCols+` FROM smtp_services s WHERE s.workspace_id = $1
		ORDER BY s.is_default DESC, s.is_campaign_default DESC, lower(s.name)`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SMTPService{}
	for rows.Next() {
		o, err := scanService(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GetSMTPService reads one service.
func GetSMTPService(ctx context.Context, db *sql.DB, wsID, id string) (*SMTPService, error) {
	o, err := scanService(db.QueryRowContext(ctx, `SELECT `+serviceCols+` FROM smtp_services s WHERE s.workspace_id = $1 AND s.id = $2`, wsID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrServiceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// DefaultSMTPService reads the transactional default, or ErrNoService.
func DefaultSMTPService(ctx context.Context, db *sql.DB, wsID string) (*SMTPService, error) {
	o, err := scanService(db.QueryRowContext(ctx, `SELECT `+serviceCols+` FROM smtp_services s WHERE s.workspace_id = $1 AND s.is_default`, wsID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoService
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// HasSMTP reports whether a workspace has any service at all.
func HasSMTP(ctx context.Context, db *sql.DB, wsID string) bool {
	var ok bool
	_ = db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1)`, wsID).Scan(&ok)
	return ok
}

// loadSealed resolves one service row (by a WHERE clause on s) to a sendable SMTP.
func loadSealed(ctx context.Context, db *sql.DB, where string, args ...any) (*SMTP, error) {
	var s SMTP
	var sealed string
	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.name, s.host, s.port, s.username, s.password_sealed, s.from_name, s.from_email, s.tls_mode
		FROM smtp_services s WHERE `+where, args...,
	).Scan(&s.ServiceID, &s.ServiceName, &s.Host, &s.Port, &s.Username, &sealed, &s.FromName, &s.FromEmail, &s.TLSMode)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoService
	}
	if err != nil {
		return nil, err
	}
	if s.Password, err = unseal(sealed); err != nil {
		return nil, err
	}
	return &s, nil
}

// LoadSMTP resolves the workspace's DEFAULT service — transactional mail:
// confirmations, inbox replies and test sends. Exported so the MCP tools and
// the inbox resolve it exactly the way the REST routes do.
func LoadSMTP(ctx context.Context, db *sql.DB, wsID string) (*SMTP, error) {
	return loadSealed(ctx, db, `s.workspace_id = $1 AND s.is_default`, wsID)
}

// LoadSMTPService resolves one named service.
func LoadSMTPService(ctx context.Context, db *sql.DB, wsID, id string) (*SMTP, error) {
	svc, err := loadSealed(ctx, db, `s.workspace_id = $1 AND s.id = $2`, wsID, id)
	if errors.Is(err, ErrNoService) {
		return nil, ErrServiceNotFound
	}
	return svc, err
}

// LoadCampaignSMTP resolves the service a campaign goes out through: its own,
// else the campaign default, else the default.
func LoadCampaignSMTP(ctx context.Context, db *sql.DB, wsID, campaignID string) (*SMTP, error) {
	return loadSealed(ctx, db, `s.workspace_id = $1 AND s.id = (
		SELECT coalesce(c.smtp_service_id,
		       (SELECT d.id FROM smtp_services d WHERE d.workspace_id = $1 AND d.is_campaign_default),
		       (SELECT d.id FROM smtp_services d WHERE d.workspace_id = $1 AND d.is_default))
		FROM campaigns c WHERE c.workspace_id = $1 AND c.id = $2)`, wsID, campaignID)
}

// normService validates and trims an input. It returns the lower-cased sender.
func normService(in *SMTPServiceInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	in.FromName = strings.TrimSpace(in.FromName)
	if in.Name == "" {
		in.Name = "Default"
	}
	if in.Host == "" {
		return errors.New("a host is required")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return errors.New("the port must be between 1 and 65535")
	}
	from, err := mail.ParseAddress(strings.TrimSpace(in.FromEmail))
	if err != nil {
		return ErrBadSender
	}
	in.FromEmail = strings.ToLower(from.Address)
	switch in.TLSMode {
	case TLSNone, TLSStartTLS, TLSImplicit:
	case "":
		in.TLSMode = TLSStartTLS
	default:
		return errors.New("tls_mode must be none, starttls or tls")
	}
	return nil
}

// CreateSMTPService adds a service. The first one a workspace adds becomes
// both defaults, so a workspace with one service needs to choose nothing.
func CreateSMTPService(ctx context.Context, db *sql.DB, wsID, userID string, in SMTPServiceInput) (*SMTPService, error) {
	if err := normService(&in); err != nil {
		return nil, err
	}
	sealed := ""
	if in.Password != nil && *in.Password != "" {
		var err error
		if sealed, err = seal(*in.Password); err != nil {
			return nil, err
		}
	}
	var id string
	err := db.QueryRowContext(ctx, `
		INSERT INTO smtp_services (workspace_id, name, host, port, username, password_sealed, from_name, from_email, tls_mode,
		                           is_default, is_campaign_default, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
		        NOT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1 AND is_default),
		        NOT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1 AND is_campaign_default), $10)
		RETURNING id`,
		wsID, in.Name, in.Host, in.Port, in.Username, sealed, in.FromName, in.FromEmail, in.TLSMode, userID).Scan(&id)
	if isUnique(err) {
		return nil, ErrServiceName
	}
	if err != nil {
		return nil, err
	}
	return GetSMTPService(ctx, db, wsID, id)
}

// UpdateSMTPService rewrites a service. A nil password keeps the stored one.
func UpdateSMTPService(ctx context.Context, db *sql.DB, wsID, id string, in SMTPServiceInput) (*SMTPService, error) {
	if err := normService(&in); err != nil {
		return nil, err
	}
	keep := in.Password == nil
	sealed := ""
	if !keep && *in.Password != "" {
		var err error
		if sealed, err = seal(*in.Password); err != nil {
			return nil, err
		}
	}
	res, err := db.ExecContext(ctx, `
		UPDATE smtp_services SET name = $3, host = $4, port = $5, username = $6,
		  password_sealed = CASE WHEN $11 THEN password_sealed ELSE $7 END,
		  from_name = $8, from_email = $9, tls_mode = $10,
		  -- A changed server is untested again.
		  last_test_at = NULL, last_test_ok = NULL, last_test_error = '', updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		wsID, id, in.Name, in.Host, in.Port, in.Username, sealed, in.FromName, in.FromEmail, in.TLSMode, keep)
	if isUnique(err) {
		return nil, ErrServiceName
	}
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrServiceNotFound
	}
	return GetSMTPService(ctx, db, wsID, id)
}

// DeleteSMTPService removes a service. Campaigns that named it return to the
// campaign default (the foreign key sets their column to NULL). The default
// itself is refused while other services remain; deleting a campaign default
// just leaves campaigns on the default.
func DeleteSMTPService(ctx context.Context, db *sql.DB, wsID, id string) error {
	var isDefault bool
	var others int64
	err := db.QueryRowContext(ctx, `
		SELECT s.is_default, (SELECT count(*) FROM smtp_services o WHERE o.workspace_id = s.workspace_id AND o.id <> s.id)
		FROM smtp_services s WHERE s.workspace_id = $1 AND s.id = $2`, wsID, id).Scan(&isDefault, &others)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrServiceNotFound
	}
	if err != nil {
		return err
	}
	if isDefault && others > 0 {
		return ErrDefaultInUse
	}
	_, err = db.ExecContext(ctx, `DELETE FROM smtp_services WHERE workspace_id = $1 AND id = $2`, wsID, id)
	return err
}

// SetSMTPDefault makes a service the default (transactional) or the campaign
// default. One statement, so there is never a moment with two or none.
func SetSMTPDefault(ctx context.Context, db *sql.DB, wsID, id string, campaign bool) error {
	col := "is_default"
	if campaign {
		col = "is_campaign_default"
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM smtp_services WHERE workspace_id = $1 AND id = $2)`, wsID, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrServiceNotFound
	}
	// Clear first, then set: the partial unique index holds at every statement.
	if _, err := tx.ExecContext(ctx, `UPDATE smtp_services SET `+col+` = false, updated_at = now()
		WHERE workspace_id = $1 AND `+col+` AND id <> $2`, wsID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE smtp_services SET `+col+` = true, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordSMTPTest keeps the outcome of a test send on the service's row, so the
// list can say which services are known to work.
func RecordSMTPTest(ctx context.Context, db *sql.DB, wsID, id string, sendErr error) {
	msg := ""
	if sendErr != nil {
		msg = trimErr(sendErr.Error())
	}
	_, _ = db.ExecContext(ctx, `UPDATE smtp_services SET last_test_at = now(), last_test_ok = $3, last_test_error = $4
		WHERE workspace_id = $1 AND id = $2`, wsID, id, sendErr == nil, msg)
}

// SMTPTestMessage is the test email a service sends.
func SMTPTestMessage(to, serviceName string) Message {
	text := "This is a test email from Moharrik. Your SMTP settings work."
	if serviceName != "" {
		text = "This is a test email from Moharrik, sent through the SMTP service “" + serviceName + "”. It works."
	}
	return Message{To: to, Subject: "Moharrik SMTP test", Text: text,
		HTML: Shell("en", "SMTP test", "<p>"+html.EscapeString(text)+"</p>", "Moharrik")}
}
