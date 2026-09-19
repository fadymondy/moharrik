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
var TenantTables = []string{"workspaces", "workspace_members", "invitations", "mcp_tokens"}

var roleName = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Run applies internal/db/schema/*.sql, then the app role + grants, then
// internal/db/post/*.sql (constraints, RLS). Idempotent. root is the repo root.
func Run(ctx context.Context, owner *sql.DB, appURL, root string, logf func(string, ...any)) error {
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
