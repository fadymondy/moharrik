// Package testdb gives each DB-backed test package its own throwaway database,
// and migrates it without racing the others.
//
// Two separate problems, both found by running the suites together:
//
//  1. Three packages migrating ONE database interfere. Package B's ALTER TABLE
//     lands while package A is mid-test, invalidating the statement
//     descriptions pgx has cached, and A fails with something unrelated-looking
//     like "could not read the company". Fixed by one database per package.
//
//  2. The app ROLE is cluster-global, not per-database. Two packages running
//     CREATE ROLE / ALTER ROLE / GRANT at once collide on the same catalogue
//     row: "tuple concurrently updated". Splitting the databases made this
//     WORSE, because a Postgres advisory lock is scoped to the database it is
//     taken in — so the lock inside migrate.Run stopped serialising anything
//     once each package had its own.
//
// So the lock is taken here, on the shared BASE database that every package
// connects to anyway, and held across the whole migration.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/db/migrate"
)

// setupLockKey is shared by every test package. Its only job is to be the same
// number in all of them.
const setupLockKey = 7_713_904

// Setup points this test binary at its own database, migrates it, and returns
// the two URLs.
//
// name should be the package's own, e.g. "marketing". root is the repo root
// relative to the test file, e.g. "../..".
//
// It SKIPS when TEST_DATABASE_URL / TEST_APP_DATABASE_URL are unset, which is
// how these suites stay optional on a machine with no Postgres.
func Setup(t *testing.T, name, root string) (ownerURL, appURL string) {
	t.Helper()

	baseOwner, baseApp := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_APP_DATABASE_URL")
	if baseOwner == "" || baseApp == "" {
		t.Skipf("set TEST_DATABASE_URL and TEST_APP_DATABASE_URL (a throwaway database) to run the %s database tests", name)
	}

	baseName, err := databaseName(baseOwner)
	if err != nil {
		t.Fatal(err)
	}
	mine := baseName + "_" + sanitise(name)

	if ownerURL, err = withDatabase(baseOwner, mine); err != nil {
		t.Fatal(err)
	}
	if appURL, err = withDatabase(baseApp, mine); err != nil {
		t.Fatal(err)
	}

	// Everything below happens under one lock held on the BASE database: the
	// database creation, and the migration that touches the shared role.
	base, err := sql.Open("pgx", baseOwner)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()

	ctx := context.Background()
	conn, err := base.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, setupLockKey); err != nil {
		t.Fatalf("could not take the test setup lock: %v", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, setupLockKey)
	}()

	// CREATE DATABASE cannot run inside a transaction and has no IF NOT EXISTS,
	// so it is a check and then a create — safe here because of the lock.
	var exists bool
	if err := conn.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, mine).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		// The name is derived from a Go identifier in this repo, not from input.
		if _, err := conn.ExecContext(ctx, `CREATE DATABASE "`+mine+`"`); err != nil &&
			!strings.Contains(err.Error(), "already exists") {
			t.Fatalf("could not create %s: %v", mine, err)
		}
	}

	owner, err := sql.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := migrate.Run(ctx, owner, appURL, root, func(string, ...any) {}); err != nil {
		t.Fatalf("could not migrate %s: %v", mine, err)
	}

	t.Setenv("TEST_DATABASE_URL", ownerURL)
	t.Setenv("TEST_APP_DATABASE_URL", appURL)
	t.Setenv("DATABASE_URL", ownerURL)
	t.Setenv("APP_DATABASE_URL", appURL)
	return ownerURL, appURL
}

func databaseName(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", fmt.Errorf("no database in %q", raw)
	}
	return name, nil
}

func withDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}

// sanitise keeps a package name usable as a database suffix.
func sanitise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
