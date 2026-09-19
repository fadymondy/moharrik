// Command migrate applies Moharrik's Postgres schema (internal/db/schema), the
// app role and its grants, then constraints and RLS (internal/db/post).
// Run with `togo migrate` (or `go run ./cmd/migrate`). Idempotent.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/db/migrate"
	"github.com/fadymondy/moharrik/internal/envfile"
	_ "github.com/fadymondy/moharrik/internal/plugins" // registers the pgx driver (db-postgres)
)

func main() {
	envfile.Load(".env")
	k := togo.New()
	defer k.Close()
	ctx := context.Background()

	owner, err := k.SQL(ctx)
	if err != nil {
		k.Log.Error("migrate: database unavailable", "err", err)
		os.Exit(1)
	}
	logf := func(f string, args ...any) { k.Log.Info(fmt.Sprintf(f, args...)) }
	if err := migrate.Run(ctx, owner, os.Getenv("APP_DATABASE_URL"), ".", logf); err != nil {
		k.Log.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	k.Log.Info("migrate complete")
}
