package research

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pglock"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Scheduled research and its budget (MH-388).

A schedule is a question (or a list of pages) and a cadence. An in-process
ticker starts the ones that are due — one run per workspace at a time, the
engine's own rule — and every automatic run spends one of the workspace's
daily budget. A person pressing "Research" does not: the budget exists because
nobody is watching a clock spend.

A schedule that cannot run says why, on the schedule (last_status/last_note):
over_budget, busy (another run was going; retried in ten minutes, without
spending), not_configured (no search, or no reader for a URL list), failed.
*/

// DefaultDailyBudget and MaxDailyBudget bound automatic runs per UTC day.
const (
	DefaultDailyBudget = 3
	MaxDailyBudget     = 50
	busyRetry          = 10 * time.Minute
)

// ErrOverBudget: today's automatic runs are spent.
var ErrOverBudget = errors.New("today's research budget is spent")

// Schedule is one recurring research run.
type ResearchSchedule struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Topic      string     `json:"topic"`
	Mode       string     `json:"mode" enum:"search,urls"`
	URLs       []string   `json:"urls"`
	Cadence    string     `json:"cadence" enum:"daily,weekly"`
	Enabled    bool       `json:"enabled"`
	NextRunAt  time.Time  `json:"next_run_at"`
	LastRunID  string     `json:"last_run_id"`
	LastRunAt  *time.Time `json:"last_run_at"`
	LastStatus string     `json:"last_status" enum:",started,over_budget,busy,not_configured,failed"`
	LastNote   string     `json:"last_note"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Budget is the workspace's automatic-research allowance.
type ResearchBudget struct {
	DailyRunBudget int    `json:"daily_run_budget"`
	RunsToday      int    `json:"runs_today"`
	Day            string `json:"day" doc:"The UTC day runs_today counts."`
}

// ScheduleInput creates or changes a schedule. nil = unchanged on update.
type ResearchScheduleInput struct {
	Name    *string   `json:"name,omitempty" maxLength:"120"`
	Topic   *string   `json:"topic,omitempty" maxLength:"500"`
	Mode    *string   `json:"mode,omitempty" enum:"search,urls"`
	URLs    *[]string `json:"urls,omitempty" maxItems:"10"`
	Cadence *string   `json:"cadence,omitempty" enum:"daily,weekly"`
	Enabled *bool     `json:"enabled,omitempty"`
}

const scheduleColumns = `id, name, topic, mode, urls::text, cadence, enabled, next_run_at, last_run_id, last_run_at, last_status, last_note, created_at`

func scanSchedule(row interface{ Scan(...any) error }) (ResearchSchedule, error) {
	var s ResearchSchedule
	var urls string
	var last sql.NullTime
	err := row.Scan(&s.ID, &s.Name, &s.Topic, &s.Mode, &urls, &s.Cadence, &s.Enabled, &s.NextRunAt, &s.LastRunID, &last,
		&s.LastStatus, &s.LastNote, &s.CreatedAt)
	s.URLs = parsePgArray(urls)
	if last.Valid {
		s.LastRunAt = &last.Time
	}
	return s, err
}

func cadenceEvery(c string) time.Duration {
	if c == "weekly" {
		return 7 * 24 * time.Hour
	}
	return 24 * time.Hour
}

// ListSchedules returns the workspace's schedules.
func ListSchedules(ctx context.Context, db *sql.DB, ws string) ([]ResearchSchedule, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM research_schedules WHERE workspace_id = $1 ORDER BY created_at`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResearchSchedule{}
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func getSchedule(ctx context.Context, db *sql.DB, ws, id string) (*ResearchSchedule, error) {
	s, err := scanSchedule(db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM research_schedules WHERE workspace_id = $1 AND id = $2`, ws, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no such research schedule")
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func validateSchedule(name, topic, mode, cadence string, urls []string) ([]string, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("a schedule needs a name")
	}
	if n := len([]rune(strings.TrimSpace(topic))); n < 3 || n > 500 {
		return nil, errors.New("a schedule needs a question of 3 to 500 characters")
	}
	if cadence != "daily" && cadence != "weekly" {
		return nil, errors.New("cadence is daily or weekly")
	}
	switch mode {
	case "search":
		return []string{}, nil
	case "urls":
		return NormaliseURLs(urls)
	}
	return nil, errors.New("mode is search or urls")
}

// CreateSchedule adds one. Its first run is due at once (within a tick).
func CreateSchedule(ctx context.Context, db *sql.DB, ws, userID string, in ResearchScheduleInput) (*ResearchSchedule, error) {
	name, topic, mode, cadence := deref(in.Name), deref(in.Topic), deref(in.Mode), deref(in.Cadence)
	if mode == "" {
		mode = "search"
	}
	if cadence == "" {
		cadence = "daily"
	}
	var urls []string
	if in.URLs != nil {
		urls = *in.URLs
	}
	urls, err := validateSchedule(name, topic, mode, cadence, urls)
	if err != nil {
		return nil, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	var id string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO research_schedules (workspace_id, name, topic, mode, urls, cadence, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		ws, strings.TrimSpace(name), strings.TrimSpace(topic), mode, pgArray(urls), cadence, enabled, userID).Scan(&id); err != nil {
		return nil, err
	}
	return getSchedule(ctx, db, ws, id)
}

// UpdateSchedule changes one.
func UpdateSchedule(ctx context.Context, db *sql.DB, ws, id string, in ResearchScheduleInput) (*ResearchSchedule, error) {
	cur, err := getSchedule(ctx, db, ws, id)
	if err != nil {
		return nil, err
	}
	name, topic, mode, cadence, urls, enabled := cur.Name, cur.Topic, cur.Mode, cur.Cadence, cur.URLs, cur.Enabled
	if in.Name != nil {
		name = *in.Name
	}
	if in.Topic != nil {
		topic = *in.Topic
	}
	if in.Mode != nil {
		mode = *in.Mode
	}
	if in.Cadence != nil {
		cadence = *in.Cadence
	}
	if in.URLs != nil {
		urls = *in.URLs
	}
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if urls, err = validateSchedule(name, topic, mode, cadence, urls); err != nil {
		return nil, err
	}
	// Turning a schedule back on makes it due now rather than at a time that
	// passed while it was off.
	if _, err := db.ExecContext(ctx, `
		UPDATE research_schedules SET name = $3, topic = $4, mode = $5, urls = $6, cadence = $7, enabled = $8,
			next_run_at = CASE WHEN $8 AND NOT enabled THEN now() ELSE next_run_at END, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		ws, id, strings.TrimSpace(name), strings.TrimSpace(topic), mode, pgArray(urls), cadence, enabled); err != nil {
		return nil, err
	}
	return getSchedule(ctx, db, ws, id)
}

// DeleteSchedule removes one. Its past runs stay, detached.
func DeleteSchedule(ctx context.Context, db *sql.DB, ws, id string) error {
	if _, err := getSchedule(ctx, db, ws, id); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `UPDATE research_runs SET schedule_id = NULL WHERE workspace_id = $1 AND schedule_id = $2`, ws, id); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DELETE FROM research_schedules WHERE workspace_id = $1 AND id = $2`, ws, id)
	return err
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(*p)
}

// ── budget ───────────────────────────────────────────────────────────────

func ensureSettings(ctx context.Context, db *sql.DB, ws string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO research_settings (workspace_id, daily_run_budget) VALUES ($1, $2)
		ON CONFLICT (workspace_id) DO NOTHING`, ws, DefaultDailyBudget)
	return err
}

// GetBudget reads the workspace's budget (today's count is 0 on a new day).
func GetBudget(ctx context.Context, db *sql.DB, ws string) (*ResearchBudget, error) {
	if err := ensureSettings(ctx, db, ws); err != nil {
		return nil, err
	}
	var b ResearchBudget
	var day time.Time
	if err := db.QueryRowContext(ctx, `
		SELECT daily_run_budget,
		       CASE WHEN budget_day = (now() AT TIME ZONE 'UTC')::date THEN runs_today ELSE 0 END,
		       (now() AT TIME ZONE 'UTC')::date
		FROM research_settings WHERE workspace_id = $1`, ws).Scan(&b.DailyRunBudget, &b.RunsToday, &day); err != nil {
		return nil, err
	}
	b.Day = day.Format("2006-01-02")
	return &b, nil
}

// SetBudget changes the daily allowance.
func SetBudget(ctx context.Context, db *sql.DB, ws string, n int) (*ResearchBudget, error) {
	if n < 0 || n > MaxDailyBudget {
		return nil, fmt.Errorf("the daily budget is 0 to %d runs", MaxDailyBudget)
	}
	if err := ensureSettings(ctx, db, ws); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE research_settings SET daily_run_budget = $2, updated_at = now() WHERE workspace_id = $1`, ws, n); err != nil {
		return nil, err
	}
	return GetBudget(ctx, db, ws)
}

// TakeBudget spends one automatic run of today's budget, atomically, or says
// it is spent.
func TakeBudget(ctx context.Context, db *sql.DB, ws string) (bool, error) {
	if err := ensureSettings(ctx, db, ws); err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `
		UPDATE research_settings SET
		  runs_today = CASE WHEN budget_day = (now() AT TIME ZONE 'UTC')::date THEN runs_today + 1 ELSE 1 END,
		  budget_day = (now() AT TIME ZONE 'UTC')::date, updated_at = now()
		WHERE workspace_id = $1
		  AND daily_run_budget > 0
		  AND (budget_day <> (now() AT TIME ZONE 'UTC')::date OR runs_today < daily_run_budget)`, ws)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RefundBudget gives back a run that was taken but could not start.
func RefundBudget(ctx context.Context, db *sql.DB, ws string) {
	_, _ = db.ExecContext(ctx, `UPDATE research_settings SET runs_today = greatest(runs_today - 1, 0), updated_at = now()
		WHERE workspace_id = $1 AND budget_day = (now() AT TIME ZONE 'UTC')::date`, ws)
}

// StartAutomatic is how anything without a person behind it starts research:
// it checks the tier, then spends the budget, then starts — and gives the
// budget back if the start fails. MH-397's discovery uses it too.
func (e *Engine) StartAutomatic(ctx context.Context, a *app.App, ws string, req Request) (string, error) {
	sctx := tenant.WithWorkspace(ctx, ws)
	caps := e.Caps()
	if req.Mode == "urls" && !caps.CanRead {
		return "", ErrNoReader
	}
	if req.Mode != "urls" && !caps.CanSearch {
		return "", ErrNoSearch
	}
	// Busy is checked before spending: a run refused for being second must not
	// cost the budget.
	var live int
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT count(*) FROM research_runs WHERE workspace_id = $1 AND status IN ('queued', 'running') AND author_kind = 'engine'`, ws).Scan(&live); err != nil {
		return "", err
	}
	if live >= maxLiveRuns {
		return "", ErrBusy
	}
	ok, err := TakeBudget(sctx, a.SQLDB, ws)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrOverBudget
	}
	id, err := e.StartRun(ctx, a, ws, "", req)
	if err != nil {
		RefundBudget(sctx, a.SQLDB, ws)
		return "", err
	}
	return id, nil
}

// ── the clock ────────────────────────────────────────────────────────────

// RunDueSchedules starts the schedules that are due and returns how many it
// looked at. The queue is read on the owner pool (a cross-tenant question, as
// in trends.CollectDue); everything after runs scoped to the workspace.
func (e *Engine) RunDueSchedules(ctx context.Context, a *app.App, limit int) int {
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT id, workspace_id FROM research_schedules
		WHERE enabled AND next_run_at <= now()
		ORDER BY next_run_at LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("research: could not read the schedule queue", "err", err)
		return 0
	}
	type due struct{ id, ws string }
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.id, &d.ws) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		e.runSchedule(ctx, a, d.ws, d.id)
	}
	return len(list)
}

func (e *Engine) runSchedule(ctx context.Context, a *app.App, ws, id string) {
	sctx := tenant.WithWorkspace(ctx, ws)
	s, err := getSchedule(sctx, a.SQLDB, ws, id)
	if err != nil {
		return
	}
	next := time.Now().Add(cadenceEvery(s.Cadence))
	status, note, runID := "started", "", ""
	if e.UsesAgent(sctx, a.SQLDB, ws) {
		// Agent mode: the schedule queues a task for the workspace's AI
		// assistant instead of running here. One waiting task per schedule.
		var waiting int
		_ = a.SQLDB.QueryRowContext(sctx, `SELECT count(*) FROM agent_tasks WHERE workspace_id = $1 AND schedule_id = $2 AND status IN ('pending', 'claimed')`,
			ws, s.ID).Scan(&waiting)
		if waiting > 0 {
			status, note = "busy", "the last task is still waiting for your AI assistant (opportunity_tasks)"
		} else if t, qerr := EnqueueAgentTask(sctx, a.SQLDB, ws, AgentTaskInput{Kind: "research", Topic: s.Topic, Mode: s.Mode, URLs: s.URLs,
			Origin: "schedule", ScheduleID: s.ID}); qerr != nil {
			status, note = "failed", trim(qerr.Error(), 300)
		} else {
			note = "queued for your AI assistant: task " + t.ID
		}
		e.recordSchedule(sctx, a, ws, id, next, status, note, "")
		return
	}
	runID, err = e.StartAutomatic(ctx, a, ws, Request{Topic: s.Topic, Mode: s.Mode, URLs: s.URLs, Origin: "schedule", ScheduleID: s.ID})
	switch {
	case err == nil:
	case errors.Is(err, ErrBusy):
		status, note, next = "busy", "another research run was going; retrying shortly", time.Now().Add(busyRetry)
	case errors.Is(err, ErrOverBudget):
		status, note = "over_budget", "today's research budget was spent; next try at the next scheduled time"
	case errors.Is(err, ErrNoSearch), errors.Is(err, ErrNoReader):
		status, note = "not_configured", err.Error()
	default:
		status, note = "failed", trim(err.Error(), 300)
	}
	e.recordSchedule(sctx, a, ws, id, next, status, note, runID)
}

func (e *Engine) recordSchedule(sctx context.Context, a *app.App, ws, id string, next time.Time, status, note, runID string) {
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE research_schedules SET next_run_at = $3, last_status = $4, last_note = $5,
			last_run_id = CASE WHEN $6 <> '' THEN $6 ELSE last_run_id END,
			last_run_at = CASE WHEN $6 <> '' THEN now() ELSE last_run_at END, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, ws, id, next, status, note, runID); err != nil {
		a.Log.Warn("research: could not record a schedule's run", "schedule", id, "err", err)
	}
}

// StartScheduler runs due schedules on a ticker for the life of the process.
// One in-process ticker, like the trends collector: the budget is atomic, so
// a double tick spends at most once per run that actually starts, but each
// tick still takes the cluster-wide advisory lock (internal/pglock) so a
// second replica does not do the same wasted work (and the tick hooks, e.g.
// MH-397's discovery schedules, run at most once per tick too).
func (e *Engine) StartScheduler(a *app.App, tick time.Duration) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		timer := time.NewTimer(20 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			_, err := pglock.WithLock(ctx, a.SysDB, "moharrik:research:scheduler", func() {
				if n := e.RunDueSchedules(ctx, a, 20); n > 0 {
					a.Log.Info("research: schedules looked at", "due", n)
				}
				for _, f := range tickHooks() {
					f(ctx, a)
				}
			})
			if err != nil {
				a.Log.Error("research scheduler: could not acquire the cluster lock", "err", err)
			}
			timer.Reset(tick)
		}
	}()
	return cancel
}

var tickFns []func(context.Context, *app.App)

// OnTick registers work for the research clock (MH-397's discovery schedules).
func OnTick(f func(context.Context, *app.App)) {
	afterMu.Lock()
	defer afterMu.Unlock()
	tickFns = append(tickFns, f)
}

func tickHooks() []func(context.Context, *app.App) {
	afterMu.RLock()
	defer afterMu.RUnlock()
	return append([]func(context.Context, *app.App){}, tickFns...)
}
