package google

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Access tokens, for the cards that read Google data (MH-440..447):

	src, err := google.TokenSource(ctx, a, workspaceID, connectionID)
	tok, err := src.Token(ctx, google.ScopeAnalytics)   // "Authorization: Bearer " + tok

Every call re-reads the connection inside its workspace (RLS), so a
connection that was disconnected, marked reauth, or never had the scope is
refused on the next call rather than served from a cache. Access tokens are
cached in memory per connection and keyed by the hash of the refresh token
they came from, so a reconnect never serves an access token of the old grant.
*/

type cached struct {
	refreshHash string
	access      string
	expires     time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
	// One refresh at a time per connection: two concurrent refreshes of a
	// rotating grant would each re-seal a different token, and one would win.
	locks = map[string]*sync.Mutex{}
)

func connLock(id string) *sync.Mutex {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	m, ok := locks[id]
	if !ok {
		m = &sync.Mutex{}
		locks[id] = m
	}
	return m
}

func resetCache() {
	cacheMu.Lock()
	cache = map[string]cached{}
	cacheMu.Unlock()
}

func dropCache(id string) {
	cacheMu.Lock()
	delete(cache, id)
	cacheMu.Unlock()
}

func cacheAccess(id, refresh, access string, expiresIn int) {
	if id == "" || access == "" {
		return
	}
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	cacheMu.Lock()
	cache[id] = cached{refreshHash: hashOf(refresh), access: access, expires: time.Now().Add(time.Duration(expiresIn) * time.Second)}
	cacheMu.Unlock()
}

func fromCache(id, refreshHash string) (string, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	c, ok := cache[id]
	if !ok || c.refreshHash != refreshHash || time.Now().After(c.expires.Add(-time.Minute)) {
		return "", false
	}
	return c.access, true
}

// Source hands out access tokens for one connection in one workspace.
type Source struct {
	a           *app.App
	workspaceID string
	id          string
}

// TokenSource returns the token source for a connection. It fails with
// ErrNotConnected when the connection is not in that workspace.
func TokenSource(ctx context.Context, a *app.App, workspaceID, connectionID string) (*Source, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	var exists bool
	if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, workspaceID),
		`SELECT EXISTS (SELECT 1 FROM google_connections WHERE workspace_id = $1 AND id = $2)`,
		workspaceID, connectionID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotConnected
	}
	return &Source{a: a, workspaceID: workspaceID, id: connectionID}, nil
}

// Token returns a live access token, refreshing it when needed. need lists the
// scopes the caller is about to use; a connection that was not granted all of
// them is refused with ErrScope. A grant Google refuses marks the connection
// reauth and returns ErrReauth.
func (s *Source) Token(ctx context.Context, need ...string) (string, error) {
	lock := connLock(s.id)
	lock.Lock()
	defer lock.Unlock()

	wctx := tenant.WithWorkspace(ctx, s.workspaceID)
	var sealed, status string
	var granted pgtext.Array
	err := s.a.SQLDB.QueryRowContext(wctx, `
		SELECT refresh_token_sealed, granted_scopes, status
		FROM google_connections WHERE workspace_id = $1 AND id = $2`,
		s.workspaceID, s.id).Scan(&sealed, &granted, &status)
	if errors.Is(err, sql.ErrNoRows) {
		dropCache(s.id)
		return "", ErrNotConnected
	}
	if err != nil {
		return "", err
	}
	if status == "reauth" {
		dropCache(s.id)
		return "", ErrReauth
	}
	if !hasAll(granted, need...) {
		return "", ErrScope
	}
	refresh, err := refreshBox.Open(sealed)
	if err != nil {
		return "", err
	}
	if tok, ok := fromCache(s.id, hashOf(refresh)); ok {
		return tok, nil
	}

	cfg := LoadConfig()
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return "", ErrNotConfigured
	}
	out, err := tokenCall(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
	})
	if err != nil {
		var te *tokenError
		if errors.As(err, &te) && (te.Code == "invalid_grant" || te.Status == http.StatusUnauthorized) {
			// The error code only: never the request, which held the token.
			_, _ = s.a.SQLDB.ExecContext(wctx, `
				UPDATE google_connections SET status = 'reauth', auth_error = $3, updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, s.workspaceID, s.id, te.Error())
			dropCache(s.id)
			return "", ErrReauth
		}
		return "", err
	}
	if out.RefreshToken != "" && out.RefreshToken != refresh {
		// Rotated: the old one is now dead, so the new one must be kept.
		resealed, err := refreshBox.Seal(out.RefreshToken)
		if err != nil {
			return "", err
		}
		if _, err := s.a.SQLDB.ExecContext(wctx, `
			UPDATE google_connections SET refresh_token_sealed = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, s.workspaceID, s.id, resealed); err != nil {
			return "", fmt.Errorf("google: the rotated grant was not stored: %w", err)
		}
		refresh = out.RefreshToken
	}
	cacheAccess(s.id, refresh, out.AccessToken, out.ExpiresIn)
	return out.AccessToken, nil
}

// ForgetAccessTokens empties the in-memory access-token cache, so the next
// Token call refreshes. For tests, and for an operator who has just rotated
// the OAuth client.
func ForgetAccessTokens() { resetCache() }
