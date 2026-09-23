// Package workspaces is the tenancy surface: creating and listing a user's
// workspaces, members, and invitations. Everything that changes WHO belongs to
// a workspace lives here, derived from the session — never from a request body
// field naming a user or workspace (the generated CRUD for these tables is
// read-only for exactly that reason; see authz.Generated).
package workspaces

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// InviteTTL is how long an invitation link stays valid.
const InviteTTL = 7 * 24 * time.Hour

// Reserved slugs: route segments and the platform workspace.
var reserved = map[string]bool{
	"platform": true, "admin": true, "api": true, "w": true, "www": true, "new": true,
	"settings": true, "invite": true, "login": true, "register": true, "workspaces": true,
}

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$`)

// Slugify derives a slug from a display name.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

// ValidSlug reports whether s may be used as a workspace slug.
func ValidSlug(s string) bool { return slugRe.MatchString(s) && !reserved[s] }

// HashToken is the stored form of an invite token.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// WorkspaceOut is a workspace as seen by one of its members.
type WorkspaceOut struct {
	ID     string `json:"id"`
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Locale string `json:"locale"`
}

type memberOut struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	Status   string    `json:"status"`
	JoinedAt time.Time `json:"joined_at"`
}

type invitationOut struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

func identity(ctx context.Context) (*auth.Identity, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id == nil {
		return nil, huma.Error401Unauthorized("unauthorized")
	}
	return id, nil
}

func scope(ctx context.Context) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	return s, nil
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// Register mounts the tenancy endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	// ---- account level (signed in, no workspace) -------------------------

	huma.Register(api, huma.Operation{
		OperationID: "my-workspaces", Method: http.MethodGet, Path: "/api/me/workspaces",
		Summary: "Workspaces I belong to", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Workspaces []WorkspaceOut `json:"workspaces"`
		}
	}, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		list, err := MyWorkspaces(ctx, a.SysDB, id.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list workspaces")
		}
		out := &struct {
			Body struct {
				Workspaces []WorkspaceOut `json:"workspaces"`
			}
		}{}
		out.Body.Workspaces = list
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "create-my-workspace", Method: http.MethodPost, Path: "/api/me/workspaces",
		DefaultStatus: http.StatusCreated, Summary: "Create a workspace (you become its owner)", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"80"`
			Slug string `json:"slug,omitempty"`
		}
	}) (*struct {
		Body struct {
			Workspace WorkspaceOut `json:"workspace"`
		}
	}, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		slug := strings.ToLower(strings.TrimSpace(in.Body.Slug))
		if slug == "" {
			slug = Slugify(name)
		}
		if !ValidSlug(slug) {
			return nil, huma.Error422UnprocessableEntity("the address must be 3–40 lowercase letters, digits or dashes, and not a reserved word")
		}
		ws, err := Create(ctx, a.SysDB, id.ID, name, slug)
		if isUniqueViolation(err) {
			return nil, huma.Error409Conflict("that address is taken")
		}
		if err != nil {
			slog.ErrorContext(ctx, "create workspace", "err", err)
			return nil, huma.Error500InternalServerError("could not create the workspace")
		}
		out := &struct {
			Body struct {
				Workspace WorkspaceOut `json:"workspace"`
			}
		}{}
		out.Body.Workspace = *ws
		return out, nil
	})

	// ---- invitations: public preview + signed-in accept ------------------

	huma.Register(api, huma.Operation{
		OperationID: "preview-invitation", Method: http.MethodGet, Path: "/api/invitations/{token}",
		Summary: "What an invitation is for (the token is the authorization)", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, in *struct {
		Token string `path:"token"`
	}) (*struct {
		Body struct {
			WorkspaceName string `json:"workspace_name"`
			Email         string `json:"email"`
			Role          string `json:"role"`
			Expired       bool   `json:"expired"`
			Accepted      bool   `json:"accepted"`
		}
	}, error) {
		out := &struct {
			Body struct {
				WorkspaceName string `json:"workspace_name"`
				Email         string `json:"email"`
				Role          string `json:"role"`
				Expired       bool   `json:"expired"`
				Accepted      bool   `json:"accepted"`
			}
		}{}
		var expires time.Time
		var accepted *time.Time
		err := a.SysDB.QueryRowContext(ctx, `
			SELECT w.name, i.email, i.role, i.expires_at, i.accepted_at
			FROM invitations i JOIN workspaces w ON w.id = i.workspace_id
			WHERE i.token_hash = $1`, HashToken(in.Token),
		).Scan(&out.Body.WorkspaceName, &out.Body.Email, &out.Body.Role, &expires, &accepted)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("invitation not found")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the invitation")
		}
		out.Body.Expired = time.Now().After(expires)
		out.Body.Accepted = accepted != nil
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "accept-invitation", Method: http.MethodPost, Path: "/api/invitations/accept",
		Summary: "Accept an invitation as the signed-in user", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, in *struct {
		Body struct {
			Token string `json:"token" minLength:"1"`
		}
	}) (*struct {
		Body struct {
			Workspace WorkspaceOut `json:"workspace"`
		}
	}, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		ws, err := Accept(ctx, a.SysDB, id.ID, id.Email, in.Body.Token)
		switch {
		case errors.Is(err, errInviteGone):
			return nil, huma.Error404NotFound("this invitation is no longer valid")
		case errors.Is(err, errInviteEmail):
			return nil, huma.Error403Forbidden("this invitation was sent to a different email address")
		case err != nil:
			return nil, huma.Error500InternalServerError("could not accept the invitation")
		}
		out := &struct {
			Body struct {
				Workspace WorkspaceOut `json:"workspace"`
			}
		}{}
		out.Body.Workspace = *ws
		return out, nil
	})

	// ---- workspace level (X-Workspace-ID, membership required) -----------

	huma.Register(api, huma.Operation{
		OperationID: "current-workspace", Method: http.MethodGet, Path: "/api/w/current",
		Summary: "The workspace in X-Workspace-ID, my role and what it allows", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, _ *wsHeader) (*struct {
		Body struct {
			Workspace struct {
				ID       string `json:"id"`
				Slug     string `json:"slug"`
				Name     string `json:"name"`
				Locale   string `json:"locale"`
				Timezone string `json:"timezone"`
				Currency string `json:"currency"`
			} `json:"workspace"`
			Role string   `json:"role"`
			Can  []string `json:"can"`
		}
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Workspace struct {
					ID       string `json:"id"`
					Slug     string `json:"slug"`
					Name     string `json:"name"`
					Locale   string `json:"locale"`
					Timezone string `json:"timezone"`
					Currency string `json:"currency"`
				} `json:"workspace"`
				Role string   `json:"role"`
				Can  []string `json:"can"`
			}
		}{}
		w := &out.Body.Workspace
		// App pool: RLS allows exactly the current workspace's row.
		err = a.SQLDB.QueryRowContext(ctx,
			`SELECT id, slug, name, locale, timezone, currency FROM workspaces WHERE id = $1`, s.WorkspaceID,
		).Scan(&w.ID, &w.Slug, &w.Name, &w.Locale, &w.Timezone, &w.Currency)
		if err != nil {
			return nil, huma.Error404NotFound("workspace not found")
		}
		out.Body.Role = s.Role
		out.Body.Can = authz.Allowed(s.Role)
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-members", Method: http.MethodGet, Path: "/api/w/members",
		Summary: "Members of the workspace", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, _ *wsHeader) (*struct {
		Body struct {
			Members []memberOut `json:"members"`
		}
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if !authz.Can(s.Role, authz.MembersRead) {
			return nil, huma.Error403Forbidden("your role cannot see the member list")
		}
		list, err := Members(ctx, a.SysDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list members")
		}
		out := &struct {
			Body struct {
				Members []memberOut `json:"members"`
			}
		}{}
		out.Body.Members = list
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-list-invitations", Method: http.MethodGet, Path: "/api/w/invitations",
		Summary: "Pending invitations", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, _ *wsHeader) (*struct {
		Body struct {
			Invitations []invitationOut `json:"invitations"`
		}
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if !authz.Can(s.Role, authz.InvitationsManage) {
			return nil, huma.Error403Forbidden("only owners and admins manage invitations")
		}
		rows, err := a.SQLDB.QueryContext(ctx, `
			SELECT id, email, role, expires_at, created_at FROM invitations
			WHERE workspace_id = $1 AND accepted_at IS NULL ORDER BY created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list invitations")
		}
		defer rows.Close()
		out := &struct {
			Body struct {
				Invitations []invitationOut `json:"invitations"`
			}
		}{}
		out.Body.Invitations = []invitationOut{}
		for rows.Next() {
			var i invitationOut
			if err := rows.Scan(&i.ID, &i.Email, &i.Role, &i.ExpiresAt, &i.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not list invitations")
			}
			out.Body.Invitations = append(out.Body.Invitations, i)
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-invitation", Method: http.MethodPost, Path: "/api/w/invitations",
		DefaultStatus: http.StatusCreated, Summary: "Invite someone by email (the link is returned once; no email is sent in P0)",
		Tags: []string{"Tenancy"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Email string `json:"email" minLength:"3"`
			Role  string `json:"role" enum:"admin,manager,member,client"`
		}
	}) (*struct {
		Body struct {
			Invitation invitationOut `json:"invitation"`
			AcceptURL  string        `json:"accept_url"`
		}
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if !authz.Can(s.Role, authz.MembersInvite) {
			return nil, huma.Error403Forbidden("only owners and admins can invite")
		}
		// You cannot hand out more than you hold.
		if !authz.Role(s.Role).AtLeast(authz.Role(in.Body.Role)) {
			return nil, huma.Error403Forbidden("you cannot invite someone to a role above your own")
		}
		addr, err := mail.ParseAddress(strings.TrimSpace(in.Body.Email))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("that is not a valid email address")
		}
		email := strings.ToLower(addr.Address)

		var already bool
		_ = a.SysDB.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM workspace_members m JOIN users u ON u.id = m.user_id
			               WHERE m.workspace_id = $1 AND lower(u.email) = $2)`, s.WorkspaceID, email,
		).Scan(&already)
		if already {
			return nil, huma.Error409Conflict("that person is already a member")
		}

		token := newToken()
		out := &struct {
			Body struct {
				Invitation invitationOut `json:"invitation"`
				AcceptURL  string        `json:"accept_url"`
			}
		}{}
		inv := &out.Body.Invitation
		err = a.SQLDB.QueryRowContext(ctx, `
			INSERT INTO invitations (workspace_id, email, role, token_hash, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, email, role, expires_at, created_at`,
			s.WorkspaceID, email, in.Body.Role, HashToken(token), s.UserID, time.Now().Add(InviteTTL).UTC(),
		).Scan(&inv.ID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not create the invitation")
		}
		out.Body.AcceptURL = "/invite/" + token
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-invitation", Method: http.MethodDelete, Path: "/api/w/invitations/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Revoke a pending invitation", Tags: []string{"Tenancy"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if !authz.Can(s.Role, authz.InvitationsManage) {
			return nil, huma.Error403Forbidden("only owners and admins manage invitations")
		}
		res, err := a.SQLDB.ExecContext(ctx,
			`DELETE FROM invitations WHERE workspace_id = $1 AND id = $2 AND accepted_at IS NULL`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not revoke the invitation")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("invitation not found")
		}
		return &struct{}{}, nil
	})
}

// MyWorkspaces lists userID's active memberships (owner pool, explicit user predicate).
func MyWorkspaces(ctx context.Context, sys *sql.DB, userID string) ([]WorkspaceOut, error) {
	rows, err := sys.QueryContext(ctx, `
		SELECT w.id, w.slug, w.name, m.role, w.locale
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 AND m.status = 'active'
		ORDER BY lower(w.name)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceOut{}
	for rows.Next() {
		var w WorkspaceOut
		if err := rows.Scan(&w.ID, &w.Slug, &w.Name, &w.Role, &w.Locale); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Create makes a workspace with userID as its owner, in one transaction.
func Create(ctx context.Context, sys *sql.DB, userID, name, slug string) (*WorkspaceOut, error) {
	tx, err := sys.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	// On the owner pool, which sets no audit context by itself (MH-392).
	if err := tenant.AuditTx(ctx, tx); err != nil {
		return nil, err
	}
	ws := &WorkspaceOut{Name: name, Slug: slug, Role: string(authz.Owner)}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO workspaces (slug, name, created_by) VALUES ($1, $2, $3) RETURNING id, locale`,
		slug, name, userID,
	).Scan(&ws.ID, &ws.Locale); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`,
		ws.ID, userID,
	); err != nil {
		return nil, err
	}
	return ws, tx.Commit()
}

// Members lists a workspace's members with their emails (owner pool — the app
// role has no grant on users — with an explicit workspace predicate).
func Members(ctx context.Context, sys *sql.DB, workspaceID string) ([]memberOut, error) {
	rows, err := sys.QueryContext(ctx, `
		SELECT m.user_id, u.email, m.role, m.status, m.created_at
		FROM workspace_members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1
		ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'manager' THEN 2 WHEN 'member' THEN 3 ELSE 4 END,
		         lower(u.email)`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []memberOut{}
	for rows.Next() {
		var m memberOut
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.Status, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

var (
	errInviteGone  = errors.New("invitation gone")
	errInviteEmail = errors.New("invitation email mismatch")
)

// Accept redeems an invite token for the signed-in user. The invitation is
// bound to its email: a forwarded link does not let someone else in.
func Accept(ctx context.Context, sys *sql.DB, userID, userEmail, token string) (*WorkspaceOut, error) {
	tx, err := sys.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := tenant.AuditTx(ctx, tx); err != nil {
		return nil, err
	}

	var invID, wsID, email, role string
	var expires time.Time
	var accepted *time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT id, workspace_id, email, role, expires_at, accepted_at FROM invitations
		WHERE token_hash = $1 FOR UPDATE`, HashToken(strings.TrimSpace(token)),
	).Scan(&invID, &wsID, &email, &role, &expires, &accepted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteGone
	}
	if err != nil {
		return nil, err
	}
	if accepted != nil || time.Now().After(expires) {
		return nil, errInviteGone
	}
	if !strings.EqualFold(email, userEmail) {
		return nil, errInviteEmail
	}
	// Already a member keeps their current role — an invite never demotes.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, $3, 'active')
		ON CONFLICT (workspace_id, user_id) DO NOTHING`, wsID, userID, role); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE invitations SET accepted_at = now(), accepted_by = $2, updated_at = now() WHERE id = $1`,
		invID, userID); err != nil {
		return nil, err
	}
	ws := &WorkspaceOut{}
	if err := tx.QueryRowContext(ctx, `
		SELECT w.id, w.slug, w.name, m.role, w.locale FROM workspaces w
		JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2
		WHERE w.id = $1`, wsID, userID,
	).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.Role, &ws.Locale); err != nil {
		return nil, err
	}
	return ws, tx.Commit()
}
