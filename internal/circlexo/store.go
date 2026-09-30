package circlexo

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// All queries here run on the owner pool (platform tables, see schema/circlexo.sql) and none
// touches a tenant table, so they are plain SQL rather than sqlc: the tenant-predicate guard is
// about tenant tables, and these are read before a workspace is known.

// OrgLink is a hub org and the workspace that serves it.
type OrgLink struct {
	OrgID         string
	OrgSlug       string
	WorkspaceID   string
	WorkspaceSlug string
	Locale        string
	Active        bool
}

const orgLinkSQL = `
	SELECT l.org_id, l.org_slug, l.workspace_id, w.slug, w.locale, l.status = 'active'
	FROM circlexo_org_links l JOIN workspaces w ON w.id = l.workspace_id`

func scanOrgLink(row *sql.Row) (*OrgLink, error) {
	var l OrgLink
	if err := row.Scan(&l.OrgID, &l.OrgSlug, &l.WorkspaceID, &l.WorkspaceSlug, &l.Locale, &l.Active); err != nil {
		return nil, err
	}
	return &l, nil
}

// OrgByID returns the link for a hub org (any status); sql.ErrNoRows when there is none.
func (s *Service) OrgByID(ctx context.Context, orgID string) (*OrgLink, error) {
	return scanOrgLink(s.DB.QueryRowContext(ctx, orgLinkSQL+` WHERE l.org_id = $1`, orgID))
}

// OrgByWorkspace returns the link of a workspace (any status); sql.ErrNoRows when it has none.
func (s *Service) OrgByWorkspace(ctx context.Context, workspaceID string) (*OrgLink, error) {
	return scanOrgLink(s.DB.QueryRowContext(ctx, orgLinkSQL+` WHERE l.workspace_id = $1`, workspaceID))
}

// UserBySubject returns the Moharrik user linked to a hub subject; sql.ErrNoRows when unlinked.
func (s *Service) UserBySubject(ctx context.Context, subject string) (string, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM circlexo_user_links WHERE subject = $1`, subject).Scan(&id)
	return id, err
}

// linkUser records subject -> user. It reports the user the subject ends up linked to, which is
// not userID when another request linked it first (or, for an already-linked user, when they have
// a different hub account): the caller must compare, never assume.
func (s *Service) linkUser(ctx context.Context, subject, userID, email, via string) (string, error) {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO circlexo_user_links (subject, user_id, email, linked_via) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, subject, userID, email, via)
	if err != nil {
		return "", err
	}
	got, err := s.UserBySubject(ctx, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // the user is linked to a different subject: the caller sees "" != userID
	}
	return got, err
}

// userByEmail finds a Moharrik user by e-mail address, ignoring case. usable says whether the
// account may be linked (or given a workspace) on the strength of an address alone. Moharrik
// registers with a password and never verifies the address, so only an account with no password
// at all (created by SSO or a hub sync) qualifies: anyone else could have registered someone
// else's address with a password of their own and be waiting for that person to sign in with
// CircleXO. A password account is linked only when its owner proves it (sso.go: the same
// signed-in Moharrik session as the hub's verified address).
func (s *Service) userByEmail(ctx context.Context, email string) (id, stored string, usable bool, err error) {
	err = s.DB.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.password_hash = '!sso'
		FROM users u WHERE lower(u.email) = lower($1) ORDER BY u.created_at LIMIT 1`, email).Scan(&id, &stored, &usable)
	return id, stored, usable, err
}

func isUnique(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

func norm(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// dedupe is the webhooks.Deduper over circlexo_webhook_events, so a retry is recognised by any
// API replica. Done runs only after the handler succeeded, so a failed event is retried.
type dedupe struct{ db *sql.DB }

func (d dedupe) Seen(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	var ok bool
	err := d.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM circlexo_webhook_events WHERE webhook_id = $1)`, id).Scan(&ok)
	return ok, err
}

func (d dedupe) Done(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO circlexo_webhook_events (webhook_id) VALUES ($1) ON CONFLICT DO NOTHING`, id)
	if err == nil {
		// Old ids only matter inside the hub's retry window (days at most).
		_, _ = d.db.ExecContext(ctx, `DELETE FROM circlexo_webhook_events WHERE received_at < now() - interval '30 days'`)
	}
	return err
}

func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
