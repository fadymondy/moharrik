package automation_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/db/migrate"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/togo-framework/togo"
)

/*
Capacity and retention against a real database (MH-234).

These need Postgres rather than a fake: the two things under test ARE database
behaviour — a unique partial index that has to refuse the second live run for a
browser, and a delete that has to cascade to steps. A stubbed store would
assert that the Go code calls the right SQL, which is not the question.

Run them the way the isolation sweep runs:

	make db
	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55470/moharrik_test?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app_test:test@localhost:55470/moharrik_test?sslmode=disable \
	go test -count=1 ./internal/automation/ -run Capacity -v
*/

// testApp migrates a throwaway database and returns the app wired to it.
func testApp(t *testing.T) *app.App {
	t.Helper()

	owner, appURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_APP_DATABASE_URL")
	if owner == "" || appURL == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_APP_DATABASE_URL (a throwaway database) to run the capacity tests")
	}

	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "capacity-test-secret-capacity-test-secret-012345")
	t.Setenv("STORAGE_DIR", t.TempDir())
	// Nothing in the background: a retention sweeper running underneath a
	// retention test would be deleting the rows the test is counting.
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")

	ctx := context.Background()
	k0 := togo.New()
	sys, err := k0.SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Run(ctx, sys, appURL, "../..", t.Logf); err != nil {
		t.Fatal(err)
	}
	k0.Close()

	k := togo.New()
	a := app.New(ctx, k)
	t.Cleanup(func() { k.Close() })
	return a
}

// workspace makes a workspace and one paired browser in it, and returns both
// ids. Written with the owner pool: this is fixture setup, not the behaviour
// under test.
func workspace(t *testing.T, a *app.App) (workspaceID, agentID string) {
	t.Helper()
	ctx := context.Background()

	suffix := time.Now().UnixNano()
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO workspaces (name, slug, created_by) VALUES ($1, $2, 'capacity-test') RETURNING id`,
		"Capacity Test", "capacity-test-"+itoa(suffix)).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `
		INSERT INTO browser_agents (workspace_id, name, token_hash, created_by, allowed_hosts)
		VALUES ($1, 'test browser', $2, '', '{example.com}') RETURNING id`,
		workspaceID, "hash-"+itoa(suffix)).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	return workspaceID, agentID
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{digits[n%10]}, buf...)
		n /= 10
	}
	return string(buf)
}

// liveRun inserts a run already in flight, without going through Start —
// the point is to occupy a slot, not to exercise the dispatcher.
func liveRun(t *testing.T, a *app.App, workspaceID, agentID string) string {
	t.Helper()
	sctx := tenant.WithWorkspace(context.Background(), workspaceID)
	var id string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, status, started_at)
		VALUES ($1, $2, 'running', now()) RETURNING id`, workspaceID, agentID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCapacityOneLiveRunPerBrowserIsEnforcedByTheDatabase(t *testing.T) {
	a := testApp(t)
	wsID, agentID := workspace(t, a)

	liveRun(t, a, wsID, agentID)

	// The guardrail used to be a count followed by an insert, with a comment
	// claiming the index enforced it. It did not — the index was not unique.
	// This inserts directly, bypassing every Go-side check, so the only thing
	// that can refuse it is the database.
	sctx := tenant.WithWorkspace(context.Background(), wsID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, status) VALUES ($1, $2, 'queued')`,
		wsID, agentID)
	if err == nil {
		t.Fatal("the database allowed a second live run for one browser — the unique index is not doing its job")
	}

	// A FINISHED run for the same browser must still be allowed, or a browser
	// could only ever be used once.
	if _, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, status, finished_at)
		VALUES ($1, $2, 'ok', now())`, wsID, agentID); err != nil {
		t.Fatalf("a finished run was refused: %v", err)
	}
}

func TestCapacityLimitsDefaultAndRoundTrip(t *testing.T) {
	a := testApp(t)
	wsID, _ := workspace(t, a)
	ctx := context.Background()

	// A workspace that never touched automation has no settings row, and must
	// still get the conservative defaults rather than "no limit".
	got := automation.ReadLimits(ctx, a, wsID)
	if got.MaxParallelRuns != automation.DefaultMaxParallelRuns || got.RetentionDays != automation.DefaultRetentionDays {
		t.Fatalf("defaults not applied for a workspace with no row: %+v", got)
	}

	if err := automation.SetLimits(ctx, a, wsID, automation.Limits{MaxParallelRuns: 7, RetentionDays: 5}); err != nil {
		t.Fatal(err)
	}
	got = automation.ReadLimits(ctx, a, wsID)
	if got.MaxParallelRuns != 7 || got.RetentionDays != 5 {
		t.Fatalf("limits did not round-trip: %+v", got)
	}

	// Out of range is refused with a sentence, not a Postgres error.
	if err := automation.SetLimits(ctx, a, wsID, automation.Limits{MaxParallelRuns: 0, RetentionDays: 5}); err == nil {
		t.Fatal("a cap of zero was accepted; that is what the kill switch is for")
	}
	if err := automation.SetLimits(ctx, a, wsID, automation.Limits{MaxParallelRuns: 100, RetentionDays: 5}); err == nil {
		t.Fatal("a cap of 100 was accepted")
	}
}

func TestCapacityLiveRunsCountsOnlyThisWorkspace(t *testing.T) {
	a := testApp(t)
	wsA, agentA := workspace(t, a)
	wsB, agentB := workspace(t, a)

	liveRun(t, a, wsA, agentA)
	liveRun(t, a, wsB, agentB)

	live, err := automation.LiveRuns(context.Background(), a, wsA)
	if err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("live runs = %d, want 1 — the other workspace's run leaked in", live)
	}
}

func TestCapacitySweepDeletesPastTheWindowAndKeepsTheRest(t *testing.T) {
	a := testApp(t)
	wsID, agentID := workspace(t, a)
	ctx := context.Background()
	sctx := tenant.WithWorkspace(ctx, wsID)

	if err := automation.SetLimits(ctx, a, wsID, automation.Limits{MaxParallelRuns: 3, RetentionDays: 30}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()

	// One finished run well past the window, one comfortably inside it, and
	// one still running with an ancient date — the last must survive, because
	// a run stuck in "running" is evidence, not litter.
	var oldID, freshID string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, status, finished_at, created_at)
		VALUES ($1, $2, 'ok', $3, $3) RETURNING id`, wsID, agentID, now.AddDate(0, 0, -90)).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, status, finished_at, created_at)
		VALUES ($1, $2, 'ok', $3, $3) RETURNING id`, wsID, agentID, now.AddDate(0, 0, -2)).Scan(&freshID); err != nil {
		t.Fatal(err)
	}
	stuckID := liveRun(t, a, wsID, agentID)
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET created_at = $2 WHERE workspace_id = $1 AND id = $3`,
		wsID, now.AddDate(0, 0, -365), stuckID); err != nil {
		t.Fatal(err)
	}

	// A step on the old run, to prove the cascade takes it with the run.
	if _, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_run_steps (workspace_id, run_id, position, command, status)
		VALUES ($1, $2, 0, 'navigate', 'ok')`, wsID, oldID); err != nil {
		t.Fatal(err)
	}

	if _, err := automation.SweepRuns(ctx, a, now); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		id      string
		want    bool
		because string
	}{
		{oldID, false, "a run 90 days past a 30-day window should be gone"},
		{freshID, true, "a run 2 days old is inside the window"},
		{stuckID, true, "a run still marked running must never be swept, however old"},
	} {
		var n int
		if err := a.SQLDB.QueryRowContext(sctx, `
			SELECT count(*) FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, c.id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if (n > 0) != c.want {
			t.Errorf("%s (found %d rows)", c.because, n)
		}
	}

	var steps int
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2`, wsID, oldID).Scan(&steps); err != nil {
		t.Fatal(err)
	}
	if steps != 0 {
		t.Errorf("the deleted run left %d steps behind; the cascade is not working", steps)
	}
}

func TestCapacitySweepKeepsEverythingWhenRetentionIsZero(t *testing.T) {
	// 0 means "keep for ever" — the escape hatch for anyone who would rather
	// decide themselves. A sweeper that treated it as "delete everything older
	// than now" would be the worst possible reading.
	a := testApp(t)
	wsID, agentID := workspace(t, a)
	ctx := context.Background()
	sctx := tenant.WithWorkspace(ctx, wsID)

	if err := automation.SetLimits(ctx, a, wsID, automation.Limits{MaxParallelRuns: 3, RetentionDays: 0}); err != nil {
		t.Fatal(err)
	}

	var id string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO automation_runs (workspace_id, agent_id, status, finished_at, created_at)
		VALUES ($1, $2, 'ok', $3, $3) RETURNING id`,
		wsID, agentID, time.Now().UTC().AddDate(-3, 0, 0)).Scan(&id); err != nil {
		t.Fatal(err)
	}

	if _, err := automation.SweepRuns(ctx, a, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM automation_runs WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("retention_days = 0 deleted a three-year-old run; it must mean keep for ever")
	}
}
