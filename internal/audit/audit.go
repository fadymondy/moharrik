// Package audit is the audit log (MH-392): who changed what, when, from where,
// with a before/after diff of the fields that changed.
//
// It is implemented once, below the handlers, in three pieces:
//
//   - Middleware (router level) puts the request — an id, the client address,
//     the user agent — on every request's context (tenant.Request).
//   - The connection wrapper (tenant/rlsdb.go) turns that context plus the
//     tenant scope into the `app.audit` setting before every statement, and the
//     audit_row_change trigger (db/post/96_audit_log.sql) writes one row per
//     changed row of every audited table, with the diff, secrets redacted.
//   - UseHuma (every REST operation) and RecordRequest (every MCP write tool)
//     name the operation and, when a write changed no audited row — it was
//     refused, it sent something, it synced — write one request row, so no
//     write goes unrecorded.
package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Middleware puts a tenant.Request on every request's context. It must run
// after the client-address middleware (the address it records is the trusted
// one) and before the guard (so every tier carries it).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientip.From(r.Context())
		if ip == "" {
			ip = r.RemoteAddr
		}
		req := &tenant.Request{
			ID:        newID(),
			IP:        ip,
			UserAgent: r.UserAgent(),
			Method:    r.Method,
			Path:      r.URL.Path,
		}
		if r.URL.Path == "/graphql" {
			req.SetOperation("graphql")
		}
		// The id is handed back, so a support question ("this request did
		// something odd") leads straight to its rows in the audit log.
		w.Header().Set("X-Request-Id", req.ID)
		next.ServeHTTP(w, r.WithContext(tenant.WithRequest(r.Context(), req)))
	})
}

func newID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// mutating reports whether an HTTP method writes.
func mutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

/*
UseHuma names every REST operation on its request, and after a write that
changed no audited row writes the request row. It must be installed before the
first huma.Register (huma applies middleware at registration).

Every mutating operation is covered — generated CRUD and hand-written routes
alike, failures included — which audit_test.go proves by calling each one.
Reads are never recorded.
*/
func UseHuma(api huma.API, a *app.App) {
	api.UseMiddleware(func(hctx huma.Context, next func(huma.Context)) {
		ctx := hctx.Context()
		req := tenant.RequestFrom(ctx)
		op := hctx.Operation()
		if req != nil && op != nil {
			req.SetOperation(op.OperationID)
		}
		next(hctx)

		if a == nil || req == nil || op == nil || !mutating(hctx.Method()) {
			return
		}
		entityType, entityID := entityOf(op.Path, func(name string) string { return hctx.Param(name) })
		RecordRequest(ctx, a, Fallback{
			Operation:  op.OperationID,
			Status:     hctx.Status(),
			EntityType: entityType,
			EntityID:   entityID,
		})
	})
}

// entityOf reads what a route acts on from its path: /api/w/contacts/{id} is
// contacts and the id; /api/w/deals/{id}/move is deals and the id.
func entityOf(pattern string, param func(string) string) (string, string) {
	p := strings.TrimPrefix(strings.TrimPrefix(pattern, "/api/w/"), "/api/")
	segs := strings.Split(p, "/")
	typ := ""
	if len(segs) > 0 && !strings.HasPrefix(segs[0], "{") {
		typ = segs[0]
	}
	id := ""
	for _, s := range segs {
		if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
			id = param(strings.Trim(s, "{}"))
			break
		}
	}
	return strings.ReplaceAll(typ, "-", "_"), clip(id, 200)
}

// Fallback is a request row: what was attempted and how it ended.
type Fallback struct {
	Operation  string
	Status     int
	EntityType string
	EntityID   string
}

/*
RecordRequest writes the request row for a write that the trigger did not
record: nothing changed in an audited table (a refusal, a send, a sync), or
the only changes were to excluded tables. It writes nothing when the request
already produced change rows, and nothing for the system's own work.

It runs on the OWNER pool: the app role may only read audit_log.
*/
func RecordRequest(ctx context.Context, a *app.App, f Fallback) {
	if a == nil || a.SysDB == nil {
		return
	}
	actor, ok := tenant.ActorFrom(ctx)
	if !ok {
		return
	}
	ws := tenant.WorkspaceID(ctx)
	if ws == "" {
		return // an account-level route: its change rows carry the workspace
	}
	req := tenant.RequestFrom(ctx)
	rid, ip, ua := "", "", ""
	if req != nil {
		rid, ip, ua = req.ID, req.IP, clip(req.UserAgent, 300)
	}
	// An id is kept only when the write succeeded, i.e. it named something in
	// this workspace. A refused or failed one names whatever the caller typed
	// — possibly another workspace's id — and the log must not become a place
	// where one tenant's ids show up in another's (the isolation sweep).
	if f.Status >= 400 {
		f.EntityID = ""
	}
	// Detached from the request: a client that hangs up must not un-record.
	c := context.WithoutCancel(ctx)
	if rid != "" {
		var changed bool
		if err := a.SysDB.QueryRowContext(c,
			`SELECT EXISTS (SELECT 1 FROM audit_log WHERE request_id = $1 AND kind = 'change')`, rid).Scan(&changed); err == nil && changed {
			return
		}
	}
	if _, err := a.SysDB.ExecContext(c, `
		INSERT INTO audit_log (workspace_id, kind, request_id, actor_kind, actor_id, actor_name, user_id,
		                       via, operation, action, entity_type, entity_id, status, ip, user_agent)
		VALUES ($1, 'request', $2, $3, $4, $5, $6, $7, $8, $8, $9, $10, $11, $12, $13)`,
		ws, rid, actor.Kind, actor.ID, actor.Name, actor.UserID, actor.Via,
		clip(f.Operation, 120), clip(f.EntityType, 80), clip(f.EntityID, 200), f.Status, ip, ua); err != nil && a.Log != nil {
		a.Log.Warn("audit: could not record a request", "operation", f.Operation, "err", err)
	}
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
