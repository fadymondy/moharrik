// Package admin is the superadmin panel's domain (MH-1184): platform analytics, users,
// workspaces and their members, the permission matrix and the admin audit log. The REST routes
// (/api/admin/*, routes.go) and the admin MCP tools (internal/mcp/admin_tools.go) call the same
// functions, and every change is appended to admin_audit with the acting admin.
//
// Everything is cross-tenant by definition and runs on the owner pool with explicit predicates.
// Access control is the request guard's admin tier (session + platform admin, 404 otherwise) and
// the admin MCP's token check; the functions here trust their Actor.
package admin

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/platform"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// Service carries the owner pool.
type Service struct {
	DB *sql.DB
}

// Actor is the platform admin acting.
type Actor struct {
	ID, Email, IP string
	Via           string // rest | mcp
}

// Context puts the admin on ctx for the workspace audit log (tenant.AuditTx), so a member change
// made from the panel is recorded in that workspace as the admin's.
func (a Actor) Context(ctx context.Context) context.Context {
	return tenant.WithActor(ctx, tenant.Actor{Kind: "admin", ID: a.ID, Name: a.Email, UserID: a.ID, Via: "admin"})
}

func notFound(what string) error { return huma.Error404NotFound(what + " not found") }

func invalid(msg string) error { return huma.Error422UnprocessableEntity(msg) }

func conflict(msg string) error { return huma.Error409Conflict(msg) }

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// audit records an admin action. Best effort is not good enough: a failure fails the call.
func (sv *Service) audit(ctx context.Context, q execer, a Actor, action, targetType, targetID, targetLabel string, changes any) error {
	return platform.Audit(ctx, q, platform.Entry{ActorID: a.ID, ActorEmail: a.Email, Action: action,
		TargetType: targetType, TargetID: targetID, TargetLabel: targetLabel, Changes: changes, IP: a.IP, Via: a.Via})
}

func (sv *Service) tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := sv.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func like(q string) string {
	q = strings.TrimSpace(q)
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%"
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

func limitOf(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
