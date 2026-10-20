package whatsapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
A workspace links one or more WhatsApp numbers. Each one is a whatsapp_links
row with its own Evolution instance, webhook (hook id + token), connection
state and pairing, so linking, re-pairing, losing or unlinking one number
never touches another.

Which number a send goes from is carried on the context (WithLink): the hook
puts the number a message arrived on there before it files it, the inbox puts
the thread's number there before it replies, and a caller that picks a sender
(a new message, a campaign, an agent's from_number) puts that one there.
Nothing on the context means the workspace's default number, which is what
every caller written before there could be two numbers gets.
*/

// Link is one linked WhatsApp number.
type Link struct {
	ID          string
	WorkspaceID string
	Label       string
	IsDefault   bool
	// DailyCap is the most messages this number sends in a day; 0 = no cap.
	DailyCap  int
	Instance  string
	HookID    string
	Token     string
	State     string
	Phone     string
	LastError string
	LinkedAt  *time.Time
	CreatedAt time.Time
}

// ErrNoLink: this workspace has not linked a number (or not the one asked for).
var ErrNoLink = errors.New("this workspace has no WhatsApp number linked")

type linkKey struct{}

// WithLink makes every send, lookup and state change below act on one linked
// number. "" leaves the context as it is (the default number).
func WithLink(ctx context.Context, linkID string) context.Context {
	if linkID = strings.TrimSpace(linkID); linkID == "" {
		return ctx
	}
	return context.WithValue(ctx, linkKey{}, linkID)
}

// LinkFrom is the number WithLink picked, or "".
func LinkFrom(ctx context.Context) string {
	id, _ := ctx.Value(linkKey{}).(string)
	return id
}

// InstanceFor is the Evolution instance name of a workspace's first number.
// Derived from the workspace id rather than chosen by anyone: an instance
// name is what Evolution routes by, so two workspaces choosing the same one
// would put one workspace's messages in the other's inbox. A further number's
// instance adds its own row id (instanceForLink).
func InstanceFor(workspaceID string) string {
	return "moh-" + strings.ReplaceAll(workspaceID, "-", "")
}

func instanceForLink(workspaceID, linkID string) string {
	id := strings.ReplaceAll(linkID, "-", "")
	if len(id) > 12 {
		id = id[:12]
	}
	return InstanceFor(workspaceID) + "-" + id
}

func newSecret(prefix string) string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		panic("whatsapp: no entropy: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

const linkCols = `id, workspace_id, label, is_default, daily_cap, instance, hook_id, token_sealed,
	state, phone, last_error, linked_at, created_at`

type rowScanner interface{ Scan(...any) error }

func scanLink(r rowScanner) (*Link, error) {
	var l Link
	var sealed string
	var linkedAt sql.NullTime
	err := r.Scan(&l.ID, &l.WorkspaceID, &l.Label, &l.IsDefault, &l.DailyCap, &l.Instance, &l.HookID, &sealed,
		&l.State, &l.Phone, &l.LastError, &linkedAt, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoLink
	}
	if err != nil {
		return nil, err
	}
	if l.Token, err = unseal(sealed); err != nil {
		return nil, err
	}
	if linkedAt.Valid {
		t := linkedAt.Time
		l.LinkedAt = &t
	}
	return &l, nil
}

// Load reads the number the context picked (WithLink), else the workspace's
// default number, unsealing its webhook token. A picked number that is not
// linked here is ErrNoLink, never quietly another number.
func Load(ctx context.Context, a *app.App, workspaceID string) (*Link, error) {
	if id := LinkFrom(ctx); id != "" {
		return LoadByID(ctx, a, workspaceID, id)
	}
	return LoadDefault(ctx, a, workspaceID)
}

// LoadDefault reads the workspace's default number (the oldest, should no row
// carry the flag).
func LoadDefault(ctx context.Context, a *app.App, workspaceID string) (*Link, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	return scanLink(a.SQLDB.QueryRowContext(sctx, `
		SELECT `+linkCols+` FROM whatsapp_links WHERE workspace_id = $1
		ORDER BY is_default DESC, created_at, id LIMIT 1`, workspaceID))
}

// LoadByID reads one of the workspace's numbers.
func LoadByID(ctx context.Context, a *app.App, workspaceID, linkID string) (*Link, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	return scanLink(a.SQLDB.QueryRowContext(sctx, `
		SELECT `+linkCols+` FROM whatsapp_links WHERE workspace_id = $1 AND id = $2`,
		workspaceID, strings.TrimSpace(linkID)))
}

// List is every number the workspace has linked, the default first.
func List(ctx context.Context, a *app.App, workspaceID string) ([]*Link, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT `+linkCols+` FROM whatsapp_links WHERE workspace_id = $1
		ORDER BY is_default DESC, created_at, id`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Resolve finds the sender a caller named: a link id, the number itself (any
// formatting; digits are compared) or its label. "" is the default number.
func Resolve(ctx context.Context, a *app.App, workspaceID, ref string) (*Link, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return LoadDefault(ctx, a, workspaceID)
	}
	links, err := List(ctx, a, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.ID == ref {
			return l, nil
		}
	}
	if digits := Number(ref); len(digits) >= 8 {
		for _, l := range links {
			if l.Phone != "" && SamePhone(l.Phone, digits) {
				return l, nil
			}
		}
	}
	for _, l := range links {
		if l.Label != "" && strings.EqualFold(l.Label, ref) {
			return l, nil
		}
	}
	return nil, ErrNoLink
}

// Sender is ctx set to send from the number ref names (its id, its phone
// number or its label; see Resolve). An empty ref leaves ctx as it is, so the
// usual choice stands: the thread's number for a reply, else the default.
func Sender(ctx context.Context, a *app.App, ref string) (context.Context, error) {
	if strings.TrimSpace(ref) == "" {
		return ctx, nil
	}
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	l, err := Resolve(ctx, a, s.WorkspaceID, ref)
	if errors.Is(err, ErrNoLink) {
		return nil, huma.Error404NotFound("no WhatsApp number in this workspace matches " + strings.TrimSpace(ref))
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the WhatsApp numbers")
	}
	return WithLink(ctx, l.ID), nil
}

// SamePhone compares two digit strings, tolerating a national form on one
// side (01012345678 vs 201012345678).
func SamePhone(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) < 8 || len(b) < 8 {
		return false
	}
	ta, tb := strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	return strings.HasSuffix(ta, tb) || strings.HasSuffix(tb, ta)
}

// Ensure returns the workspace's default number, creating the first row
// (instance name, hook id, webhook token) when there is none. It does NOT talk
// to Evolution: that is the caller's next step, and keeping them apart means a
// failed call to Evolution does not leave a half-written row behind.
func Ensure(ctx context.Context, a *app.App, workspaceID, userID string) (*Link, error) {
	if l, err := LoadDefault(ctx, a, workspaceID); err == nil {
		return l, nil
	} else if !errors.Is(err, ErrNoLink) {
		return nil, err
	}
	return Create(ctx, a, workspaceID, userID, "")
}

// Create adds a number to the workspace: a new row, with its own instance,
// hook id and webhook token. The first number in a workspace keeps the
// instance name a single-number workspace always had, and becomes the
// default; adding any other number leaves every existing row as it was.
func Create(ctx context.Context, a *app.App, workspaceID, userID, label string) (*Link, error) {
	token := newSecret("whk_")
	sealed, err := seal(token)
	if err != nil {
		return nil, err
	}
	hookID := newSecret("wh_")

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var id string
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		return nil, err
	}
	instance := InstanceFor(workspaceID)
	var taken bool
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $1 AND instance = $2)`,
		workspaceID, instance).Scan(&taken); err != nil {
		return nil, err
	}
	if taken {
		instance = instanceForLink(workspaceID, id)
	}
	insert := func(asDefault bool) error {
		_, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO whatsapp_links (id, workspace_id, label, is_default, instance, hook_id, token_sealed, created_by)
			VALUES ($1, $2, $3, $4 AND NOT EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $2 AND is_default),
			        $5, $6, $7, $8)`,
			id, workspaceID, clip(strings.TrimSpace(label), 60), asDefault, instance, hookID, sealed, userID)
		return err
	}
	if err := insert(true); err != nil {
		// Two numbers added at the same moment can both find no default; the
		// unique index lets one win and the other is simply not the default.
		if !strings.Contains(err.Error(), "whatsapp_links_default_uniq") {
			return nil, err
		}
		if err := insert(false); err != nil {
			return nil, err
		}
	}
	return LoadByID(ctx, a, workspaceID, id)
}

// Update changes a number's label and/or daily cap (nil leaves it).
func Update(ctx context.Context, a *app.App, workspaceID, linkID string, label *string, dailyCap *int) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var lbl sql.NullString
	if label != nil {
		lbl = sql.NullString{String: clip(strings.TrimSpace(*label), 60), Valid: true}
	}
	var dc sql.NullInt64
	if dailyCap != nil {
		dc = sql.NullInt64{Int64: int64(max(*dailyCap, 0)), Valid: true}
	}
	res, err := a.SQLDB.ExecContext(sctx, `
		UPDATE whatsapp_links SET label = COALESCE($3, label), daily_cap = COALESCE($4, daily_cap), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, linkID, lbl, dc)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoLink
	}
	return nil
}

// SetDefault makes one number the workspace's default sender.
func SetDefault(ctx context.Context, a *app.App, workspaceID, linkID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	tx, err := a.SQLDB.BeginTx(sctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(sctx, `SELECT EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $1 AND id = $2)`,
		workspaceID, linkID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNoLink
	}
	if _, err := tx.ExecContext(sctx, `UPDATE whatsapp_links SET is_default = false, updated_at = now()
		WHERE workspace_id = $1 AND is_default AND id <> $2`, workspaceID, linkID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(sctx, `UPDATE whatsapp_links SET is_default = true, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, linkID); err != nil {
		return err
	}
	return tx.Commit()
}

// ByHookID resolves a webhook delivery to its number (and so its workspace).
//
// On the OWNER pool, because the caller is Evolution: it has no session and no
// workspace, and the unguessable id in the URL is what identifies the link.
// The predicate is explicit, and only the row for that exact id comes back.
func ByHookID(ctx context.Context, a *app.App, hookID string) (*Link, error) {
	return scanLink(a.SysDB.QueryRowContext(ctx, `
		SELECT `+linkCols+` FROM whatsapp_links WHERE hook_id = $1`, strings.TrimSpace(hookID)))
}

// SetState records what Evolution last said about the number the context
// picked, else the default one. See SetLinkState.
func SetState(ctx context.Context, a *app.App, workspaceID, state, phone string) {
	l, err := Load(ctx, a, workspaceID)
	if err != nil {
		return
	}
	SetLinkState(ctx, a, workspaceID, l.ID, state, phone)
}

// SetLinkState records what Evolution last said about one number's connection.
func SetLinkState(ctx context.Context, a *app.App, workspaceID, linkID, state, phone string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	linked := "linked_at"
	if state == "open" {
		linked = "COALESCE(linked_at, now())"
	}
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE whatsapp_links SET state = $3,
		    phone = CASE WHEN $4 = '' THEN phone ELSE $4 END,
		    linked_at = `+linked+`,
		    last_error = CASE WHEN $3 = 'open' THEN '' ELSE last_error END,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, linkID, state, Number(phone))
}

// SetError records why a number is not working, so the page can say it rather
// than showing a number that quietly stopped receiving.
func SetError(ctx context.Context, a *app.App, workspaceID, linkID string, err error) {
	if err == nil {
		return
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE whatsapp_links SET last_error = $3, last_error_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, linkID, clip(err.Error(), 300))
}

// UnlinkLink removes one number's row. The caller deletes its Evolution
// instance first: dropping our row while the instance still holds a phone
// session would leave a linked number nobody can see or unlink. When the
// default goes, the oldest remaining number becomes the default.
func UnlinkLink(ctx context.Context, a *app.App, workspaceID, linkID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	if _, err := a.SQLDB.ExecContext(sctx, `DELETE FROM whatsapp_links WHERE workspace_id = $1 AND id = $2`,
		workspaceID, linkID); err != nil {
		return err
	}
	_, err := a.SQLDB.ExecContext(sctx, `
		UPDATE whatsapp_links SET is_default = true, updated_at = now()
		WHERE id = (SELECT id FROM whatsapp_links WHERE workspace_id = $1 ORDER BY created_at, id LIMIT 1)
		  AND NOT EXISTS (SELECT 1 FROM whatsapp_links WHERE workspace_id = $1 AND is_default)`, workspaceID)
	return err
}

// Unlink removes every number's row (the workspace is going). The caller
// deletes the Evolution instances first, as for UnlinkLink.
func Unlink(ctx context.Context, a *app.App, workspaceID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `DELETE FROM whatsapp_links WHERE workspace_id = $1`, workspaceID)
	return err
}

// ClientFor builds the Evolution client for one number's instance.
func ClientFor(cfg Config, l *Link) *Client {
	return &Client{Cfg: cfg, Instance: l.Instance}
}
