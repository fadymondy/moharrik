package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Bring-your-own-agent research: the calling agent is the provider.

A workspace with no search service (SEARXNG_URL) and no model key used to be
blocked: start_research and run_discovery answered "search is not
configured". In agent mode the work goes into a queue instead (agent_tasks).
An MCP client — Claude, Codex, anything — claims a task, does the searching,
reading and thinking with its own model and web tools, and hands back what it
found. Moharrik keeps what it always kept: the sources, the evidence, the
company suggestions, the scores, the CRM link. Nothing here calls a model or
the web.

The results are written exactly where a server run writes them — one
research_runs row (author_kind = 'agent', the agent's name and model on it),
research_sources, research_evidence, research_companies — so every reader
(get_research, find_evidence, find_opportunities, research_overview, the
pages) works unchanged, and the run's origin decides what happens when it
settles (a discovery run becomes prospects, through the same hook).

Three rules on top of research's own:

	EVERY CLAIM HAS A PUBLIC URL. An agent's evidence without an http(s)
	address on a public host is refused, not stored.

	AN AGENT'S QUOTE IS NOT VERIFIED HERE. Moharrik did not read the page, so
	agent evidence is capped at AgentMaxConfidence and says who extracted it
	(extractor = "agent:<name>/<model>").

	TEXT IS TEXT. Every string is stripped of markup and control characters
	before it is stored (SanitizeText).
*/

// Provider modes.
const (
	ProviderAuto   = "auto"
	ProviderServer = "server"
	ProviderAgent  = "agent"
)

// AgentMaxConfidence caps the confidence of evidence Moharrik did not read itself.
const AgentMaxConfidence = 0.7

// AgentLease is how long a claimed task is the claimant's before another agent may take it.
const AgentLease = 30 * time.Minute

// AgentMaxAttempts: a task whose lease ran out this many times is failed, not handed out again.
const AgentMaxAttempts = 3

var (
	ErrNoTask      = errors.New("no such agent task")
	ErrTaskClosed  = errors.New("that task is already finished")
	ErrTaskLeased  = errors.New("another agent holds that task; claim a different one with opportunity_tasks")
	ErrBadProvider = errors.New("provider mode is auto, server or agent")
)

// Provider is who does a workspace's research, and why.
type Provider struct {
	Mode      string `json:"mode" enum:"auto,server,agent" doc:"What the workspace chose."`
	Effective string `json:"effective" enum:"server,agent" doc:"Who actually does it: the server when its search is configured and the mode allows, else the caller's agent over MCP."`
	CanServer bool   `json:"can_server" doc:"This deployment has a search service, so server mode can run."`
	Pending   int    `json:"pending_tasks" doc:"Tasks waiting for an agent."`
	Claimed   int    `json:"claimed_tasks" doc:"Tasks an agent is working on now."`
}

// EffectiveProvider resolves a stored mode against what the deployment can do.
// server without a search service falls back to agent: there is nobody else.
func EffectiveProvider(mode string, caps Capabilities) string {
	if mode == ProviderAgent || !caps.CanSearch {
		return ProviderAgent
	}
	return ProviderServer
}

// GetProvider reads the workspace's provider mode and its queue.
func (e *Engine) GetProvider(ctx context.Context, db *sql.DB, ws string) (*Provider, error) {
	if err := ensureSettings(ctx, db, ws); err != nil {
		return nil, err
	}
	p := &Provider{}
	if err := db.QueryRowContext(ctx, `SELECT provider_mode FROM research_settings WHERE workspace_id = $1`, ws).Scan(&p.Mode); err != nil {
		return nil, err
	}
	caps := e.Caps()
	p.CanServer = caps.CanSearch
	p.Effective = EffectiveProvider(p.Mode, caps)
	if err := db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE status = 'pending'),
			count(*) FILTER (WHERE status = 'claimed' AND lease_until > now())
		FROM agent_tasks WHERE workspace_id = $1`, ws).Scan(&p.Pending, &p.Claimed); err != nil {
		return nil, err
	}
	return p, nil
}

// SetProvider changes the workspace's provider mode.
func (e *Engine) SetProvider(ctx context.Context, db *sql.DB, ws, mode string) (*Provider, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != ProviderAuto && mode != ProviderServer && mode != ProviderAgent {
		return nil, ErrBadProvider
	}
	if err := ensureSettings(ctx, db, ws); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE research_settings SET provider_mode = $2, updated_at = now() WHERE workspace_id = $1`, ws, mode); err != nil {
		return nil, err
	}
	return e.GetProvider(ctx, db, ws)
}

// UsesAgent reports whether the workspace's research goes to the caller's agent.
// A nil engine has no server tier, so it always does.
func (e *Engine) UsesAgent(ctx context.Context, db *sql.DB, ws string) bool {
	p, err := e.GetProvider(ctx, db, ws)
	if err != nil {
		return !e.Caps().CanSearch
	}
	return p.Effective == ProviderAgent
}

// ── the queue ────────────────────────────────────────────────────────────

// AgentTask is one job for an agent.
type AgentTask struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind" enum:"discovery,research"`
	Status      string     `json:"status" enum:"pending,claimed,done,failed,cancelled"`
	Topic       string     `json:"topic"`
	Mode        string     `json:"mode" enum:"search,urls"`
	URLs        []string   `json:"urls"`
	Origin      string     `json:"origin" enum:"manual,schedule,discovery,assistant"`
	RunID       string     `json:"run_id" doc:"The research run holding its results, once something was submitted."`
	AgentName   string     `json:"agent_name"`
	AgentModel  string     `json:"agent_model"`
	LeaseUntil  *time.Time `json:"lease_until"`
	Attempts    int        `json:"attempts"`
	Received    int        `json:"received" doc:"Candidates or findings the agent sent."`
	Stored      int        `json:"stored" doc:"How many of them were stored."`
	Note        string     `json:"note"`
	Error       string     `json:"error"`
	CreatedAt   time.Time  `json:"created_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	claimedBy   string
	requestedBy string
}

// AgentTaskInput is a job to queue.
type AgentTaskInput struct {
	Kind        string
	Topic       string
	Mode        string
	URLs        []string
	Origin      string
	ScheduleID  string
	RequestedBy string
}

const agentTaskColumns = `id, kind, status, topic, mode, urls::text, origin, run_id, agent_name, agent_model, lease_until, attempts,
	received, stored, note, error, created_at, finished_at, claimed_by, requested_by`

func scanAgentTask(row interface{ Scan(...any) error }) (AgentTask, error) {
	var t AgentTask
	var urls string
	var lease, fin sql.NullTime
	err := row.Scan(&t.ID, &t.Kind, &t.Status, &t.Topic, &t.Mode, &urls, &t.Origin, &t.RunID, &t.AgentName, &t.AgentModel, &lease,
		&t.Attempts, &t.Received, &t.Stored, &t.Note, &t.Error, &t.CreatedAt, &fin, &t.claimedBy, &t.requestedBy)
	t.URLs = parsePgArray(urls)
	if lease.Valid {
		t.LeaseUntil = &lease.Time
	}
	if fin.Valid {
		t.FinishedAt = &fin.Time
	}
	return t, err
}

// EnqueueAgentTask queues a job. A discovery job is not queued twice: while
// one is waiting, that one is returned.
func EnqueueAgentTask(ctx context.Context, db *sql.DB, ws string, in AgentTaskInput) (*AgentTask, error) {
	topic := strings.TrimSpace(SanitizeText(in.Topic, 500))
	if topic == "" {
		return nil, errors.New("a task needs a question")
	}
	if in.Kind != "discovery" && in.Kind != "research" {
		return nil, errors.New("a task is discovery or research")
	}
	mode := in.Mode
	if mode == "" {
		mode = "search"
	}
	var urls []string
	switch mode {
	case "search":
	case "urls":
		var err error
		if urls, err = NormaliseURLs(in.URLs); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("mode is search or urls")
	}
	origin := in.Origin
	if origin == "" {
		origin = "manual"
	}
	if in.Kind == "discovery" {
		t, err := scanAgentTask(db.QueryRowContext(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks
			WHERE workspace_id = $1 AND kind = 'discovery' AND status = 'pending' AND topic = $2 ORDER BY created_at LIMIT 1`, ws, topic))
		if err == nil {
			return &t, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	t, err := scanAgentTask(db.QueryRowContext(ctx, `INSERT INTO agent_tasks (workspace_id, kind, topic, mode, urls, origin, schedule_id, requested_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING `+agentTaskColumns,
		ws, in.Kind, topic, mode, pgArray(urls), origin, in.ScheduleID, in.RequestedBy))
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// expireLeases hands back tasks whose lease ran out, and fails the ones that
// ran out too often.
func expireLeases(ctx context.Context, db *sql.DB, ws string) error {
	if _, err := db.ExecContext(ctx, `UPDATE agent_tasks SET status = 'failed', error = 'the agent never finished it (lease expired too often)',
			finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND status = 'claimed' AND lease_until < now() AND attempts >= $2`, ws, AgentMaxAttempts); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `UPDATE agent_tasks SET status = 'pending', claimed_by = '', updated_at = now()
		WHERE workspace_id = $1 AND status = 'claimed' AND lease_until < now()`, ws)
	return err
}

// Claimant is who takes a task: the credential, and what the agent says it is.
type Claimant struct {
	TokenID string
	Agent   string
	Model   string
}

// ClaimAgentTasks leases up to limit waiting tasks (oldest first) to the
// claimant, and returns them with the ones it already holds. kind "" is any.
func ClaimAgentTasks(ctx context.Context, db *sql.DB, ws string, c Claimant, kind string, limit int) ([]AgentTask, error) {
	if limit <= 0 {
		limit = 1
	}
	if limit > 5 {
		limit = 5
	}
	if err := expireLeases(ctx, db, ws); err != nil {
		return nil, err
	}
	out := []AgentTask{}
	// Already this claimant's: renew the lease and hand them back first.
	rows, err := db.QueryContext(ctx, `UPDATE agent_tasks SET lease_until = now() + $3::interval, updated_at = now()
		WHERE workspace_id = $1 AND status = 'claimed' AND claimed_by = $2 AND ($4 = '' OR kind = $4)
		RETURNING `+agentTaskColumns, ws, c.TokenID, leaseInterval(), kind)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		t, err := scanAgentTask(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	rows.Close()
	if len(out) >= limit {
		return out[:limit], nil
	}
	rows, err = db.QueryContext(ctx, `UPDATE agent_tasks SET status = 'claimed', claimed_by = $2, agent_name = $3, agent_model = $4,
			lease_until = now() + $5::interval, attempts = attempts + 1, updated_at = now()
		WHERE workspace_id = $1 AND id IN (
			SELECT id FROM agent_tasks WHERE workspace_id = $1 AND status = 'pending' AND ($6 = '' OR kind = $6)
			ORDER BY created_at LIMIT $7 FOR UPDATE SKIP LOCKED)
		RETURNING `+agentTaskColumns, ws, c.TokenID, trim(c.Agent, 80), trim(c.Model, 80), leaseInterval(), kind, limit-len(out))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanAgentTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func leaseInterval() string { return fmt.Sprintf("%d seconds", int(AgentLease.Seconds())) }

// GetAgentTask reads one task.
func GetAgentTask(ctx context.Context, db *sql.DB, ws, id string) (*AgentTask, error) {
	t, err := scanAgentTask(db.QueryRowContext(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE workspace_id = $1 AND id = $2`, ws, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoTask
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListAgentTasks returns the workspace's recent tasks, newest first.
func ListAgentTasks(ctx context.Context, db *sql.DB, ws string, limit int) ([]AgentTask, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	if err := expireLeases(ctx, db, ws); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT `+agentTaskColumns+` FROM agent_tasks WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT $2`, ws, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentTask{}
	for rows.Next() {
		t, err := scanAgentTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// HoldAgentTask makes sure the claimant may write to a task: it is open, and
// it is theirs (a waiting task is claimed on the spot). The lease is renewed.
func HoldAgentTask(ctx context.Context, db *sql.DB, ws, id string, c Claimant) (*AgentTask, error) {
	if err := expireLeases(ctx, db, ws); err != nil {
		return nil, err
	}
	t, err := GetAgentTask(ctx, db, ws, id)
	if err != nil {
		return nil, err
	}
	switch t.Status {
	case "done", "failed", "cancelled":
		return nil, ErrTaskClosed
	case "claimed":
		if t.claimedBy != c.TokenID {
			return nil, ErrTaskLeased
		}
	}
	res, err := db.ExecContext(ctx, `UPDATE agent_tasks SET status = 'claimed', claimed_by = $3,
			agent_name = CASE WHEN $4 <> '' THEN $4 ELSE agent_name END,
			agent_model = CASE WHEN $5 <> '' THEN $5 ELSE agent_model END,
			attempts = CASE WHEN status = 'pending' THEN attempts + 1 ELSE attempts END,
			lease_until = now() + $6::interval, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND (status = 'pending' OR (status = 'claimed' AND claimed_by = $3))`,
		ws, t.ID, c.TokenID, trim(c.Agent, 80), trim(c.Model, 80), leaseInterval())
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTaskLeased
	}
	return GetAgentTask(ctx, db, ws, t.ID)
}

// AgentRun returns the task's research run, creating it on the first
// submission: status running, author_kind agent, the agent's name and model.
func AgentRun(ctx context.Context, db *sql.DB, ws string, t *AgentTask, c Claimant) (string, error) {
	if t.RunID != "" {
		return t.RunID, nil
	}
	origin := "manual"
	switch {
	case t.Kind == "discovery":
		origin = "discovery"
	case t.Origin == "schedule":
		origin = "schedule"
	}
	stages := []ResearchStage{}
	now := time.Now().UTC()
	for _, n := range []string{"plan", "search", "read", "extract", "summarise"} {
		stages = append(stages, ResearchStage{Stage: n, Status: "done", Note: "done by the agent (" + agentLabel(c) + ")", StartedAt: &now, FinishedAt: &now})
	}
	sjb, _ := json.Marshal(stages)
	sj := string(sjb)
	var runID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO research_runs (workspace_id, topic, query, status, requested_by, mode, urls, origin, stages, model,
			author_kind, agent_name, agent_model, started_at)
		VALUES ($1, $2, $2, 'running', $3, $4, $5, $6, $7::jsonb, $8, 'agent', $9, $10, now()) RETURNING id`,
		ws, t.Topic, t.requestedBy, t.Mode, pgArray(t.URLs), origin, sj, "agent:"+agentLabel(c), trim(c.Agent, 80), trim(c.Model, 80)).Scan(&runID); err != nil {
		return "", err
	}
	res, err := db.ExecContext(ctx, `UPDATE agent_tasks SET run_id = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2 AND run_id = ''`, ws, t.ID, runID)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Another submission created it first: use that one, drop ours.
		_, _ = db.ExecContext(ctx, `DELETE FROM research_runs WHERE workspace_id = $1 AND id = $2`, ws, runID)
		var existing string
		if err := db.QueryRowContext(ctx, `SELECT run_id FROM agent_tasks WHERE workspace_id = $1 AND id = $2`, ws, t.ID).Scan(&existing); err != nil {
			return "", err
		}
		runID = existing
	}
	t.RunID = runID
	return runID, nil
}

func agentLabel(c Claimant) string {
	name := strings.TrimSpace(c.Agent)
	if name == "" {
		name = "agent"
	}
	if m := strings.TrimSpace(c.Model); m != "" {
		name += "/" + m
	}
	return trim(name, 120)
}

// AgentLabel is the extractor/generator label for an agent's work.
func AgentLabel(c Claimant) string { return "agent:" + agentLabel(c) }

// AgentFinding is one piece of evidence an agent submits.
type AgentFinding struct {
	URL           string
	Title         string
	Quote         string
	Claim         string
	CompanyName   string
	CompanyDomain string
	SignalType    string
	Confidence    float64
	PublishedAt   *time.Time
}

// StoreAgentFindings writes findings into the run: a source per new URL, the
// evidence (the same quote from the same page once), and the company
// suggestions the evidence names. Returns how many evidence rows were new and
// the companies seen for the first time.
func StoreAgentFindings(ctx context.Context, db *sql.DB, ws, runID string, c Claimant, findings []AgentFinding) (int, []string, error) {
	var pos int
	if err := db.QueryRowContext(ctx, `SELECT coalesce(max(position) + 1, 0) FROM research_sources WHERE workspace_id = $1 AND run_id = $2`, ws, runID).Scan(&pos); err != nil {
		return 0, nil, err
	}
	sources := []ResearchSource{}
	byURL := map[string]int{}
	rows, err := db.QueryContext(ctx, `SELECT id, position, url, title FROM research_sources WHERE workspace_id = $1 AND run_id = $2`, ws, runID)
	if err != nil {
		return 0, nil, err
	}
	for rows.Next() {
		var s ResearchSource
		if err := rows.Scan(&s.ID, &s.Position, &s.URL, &s.Title); err != nil {
			rows.Close()
			return 0, nil, err
		}
		byURL[s.URL] = s.Position
		sources = append(sources, s)
	}
	rows.Close()

	extractor := AgentLabel(c)
	var ev []sourced
	for _, f := range findings {
		p, ok := byURL[f.URL]
		if !ok {
			src := ResearchSource{Position: pos, URL: f.URL, Title: trim(f.Title, 300), Snippet: trim(f.Quote, 1000)}
			if src.Title == "" {
				src.Title = hostOf(f.URL)
			}
			if err := db.QueryRowContext(ctx, `
				INSERT INTO research_sources (workspace_id, run_id, position, url, title, snippet, content, fetched, error)
				VALUES ($1, $2, $3, $4, $5, $6, '', false, $7) RETURNING id`,
				ws, runID, src.Position, src.URL, src.Title, src.Snippet, "read by the agent, not by Moharrik").Scan(&src.ID); err != nil {
				return 0, nil, err
			}
			byURL[f.URL] = pos
			p = pos
			sources = append(sources, src)
			pos++
		}
		conf := f.Confidence
		if conf <= 0 {
			conf = 0.6
		}
		if f.SignalType != "" {
			conf += 0.05
		}
		if conf > AgentMaxConfidence {
			conf = AgentMaxConfidence
		}
		ev = append(ev, sourced{pos: p, x: Extracted{Kind: "claim", Quote: trim(f.Quote, 1000), Claim: trim(f.Claim, 500),
			CompanyName: trim(f.CompanyName, 120), CompanyDomain: RegistrableDomain(f.CompanyDomain), SignalType: f.SignalType,
			Confidence: round2(conf), PublishedAt: f.PublishedAt, Extractor: extractor}})
	}
	stored, err := storeEvidence(ctx, db, ws, runID, sources, ev)
	if err != nil {
		return stored, nil, err
	}
	fresh, err := upsertCompanies(ctx, db, ws, runID)
	if err != nil {
		return stored, nil, err
	}
	_, err = db.ExecContext(ctx, `UPDATE research_runs SET evidence_count = (SELECT count(*) FROM research_evidence WHERE workspace_id = $1 AND run_id = $2),
			found_count = (SELECT count(*) FROM research_sources WHERE workspace_id = $1 AND run_id = $2), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, ws, runID)
	return stored, fresh, err
}

// SetAgentSummary stores the agent's answer on the run.
func SetAgentSummary(ctx context.Context, db *sql.DB, ws, runID, summary string) error {
	_, err := db.ExecContext(ctx, `UPDATE research_runs SET summary = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		ws, runID, trim(summary, 8000))
	return err
}

// CountAgentSubmitted adds to the task's counters.
func CountAgentSubmitted(ctx context.Context, db *sql.DB, ws, taskID string, received, stored int) error {
	_, err := db.ExecContext(ctx, `UPDATE agent_tasks SET received = received + $3, stored = stored + $4, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, ws, taskID, received, stored)
	return err
}

// CloseAgentTask finishes a task (done or failed) and settles its run the way
// a server run settles: the run is finished, a finding is announced, and the
// run-finished hooks fire with its origin — so a discovery task's companies
// become prospects exactly as a discovery run's do.
func CloseAgentTask(ctx context.Context, a *app.App, ws, id string, c Claimant, status, note string) (*AgentTask, error) {
	if status != "done" && status != "failed" {
		return nil, errors.New("a task closes as done or failed")
	}
	t, err := HoldAgentTask(ctx, a.SQLDB, ws, id, c)
	if err != nil {
		return nil, err
	}
	note = SanitizeText(note, 500)
	errText := ""
	if status == "failed" {
		errText, note = note, ""
		if errText == "" {
			errText = "the agent gave up"
		}
	}
	res, err := a.SQLDB.ExecContext(ctx, `UPDATE agent_tasks SET status = $3, note = $4, error = $5, lease_until = NULL, finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'claimed' AND claimed_by = $6`, ws, t.ID, status, note, errText, c.TokenID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrTaskLeased
	}
	if t.RunID != "" {
		settleAgentRun(ctx, a, ws, t, status, errText)
	}
	return GetAgentTask(ctx, a.SQLDB, ws, t.ID)
}

func settleAgentRun(ctx context.Context, a *app.App, ws string, t *AgentTask, status, errText string) {
	sctx := tenant.WithWorkspace(ctx, ws)
	var evidence int
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT count(*) FROM research_evidence WHERE workspace_id = $1 AND run_id = $2`, ws, t.RunID).Scan(&evidence)
	runStatus := "ok"
	if status == "failed" && evidence == 0 {
		runStatus = "failed"
	}
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE research_runs SET status = $3, error = $4, stage = '', finished_at = now(), updated_at = now(),
			read_count = 0
		WHERE workspace_id = $1 AND id = $2 AND status IN ('queued', 'running')`, ws, t.RunID, runStatus, trim(errText, 500)); err != nil {
		a.Log.Warn("research: could not settle an agent run", "run", t.RunID, "err", err)
		return
	}
	if runStatus != "ok" {
		return
	}
	var fresh []string
	rows, err := a.SQLDB.QueryContext(sctx, `SELECT name FROM research_companies WHERE workspace_id = $1 AND first_run_id = $2 AND status = 'suggested'`, ws, t.RunID)
	if err == nil {
		for rows.Next() {
			var n string
			if rows.Scan(&n) == nil {
				fresh = append(fresh, n)
			}
		}
		rows.Close()
	}
	notifyFindings(sctx, a, ws, t.RunID, t.Topic, fresh)
	origin := "manual"
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT origin FROM research_runs WHERE workspace_id = $1 AND id = $2`, ws, t.RunID).Scan(&origin)
	afterMu.RLock()
	hooks := append([]func(context.Context, *app.App, string, string, string){}, afterRun...)
	afterMu.RUnlock()
	for _, h := range hooks {
		h(sctx, a, ws, t.RunID, origin)
	}
}

// CancelAgentTask is a person withdrawing a task that has not finished.
func CancelAgentTask(ctx context.Context, db *sql.DB, ws, id string) error {
	res, err := db.ExecContext(ctx, `UPDATE agent_tasks SET status = 'cancelled', lease_until = NULL, finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status IN ('pending', 'claimed')`, ws, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTaskClosed
	}
	_, _ = db.ExecContext(ctx, `UPDATE research_runs SET status = 'cancelled', stage = '', finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = (SELECT run_id FROM agent_tasks WHERE workspace_id = $1 AND id = $2) AND status IN ('queued', 'running')`,
		ws, strings.TrimSpace(id))
	return nil
}

// ── what an agent may hand us ────────────────────────────────────────────

var (
	agentMarkup     = regexp.MustCompile(`(?s)<\s*(script|style)[^>]*>.*?<\s*/\s*(script|style)\s*>|<[^>]*>`)
	agentSpaces    = regexp.MustCompile(`[ \t]+`)
	agentBlankLines  = regexp.MustCompile(`\n{3,}`)
	privateHost = []string{"localhost", ".local", ".internal", ".lan", ".home", ".corp", ".localdomain", ".test", ".invalid", ".example"}
)

// SanitizeText makes agent-supplied text safe to store and show: markup and
// script removed, entities decoded, control characters dropped, whitespace
// collapsed, cut at max runes.
func SanitizeText(s string, max int) string {
	s = clean(s)
	for i := 0; i < 2; i++ { // twice: &lt;b&gt; decodes into a tag
		s = agentMarkup.ReplaceAllString(s, " ")
		s = html.UnescapeString(s)
	}
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t' || r == '\r':
			return ' '
		case unicode.IsControl(r), r == '‎' || r == '‏' || (r >= '‪' && r <= '‮') || (r >= '⁦' && r <= '⁩'):
			return -1
		}
		return r
	}, s)
	s = agentSpaces.ReplaceAllString(s, " ")
	s = agentBlankLines.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if max > 0 {
		if r := []rune(s); len(r) > max {
			s = strings.TrimSpace(string(r[:max]))
		}
	}
	return s
}

// PublicURL accepts an http(s) address on a named public host and returns it
// normalised. It refuses everything else: other schemes, credentials in the
// URL, IP literals, localhost, private and reserved suffixes (.local,
// .internal, .test, .example, …).
func PublicURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("a URL is required")
	}
	if len(raw) > 2000 {
		return "", errors.New("that URL is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not a web address (http:// or https://)", trim(raw, 80))
	}
	if u.User != nil {
		return "", errors.New("a URL with a username or password in it is refused")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return "", fmt.Errorf("%q is an IP address; give the site's name", host)
	}
	if !strings.Contains(host, ".") {
		return "", fmt.Errorf("%q is not a public web address", host)
	}
	for _, p := range privateHost {
		if host == strings.TrimPrefix(p, ".") || strings.HasSuffix(host, p) {
			return "", fmt.Errorf("%q is not a public web address", host)
		}
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

// ValidSignal reports whether s is one of SignalTypes (or empty).
func ValidSignal(s string) bool { return s == "" || validSignal(s) }
