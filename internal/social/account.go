package social

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"
	"golang.org/x/crypto/bcrypt"

	"github.com/fadymondy/moharrik/internal/platform"
)

/*
The person's own account: the sign-in methods connected to it, and deleting it.

Deleting an account (App Store 5.1.1(v), Play's account-deletion policy) needs a fresh proof that
it is the person: their password, or — for an account with no password, or a person who signed
in with Apple and never set one — a sign-in within the last ten minutes (the web sends them
through Google/Apple again with intent=reauth; the app signs in again). Moharrik sends no system
e-mail, so an e-mailed code is not an option.

Workspaces:
  - one the person owns with nobody else in it is deleted with the account (it is told so first:
    GET /api/me/account/deletion lists them);
  - one where they are the ONLY owner but other people are members refuses the deletion (409):
    deleting it would destroy the other members' work, and silently promoting someone is not ours
    to decide. They transfer ownership (or delete the workspace) first;
  - anywhere else they simply leave.

Rows that must outlive the person: a webhook or collected notification source they created
(webhook_sources / notification_sources.created_by, NOT NULL, ON DELETE RESTRICT) keeps working
for the workspace, so it is handed to a remaining member — an owner first — instead of being
deleted or nulled. Everything else that names a user already cascades or sets null.
*/

const freshFor = 10 * time.Minute

// SignInIdentity is one connected provider account.
type SignInIdentity struct {
	Provider    string    `json:"provider" enum:"google,apple"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	LinkedAt    time.Time `json:"linked_at"`
	LastLoginAt time.Time `json:"last_login_at"`
}

// SignInMethods is how the person can sign in, and what this server offers.
type SignInMethods struct {
	Email       string           `json:"email"`
	HasPassword bool             `json:"has_password"`
	CircleXO    bool             `json:"circlexo" doc:"Signed in with CircleXO at least once (linked)."`
	Identities  []SignInIdentity `json:"identities"`
	Google      bool             `json:"google_available" doc:"Sign in with Google is configured on this server."`
	Apple       bool             `json:"apple_available" doc:"Sign in with Apple is configured on this server (web)."`
}

type signInMethodsOut struct{ Body SignInMethods }

// DeletionWorkspace names a workspace in the deletion preview.
type DeletionWorkspace struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	Members int    `json:"members"`
}

// AccountDeletion previews what deleting the account does.
type AccountDeletion struct {
	Email       string `json:"email"`
	HasPassword bool   `json:"has_password"`
	Fresh       bool   `json:"fresh" doc:"Signed in within the last ten minutes: no password needed to confirm."`
	// Deleted with the account: workspaces where the person is the only member.
	DeleteWorkspaces []DeletionWorkspace `json:"delete_workspaces"`
	// Blocking: workspaces where the person is the only owner and others are members.
	BlockingWorkspaces []DeletionWorkspace `json:"blocking_workspaces"`
}

type accountDeletionOut struct{ Body AccountDeletion }

// DeleteAccountInput confirms a deletion.
type DeleteAccountInput struct {
	Confirm  string `json:"confirm" maxLength:"320" doc:"The account's e-mail address, typed by the person."`
	Password string `json:"password,omitempty" maxLength:"200" doc:"Required unless the session is fresh (signed in within ten minutes)."`
}

// Credentials reads the caller's session/token, to tell how fresh it is. Exported: huma only
// reads headers and cookies from exported embedded structs.
type Credentials struct {
	Authorization string `header:"Authorization"`
	Session       string `cookie:"togo_session"`
}

func (c Credentials) request() *http.Request {
	r := &http.Request{Header: http.Header{}}
	if c.Authorization != "" {
		r.Header.Set("Authorization", c.Authorization)
	}
	if c.Session != "" {
		r.AddCookie(&http.Cookie{Name: auth.SessionCookie, Value: c.Session})
	}
	return r
}

func caller(ctx context.Context) (string, error) {
	id, ok := auth.IdentityFrom(ctx)
	if !ok || id == nil {
		return "", huma.Error401Unauthorized("unauthorized")
	}
	return id.ID, nil
}

// coded is an error with a machine code next to the message: {"error", "code"}.
type coded struct {
	status int
	Msg    string `json:"error"`
	Code   string `json:"code"`
}

func (e *coded) Error() string  { return e.Msg }
func (e *coded) GetStatus() int { return e.status }

func (s *Service) methods(ctx context.Context, userID string) (*SignInMethods, error) {
	m := &SignInMethods{Identities: []SignInIdentity{}}
	var hash string
	if err := s.DB.QueryRowContext(ctx, `SELECT email, password_hash FROM users WHERE id = $1`, userID).Scan(&m.Email, &hash); err != nil {
		return nil, err
	}
	m.HasPassword = hash != "" && hash != "!sso"
	_ = s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM circlexo_user_links WHERE user_id = $1)`, userID).Scan(&m.CircleXO)
	rows, err := s.DB.QueryContext(ctx, `SELECT provider, email, display_name, created_at, last_login_at FROM user_identities WHERE user_id = $1 ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i SignInIdentity
		if err := rows.Scan(&i.Provider, &i.Email, &i.DisplayName, &i.LinkedAt, &i.LastLoginAt); err != nil {
			return nil, err
		}
		m.Identities = append(m.Identities, i)
	}
	c := s.Env()
	m.Google, m.Apple = c.GoogleWeb(), c.AppleWeb()
	return m, rows.Err()
}

// fresh reports whether the caller's credential was issued within freshFor.
func (s *Service) fresh(ctx context.Context, cred Credentials) bool {
	iat, ok := platform.IssuedAt(ctx, s.DB, cred.request())
	return ok && time.Since(iat) <= freshFor
}

func (s *Service) workspacesOf(ctx context.Context, userID string) (del, blocking []DeletionWorkspace, err error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT w.id, w.name, w.slug,
		  (SELECT count(*) FROM workspace_members o WHERE o.workspace_id = w.id) AS members,
		  (SELECT count(*) FROM workspace_members o WHERE o.workspace_id = w.id AND o.role = 'owner' AND o.user_id <> $1) AS owners
		FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.user_id = $1 AND m.role = 'owner'
		ORDER BY w.name`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	del, blocking = []DeletionWorkspace{}, []DeletionWorkspace{}
	for rows.Next() {
		var w DeletionWorkspace
		var owners int
		if err := rows.Scan(&w.ID, &w.Name, &w.Slug, &w.Members, &owners); err != nil {
			return nil, nil, err
		}
		switch {
		case w.Members <= 1:
			del = append(del, w)
		case owners == 0:
			blocking = append(blocking, w)
		}
	}
	return del, blocking, rows.Err()
}

// revokeApple ends the person's Sign in with Apple authorizations (best effort: a failure is
// logged, never a reason to keep an account the person asked to delete).
func (s *Service) revokeApple(ctx context.Context, userID string) {
	rows, err := s.DB.QueryContext(ctx, `SELECT client_id, refresh_token FROM user_identities WHERE user_id = $1 AND provider = 'apple' AND refresh_token <> ''`, userID)
	if err != nil {
		s.Log.Error("social: reading Apple tokens to revoke", "err", err)
		return
	}
	type tok struct{ client, sealed string }
	var list []tok
	for rows.Next() {
		var t tok
		if rows.Scan(&t.client, &t.sealed) == nil {
			list = append(list, t)
		}
	}
	rows.Close()
	c := s.Env()
	for _, t := range list {
		plain, err := appleBox.Open(t.sealed)
		if err == nil {
			err = appleRevoke(ctx, c, t.client, plain)
		}
		if err != nil {
			s.Log.Error("social: revoking the Apple authorization", "err", err)
		}
	}
}

// DeleteUser removes a user and what is only theirs, after the workspace rules were checked.
func (s *Service) DeleteUser(ctx context.Context, userID string, solo []DeletionWorkspace) error {
	for _, w := range solo {
		if err := s.DeleteWorkspace(ctx, w.ID); err != nil {
			return err
		}
	}
	s.revokeApple(ctx, userID)

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	// The sources they created stay with the workspace, under a remaining member (owners first).
	for _, table := range []string{"webhook_sources", "notification_sources"} {
		if _, err := tx.ExecContext(ctx, `
			UPDATE `+table+` src SET created_by = (
			  SELECT m.user_id FROM workspace_members m
			  WHERE m.workspace_id = src.workspace_id AND m.user_id <> $1
			  ORDER BY (m.role = 'owner') DESC, (m.role = 'admin') DESC, m.created_at LIMIT 1)
			WHERE src.created_by = $1 AND EXISTS (
			  SELECT 1 FROM workspace_members m WHERE m.workspace_id = src.workspace_id AND m.user_id <> $1)`, userID); err != nil {
			return err
		}
	}
	// Platform tables keyed by user with no foreign key (some are the auth plugin's own).
	for _, q := range []string{
		`DELETE FROM device_tokens WHERE user_id = $1`,
		`DELETE FROM personal_access_tokens WHERE user_id = $1`,
		`DELETE FROM app_signin_codes WHERE user_id = $1`,
		`DELETE FROM social_login_states WHERE signed_in_user = $1 OR result_user = $1`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	for _, t := range []string{"auth_totp", "auth_pins", "otp_codes"} {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+t).Scan(&exists); err != nil {
			return err
		}
		if exists {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE subject = $1 OR subject = (SELECT email FROM users WHERE id = $1)`, userID); err != nil {
				return err
			}
		}
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	platform.Invalidate(userID)
	return nil
}

// RegisterAccount mounts the account routes (signed-in tier).
func RegisterAccount(api huma.API, svc func() *Service) {
	tags := []string{"Account"}

	huma.Register(api, huma.Operation{OperationID: "my-sign-in-methods", Method: http.MethodGet, Path: "/api/me/identities",
		Summary: "How I can sign in: password, CircleXO, and connected Google/Apple accounts", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*signInMethodsOut, error) {
			uid, err := caller(ctx)
			if err != nil {
				return nil, err
			}
			m, err := svc().methods(ctx, uid)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read your sign-in methods")
			}
			return &signInMethodsOut{Body: *m}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "unlink-my-identity", Method: http.MethodDelete, Path: "/api/me/identities/{provider}",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Disconnect a Google or Apple account from mine (not the last way to sign in)",
		Description: "Refused with code last_method when it is the only way left to sign in (no password, no other provider, " +
			"no CircleXO). Disconnecting Apple also revokes Moharrik's Sign in with Apple authorization at Apple."},
		func(ctx context.Context, in *struct {
			Provider string `path:"provider" doc:"google or apple"`
		}) (*struct{}, error) {
			uid, err := caller(ctx)
			if err != nil {
				return nil, err
			}
			// Checked here rather than with an enum: a validation error echoes the value back.
			if in.Provider != "google" && in.Provider != "apple" {
				return nil, huma.Error404NotFound("that provider is not connected")
			}
			s := svc()
			m, err := s.methods(ctx, uid)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read your sign-in methods")
			}
			found, others := false, 0
			for _, i := range m.Identities {
				if i.Provider == in.Provider {
					found = true
				} else {
					others++
				}
			}
			if !found {
				return nil, huma.Error404NotFound("that provider is not connected")
			}
			if !m.HasPassword && !m.CircleXO && others == 0 {
				return nil, &coded{status: http.StatusConflict, Code: "last_method",
					Msg: "this is the only way you can sign in — set a password or connect another account first"}
			}
			if in.Provider == "apple" {
				s.revokeApple(ctx, uid)
			}
			if _, err := s.DB.ExecContext(ctx, `DELETE FROM user_identities WHERE user_id = $1 AND provider = $2`, uid, in.Provider); err != nil {
				return nil, huma.Error500InternalServerError("could not disconnect it")
			}
			return nil, nil
		})

	huma.Register(api, huma.Operation{OperationID: "my-account-deletion", Method: http.MethodGet, Path: "/api/me/account/deletion",
		Summary: "What deleting my account would do", Tags: tags},
		func(ctx context.Context, in *Credentials) (*accountDeletionOut, error) {
			uid, err := caller(ctx)
			if err != nil {
				return nil, err
			}
			s := svc()
			m, err := s.methods(ctx, uid)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read your account")
			}
			del, blocking, err := s.workspacesOf(ctx, uid)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read your workspaces")
			}
			return &accountDeletionOut{Body: AccountDeletion{Email: m.Email, HasPassword: m.HasPassword, Fresh: s.fresh(ctx, *in),
				DeleteWorkspaces: del, BlockingWorkspaces: blocking}}, nil
		})

	type deleteOut struct {
		SetCookie http.Cookie `header:"Set-Cookie"`
	}
	huma.Register(api, huma.Operation{OperationID: "delete-my-account", Method: http.MethodPost, Path: "/api/me/account/delete",
		DefaultStatus: http.StatusNoContent, Tags: tags,
		Summary: "Delete my account and everything only I have (no undo)",
		Description: "confirm must be the account's e-mail. Needs the password, or a sign-in within the last ten minutes " +
			"(code reauth_required). Refused with code sole_owner while the caller is the only owner of a workspace that has " +
			"other members. Workspaces where the caller is the only member are deleted with the account; Sign in with Apple is " +
			"revoked at Apple; every session, app token and push registration ends."},
		func(ctx context.Context, in *struct {
			Credentials
			Body DeleteAccountInput
		}) (*deleteOut, error) {
			uid, err := caller(ctx)
			if err != nil {
				return nil, err
			}
			s := svc()
			var email, hash string
			if err := s.DB.QueryRowContext(ctx, `SELECT email, password_hash FROM users WHERE id = $1`, uid).Scan(&email, &hash); err != nil {
				return nil, huma.Error404NotFound("account not found")
			}
			if !strings.EqualFold(strings.TrimSpace(in.Body.Confirm), email) {
				return nil, &coded{status: http.StatusUnprocessableEntity, Code: "confirm_mismatch", Msg: "type your e-mail address to confirm"}
			}
			proved := false
			if in.Body.Password != "" {
				if hash == "" || hash == "!sso" || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Body.Password)) != nil {
					return nil, &coded{status: http.StatusForbidden, Code: "wrong_password", Msg: "that password is not right"}
				}
				proved = true
			} else {
				proved = s.fresh(ctx, in.Credentials)
			}
			if !proved {
				return nil, &coded{status: http.StatusForbidden, Code: "reauth_required",
					Msg: "confirm it is you: enter your password, or sign in again and delete within ten minutes"}
			}
			del, blocking, err := s.workspacesOf(ctx, uid)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read your workspaces")
			}
			if len(blocking) > 0 {
				names := make([]string, 0, len(blocking))
				for _, w := range blocking {
					names = append(names, w.Name)
				}
				return nil, &coded{status: http.StatusConflict, Code: "sole_owner",
					Msg: "you are the only owner of " + strings.Join(names, ", ") + " — make someone else an owner or delete the workspace first"}
			}
			if err := s.DeleteUser(ctx, uid, del); err != nil {
				s.Log.Error("social: deleting an account", "err", err, "user", uid)
				if errors.Is(err, ErrWorkspaceRelease) {
					return nil, huma.Error502BadGateway("could not unlink a workspace's WhatsApp number, so nothing was deleted — try again")
				}
				return nil, huma.Error500InternalServerError("could not delete the account")
			}
			s.Log.Info("account deleted", "user", uid)
			return &deleteOut{SetCookie: http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
				Secure: secureCookie(s.Env()), SameSite: http.SameSiteLaxMode}}, nil
		})
}

// ErrWorkspaceRelease wraps a failure to release a workspace's WhatsApp number before deleting it.
var ErrWorkspaceRelease = errors.New("release the workspace's WhatsApp number")
