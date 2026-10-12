package prospects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Discovery (MH-397): a research run aimed at the profile, and what happens
when it settles.

Each pass searches ONE of the profile's queries, in turn — one research run,
the engine's one-at-a-time rule and its daily budget unchanged. When a
discovery run finishes, every company its evidence names that is still only a
suggestion becomes a prospect: scored, with the person the pages name (if
any), and announced in one digest notification. Nothing reaches the CRM here;
that is Accept, which a person does.
*/

// ErrNotConfigured: the profile names nothing to look for.
var ErrNotConfigured = errors.New("the discovery profile names nothing to look for: add an industry, a keyword or a query")

const busyRetry = 10 * time.Minute

// Init wires discovery into research: settled discovery runs become
// prospects, and scheduled discovery rides the research clock.
//
// Once per process: Boot can run more than once in a test binary, and a hook
// registered twice would score every run twice.
func Init(engine *research.Engine) {
	initOnce.Do(func() { initHooks(engine) })
}

var initOnce sync.Once

func initHooks(engine *research.Engine) {
	research.OnRunFinished(func(ctx context.Context, a *app.App, ws, runID, origin string) {
		if origin != "discovery" {
			return
		}
		created, err := Ingest(ctx, a.SQLDB, ws, runID)
		if err != nil {
			a.Log.Warn("prospects: could not score a discovery run", "run", runID, "err", err)
			return
		}
		if _, err := RescoreOpen(ctx, a.SQLDB, ws); err != nil {
			a.Log.Warn("prospects: could not rescore", "workspace", ws, "err", err)
		}
		// An agent's run made most of its prospects as it submitted them, so
		// the digest names every prospect this run found, not only Ingest's.
		if all, err := foundBy(ctx, a.SQLDB, ws, runID); err == nil && len(all) > len(created) {
			created = all
		}
		announce(ctx, a, ws, runID, created)
	})
	research.OnTick(func(ctx context.Context, a *app.App) { RunDue(ctx, a, engine, 20) })
}

// Ingest turns the companies a run's evidence names into prospects, and
// returns the ones it created. A company already in the CRM, accepted or
// dismissed is not a prospect; one that is already a prospect is rescored
// by RescoreOpen, not duplicated.
func Ingest(ctx context.Context, db *sql.DB, ws, runID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.name, s.domain, s.company_key FROM research_companies s
		WHERE s.workspace_id = $1 AND s.status = 'suggested'
		  AND s.company_key IN (SELECT DISTINCT company_key FROM research_evidence WHERE workspace_id = $1 AND run_id = $2 AND company_key <> '')`,
		ws, runID)
	if err != nil {
		return nil, err
	}
	type sug struct{ id, name, domain, key string }
	var list []sug
	for rows.Next() {
		var s sug
		if err := rows.Scan(&s.id, &s.name, &s.domain, &s.key); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, s)
	}
	rows.Close()

	p, err := GetProfile(ctx, db, ws)
	if err != nil {
		return nil, err
	}
	hist, err := history(ctx, db, ws)
	if err != nil {
		return nil, err
	}
	var created []string
	for _, s := range list {
		var id string
		// The prospect is written by whoever wrote the run: the engine, or a
		// caller's agent (submit_research on a discovery task).
		err := db.QueryRowContext(ctx, `INSERT INTO prospects (workspace_id, suggestion_id, first_run_id, author_kind)
			VALUES ($1, $2, $3, coalesce((SELECT author_kind FROM research_runs WHERE workspace_id = $1 AND id = $3), 'engine'))
			ON CONFLICT (workspace_id, suggestion_id) DO NOTHING RETURNING id`, ws, s.id, runID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return created, err
		}
		pages, err := companyPages(ctx, db, ws, s.key)
		if err != nil {
			return created, err
		}
		c := FindContact(pages, s.name, s.domain)
		if _, err := db.ExecContext(ctx, `UPDATE prospects SET contact_name = $3, contact_title = $4, contact_email = $5, contact_source_url = $6
			WHERE workspace_id = $1 AND id = $2`, ws, id, c.Name, c.Title, c.Email, c.SourceURL); err != nil {
			return created, err
		}
		if _, err := scoreOne(ctx, db, ws, id, s.name, s.key, p, hist); err != nil {
			return created, err
		}
		created = append(created, id)
	}
	return created, nil
}

// foundBy is every prospect a run found first.
func foundBy(ctx context.Context, db *sql.DB, ws, runID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM prospects WHERE workspace_id = $1 AND first_run_id = $2`, ws, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// companyPages are the read pages behind a company's evidence.
func companyPages(ctx context.Context, db *sql.DB, ws, key string) ([]Page, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT s.url, s.content FROM research_sources s
		JOIN research_evidence e ON e.workspace_id = s.workspace_id AND e.source_id = s.id
		WHERE s.workspace_id = $1 AND e.company_key = $2 AND s.fetched LIMIT 10`, ws, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Page
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.URL, &p.Content); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Discover starts one discovery pass: the profile's next query, as a
// research run with origin "discovery". auto=true is the clock, which spends
// the daily budget; a person pressing the button does not.
func Discover(ctx context.Context, a *app.App, engine *research.Engine, ws, userID string, auto bool) (string, string, error) {
	sctx := tenant.WithWorkspace(ctx, ws)
	if !auto {
		sctx = ctx
	}
	q, next, n, err := nextQuery(sctx, a.SQLDB, ws)
	if err != nil {
		return "", "", err
	}
	req := research.Request{Topic: q, Origin: "discovery"}
	var runID string
	if auto {
		runID, err = engine.StartAutomatic(ctx, a, ws, req)
	} else {
		runID, err = engine.StartRun(ctx, a, ws, userID, req)
	}
	if err != nil {
		return "", q, err
	}
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE prospect_profiles SET next_query = $2, updated_at = now() WHERE workspace_id = $1`,
		ws, (next+1)%n)
	return runID, q, nil
}

// nextQuery is the profile's next discovery query, its position, and how many there are.
func nextQuery(ctx context.Context, db *sql.DB, ws string) (string, int, int, error) {
	p, err := GetProfile(ctx, db, ws)
	if err != nil {
		return "", 0, 0, err
	}
	queries := p.EffectiveQueries
	if len(queries) == 0 {
		return "", 0, 0, ErrNotConfigured
	}
	var next int
	if err := db.QueryRowContext(ctx, `SELECT next_query FROM prospect_profiles WHERE workspace_id = $1`, ws).Scan(&next); err != nil {
		return "", 0, 0, err
	}
	return queries[next%len(queries)], next, len(queries), nil
}

// MaxWaitingDiscovery is how many discovery tasks may wait for an agent at once;
// the clock queues no more until one is taken.
const MaxWaitingDiscovery = 3

// QueueDiscovery is discovery in agent mode: the profile's next query becomes
// a task for the caller's AI assistant (opportunity_tasks) instead of a run
// here. origin is manual (a person), discovery (the clock) or assistant (an
// agent asking for work).
func QueueDiscovery(ctx context.Context, db *sql.DB, ws, userID, origin string) (*research.AgentTask, error) {
	q, next, n, err := nextQuery(ctx, db, ws)
	if err != nil {
		return nil, err
	}
	t, err := research.EnqueueAgentTask(ctx, db, ws, research.AgentTaskInput{Kind: "discovery", Topic: q, Origin: origin, RequestedBy: userID})
	if err != nil {
		return nil, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE prospect_profiles SET next_query = $2, updated_at = now() WHERE workspace_id = $1`, ws, (next+1)%n)
	return t, nil
}

// DiscoverResult is one discovery pass started: a run here, or a task for an agent.
type DiscoverResult struct {
	Provider string `json:"provider" enum:"server,agent"`
	RunID    string `json:"run_id,omitempty"`
	TaskID   string `json:"task_id,omitempty"`
	Query    string `json:"query"`
}

// DiscoverNow is a person asking for a discovery pass: a run when the server
// does the research, a queued task when the workspace's agent does.
func DiscoverNow(ctx context.Context, a *app.App, engine *research.Engine, ws, userID string) (*DiscoverResult, error) {
	if engine.UsesAgent(ctx, a.SQLDB, ws) {
		t, err := QueueDiscovery(ctx, a.SQLDB, ws, userID, "manual")
		if err != nil {
			return nil, err
		}
		return &DiscoverResult{Provider: research.ProviderAgent, TaskID: t.ID, Query: t.Topic}, nil
	}
	runID, q, err := Discover(ctx, a, engine, ws, userID, false)
	if err != nil {
		return nil, err
	}
	return &DiscoverResult{Provider: research.ProviderServer, RunID: runID, Query: q}, nil
}

// RunDue runs the scheduled discovery that is due. The queue is read on the
// owner pool, as research's schedules are; each pass runs scoped.
func RunDue(ctx context.Context, a *app.App, engine *research.Engine, limit int) int {
	rows, err := a.SysDB.QueryContext(ctx, `SELECT workspace_id, cadence FROM prospect_profiles
		WHERE enabled AND next_run_at <= now() ORDER BY next_run_at LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("prospects: could not read the discovery queue", "err", err)
		return 0
	}
	type due struct{ ws, cadence string }
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.ws, &d.cadence) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		runScheduled(ctx, a, engine, d.ws, d.cadence)
	}
	return len(list)
}

func runScheduled(ctx context.Context, a *app.App, engine *research.Engine, ws, cadence string) {
	every := 24 * time.Hour
	if cadence == "weekly" {
		every = 7 * 24 * time.Hour
	}
	next := time.Now().Add(every)
	status, note := "started", ""
	sctx := tenant.WithWorkspace(ctx, ws)
	var runID, q string
	var err error
	if engine.UsesAgent(sctx, a.SQLDB, ws) {
		// Agent mode: queue the pass for the workspace's AI assistant, a few at most.
		var waiting int
		_ = a.SQLDB.QueryRowContext(sctx, `SELECT count(*) FROM agent_tasks WHERE workspace_id = $1 AND kind = 'discovery' AND status = 'pending'`, ws).Scan(&waiting)
		if waiting >= MaxWaitingDiscovery {
			err = fmt.Errorf("%d discovery tasks are still waiting for your AI assistant (opportunity_tasks)", waiting)
			status, note = "busy", err.Error()
		} else if t, qerr := QueueDiscovery(sctx, a.SQLDB, ws, "", "discovery"); qerr != nil {
			err = qerr
		} else {
			q = "queued for your AI assistant: " + t.Topic
		}
	} else {
		runID, q, err = Discover(ctx, a, engine, ws, "", true)
	}
	switch {
	case status == "busy":
	case err == nil:
		note = q
	case errors.Is(err, research.ErrBusy):
		status, note, next = "busy", "another research run was going; retrying shortly", time.Now().Add(busyRetry)
	case errors.Is(err, research.ErrOverBudget):
		status, note = "over_budget", "today's research budget was spent; next try at the next scheduled time"
	case errors.Is(err, research.ErrNoSearch), errors.Is(err, research.ErrNoReader):
		status, note = "not_configured", err.Error()
	case errors.Is(err, ErrNotConfigured):
		status, note = "no_queries", err.Error()
	default:
		status, note = "failed", err.Error()
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE prospect_profiles SET next_run_at = $2, last_status = $3, last_note = $4,
			last_run_id = CASE WHEN $5 <> '' THEN $5 ELSE last_run_id END,
			last_run_at = CASE WHEN $5 <> '' THEN now() ELSE last_run_at END, updated_at = now()
		WHERE workspace_id = $1`, ws, next, status, trim(note, 300), runID); err != nil {
		a.Log.Warn("prospects: could not record a discovery pass", "workspace", ws, "err", err)
	}
}

// announce posts one digest item for a discovery run that found prospects.
func announce(ctx context.Context, a *app.App, ws, runID string, created []string) {
	if len(created) == 0 {
		return
	}
	type top struct {
		name  string
		score float64
	}
	var best []top
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT s.name, p.score FROM prospects p
		JOIN research_companies s ON s.workspace_id = p.workspace_id AND s.id = p.suggestion_id
		WHERE p.workspace_id = $1 AND p.id = ANY($2::text[]) ORDER BY p.score DESC LIMIT 3`, ws, pgArr(created))
	if err == nil {
		for rows.Next() {
			var t top
			if rows.Scan(&t.name, &t.score) == nil {
				best = append(best, t)
			}
		}
		rows.Close()
	}
	var locale, slug string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT locale, slug FROM workspaces WHERE id = $1`, ws).Scan(&locale, &slug)
	ar := locale == "ar"
	title := fmt.Sprintf("Discovery: %d new prospect%s to review", len(created), plural(len(created)))
	source := "Prospects"
	if ar {
		title = fmt.Sprintf("الاستكشاف: %d عميلاً محتملاً جديداً للمراجعة", len(created))
		source = "العملاء المحتملون"
	}
	var lines []string
	for _, t := range best {
		lines = append(lines, fmt.Sprintf("• %s — %.0f", t.name, t.score))
	}
	loc := "en"
	if ar {
		loc = "ar"
	}
	ext := "prospects:" + runID
	if err := notifications.Emit(ctx, a.SQLDB, ws, notifications.Internal{
		SourceName: source, ExternalID: ext, Kind: "prospects", Severity: "info", Title: title,
		Body: strings.Join(lines, "\n"), URL: publicBase() + "/" + loc + "/w/" + slug + "/prospects",
	}); err != nil {
		a.Log.Warn("prospects: could not post the digest", "run", runID, "err", err)
		return
	}
	var itemID string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT id FROM notification_items WHERE workspace_id = $1 AND source_id IS NULL AND external_id = $2`,
		ws, ext).Scan(&itemID)
	alerts.Emit(ctx, a, ws, realtime.Event{Kind: "notification", ItemID: itemID, Title: title, Severity: "info",
		Source: source, Permission: authz.NotificationsRead, Alerting: true})
}

func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
