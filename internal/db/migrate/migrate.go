// Package migrate applies Moharrik's schema to Postgres. Shared by cmd/migrate
// (`togo migrate`) and the DB-backed tests, so both build the exact same
// database — including the app role and RLS, which the tests exist to check.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// TenantTables get DML grants for the app role. Everything else (users, auth
// plugin tables, admin_mcp_tokens) gets none.
var TenantTables = []string{"workspaces", "workspace_members", "invitations", "mcp_tokens",
	"mail_lists", "contacts", "list_subscriptions", "campaigns", "smtp_settings",
	"companies", "pipeline_stages", "deals", "activities",
	"webhook_sources", "notification_items",
	"chat_widgets", "conversations", "messages",
	"feed_sources", "topics", "news_items",
	"browser_agents", "agent_pairings", "automation_runs", "automation_run_steps", "automation_settings",
	"mailboxes", "whatsapp_links", "automation_schedules", "automation_workflows",
	"research_runs", "research_sources", "subscribe_forms", "leads",
	"google_connections", "google_oauth_states"}

var roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// migrationLockKey is an arbitrary constant shared by every process that
// migrates this database. Its only job is to be the same number everywhere.
const migrationLockKey = 8_242_517

// Run applies internal/db/schema/*.sql, then the app role + grants, then
// internal/db/post/*.sql (constraints, RLS). Idempotent. root is the repo root.
func Run(ctx context.Context, owner *sql.DB, appURL, root string, logf func(string, ...any)) error {
	// The tenant tables FORCE row-level security, so the owner pool (tenant resolution, creating a
	// workspace, admin MCP) only works if the owner role bypasses it. A superuser does locally; on
	// pg-shared the owner is not one and needs BYPASSRLS — without it every workspace create 500s.
	var super, bypass bool
	if err := owner.QueryRowContext(ctx,
		`SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = current_user`).Scan(&super, &bypass); err != nil {
		return err
	}
	if !super && !bypass {
		return fmt.Errorf("DATABASE_URL's role must be superuser or BYPASSRLS (as a superuser: ALTER ROLE <owner> BYPASSRLS)")
	}
	// ONE migration at a time, across every process sharing this database.
	//
	// "CREATE TABLE IF NOT EXISTS" is idempotent but NOT concurrency-safe: two
	// sessions both see "not exists", both create, and one dies with a
	// duplicate key on pg_class. That is not hypothetical — two test packages
	// migrating the same throwaway database hit it immediately, and two API
	// instances booting together would hit it in production.
	//
	// A session-level advisory lock on a fixed key. It is released explicitly
	// below and, failing that, when the connection closes.
	conn, err := owner.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("could not take the migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
	}()

	if err := applyDir(ctx, owner, filepath.Join(root, "internal", "db", "schema"), logf); err != nil {
		return err
	}
	if err := ensureAppRole(ctx, owner, appURL, logf); err != nil {
		return err
	}
	return applyDir(ctx, owner, filepath.Join(root, "internal", "db", "post"), logf)
}

func applyDir(ctx context.Context, db *sql.DB, dir string, logf func(string, ...any)) error {
	files, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(b)); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		logf("applied %s", filepath.Base(f))
	}
	return nil
}

// ensureAppRole creates/updates the role named in APP_DATABASE_URL: login,
// no superuser, no BYPASSRLS, owns nothing, DML on tenant tables only.
func ensureAppRole(ctx context.Context, db *sql.DB, appURL string, logf func(string, ...any)) error {
	if appURL == "" {
		return fmt.Errorf("APP_DATABASE_URL is not set — the API needs a separate, non-owner role for RLS (README)")
	}
	cfg, err := pgx.ParseConfig(appURL)
	if err != nil {
		return fmt.Errorf("APP_DATABASE_URL: %w", err)
	}
	role, pass := cfg.User, cfg.Password
	if !roleName.MatchString(role) {
		return fmt.Errorf("APP_DATABASE_URL user %q is not a plain lowercase role name", role)
	}
	var current string
	if err := db.QueryRowContext(ctx, `SELECT current_user`).Scan(&current); err != nil {
		return err
	}
	if current == role {
		return fmt.Errorf("APP_DATABASE_URL uses the owner role %q — it must be a different role, or RLS never applies", role)
	}
	quoted := "'" + strings.ReplaceAll(pass, "'", "''") + "'"
	stmts := []string{
		fmt.Sprintf(`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%s') THEN CREATE ROLE %s LOGIN; END IF; END $$`, role, role),
		// Only LOGIN and the password: setting NOSUPERUSER/NOBYPASSRLS needs a superuser, and the
		// owner role deliberately is not one (production: pg-shared). The flags are checked below.
		fmt.Sprintf(`ALTER ROLE %s WITH LOGIN PASSWORD %s`, role, quoted),
		fmt.Sprintf(`DO $$ BEGIN EXECUTE format('GRANT CONNECT ON DATABASE %%I TO %s', current_database()); END $$`, role),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO %s`, role),
		fmt.Sprintf(`GRANT SELECT, INSERT, UPDATE, DELETE ON %s TO %s`, strings.Join(TenantTables, ", "), role),
		fmt.Sprintf(`REVOKE ALL ON users, admin_mcp_tokens FROM %s`, role),
	}
	for _, s := range stmts {
		if _, err := db.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("app role: %w", err)
		}
	}
	// RLS only protects anything if the app role cannot step around it. Verified rather than
	// set, so a role someone widened by hand stops the migration instead of running open.
	var super, bypass, createRole, createDB bool
	if err := db.QueryRowContext(ctx,
		`SELECT rolsuper, rolbypassrls, rolcreaterole, rolcreatedb FROM pg_roles WHERE rolname = $1`, role,
	).Scan(&super, &bypass, &createRole, &createDB); err != nil {
		return fmt.Errorf("app role: %w", err)
	}
	if super || bypass || createRole || createDB {
		return fmt.Errorf("app role %s must not be superuser, bypass RLS, create roles or create databases (super=%v bypassrls=%v createrole=%v createdb=%v) — fix it as a superuser", role, super, bypass, createRole, createDB)
	}
	logf("app role %s: login, no superuser, no BYPASSRLS, DML on %d tenant tables", role, len(TenantTables))
	return nil
}
