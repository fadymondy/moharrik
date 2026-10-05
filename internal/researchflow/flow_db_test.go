package researchflow

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
The research overview against a real database with RLS on (MH-1066). Every expected figure is worked
out by hand from the rows inserted below.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/researchflow/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "rflow", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "rflow-test-secret-rflow-test-secret-rflow-0001")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	if a.SQLDB == nil || a.SysDB == nil {
		t.Fatal("both pools are required")
	}
	return a
}

type seeder struct {
	t  *testing.T
	db *sql.DB
	ws string
	u  string
}

func (s seeder) id(q string, args ...any) string {
	s.t.Helper()
	var id string
	if err := s.db.QueryRow(q, append([]any{s.ws}, args...)...).Scan(&id); err != nil {
		s.t.Fatalf("%v\n%s", err, q)
	}
	return id
}

func (s seeder) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(q, append([]any{s.ws}, args...)...); err != nil {
		s.t.Fatalf("%v\n%s", err, q)
	}
}

func newWorkspace(t *testing.T, a *app.App) seeder {
	t.Helper()
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "-")
	user := "u-rflow-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`, user, "rflow-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by, currency, timezone) VALUES ('Rflow', $1, $2, 'USD', 'Africa/Cairo') RETURNING id`,
		"rflow-"+suffix, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, user); err != nil {
		t.Fatal(err)
	}
	return seeder{t: t, db: a.SysDB, ws: ws, u: user}
}

func read(t *testing.T, a *app.App, sc tenant.Scope, runs int) *ResearchOverview {
	t.Helper()
	got, err := Read(tenant.With(context.Background(), &sc), a.SQLDB, runs)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOverview(t *testing.T) {
	a := testApp(t)
	s := newWorkspace(t, a)
	now := time.Now().UTC().Truncate(time.Second)

	// ---- runs: a finished discovery run, a manual run still going, a failed one -----------------
	stages := `[{"stage":"plan","status":"done","count":0,"note":"","started_at":"` + now.Add(-3*time.Minute).Format(time.RFC3339) + `","finished_at":"` + now.Add(-179*time.Second).Format(time.RFC3339) + `"},` +
		`{"stage":"search","status":"done","count":8,"note":"","started_at":null,"finished_at":null},` +
		`{"stage":"read","status":"done","count":5,"note":"","started_at":null,"finished_at":null},` +
		`{"stage":"extract","status":"done","count":9,"note":"","started_at":null,"finished_at":null},` +
		`{"stage":"summarise","status":"skipped","count":0,"note":"no model","started_at":null,"finished_at":null}]`
	disc := s.id(`INSERT INTO research_runs (workspace_id, topic, status, origin, stage, stages, created_at)
		VALUES ($1, 'logistics in cairo', 'ok', 'discovery', '', $2::jsonb, $3) RETURNING id`, stages, now.Add(-1*time.Hour))
	live := s.id(`INSERT INTO research_runs (workspace_id, topic, status, origin, stage, stages, created_at)
		VALUES ($1, 'fintech hiring', 'running', 'manual', 'read', '[{"stage":"plan","status":"done","count":0,"note":"","started_at":null,"finished_at":null},{"stage":"search","status":"done","count":4,"note":"","started_at":null,"finished_at":null},{"stage":"read","status":"running","count":1,"note":"","started_at":null,"finished_at":null}]'::jsonb, $2) RETURNING id`, now.Add(-1*time.Minute))
	s.id(`INSERT INTO research_runs (workspace_id, topic, status, origin, error, created_at) VALUES ($1, 'broken', 'failed', 'manual', 'search is down', $2) RETURNING id`, now.Add(-2*time.Hour))

	// ---- evidence and companies ------------------------------------------------------------------
	ev := func(run, key string) {
		s.exec(`INSERT INTO research_evidence (workspace_id, run_id, quote, source_url, company_key, content_hash) VALUES ($1, $2, 'q ' || $3, 'https://x.test/' || $3, $3, $2 || $3 || random()::text)`, run, key)
	}
	for _, k := range []string{"acme", "acme", "globex", "initech", "hooli"} { // 5 rows, 4 companies in the discovery run
		ev(disc, k)
	}
	ev(live, "umbrella")
	ev(live, "")
	company := func(key string) string {
		return s.id(`INSERT INTO research_companies (workspace_id, company_key, name, domain) VALUES ($1, $2, $2, $2 || '.test') RETURNING id`, key)
	}
	cAcme, cGlobex, cInitech, cHooli := company("acme"), company("globex"), company("initech"), company("hooli")
	company("umbrella")
	company("unrelated") // named by no run in this test

	// ---- prospects: 4 scored by the discovery run, plus one from nowhere ------------------------------
	prospect := func(c string, score float32, status, run string) {
		s.exec(`INSERT INTO prospects (workspace_id, suggestion_id, score, status, first_run_id) VALUES ($1, $2, $3, $4, $5)`, c, score, status, run)
	}
	prospect(cAcme, 95, "new", disc)
	prospect(cGlobex, 70, "new", disc) // exactly the threshold: high-fit
	prospect(cInitech, 69, "new", disc)
	prospect(cHooli, 99, "accepted", disc)

	// ---- schedules ----------------------------------------------------------------------------------
	s.exec(`INSERT INTO research_schedules (workspace_id, name, topic, cadence, enabled, next_run_at) VALUES ($1, 'a', 'topic a', 'daily', true, $2)`, now.Add(5*time.Hour))
	s.exec(`INSERT INTO research_schedules (workspace_id, name, topic, cadence, enabled, next_run_at) VALUES ($1, 'b', 'topic b', 'weekly', true, $2)`, now.Add(2*time.Hour))
	s.exec(`INSERT INTO research_schedules (workspace_id, name, topic, cadence, enabled, next_run_at) VALUES ($1, 'c', 'topic c', 'weekly', false, $2)`, now.Add(1*time.Hour))

	owner := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "owner", Via: "session"}
	got := read(t, a, owner, 0)

	if got.Thresholds.HighFitScore != 70 {
		t.Errorf("threshold = %d", got.Thresholds.HighFitScore)
	}
	// 6 companies; 7 evidence rows; 4 prospects; high-fit = 95, 70, 99; 1 accepted.
	if w := (FlowCounts{Found: 6, Evidence: 7, Prospects: 4, HighFit: 3, Accepted: 1}); got.Totals != w {
		t.Errorf("totals = %+v, want %+v", got.Totals, w)
	}
	if got.Schedules.Total != 3 || got.Schedules.Enabled != 2 || got.Schedules.NextRunAt == nil || got.Schedules.NextRunAt.Sub(now.Add(2*time.Hour)).Abs() > time.Second {
		t.Errorf("schedules = %+v (the paused schedule must not set the next run)", got.Schedules)
	}
	if len(got.Runs) != 3 || got.Runs[0].ID != live || got.Runs[1].ID != disc || got.Runs[2].Status != "failed" {
		t.Fatalf("runs = %d, newest first expected", len(got.Runs))
	}
	d := got.Runs[1]
	// Discovery run: 4 companies named by its evidence, 5 evidence rows, 4 prospects, 3 high-fit, 1 accepted.
	if w := (FlowCounts{Found: 4, Evidence: 5, Prospects: 4, HighFit: 3, Accepted: 1}); d.Counts != w {
		t.Errorf("discovery run counts = %+v, want %+v", d.Counts, w)
	}
	if len(d.Stages) != 5 || d.Stages[0].Stage != "plan" || d.Stages[0].StartedAt == nil || d.Stages[1].Count != 8 || d.Stages[4].Status != "skipped" || d.Stages[4].Note != "no model" {
		t.Errorf("stages must come back as stored: %+v", d.Stages)
	}
	l := got.Runs[0]
	// Manual run: its company, its two evidence rows (one without a company), and no prospects: manual runs are never scored.
	if w := (FlowCounts{Found: 1, Evidence: 2}); l.Counts != w {
		t.Errorf("manual run counts = %+v, want %+v", l.Counts, w)
	}
	if l.Stage != "read" || len(l.Stages) != 3 || l.Stages[2].Status != "running" {
		t.Errorf("live run stage = %q %+v", l.Stage, l.Stages)
	}
	if f := got.Runs[2]; f.Error != "search is down" || len(f.Stages) != 0 || f.Stages == nil {
		t.Errorf("failed run = %+v", f)
	}
	if lim := read(t, a, owner, 1); len(lim.Runs) != 1 || lim.Totals != got.Totals {
		t.Errorf("limit caps the runs, not the totals: %d runs", len(lim.Runs))
	}

	// A token without research:read is refused; one with it is not.
	no := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "owner", Via: "mcp", Scopes: []string{"contacts:read"}}
	if _, err := Read(tenant.With(context.Background(), &no), a.SQLDB, 0); err == nil {
		t.Error("a token without research:read must be refused")
	}
	yes := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "owner", Via: "mcp", Scopes: []string{"research:read"}}
	if r := read(t, a, yes, 0); len(r.Runs) != 3 {
		t.Errorf("token with research:read saw %d runs", len(r.Runs))
	}
	// A client role cannot read research at all.
	cl := tenant.Scope{WorkspaceID: s.ws, UserID: s.u, Role: "client", Via: "session"}
	if _, err := Read(tenant.With(context.Background(), &cl), a.SQLDB, 0); err == nil {
		t.Error("a client must be refused")
	}

	// Another workspace's rows never leak in (RLS).
	other := newWorkspace(t, a)
	or := read(t, a, tenant.Scope{WorkspaceID: other.ws, UserID: other.u, Role: "owner", Via: "session"}, 0)
	if len(or.Runs) != 0 || or.Totals != (FlowCounts{}) || or.Schedules.Total != 0 {
		t.Errorf("empty workspace sees %+v", or)
	}

	if _, err := Read(tenant.With(context.Background(), &owner), a.SQLDB, 26); err == nil {
		t.Error("a limit over the cap must be rejected")
	}
}
