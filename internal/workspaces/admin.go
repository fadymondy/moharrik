package workspaces

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/authz"
)

/*
The workspace changes both the REST routes and the MCP tools make (MH-680):
rename, the picture, and invitations. Each checks the caller's role itself, so
a token scoped for it gets exactly what the page gives the same member.

db is the tenant-scoped pool (a.SQLDB), sys the owner pool (a.SysDB).
Errors are huma status errors, which say what went wrong in plain words.
*/

// Info is a workspace as the manage page and the tools show it.
type Info struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url"`
}

// Invitation is a pending invitation.
type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Read returns the workspace's name, address and picture.
func Read(ctx context.Context, sys *sql.DB, workspaceID string) (*Info, error) {
	var b Info
	var at sql.NullTime
	err := sys.QueryRowContext(ctx, `
		SELECT w.id, w.slug, w.name, a.updated_at
		FROM workspaces w LEFT JOIN workspace_avatars a ON a.workspace_id = w.id
		WHERE w.id = $1`, workspaceID).Scan(&b.ID, &b.Slug, &b.Name, &at)
	if err != nil {
		return nil, huma.Error404NotFound("workspace not found")
	}
	b.AvatarURL = AvatarURL(b.ID, at)
	return &b, nil
}

func canUpdate(actorRole string) error {
	if !authz.Can(actorRole, authz.WorkspaceUpdate) {
		return huma.Error403Forbidden("only owners and admins can change the workspace")
	}
	return nil
}

// Rename sets the workspace's name.
func Rename(ctx context.Context, db *sql.DB, workspaceID, actorRole, name string) error {
	if err := canUpdate(actorRole); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return huma.Error422UnprocessableEntity("a name is required")
	}
	if len([]rune(name)) > 80 {
		return huma.Error422UnprocessableEntity("a name is at most 80 characters")
	}
	_, err := db.ExecContext(ctx, `UPDATE workspaces SET name = $2, updated_at = now() WHERE id = $1`, workspaceID, name)
	return err
}

// SetAvatar uploads or replaces the picture: PNG, JPEG or WEBP, up to 2MB.
func SetAvatar(ctx context.Context, sys *sql.DB, workspaceID, actorRole, contentType string, raw []byte) error {
	if err := canUpdate(actorRole); err != nil {
		return err
	}
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if !avatarContentTypes[ct] {
		return huma.Error422UnprocessableEntity("only PNG, JPEG or WEBP images are allowed")
	}
	if len(raw) == 0 {
		return huma.Error422UnprocessableEntity("the image is empty")
	}
	if len(raw) > avatarMaxSize {
		return huma.Error422UnprocessableEntity("images are limited to 2MB")
	}
	_, err := sys.ExecContext(ctx, `
		INSERT INTO workspace_avatars (workspace_id, content_type, size_bytes, data)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id) DO UPDATE SET content_type = EXCLUDED.content_type,
			size_bytes = EXCLUDED.size_bytes, data = EXCLUDED.data, updated_at = clock_timestamp()`,
		workspaceID, ct, len(raw), raw)
	return err
}

// RemoveAvatar takes the picture away; the initial shows again.
func RemoveAvatar(ctx context.Context, sys *sql.DB, workspaceID, actorRole string) error {
	if err := canUpdate(actorRole); err != nil {
		return err
	}
	_, err := sys.ExecContext(ctx, `DELETE FROM workspace_avatars WHERE workspace_id = $1`, workspaceID)
	return err
}

// Invitations lists the pending invitations, newest first.
func Invitations(ctx context.Context, db *sql.DB, workspaceID, actorRole string) ([]Invitation, error) {
	if !authz.Can(actorRole, authz.InvitationsManage) {
		return nil, huma.Error403Forbidden("only owners and admins manage invitations")
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, email, role, expires_at, created_at FROM invitations
		WHERE workspace_id = $1 AND accepted_at IS NULL ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invitation{}
	for rows.Next() {
		var i Invitation
		if err := rows.Scan(&i.ID, &i.Email, &i.Role, &i.ExpiresAt, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Invite creates an invitation bound to email and returns it with its accept
// path ("/invite/<token>"), which is shown once: the token is stored hashed.
// No email is sent — the inviter passes the link on.
func Invite(ctx context.Context, db, sys *sql.DB, workspaceID, actorRole, inviterID, email, role string) (*Invitation, string, error) {
	if !authz.Can(actorRole, authz.MembersInvite) {
		return nil, "", huma.Error403Forbidden("only owners and admins can invite")
	}
	switch role {
	case string(authz.Admin), string(authz.Manager), string(authz.Member), string(authz.Client):
	default:
		return nil, "", huma.Error422UnprocessableEntity("role must be admin, manager, member or client")
	}
	// You cannot hand out more than you hold.
	if !authz.Role(actorRole).AtLeast(authz.Role(role)) {
		return nil, "", huma.Error403Forbidden("you cannot invite someone to a role above your own")
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil {
		return nil, "", huma.Error422UnprocessableEntity("that is not a valid email address")
	}
	email = strings.ToLower(addr.Address)

	var already bool
	_ = sys.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM workspace_members m JOIN users u ON u.id = m.user_id
		               WHERE m.workspace_id = $1 AND lower(u.email) = $2)`, workspaceID, email,
	).Scan(&already)
	if already {
		return nil, "", huma.Error409Conflict("that person is already a member")
	}

	// A workspace governed by a CircleXO hub org may be capped on members (internal/circlexo).
	if BeforeInvite != nil {
		if err := BeforeInvite(ctx, workspaceID); err != nil {
			return nil, "", err
		}
	}

	token := newToken()
	var inv Invitation
	err = db.QueryRowContext(ctx, `
		INSERT INTO invitations (workspace_id, email, role, token_hash, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, email, role, expires_at, created_at`,
		workspaceID, email, role, HashToken(token), inviterID, time.Now().Add(InviteTTL).UTC(),
	).Scan(&inv.ID, &inv.Email, &inv.Role, &inv.ExpiresAt, &inv.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	return &inv, "/invite/" + token, nil
}

// RevokeInvitation deletes a pending invitation; its link stops working.
func RevokeInvitation(ctx context.Context, db *sql.DB, workspaceID, actorRole, id string) error {
	if !authz.Can(actorRole, authz.InvitationsManage) {
		return huma.Error403Forbidden("only owners and admins manage invitations")
	}
	res, err := db.ExecContext(ctx,
		`DELETE FROM invitations WHERE workspace_id = $1 AND id = $2 AND accepted_at IS NULL`, workspaceID, id)
	if err != nil {
		if isInvalidUUID(err) {
			return huma.Error404NotFound("invitation not found")
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("invitation not found")
	}
	return nil
}

func isInvalidUUID(err error) bool {
	var pg interface{ SQLState() string }
	return errors.As(err, &pg) && pg.SQLState() == "22P02"
}

// BeforeInvite, when set, is asked before an invitation is created and may refuse it (an error
// that is already a huma error is returned as is). internal/circlexo sets it when the hub
// integration is on, to hold a hub-linked workspace to the `members` limit of its plan. Nil (the
// default, and always when CIRCLEXO_ISSUER is unset) allows everything.
var BeforeInvite func(ctx context.Context, workspaceID string) error
