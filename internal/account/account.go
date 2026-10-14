package account

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/platform"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

// The account page: what the account is signed in with, and deleting it —
// from the web and from inside the apps (App Store Review Guideline 5.1.1(v)).

// Identity is one provider sign-in linked to the account.
type Identity struct {
	Provider  string    `json:"provider" enum:"google,apple"`
	Email     string    `json:"email" doc:"The address the provider vouched for (Apple's relay address for \"Hide My Email\")."`
	CreatedAt time.Time `json:"created_at"`
}

// BlockingWorkspace is a workspace the person is the only owner of while
// others are still members: it needs another owner before the account can go.
type BlockingWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Account is GET /api/me/account.
type Account struct {
	Email              string              `json:"email"`
	EmailVerified      bool                `json:"email_verified"`
	HasPassword        bool                `json:"has_password" doc:"False for an account made with Google or Apple: \"Forgot password?\" sets one."`
	Identities         []Identity          `json:"identities"`
	BlockingWorkspaces []BlockingWorkspace `json:"blocking_workspaces" doc:"Deleting the account is refused (409 sole_owner) until each has another owner."`
}

// membership is one workspace of the person, as deleting the account sees it.
type membership struct {
	id, name, role string
	otherOwners    int
	others         int
}

func memberships(ctx context.Context, s *Service, userID string) ([]membership, error) {
	rows, err := s.a.SysDB.QueryContext(ctx, `
		SELECT w.id, w.name, m.role,
		       (SELECT count(*) FROM workspace_members o WHERE o.workspace_id = w.id AND o.user_id <> $1 AND o.role = 'owner' AND o.status = 'active'),
		       (SELECT count(*) FROM workspace_members o WHERE o.workspace_id = w.id AND o.user_id <> $1)
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 ORDER BY w.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []membership
	for rows.Next() {
		var m membership
		if err := rows.Scan(&m.id, &m.name, &m.role, &m.otherOwners, &m.others); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// blocks reports whether leaving m would leave its other members with no owner.
func (m membership) blocks() bool { return m.role == "owner" && m.otherOwners == 0 && m.others > 0 }

func registerAccount(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{OperationID: "me-account", Method: http.MethodGet, Path: "/api/me/account",
		Summary: "My account: sign-in methods, and what stops deleting it", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*body[Account], error) {
			s := get()
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			if s == nil {
				return nil, huma.Error503ServiceUnavailable("not configured")
			}
			acc := Account{Identities: []Identity{}, BlockingWorkspaces: []BlockingWorkspace{}}
			var hash string
			var verified *time.Time
			if err := s.a.SysDB.QueryRowContext(ctx, `
				SELECT u.email, u.password_hash, p.email_verified_at
				FROM users u LEFT JOIN platform_users p ON p.user_id = u.id WHERE u.id = $1`, id.ID).
				Scan(&acc.Email, &hash, &verified); err != nil {
				return nil, statusOr(a, err, "load the account")
			}
			acc.EmailVerified, acc.HasPassword = verified != nil, hash != "" && hash != noPassword
			rows, err := s.a.SysDB.QueryContext(ctx,
				`SELECT provider, email, created_at FROM user_identities WHERE user_id = $1 ORDER BY created_at`, id.ID)
			if err != nil {
				return nil, statusOr(a, err, "load the account")
			}
			defer rows.Close()
			for rows.Next() {
				var i Identity
				if err := rows.Scan(&i.Provider, &i.Email, &i.CreatedAt); err != nil {
					return nil, statusOr(a, err, "load the account")
				}
				acc.Identities = append(acc.Identities, i)
			}
			ms, err := memberships(ctx, s, id.ID)
			if err != nil {
				return nil, statusOr(a, err, "load the account")
			}
			for _, m := range ms {
				if m.blocks() {
					acc.BlockingWorkspaces = append(acc.BlockingWorkspaces, BlockingWorkspace{ID: m.id, Name: m.name})
				}
			}
			return out(acc), nil
		})

	huma.Register(api, huma.Operation{OperationID: "me-account-delete", Method: http.MethodPost, Path: "/api/me/account/delete",
		DefaultStatus: http.StatusNoContent, Summary: "Delete my account", Tags: tags,
		Description: "Deletes the account for good. Workspaces where the person is the only member are deleted with it; " +
			"the others lose the member. 409 sole_owner while a workspace with other members has no other owner " +
			"(GET /api/me/account lists them). Apple's sign-in tokens are revoked, so the app leaves the person's " +
			"\"Sign in with Apple\" list. Signs this browser out."},
		func(ctx context.Context, in *struct {
			Body struct {
				Confirm string `json:"confirm" enum:"delete" doc:"The word \"delete\", so a stray request cannot delete an account."`
			}
		}) (*struct {
			SetCookie http.Cookie `header:"Set-Cookie"`
		}, error) {
			s := get()
			id, err := identity(ctx)
			if err != nil {
				return nil, err
			}
			if s == nil {
				return nil, huma.Error503ServiceUnavailable("not configured")
			}
			if err := s.deleteAccount(ctx, id.ID); err != nil {
				return nil, statusOr(a, err, "delete the account")
			}
			return &struct {
				SetCookie http.Cookie `header:"Set-Cookie"`
			}{http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode}}, nil
		})
}

// personalTables hold rows of one person with no foreign key to users, so
// deleting the users row would leave them behind.
var personalTables = []string{
	"device_tokens", "app_signin_codes", "personal_access_tokens", "push_subscriptions", "push_prefs",
	"member_whatsapp_messages", "member_whatsapp_events", "member_whatsapp", "member_trend_digest",
	"trend_digest_sends", "google_oauth_states",
}

func (s *Service) deleteAccount(ctx context.Context, userID string) error {
	db := s.a.SysDB
	var email string
	if err := db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		return err
	}
	ms, err := memberships(ctx, s, userID)
	if err != nil {
		return err
	}
	for _, m := range ms {
		if m.blocks() {
			return huma.Error409Conflict("sole_owner")
		}
	}
	for _, m := range ms {
		if m.others == 0 {
			err = workspaces.Delete(ctx, db, m.id)
		} else {
			err = workspaces.RemoveMember(ctx, db, m.id, m.role, userID, true)
		}
		if err != nil {
			return err
		}
	}

	// Apple first: once the row is gone so is the token. A failure is logged,
	// not fatal — the person asked to be deleted, and Apple's side expires.
	s.revokeAppleTokens(ctx, userID)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Webhook and notification sources outlive the member who made them (their
	// foreign key refuses the delete): another member of the workspace, an
	// owner first, takes them over.
	for _, t := range []string{"webhook_sources", "notification_sources"} {
		if _, err := tx.ExecContext(ctx, `
			UPDATE `+t+` x SET created_by = (
				SELECT m.user_id FROM workspace_members m WHERE m.workspace_id = x.workspace_id AND m.user_id <> $1
				ORDER BY (m.role = 'owner') DESC, m.id LIMIT 1)
			WHERE created_by = $1`, userID); err != nil {
			return err
		}
	}
	for _, t := range personalTables {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE user_id = $1`, userID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		return err
	}
	if err := platform.Audit(ctx, tx, platform.Entry{ActorID: userID, ActorEmail: email, Action: "account.deleted",
		TargetType: "user", TargetID: userID, TargetLabel: email, IP: clientip.From(ctx), Via: "rest"}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	platform.Invalidate(userID)
	return nil
}

func (s *Service) revokeAppleTokens(ctx context.Context, userID string) {
	rows, err := s.a.SysDB.QueryContext(ctx,
		`SELECT refresh_token, client_id FROM user_identities WHERE user_id = $1 AND provider = 'apple' AND refresh_token <> ''`, userID)
	if err != nil {
		s.a.Log.Error("account: read Apple tokens", "err", err)
		return
	}
	type tok struct{ sealed, client string }
	var toks []tok
	for rows.Next() {
		var t tok
		if err := rows.Scan(&t.sealed, &t.client); err == nil {
			toks = append(toks, t)
		}
	}
	rows.Close()
	if len(toks) > 0 && s.p.Apple == nil {
		s.a.Log.Warn("account: Apple is not configured, so its tokens cannot be revoked", "user", userID)
		return
	}
	for _, t := range toks {
		raw, err := refreshBox.Open(t.sealed)
		if err == nil {
			err = revokeApple(ctx, s.p.Apple, t.client, raw)
		}
		if err != nil {
			s.a.Log.Warn("account: could not revoke an Apple token", "user", userID, "err", err)
		}
	}
}
