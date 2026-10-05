package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/platform"
)

/*
Platform-level gates (MH-1184), shared by every tier that has a session:

	accountGate          — a suspended account gets 403 account_suspended on every call; a session
	                       issued before an admin's "sign out everywhere" gets 401 session_revoked;
	                       otherwise the account is marked seen (the panel's "active users").
	requirePlatformAdmin — the admin tier: a platform admin, or 404.
	notFoundOn401        — the admin tier answers 404 to no session too, so /api/admin/* does not
	                       reveal that it exists.
	refuseSuspendedLogin — POST /api/auth/login with a suspended account's email is refused before
	                       the auth plugin mints a session.
*/

func writeCoded(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

func accountGate(a *app.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok || id == nil {
			next.ServeHTTP(w, r)
			return
		}
		u, err := platform.Load(r.Context(), a.SysDB, id.ID)
		if errors.Is(err, platform.ErrNoUser) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err != nil {
			// The database is the problem, not the caller: say so rather than let a
			// suspended account through on a hiccup.
			writeError(w, http.StatusServiceUnavailable, "could not check the account, try again")
			return
		}
		if u.Suspended() {
			writeCoded(w, http.StatusForbidden, "account_suspended", u.SuspendedMessage())
			return
		}
		if platform.SessionRevoked(r.Context(), a.SysDB, u, r) {
			writeCoded(w, http.StatusUnauthorized, "session_revoked", "your session was ended — sign in again")
			return
		}
		platform.TouchSeen(r.Context(), a.SysDB, u.ID)
		next.ServeHTTP(w, r)
	})
}

func requirePlatformAdmin(a *app.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok || id == nil {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		u, err := platform.Load(r.Context(), a.SysDB, id.ID)
		if err != nil || !u.Admin {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// notFoundOn401 turns the auth middleware's 401 into a 404.
func notFoundOn401(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&hide401{ResponseWriter: w}, r)
	})
}

type hide401 struct {
	http.ResponseWriter
	hidden bool
}

func (h *hide401) WriteHeader(code int) {
	if code == http.StatusUnauthorized {
		h.hidden = true
		writeError(h.ResponseWriter, http.StatusNotFound, "not found")
		return
	}
	h.ResponseWriter.WriteHeader(code)
}

func (h *hide401) Write(b []byte) (int, error) {
	if h.hidden {
		return len(b), nil
	}
	return h.ResponseWriter.Write(b)
}

func (h *hide401) Unwrap() http.ResponseWriter { return h.ResponseWriter }

const maxLoginBody = 64 << 10

func refuseSuspendedLogin(a *app.App, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/auth/login" || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBody+1))
		_ = r.Body.Close()
		if err != nil || len(raw) > maxLoginBody {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var in struct {
			Email string `json:"email"`
		}
		if json.Unmarshal(raw, &in) == nil && in.Email != "" {
			if u, suspended := platform.SuspendedByEmail(r.Context(), a.SysDB, in.Email); suspended {
				writeCoded(w, http.StatusForbidden, "account_suspended", u.SuspendedMessage())
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
