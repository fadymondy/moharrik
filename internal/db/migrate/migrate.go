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
	"strconv"
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
	"feed_sources", "topics", "news_items", "trend_clusters", "trend_settings",
	"browser_agents", "agent_pairings", "automation_runs", "automation_run_steps", "automation_settings",
	"mailboxes", "whatsapp_links", "automation_schedules", "automation_workflows", "automation_workflow_versions",
	"research_runs", "research_sources", "subscribe_forms", "leads",
	"google_connections", "google_oauth_states", "push_subscriptions",
	"google_resources", "google_discoveries",
	"ga4_reports", "ga4_daily",
	"canned_replies",
	"gsc_search_analytics_daily", "gsc_search_totals_daily", "gsc_import_days",
	"youtube_channels", "youtube_videos", "youtube_comments", "youtube_analytics",
	"gsc_url_inspections", "youtube_report_jobs", "youtube_reports",
	"whatsapp_sends", "whatsapp_confirmations",
	"oauth_grants",   // connected apps (MH-391)
	"audit_settings", // audit log retention (MH-392)
	"smtp_services",  // several SMTP services per workspace (MH-412)
	// members' WhatsApp notifications (MH-411)
	"member_whatsapp", "member_whatsapp_events", "member_whatsapp_messages", "member_whatsapp_marks",
	// collected notification sources (MH-384)
	"notification_sources",
}

/*
ReadOnlyTenantTables are workspace-scoped and RLS-protected like the tenant
tables, but the app role may only SELECT them. audit_log (MH-392) is written by
the audit_row_change trigger (SECURITY DEFINER) and by the owner pool, so the
API's own role can read its workspace's history and never rewrite it.
*/
var ReadOnlyTenantTables = []string{"audit_log"}

/*
AuditExcluded are the tenant tables the audit trigger is NOT attached to, each
with the reason. Every other tenant table is audited — a new table is recorded
until someone decides here that it should not be (audit_test.go checks both
lists against the database).

Excluding a table does not make a write to it unrecorded: a request that
changes only excluded tables still gets its request row (internal/audit).
*/
var AuditExcluded = map[string]string{
	"google_oauth_states":        "a ten-minute CSRF state for Google's redirect, deleted on use",
	"google_discoveries":         "what Google reported the account can see, re-fetched by every sync",
	"ga4_reports":                "a mirror of Google Analytics, rewritten by every sync",
	"ga4_daily":                  "a mirror of Google Analytics, rewritten by every sync",
	"gsc_search_analytics_daily": "a mirror of Search Console, rewritten by every import",
	"gsc_search_totals_daily":    "a mirror of Search Console, rewritten by every import",
	"gsc_import_days":            "the import's own bookkeeping",
	"youtube_analytics":          "a mirror of YouTube Analytics, rewritten by every sync",
	"youtube_report_jobs":        "a mirror of YouTube Reporting, rewritten by every sync",
	"youtube_reports":            "a mirror of YouTube Reporting, rewritten by every sync",
	"youtube_videos":             "a mirror of the channel's videos, rewritten by every sync",
	"youtube_comments":           "a mirror of the channel's comments, rewritten by every sync",
	"automation_run_steps":       "one row per browser step of a run; the run itself is audited",
	"research_sources":           "evidence a research run collected; the run itself is audited",
	"news_items":                 "items a feed collected; saving one is recorded by its request row",
	"whatsapp_confirmations":     "a one-time confirmation code, deleted on use",
	"notification_items":         "the inbound feed; marking items read or saved is recorded by its request row",
	"member_whatsapp_events":     "the WhatsApp notifier's outbox, written and marked sent by every tick",
	"member_whatsapp_messages":   "the WhatsApp notifier's send log, itself a record",
	"member_whatsapp_marks":      "the WhatsApp notifier's watermarks, moved by every tick",
}

// AuditedTables are the tenant tables the audit trigger is attached to.
func AuditedTables() []string {
	out := []string{}
	for _, t := range TenantTables {
		if _, skip := AuditExcluded[t]; !skip {
			out = append(out, t)
		}
	}
	return out
}

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
	if err := applyDir(ctx, owner, filepath.Join(root, "internal", "db", "post"), logf); err != nil {
		return err
	}
	return attachAuditTriggers(ctx, owner, logf)
}

// attachAuditTriggers puts audit_row_change (96_audit_log.sql) on every
// audited tenant table and takes it off the excluded ones. Idempotent: an
// existing trigger is left as it is, so there is no window without one.
func attachAuditTriggers(ctx context.Context, db *sql.DB, logf func(string, ...any)) error {
	for _, t := range AuditedTables() {
		stmt := fmt.Sprintf(`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = 'audit_row_change' AND tgrelid = '%s'::regclass) THEN
				CREATE TRIGGER audit_row_change AFTER INSERT OR UPDATE OR DELETE ON %s
					FOR EACH ROW EXECUTE FUNCTION audit_row_change();
			END IF;
		END $$`, t, t)
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("audit trigger on %s: %w", t, err)
		}
	}
	for t := range AuditExcluded {
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP TRIGGER IF EXISTS audit_row_change ON %s`, t)); err != nil {
			return fmt.Errorf("audit trigger off %s: %w", t, err)
		}
	}
	logf("audit trigger on %d tenant tables (%d excluded)", len(AuditedTables()), len(AuditExcluded))
	return nil
}

func applyDir(ctx context.Context, db *sql.DB, dir string, logf func(string, ...any)) error {
	files, _ := filepath.Glob(filepath.Join(dir, "*.sql"))
	sortMigrations(files)
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

// sortMigrations orders files by their leading number, not as text: "115_x" runs
// after "96_y", which a plain string sort gets backwards once numbers pass 99.
// Files without a number keep their text order, after the numbered ones.
func sortMigrations(files []string) {
	num := func(f string) (int, bool) {
		base := filepath.Base(f)
		i := 0
		for i < len(base) && base[i] >= '0' && base[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, err := strconv.Atoi(base[:i])
		return n, err == nil
	}
	sort.SliceStable(files, func(a, b int) bool {
		na, oka := num(files[a])
		nb, okb := num(files[b])
		switch {
		case oka && okb && na != nb:
			return na < nb
		case oka != okb:
			return oka
		}
		return filepath.Base(files[a]) < filepath.Base(files[b])
	})
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
		// Read-only tenant tables: SELECT, and explicitly nothing else.
		fmt.Sprintf(`REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON %s FROM %s`, strings.Join(ReadOnlyTenantTables, ", "), role),
		fmt.Sprintf(`GRANT SELECT ON %s TO %s`, strings.Join(ReadOnlyTenantTables, ", "), role),
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
