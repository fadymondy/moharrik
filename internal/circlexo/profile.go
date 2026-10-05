package circlexo

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	circlexo "github.com/circlexo/circlexo-go"
)

/*
Profile sync (MH-1149). The hub owns who a person is. Moharrik keeps no name or picture of its
own (people are shown by address), so it copies the hub's address onto the account and keeps the
hub's display name, picture URL and locale on the link (circlexo_user_links), on every CircleXO
sign-in (from the ID token) and whenever the hub sends user.updated. A new address is taken only
when the hub says it is verified and no other Moharrik account holds it.
*/

// SyncProfile overwrites userID's copy of their hub profile. Failures are logged: a sign-in never
// fails because of it.
func (s *Service) SyncProfile(ctx context.Context, userID string, p circlexo.Profile) {
	name := strings.TrimSpace(p.Name)
	if len([]rune(name)) > 120 {
		name = string([]rune(name)[:120])
	}
	picture := ""
	if strings.HasPrefix(p.Picture, "https://") && len(p.Picture) <= 2048 {
		picture = p.Picture
	}
	if _, err := s.DB.ExecContext(ctx, `
		UPDATE circlexo_user_links SET display_name = $2, picture_url = $3, locale = $4 WHERE user_id = $1`,
		userID, name, picture, strings.ToLower(p.Locale)); err != nil {
		s.Log.Warn("circlexo: syncing the profile", "user", userID, "err", err)
	}
	if email := norm(p.Email); email != "" && p.EmailVerified {
		s.syncEmail(ctx, userID, email)
	}
}

// syncEmail moves the account to the hub's address unless another account holds it.
func (s *Service) syncEmail(ctx context.Context, userID, email string) {
	var cur string
	if err := s.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id = $1`, userID).Scan(&cur); err != nil || strings.EqualFold(cur, email) {
		return
	}
	var other string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = $1 AND id <> $2`, email, userID).Scan(&other)
	if err == nil {
		s.Log.Warn("circlexo: the hub's address belongs to another Moharrik account; keeping the old one", "user", userID)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET email = $2 WHERE id = $1`, userID, email); err != nil {
		s.Log.Warn("circlexo: syncing the address", "user", userID, "err", err)
		return
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE circlexo_user_links SET email = $2 WHERE user_id = $1`, userID, email)
}
