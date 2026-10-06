package workspaces_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/fadymondy/moharrik/internal/testdb"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
MH-1108: signing in opens the workspace I was last active in, so MyWorkspaces
says when that was for each of mine.

  - a workspace I never acted in reports when I joined it
  - my latest change in a workspace moves it past one I joined later
  - another member's activity does not count as mine
*/
func TestMyWorkspacesLastActive(t *testing.T) {
	owner, _ := testdb.Setup(t, "workspaces", "../..")
	sys, err := sql.Open("pgx", owner)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sys.Close() })
	ctx := context.Background()

	for _, u := range []string{"u-1108", "u-1108-other"} {
		if _, err := sys.ExecContext(ctx,
			`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $1 || '@example.test', 'x', now()::text) ON CONFLICT DO NOTHING`, u); err != nil {
			t.Fatal(err)
		}
	}
	older, err := workspaces.Create(ctx, sys, "u-1108", "Older 1108", "older-1108")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := workspaces.Create(ctx, sys, "u-1108", "Newer 1108", "newer-1108")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sys.ExecContext(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, 'u-1108-other', 'member', 'active')`, newer.ID); err != nil {
		t.Fatal(err)
	}

	lastActive := func() map[string]time.Time {
		t.Helper()
		list, err := workspaces.MyWorkspaces(ctx, sys, "u-1108")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]time.Time{}
		for _, w := range list {
			if w.LastActiveAt == nil {
				t.Fatalf("%s: no last_active_at", w.Slug)
			}
			out[w.Slug] = *w.LastActiveAt
		}
		return out
	}

	got := lastActive()
	if !got["newer-1108"].After(got["older-1108"]) && !got["newer-1108"].Equal(got["older-1108"]) {
		t.Fatalf("never active: want join times, newer joined last; got %v", got)
	}

	audit := func(wsID, actor string, at time.Time) {
		t.Helper()
		if _, err := sys.ExecContext(ctx, `
			INSERT INTO audit_log (workspace_id, occurred_at, kind, actor_kind, actor_id, user_id, action)
			VALUES ($1, $2, 'change', 'member', $3, $3, 'update')`, wsID, at, actor); err != nil {
			t.Fatal(err)
		}
	}
	later := time.Now().Add(time.Hour)
	audit(older.ID, "u-1108", later)
	audit(newer.ID, "u-1108-other", later.Add(time.Hour))

	got = lastActive()
	if !got["older-1108"].After(got["newer-1108"]) {
		t.Fatalf("my change in the older workspace should make it the latest (and another member's should not count); got %v", got)
	}
}
