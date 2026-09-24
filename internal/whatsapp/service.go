package whatsapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Link is one workspace's WhatsApp connection.
type Link struct {
	WorkspaceID string
	Instance    string
	HookID      string
	Token       string
	State       string
	Phone       string
	LastError   string
	LinkedAt    *time.Time
}

// ErrNoLink: this workspace has not linked a number.
var ErrNoLink = errors.New("this workspace has no WhatsApp number linked")

// InstanceFor is the Evolution instance name for a workspace. Derived from the
// workspace id rather than chosen by anyone: an instance name is what
// Evolution routes by, so two workspaces choosing the same one would put one
// workspace's messages in the other's inbox.
func InstanceFor(workspaceID string) string {
	return "moh-" + strings.ReplaceAll(workspaceID, "-", "")
}

func newSecret(prefix string) string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		panic("whatsapp: no entropy: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

// Load reads a workspace's link, unsealing its webhook token.
func Load(ctx context.Context, a *app.App, workspaceID string) (*Link, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var l Link
	var sealed string
	var linkedAt sql.NullTime
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT instance, hook_id, token_sealed, state, phone, last_error, linked_at
		FROM whatsapp_links WHERE workspace_id = $1`, workspaceID,
	).Scan(&l.Instance, &l.HookID, &sealed, &l.State, &l.Phone, &l.LastError, &linkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoLink
	}
	if err != nil {
		return nil, err
	}
	if l.Token, err = unseal(sealed); err != nil {
		return nil, err
	}
	l.WorkspaceID = workspaceID
	if linkedAt.Valid {
		t := linkedAt.Time
		l.LinkedAt = &t
	}
	return &l, nil
}

// Ensure returns the workspace's link, creating the row (instance name, hook
// id, webhook token) the first time. It does NOT talk to Evolution: that is
// the caller's next step, and keeping them apart means a failed call to
// Evolution does not leave a half-written row behind.
func Ensure(ctx context.Context, a *app.App, workspaceID, userID string) (*Link, error) {
	if l, err := Load(ctx, a, workspaceID); err == nil {
		return l, nil
	} else if !errors.Is(err, ErrNoLink) {
		return nil, err
	}

	token := newSecret("whk_")
	sealed, err := seal(token)
	if err != nil {
		return nil, err
	}
	hookID := newSecret("wh_")
	instance := InstanceFor(workspaceID)

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	if _, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO whatsapp_links (workspace_id, instance, hook_id, token_sealed, created_by)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id) DO NOTHING`,
		workspaceID, instance, hookID, sealed, userID); err != nil {
		return nil, err
	}
	return Load(ctx, a, workspaceID)
}

// ByHookID resolves a webhook delivery to its workspace.
//
// On the OWNER pool, because the caller is Evolution: it has no session and no
// workspace, and the unguessable id in the URL is what identifies the link.
// The predicate is explicit, and only the row for that exact id comes back.
func ByHookID(ctx context.Context, a *app.App, hookID string) (*Link, error) {
	var l Link
	var sealed string
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT workspace_id, instance, hook_id, token_sealed, state, phone, linked_at
		FROM whatsapp_links WHERE hook_id = $1`, strings.TrimSpace(hookID),
	).Scan(&l.WorkspaceID, &l.Instance, &l.HookID, &sealed, &l.State, &l.Phone, &l.LinkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoLink
	}
	if err != nil {
		return nil, err
	}
	if l.Token, err = unseal(sealed); err != nil {
		return nil, err
	}
	return &l, nil
}

// SetState records what Evolution last said about the connection.
func SetState(ctx context.Context, a *app.App, workspaceID, state, phone string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	linked := "linked_at"
	if state == "open" {
		linked = "COALESCE(linked_at, now())"
	}
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE whatsapp_links SET state = $2,
		    phone = CASE WHEN $3 = '' THEN phone ELSE $3 END,
		    linked_at = `+linked+`,
		    last_error = CASE WHEN $2 = 'open' THEN '' ELSE last_error END,
		    updated_at = now()
		WHERE workspace_id = $1`, workspaceID, state, Number(phone))
}

// SetError records why a link is not working, so the page can say it rather
// than showing a number that quietly stopped receiving.
func SetError(ctx context.Context, a *app.App, workspaceID string, err error) {
	if err == nil {
		return
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE whatsapp_links SET last_error = $2, last_error_at = now(), updated_at = now()
		WHERE workspace_id = $1`, workspaceID, clip(err.Error(), 300))
}

// Unlink removes the row. The caller deletes the Evolution instance first:
// dropping our row while the instance still holds a phone session would leave
// a linked number nobody can see or unlink.
func Unlink(ctx context.Context, a *app.App, workspaceID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `DELETE FROM whatsapp_links WHERE workspace_id = $1`, workspaceID)
	return err
}

// ClientFor builds the Evolution client for one workspace's instance.
func ClientFor(cfg Config, l *Link) *Client {
	return &Client{Cfg: cfg, Instance: l.Instance}
}
