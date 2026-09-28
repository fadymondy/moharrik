package workspaces

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Managing a workspace (MH-679): "we must have manage workspaces page that we can
edit/delete/add team avatar and add team members to the team" (Fady, verbatim).

	PUT    /api/w/workspace              rename                        admin+
	PUT    /api/w/workspace/avatar       upload or replace the picture admin+
	DELETE /api/w/workspace/avatar       back to the initial           admin+
	DELETE /api/w/workspace              delete it and everything in it owner
	PUT    /api/w/members/{user_id}      change a member's role        admin+
	DELETE /api/w/members/{user_id}      remove a member, or leave     admin+ / anyone for themselves
	GET    /api/me/workspaces/{id}/avatar  the picture, for members

Adding people stays with invitations (workspaces.go): an invite is bound to
its email, and nobody joins a workspace without accepting.

The rules every membership change keeps:
  - a workspace always has at least one owner — the last one can be neither
    demoted nor removed, and cannot leave;
  - only an owner touches an owner, or makes someone one;
  - nobody grants a role above their own.
*/

const avatarMaxSize = 2 << 20 // 2MB, the same cap as a contact photo (MH-602)

var avatarContentTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/webp": true}

// AvatarURL is the picture's address for a workspace whose avatar row was last
// written at updatedAt, or "" when it has none. The version parameter makes a
// replaced picture a new URL, so a cached old one never shows.
func AvatarURL(workspaceID string, updatedAt sql.NullTime) string {
	if !updatedAt.Valid {
		return ""
	}
	return fmt.Sprintf("/api/me/workspaces/%s/avatar?v=%d", workspaceID, updatedAt.Time.UnixMilli())
}

func avatarURLFor(ctx context.Context, sys *sql.DB, workspaceID string) string {
	var at sql.NullTime
	if err := sys.QueryRowContext(ctx, `SELECT updated_at FROM workspace_avatars WHERE workspace_id = $1`, workspaceID).Scan(&at); err != nil {
		return ""
	}
	return AvatarURL(workspaceID, at)
}

// releaseWhatsApp lets go of the workspace's WhatsApp number before the
// workspace goes: Evolution's instance first, exactly as the unlink route
// does, so no phone session is left behind that nobody can see or unlink.
// A var so tests never reach a real Evolution server.
var releaseWhatsApp = func(ctx context.Context, a *app.App, workspaceID string) error {
	l, err := whatsapp.Load(ctx, a, workspaceID)
	if errors.Is(err, whatsapp.ErrNoLink) {
		return nil
	}
	if err != nil {
		return err
	}
	if cfg := whatsapp.LoadConfig(); cfg.Configured() {
		if err := whatsapp.ClientFor(cfg, l).Delete(ctx); err != nil {
			return err
		}
	}
	return whatsapp.Unlink(ctx, a, workspaceID)
}

// ErrLastOwner: the change would leave the workspace without an owner.
var ErrLastOwner = errors.New("a workspace needs at least one owner")

// Delete removes a workspace and everything in it, in one transaction.
//
// 74 tables reference workspaces with ON DELETE CASCADE, but about 35 carry a
// workspace_id with no foreign key at all (the audit log, analytics caches,
// channel links…), so those are cleared first. Some of them reference each
// other, so a pass that trips a foreign key is simply retried after the rest.
// No audit context is set: the rows go with the workspace, and the audit log
// is one of them.
func Delete(ctx context.Context, sys *sql.DB, workspaceID string) error {
	tx, err := sys.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	rows, err := tx.QueryContext(ctx, `
		SELECT c.table_name
		FROM information_schema.columns c
		JOIN information_schema.tables t ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = 'public' AND c.column_name = 'workspace_id' AND t.table_type = 'BASE TABLE'
		  AND NOT EXISTS (
		    SELECT 1 FROM pg_constraint k
		    JOIN pg_attribute att ON att.attrelid = k.conrelid AND att.attnum = ANY (k.conkey)
		    WHERE k.contype = 'f' AND k.confrelid = 'workspaces'::regclass
		      AND k.conrelid = format('public.%I', c.table_name)::regclass AND att.attname = 'workspace_id')
		ORDER BY c.table_name`)
	if err != nil {
		return err
	}
	var pending []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for pass := 0; len(pending) > 0; pass++ {
		if pass == 6 {
			return fmt.Errorf("could not clear %s", strings.Join(pending, ", "))
		}
		var left []string
		for i, t := range pending {
			sp := fmt.Sprintf("ws_del_%d_%d", pass, i)
			if _, err := tx.ExecContext(ctx, "SAVEPOINT "+sp); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM "`+strings.ReplaceAll(t, `"`, `""`)+`" WHERE workspace_id::text = $1`, workspaceID)
			if err != nil {
				if !isForeignKeyViolation(err) {
					return fmt.Errorf("clear %s: %w", t, err)
				}
				if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+sp); err != nil {
					return err
				}
				left = append(left, t)
				continue
			}
			if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
				return err
			}
		}
		pending = left
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM workspaces WHERE id = $1`, workspaceID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func isForeignKeyViolation(err error) bool {
	var pg interface{ SQLState() string }
	return errors.As(err, &pg) && pg.SQLState() == "23503"
}

// SetRole changes one member's role, keeping the rules in this file's header.
// actorRole is the caller's role in the workspace.
func SetRole(ctx context.Context, sys *sql.DB, workspaceID, actorRole, userID, role string) error {
	if !authz.Valid(role) {
		return huma.Error422UnprocessableEntity("unknown role: " + role)
	}
	if !authz.Role(actorRole).AtLeast(authz.Role(role)) {
		return huma.Error403Forbidden("you cannot give a role above your own")
	}
	return memberChange(ctx, sys, workspaceID, userID, func(tx *sql.Tx, current string, owners int) error {
		if (current == string(authz.Owner) || role == string(authz.Owner)) && actorRole != string(authz.Owner) {
			return huma.Error403Forbidden("only an owner can change an owner, or make someone one")
		}
		if current == string(authz.Owner) && role != string(authz.Owner) && owners <= 1 {
			return huma.Error409Conflict(ErrLastOwner.Error())
		}
		_, err := tx.ExecContext(ctx, `UPDATE workspace_members SET role = $3, updated_at = now() WHERE workspace_id = $1 AND user_id = $2`,
			workspaceID, userID, role)
		return err
	})
}

// RemoveMember takes userID out of the workspace. self is true when they are
// leaving on their own, which needs no permission — only that an owner is left.
// Their MCP tokens and OAuth grants for this workspace are revoked with them.
func RemoveMember(ctx context.Context, sys *sql.DB, workspaceID, actorRole, userID string, self bool) error {
	return memberChange(ctx, sys, workspaceID, userID, func(tx *sql.Tx, current string, owners int) error {
		if !self && current == string(authz.Owner) && actorRole != string(authz.Owner) {
			return huma.Error403Forbidden("only an owner can remove an owner")
		}
		if current == string(authz.Owner) && owners <= 1 {
			if self {
				return huma.Error409Conflict("you are the only owner: make someone else an owner before you leave, or delete the workspace")
			}
			return huma.Error409Conflict(ErrLastOwner.Error())
		}
		for _, q := range []string{
			`UPDATE mcp_tokens SET revoked_at = now() WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			`UPDATE oauth_grants SET revoked_at = now(), updated_at = now() WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL`,
			`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`,
		} {
			if _, err := tx.ExecContext(ctx, q, workspaceID, userID); err != nil {
				return err
			}
		}
		return nil
	})
}

// memberChange runs fn in an audited owner-pool transaction with the target's
// current role and the workspace's owner count, both read under a row lock so
// two owners demoting each other at once cannot leave nobody in charge.
func memberChange(ctx context.Context, sys *sql.DB, workspaceID, userID string, fn func(tx *sql.Tx, current string, owners int) error) error {
	tx, err := sys.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := tenant.AuditTx(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM workspace_members WHERE workspace_id = $1 AND role = 'owner' FOR UPDATE`, workspaceID); err != nil {
		return err
	}
	var current string
	err = tx.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 FOR UPDATE`,
		workspaceID, userID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return huma.Error404NotFound("member not found")
	}
	if err != nil {
		return err
	}
	var owners int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workspace_members WHERE workspace_id = $1 AND role = 'owner' AND status = 'active'`,
		workspaceID).Scan(&owners); err != nil {
		return err
	}
	if err := fn(tx, current, owners); err != nil {
		return err
	}
	return tx.Commit()
}

// asHuma passes a huma error through and turns anything else into a 500.
func asHuma(ctx context.Context, err error, what string) error {
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	slog.ErrorContext(ctx, what, "err", err)
	return huma.Error500InternalServerError("could not " + what)
}

type workspaceOut struct {
	Body Info
}

func readWorkspace(ctx context.Context, sys *sql.DB, workspaceID string) (*workspaceOut, error) {
	b, err := Read(ctx, sys, workspaceID)
	if err != nil {
		return nil, err
	}
	return &workspaceOut{Body: *b}, nil
}

func registerManage(api huma.API, a *app.App) {
	tags := []string{"Tenancy"}

	huma.Register(api, huma.Operation{
		OperationID: "w-update-workspace", Method: http.MethodPut, Path: "/api/w/workspace",
		Summary: "Rename the workspace (MH-679). Admin/owner only.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"80"`
		}
	}) (*workspaceOut, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if err := Rename(ctx, a.SQLDB, s.WorkspaceID, s.Role, in.Body.Name); err != nil {
			return nil, asHuma(ctx, err, "update the workspace")
		}
		return readWorkspace(ctx, a.SysDB, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-upload-workspace-avatar", Method: http.MethodPut, Path: "/api/w/workspace/avatar",
		// 2MB of image is ~2.7MB of base64 — above Huma's 1MB default.
		MaxBodyBytes: 3 << 20,
		Summary:      "Upload or replace the workspace picture: base64 bytes, PNG/JPEG/WEBP, up to 2MB (MH-679). Admin/owner only.",
		Tags:         tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Data        string `json:"data" minLength:"1" doc:"The image, base64-encoded."`
			ContentType string `json:"content_type" doc:"image/png, image/jpeg or image/webp."`
		}
	}) (*workspaceOut, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		raw, err := base64.StdEncoding.DecodeString(in.Body.Data)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("the image data is not valid base64")
		}
		if err := SetAvatar(ctx, a.SysDB, s.WorkspaceID, s.Role, in.Body.ContentType, raw); err != nil {
			return nil, asHuma(ctx, err, "save the picture")
		}
		return readWorkspace(ctx, a.SysDB, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-remove-workspace-avatar", Method: http.MethodDelete, Path: "/api/w/workspace/avatar",
		Summary: "Remove the workspace picture; the initial shows again (MH-679). Admin/owner only.", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*workspaceOut, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if err := RemoveAvatar(ctx, a.SysDB, s.WorkspaceID, s.Role); err != nil {
			return nil, asHuma(ctx, err, "remove the picture")
		}
		return readWorkspace(ctx, a.SysDB, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-workspace", Method: http.MethodDelete, Path: "/api/w/workspace",
		DefaultStatus: http.StatusNoContent,
		Summary: "Delete the workspace and everything in it (MH-679). Owners only; confirm must equal the workspace's " +
			"address. Its WhatsApp number is unlinked first. There is no undo.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Confirm string `query:"confirm" doc:"The workspace's address (slug), typed by the owner."`
	}) (*struct{}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if !authz.Can(s.Role, authz.WorkspaceDelete) {
			return nil, huma.Error403Forbidden("only an owner can delete the workspace")
		}
		var slug string
		if err := a.SysDB.QueryRowContext(ctx, `SELECT slug FROM workspaces WHERE id = $1`, s.WorkspaceID).Scan(&slug); err != nil {
			return nil, huma.Error404NotFound("workspace not found")
		}
		if strings.TrimSpace(in.Confirm) != slug {
			return nil, huma.Error422UnprocessableEntity("type the workspace's address to confirm")
		}
		if err := releaseWhatsApp(ctx, a, s.WorkspaceID); err != nil {
			slog.ErrorContext(ctx, "delete workspace: release whatsapp", "err", err)
			return nil, huma.Error502BadGateway("could not unlink the workspace's WhatsApp number, so nothing was deleted — try again, or unlink it in Settings first")
		}
		if err := Delete(ctx, a.SysDB, s.WorkspaceID); err != nil {
			slog.ErrorContext(ctx, "delete workspace", "err", err, "workspace", s.WorkspaceID)
			return nil, huma.Error500InternalServerError("could not delete the workspace")
		}
		slog.InfoContext(ctx, "workspace deleted", "workspace", s.WorkspaceID, "slug", slug, "by", s.UserID)
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-set-member-role", Method: http.MethodPut, Path: "/api/w/members/{user_id}",
		Summary: "Change a member's role (MH-679). Admin/owner only; only an owner changes an owner or makes one, and " +
			"the last owner cannot be demoted.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		UserID string `path:"user_id"`
		Body   struct {
			Role string `json:"role" enum:"owner,admin,manager,member,client"`
		}
	}) (*struct {
		Body struct {
			Members []memberOut `json:"members"`
		}
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		if !authz.Can(s.Role, authz.MembersManage) {
			return nil, huma.Error403Forbidden("only owners and admins can change roles")
		}
		if err := SetRole(ctx, a.SysDB, s.WorkspaceID, s.Role, in.UserID, in.Body.Role); err != nil {
			return nil, asHuma(ctx, err, "change the role")
		}
		return membersBody(ctx, a.SysDB, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-remove-member", Method: http.MethodDelete, Path: "/api/w/members/{user_id}",
		Summary: "Remove a member, or leave the workspace when user_id is you (MH-679). Removing someone else needs " +
			"admin/owner; only an owner removes an owner; the last owner cannot go.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		UserID string `path:"user_id"`
	}) (*struct {
		Body struct {
			Members []memberOut `json:"members"`
		}
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		self := in.UserID == s.UserID
		if !self && !authz.Can(s.Role, authz.MembersManage) {
			return nil, huma.Error403Forbidden("only owners and admins can remove members")
		}
		if err := RemoveMember(ctx, a.SysDB, s.WorkspaceID, s.Role, in.UserID, self); err != nil {
			return nil, asHuma(ctx, err, "remove the member")
		}
		if self {
			// They can no longer read the list they just left.
			return &struct {
				Body struct {
					Members []memberOut `json:"members"`
				}
			}{}, nil
		}
		return membersBody(ctx, a.SysDB, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "my-workspace-avatar", Method: http.MethodGet, Path: "/api/me/workspaces/{id}/avatar",
		Summary: "A workspace's picture, for its members (MH-679)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		ID string `path:"id"`
		V  string `query:"v" doc:"Cache-busting version; ignored."`
	}) (*huma.StreamResponse, error) {
		id, err := identity(ctx)
		if err != nil {
			return nil, err
		}
		var contentType string
		var data []byte
		var at time.Time
		// Members only, and a non-member hears the same 404 as a workspace
		// without a picture: whether a workspace exists is not their business.
		err = a.SysDB.QueryRowContext(ctx, `
			SELECT a.content_type, a.data, a.updated_at
			FROM workspace_avatars a
			JOIN workspace_members m ON m.workspace_id = a.workspace_id AND m.user_id = $2 AND m.status = 'active'
			WHERE a.workspace_id = $1`, in.ID, id.ID).Scan(&contentType, &data, &at)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("no picture")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the picture")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", contentType)
			// The URL changes with every upload (?v=), so a long private
			// cache is safe.
			hctx.SetHeader("Cache-Control", "private, max-age=86400")
			hctx.SetHeader("Last-Modified", at.UTC().Format(http.TimeFormat))
			hctx.BodyWriter().Write(data) //nolint:errcheck
		}}, nil
	})
}

func membersBody(ctx context.Context, sys *sql.DB, workspaceID string) (*struct {
	Body struct {
		Members []memberOut `json:"members"`
	}
}, error) {
	list, err := Members(ctx, sys, workspaceID)
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
}
