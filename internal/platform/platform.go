// Package platform holds platform-level (cross-tenant) account state that the request guard, the
// admin MCP and the superadmin panel share (MH-1184): whether a user is a platform admin,
// suspended, or had their sessions ended; when they were last seen; and the admin audit trail.
//
// Everything runs on the owner pool: `users`, `platform_users` and `admin_audit` have no grant
// for the app role.
package platform

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// AdminRole is the entry in the auth plugin's CSV users.roles that makes a platform admin.
const AdminRole = "admin"

// IsAdmin reads the auth plugin's CSV roles column.
func IsAdmin(csv string) bool {
	for _, r := range strings.Split(csv, ",") {
		if strings.TrimSpace(r) == AdminRole {
			return true
		}
	}
	return false
}

// User is the platform view of an account.
type User struct {
	ID                 string
	Email              string
	Admin              bool
	SuspendedAt        *time.Time
	SuspendedReason    string
	SessionsValidAfter *time.Time
}

// Suspended reports whether the account is suspended.
func (u *User) Suspended() bool { return u != nil && u.SuspendedAt != nil }

// SuspendedMessage is what a suspended account is told.
func (u *User) SuspendedMessage() string {
	msg := "Your account is suspended"
	if r := strings.TrimSpace(u.SuspendedReason); r != "" {
		msg += ": " + r
	}
	return msg + ". Contact support to restore access."
}

const userTTL = 10 * time.Second

type cachedUser struct {
	u  *User
	at time.Time
}

var (
	mu    sync.Mutex
	users = map[string]cachedUser{}
	seen  = map[string]time.Time{}
)

// Invalidate drops the cached state of a user (after an admin changes it).
func Invalidate(userID string) {
	mu.Lock()
	delete(users, userID)
	mu.Unlock()
}

// ErrNoUser is an id that names no account.
var ErrNoUser = errors.New("user not found")

// Load reads a user's platform state, cached for a few seconds; a change made in this process
// invalidates it at once.
func Load(ctx context.Context, db *sql.DB, userID string) (*User, error) {
	mu.Lock()
	c, ok := users[userID]
	mu.Unlock()
	if ok && time.Since(c.at) < userTTL {
		return c.u, nil
	}
	u := &User{ID: userID}
	var roles string
	var reason sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT u.email, u.roles, p.suspended_at, p.suspended_reason, p.sessions_valid_after
		FROM users u LEFT JOIN platform_users p ON p.user_id = u.id WHERE u.id = $1`, userID,
	).Scan(&u.Email, &roles, &u.SuspendedAt, &reason, &u.SessionsValidAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	u.Admin = IsAdmin(roles)
	u.SuspendedReason = reason.String
	mu.Lock()
	users[userID] = cachedUser{u: u, at: time.Now()}
	mu.Unlock()
	return u, nil
}

// SuspendedByEmail reports a suspended account by its email (the sign-in pre-check).
func SuspendedByEmail(ctx context.Context, db *sql.DB, email string) (*User, bool) {
	var id string
	if err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE lower(email) = lower($1)`, strings.TrimSpace(email)).Scan(&id); err != nil {
		return nil, false
	}
	u, err := Load(ctx, db, id)
	if err != nil || !u.Suspended() {
		return nil, false
	}
	return u, true
}

// TouchSeen records activity for the "active users" numbers, at most once per five minutes per
// user per process.
func TouchSeen(ctx context.Context, db *sql.DB, userID string) {
	mu.Lock()
	last, ok := seen[userID]
	if ok && time.Since(last) < 5*time.Minute {
		mu.Unlock()
		return
	}
	seen[userID] = time.Now()
	mu.Unlock()
	_, _ = db.ExecContext(ctx, `
		INSERT INTO platform_users (user_id, last_seen_at) VALUES ($1, now())
		ON CONFLICT (user_id) DO UPDATE SET last_seen_at = now()`, userID)
}

// IssuedAt returns when the session token on r was issued (the JWT's iat). The auth plugin has
// already verified the token; this only reads its claims. With a server-side session driver the
// cookie holds a session id, resolved through auth_sessions.
func IssuedAt(ctx context.Context, db *sql.DB, r *http.Request) (time.Time, bool) {
	tok := ""
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		tok = strings.TrimSpace(h[7:])
	} else if c, err := r.Cookie("togo_session"); err == nil {
		tok = c.Value
	}
	if tok == "" {
		return time.Time{}, false
	}
	if strings.Count(tok, ".") != 2 && db != nil {
		var jwt string
		if db.QueryRowContext(ctx, `SELECT token FROM auth_sessions WHERE sid = $1`, tok).Scan(&jwt) != nil {
			return time.Time{}, false
		}
		tok = jwt
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Iat float64 `json:"iat"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Iat == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Iat), 0), true
}

// SessionRevoked reports whether r's session was issued before the user's sessions were ended.
// A session with no readable issue time is refused once that has happened.
func SessionRevoked(ctx context.Context, db *sql.DB, u *User, r *http.Request) bool {
	if u == nil || u.SessionsValidAfter == nil {
		return false
	}
	iat, ok := IssuedAt(ctx, db, r)
	return !ok || iat.Before(u.SessionsValidAfter.Truncate(time.Second))
}

// Entry is one admin action.
type Entry struct {
	ActorID, ActorEmail               string
	Action                            string
	TargetType, TargetID, TargetLabel string
	Changes                           any
	IP                                string
	Via                               string // rest | mcp
}

// Audit appends an entry to admin_audit. q may be a *sql.DB or *sql.Tx.
func Audit(ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, e Entry) error {
	var changes any
	if e.Changes != nil {
		b, err := json.Marshal(e.Changes)
		if err != nil {
			return err
		}
		changes = string(b)
	}
	if e.Via == "" {
		e.Via = "rest"
	}
	_, err := q.ExecContext(ctx, `INSERT INTO admin_audit (actor_id, actor_email, action, target_type, target_id, target_label, changes, ip, via)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)`,
		e.ActorID, e.ActorEmail, e.Action, e.TargetType, e.TargetID, e.TargetLabel, changes, e.IP, e.Via)
	return err
}
