package research

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
A research run (MH-227, MH-388): search or read, extract, summarise — and keep
the evidence.

	SearXNG    finds the pages (or the run is given a list of URLs)
	Firecrawl  reads them, in a real browser, so JavaScript pages come back as text
	extraction turns each page into quotes and claims, with the company each is about
	a model    writes the answer, from those pages only

Four rules shape this file.

	THE SOURCES OUTLIVE THE SUMMARY. Every page found is stored with its URL and
	its position, whether or not it could be read.

	THE MODEL IS OPTIONAL. No provider configured is a supported way to run
	this: extraction falls back to verbatim sentences, and the pages and their
	evidence are the answer.

	NOTHING IS INVENTED. A quote is always the page's own words; a model's
	claim is kept only when its quote is really on the page.

	EVERY STAGE SAYS HOW IT WENT. A run records each stage — plan, search (or
	the URL list), read, extract, summarise — with a count and a note, and what
	it spent, so "why is this thin?" is answered on the page.
*/

const (
	// How many search results a run keeps.
	MaxFindings = 8

	// How many of them are actually fetched.
	MaxReads = 5

	// How many URLs a URL-list run may be given. Each is a browser render.
	MaxURLs = 10

	// Per source.
	maxSourceChars = 12000

	// Per PROMPT, across every source together (see promptBudget).
	defaultPromptChars = 24000

	// The whole run.
	RunDeadline = 5 * time.Minute

	// One run at a time per workspace.
	maxLiveRuns = 1
)

// ErrBusy is one-run-at-a-time answering.
var ErrBusy = errors.New("this workspace is already running some research")

// ErrNoReader: a URL-list run reads pages and nothing else, so it needs the reader.
var ErrNoReader = errors.New("no page reader is configured for this deployment (FIRECRAWL_URL), so a list of URLs cannot be read")

// ResearchRun is the record of one question.
type ResearchRun struct {
	ID            string          `json:"id"`
	Topic         string          `json:"topic"`
	Query         string          `json:"query"`
	Status        string          `json:"status"`
	Summary       string          `json:"summary"`
	Model         string          `json:"model"`
	Error         string          `json:"error"`
	FoundCount    int             `json:"found_count"`
	ReadCount     int             `json:"read_count"`
	RequestedBy   string          `json:"requested_by"`
	Mode          string          `json:"mode" enum:"search,urls"`
	URLs          []string        `json:"urls"`
	Origin        string          `json:"origin" enum:"manual,schedule,discovery"`
	ScheduleID    string          `json:"schedule_id"`
	Stage         string          `json:"stage" doc:"The stage it is in while it runs; empty once settled."`
	Stages        []ResearchStage `json:"stages"`
	Cost          ResearchCost    `json:"cost"`
	EvidenceCount int             `json:"evidence_count"`
	StartedAt     *time.Time      `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Stage is one step of a run, as it went.
type ResearchStage struct {
	Stage      string     `json:"stage" enum:"plan,search,urls,read,extract,summarise"`
	Status     string     `json:"status" enum:"pending,running,done,skipped,failed"`
	Count      int        `json:"count"`
	Note       string     `json:"note"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// Cost is what a run used. Units, never a price: this code does not know what
// a query or a token costs this deployment, and a made-up figure would be worse
// than none.
type ResearchCost struct {
	SearchQueries int      `json:"search_queries"`
	PagesTried    int      `json:"pages_tried"`
	PagesRead     int      `json:"pages_read"`
	ModelCalls    int      `json:"model_calls"`
	PaidCalls     int      `json:"paid_calls"`
	InputTokens   int      `json:"input_tokens"`
	OutputTokens  int      `json:"output_tokens"`
	Providers     []string `json:"providers"`
}

// ResearchSource is one page that went into an answer.
type ResearchSource struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Snippet  string `json:"snippet"`
	Content  string `json:"content,omitempty"`
	Fetched  bool   `json:"fetched"`
	Error    string `json:"error,omitempty"`
}

// Request is what to research.
type Request struct {
	Topic      string
	Mode       string // search (default) | urls
	URLs       []string
	Origin     string // manual (default) | schedule | discovery
	ScheduleID string
}

// Engine holds what a run needs. Built once at boot.
type Engine struct {
	Search *Searcher
	Read   *Reader
	AI     *ai.Client
	Log    *slog.Logger
}

// AfterRun is called once a run has settled with status ok, with its origin.
// Prospect discovery (MH-397) registers here; research does not import it.
var (
	afterMu  sync.RWMutex
	afterRun []func(ctx context.Context, a *app.App, workspaceID, runID, origin string)
)

// OnRunFinished registers a hook for settled runs.
func OnRunFinished(f func(ctx context.Context, a *app.App, workspaceID, runID, origin string)) {
	afterMu.Lock()
	defer afterMu.Unlock()
	afterRun = append(afterRun, f)
}

// NewEngine wires the tiers from the environment. Each is independently optional.
func NewEngine(log *slog.Logger) *Engine {
	e := &Engine{Search: NewSearcher(), Read: NewReader(), Log: log}

	// An EMPTY configuration is valid to ai.New — it returns a client whose
	// every call fails. Keeping that client would make the engine report that
	// it can summarise and then fail at the end of every run. So no providers
	// means no client.
	cfg, err := ai.LoadConfig()
	if err != nil {
		log.Warn("research: AI config unreadable, running without a model", "err", err)
		return e
	}
	if len(cfg.Providers) == 0 {
		log.Info("research: no model configured (AI_PROVIDERS is empty); runs collect and extract evidence without a model")
		return e
	}
	client, err := ai.New(cfg, log)
	if err != nil {
		log.Warn("research: no model configured", "err", err)
		return e
	}
	e.AI = client
	return e
}

// Capabilities is what this deployment can do, for the UI and the tools.
type Capabilities struct {
	CanSearch    bool `json:"can_search"`
	CanRead      bool `json:"can_read"`
	CanSummarise bool `json:"can_summarise"`
}

// Caps reports the engine's tiers. A nil engine can do nothing.
func (e *Engine) Caps() Capabilities {
	if e == nil {
		return Capabilities{}
	}
	return Capabilities{CanSearch: e.Search != nil, CanRead: e.Read != nil, CanSummarise: e.AI != nil}
}

// Start is a manual search run — the original entry point.
func (e *Engine) Start(ctx context.Context, a *app.App, workspaceID, userID, topic string) (string, error) {
	return e.StartRun(ctx, a, workspaceID, userID, Request{Topic: topic})
}

// NormaliseURLs checks and de-duplicates a URL list.
func NormaliseURLs(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("%q is not a web address (it needs http:// or https://)", trim(raw, 80))
		}
		s := u.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("a URL-list run needs at least one address")
	}
	if len(out) > MaxURLs {
		return nil, fmt.Errorf("at most %d addresses per run", MaxURLs)
	}
	return out, nil
}

// StartRun records the run and does the work in the background.
func (e *Engine) StartRun(ctx context.Context, a *app.App, workspaceID, userID string, req Request) (string, error) {
	if e == nil {
		return "", ErrNoSearch
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		return "", errors.New("a research run needs a question")
	}
	if len([]rune(topic)) > 500 {
		return "", errors.New("that question is too long")
	}
	mode := req.Mode
	if mode == "" {
		mode = "search"
	}
	origin := req.Origin
	if origin == "" {
		origin = "manual"
	}
	var urls []string
	switch mode {
	case "search":
		if e.Search == nil {
			return "", ErrNoSearch
		}
	case "urls":
		var err error
		if urls, err = NormaliseURLs(req.URLs); err != nil {
			return "", err
		}
		if e.Read == nil {
			return "", ErrNoReader
		}
	default:
		return "", errors.New("mode is search or urls")
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)

	var live int
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM research_runs
		WHERE workspace_id = $1 AND status IN ('queued', 'running') AND author_kind = 'engine'`, workspaceID).Scan(&live); err != nil {
		return "", err
	}
	if live >= maxLiveRuns {
		return "", ErrBusy
	}

	stages := initialStages(mode)
	sj, _ := json.Marshal(stages)
	var runID string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO research_runs (workspace_id, topic, query, status, requested_by, mode, urls, origin, schedule_id, stages)
		VALUES ($1, $2, $2, 'queued', $3, $4, $5, $6, $7, $8::jsonb) RETURNING id`,
		workspaceID, topic, userID, mode, pgArray(urls), origin, nullable(req.ScheduleID), string(sj)).Scan(&runID); err != nil {
		return "", err
	}

	go e.execute(a, workspaceID, runID, topic, mode, urls, origin)
	return runID, nil
}

func initialStages(mode string) []ResearchStage {
	find := "search"
	if mode == "urls" {
		find = "urls"
	}
	names := []string{"plan", find, "read", "extract", "summarise"}
	out := make([]ResearchStage, len(names))
	for i, n := range names {
		out[i] = ResearchStage{Stage: n, Status: "pending"}
	}
	return out
}

// progress is a run's live record: its stages and its spend, written to the
// row as they change so a page polling the run sees where it is.
type progress struct {
	e      *Engine
	a      *app.App
	ctx    context.Context
	ws, id string
	stages []ResearchStage
	cost   ResearchCost
	aiRun  *ai.Run
}

func (p *progress) find(name string) *ResearchStage {
	for i := range p.stages {
		if p.stages[i].Stage == name {
			return &p.stages[i]
		}
	}
	return nil
}

func (p *progress) begin(name string) {
	if s := p.find(name); s != nil {
		now := time.Now().UTC()
		s.Status, s.StartedAt = "running", &now
	}
	p.save(name)
}

func (p *progress) end(name, status string, count int, note string) {
	if s := p.find(name); s != nil {
		now := time.Now().UTC()
		if s.StartedAt == nil {
			s.StartedAt = &now
		}
		s.Status, s.Count, s.Note, s.FinishedAt = status, count, trim(note, 300), &now
	}
	p.save("")
}

func (p *progress) save(current string) {
	if p.aiRun != nil {
		st := p.aiRun.Stats()
		p.cost.ModelCalls = st.PaidCalls + st.FreeCalls
		p.cost.PaidCalls = st.PaidCalls
		p.cost.InputTokens, p.cost.OutputTokens = st.InputTokens, st.OutputTokens
		p.cost.Providers = p.cost.Providers[:0]
		for name := range st.ByProvider {
			p.cost.Providers = append(p.cost.Providers, name)
		}
	}
	sj, _ := json.Marshal(p.stages)
	cj, _ := json.Marshal(p.cost)
	if _, err := p.a.SQLDB.ExecContext(p.ctx, `
		UPDATE research_runs SET stage = $3, stages = $4::jsonb, cost = $5::jsonb, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, p.ws, p.id, current, string(sj), string(cj)); err != nil {
		p.e.Log.Warn("research: could not record progress", "run", p.id, "err", err)
	}
}

// skipRest marks every stage still pending as skipped, with why.
func (p *progress) skipRest(note string) {
	for i := range p.stages {
		if p.stages[i].Status == "pending" || p.stages[i].Status == "running" {
			p.stages[i].Status = "skipped"
			p.stages[i].Note = note
		}
	}
	p.save("")
}

// execute is the run itself.
func (e *Engine) execute(a *app.App, workspaceID, runID, topic, mode string, urls []string, origin string) {
	ctx, cancel := context.WithTimeout(context.Background(), RunDeadline)
	defer cancel()
	aiRun := ai.NewRun(runID, -1)
	ctx = ai.WithRun(ctx, aiRun)
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	pr := &progress{e: e, a: a, ctx: sctx, ws: workspaceID, id: runID, stages: initialStages(mode), aiRun: aiRun}

	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE research_runs SET status = 'running', started_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'queued'`, workspaceID, runID)

	// 1. Plan: what is searched, and what makes a sentence on-topic (MH-466).
	pr.begin("plan")
	plan := PlanQuery(topic)
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE research_runs SET query = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID, trim(plan.Summary(), 1000))
	pr.end("plan", "done", len(plan.Queries), plan.Summary())

	// 2. Find the pages: a search, or the list given.
	var findings []Finding
	if mode == "urls" {
		pr.begin("urls")
		for _, u := range urls {
			findings = append(findings, Finding{URL: u})
		}
		pr.end("urls", "done", len(findings), "")
	} else {
		pr.begin("search")
		f, queries, err := e.Search.SearchPlanCounted(ctx, plan, MaxFindings)
		pr.cost.SearchQueries = queries
		if err != nil {
			pr.end("search", "failed", 0, err.Error())
			pr.skipRest("the search failed")
			e.finish(sctx, a, workspaceID, runID, "failed", err.Error(), "", "", 0, 0)
			return
		}
		findings = f
		pr.end("search", "done", len(findings), fmt.Sprintf("%d quer%s sent", queries, plural(queries, "y", "ies")))
	}
	if len(findings) == 0 {
		pr.skipRest("nothing was found")
		e.finish(sctx, a, workspaceID, runID, "ok", "", "Nothing was found for that question.", "", 0, 0)
		return
	}

	// 3. Read.
	pr.begin("read")
	sources := make([]ResearchSource, 0, len(findings))
	read := 0
	limit := MaxReads
	if mode == "urls" {
		limit = MaxURLs
	}
	for i, f := range findings {
		src := ResearchSource{Position: i, URL: clean(f.URL), Title: clean(f.Title), Snippet: clean(f.Snippet)}

		if e.Read != nil && i < limit {
			pr.cost.PagesTried++
			md, err := e.Read.Read(ctx, f.URL)
			if err != nil {
				src.Error = trim(clean(err.Error()), 300)
			} else {
				src.Content = trim(clean(md), maxSourceChars)
				src.Fetched = true
				read++
				pr.cost.PagesRead++
				if src.Title == "" {
					src.Title = trim(firstHeading(src.Content), 300)
				}
			}
		} else if e.Read == nil {
			src.Error = "no reader is configured for this deployment"
		}
		if src.Title == "" {
			src.Title = hostOf(src.URL)
		}

		if err := a.SQLDB.QueryRowContext(sctx, `
			INSERT INTO research_sources (workspace_id, run_id, position, url, title, snippet, content, fetched, error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			workspaceID, runID, src.Position, src.URL, trim(src.Title, 300),
			trim(src.Snippet, 1000), src.Content, src.Fetched, src.Error).Scan(&src.ID); err != nil {
			e.Log.Error("research: could not store a source", "run", runID, "err", err)
		}
		sources = append(sources, src)
		pr.save("read")

		if cancelled(sctx, a, workspaceID, runID) {
			pr.skipRest("stopped")
			return
		}
	}
	readNote := ""
	if e.Read == nil {
		readNote = "no reader is configured; evidence comes from the search snippets"
	} else if read < len(sources) && mode == "search" && len(sources) > MaxReads {
		readNote = fmt.Sprintf("the first %d pages are read; the rest keep their snippet", MaxReads)
	}
	pr.end("read", "done", read, readNote)

	// 4. Extract: quotes and claims, and the companies they name.
	pr.begin("extract")
	evidence, extractNote := e.extract(ctx, plan, sources)
	stored, err := storeEvidence(sctx, a.SQLDB, workspaceID, runID, sources, evidence)
	if err != nil {
		e.Log.Error("research: could not store evidence", "run", runID, "err", err)
		extractNote = strings.TrimSpace(extractNote + " — some evidence could not be stored")
	}
	newCompanies, err := upsertCompanies(sctx, a.SQLDB, workspaceID, runID)
	if err != nil {
		e.Log.Error("research: could not update company suggestions", "run", runID, "err", err)
	}
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE research_runs SET evidence_count = $3 WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID, stored)
	pr.end("extract", "done", stored, extractNote)

	if cancelled(sctx, a, workspaceID, runID) {
		pr.skipRest("stopped")
		return
	}

	// 5. Summarise.
	pr.begin("summarise")
	if e.AI == nil && len(sources) > 0 && e.UsesAgent(sctx, a.SQLDB, workspaceID) {
		// No model here: the caller's AI assistant summarises it over MCP.
		pr.end("summarise", "pending", 0, "waiting for your AI assistant (MCP: agent_tasks → submit_research)")
		e.finish(sctx, a, workspaceID, runID, "ok", "", "", "", len(sources), read)
		if t, err := QueueSummary(sctx, a.SQLDB, workspaceID, runID, ""); err != nil {
			e.Log.Error("research: could not queue a summary for the agent", "run", runID, "err", err)
		} else {
			_ = setSummariseStage(sctx, a.SQLDB, workspaceID, runID, "pending", 0,
				"waiting for your AI assistant (MCP: agent_tasks → submit_research, task "+t.ID+")", "", "")
		}
	} else if summary, model, err := e.summarise(ctx, topic, sources); err != nil {
		pr.end("summarise", "skipped", 0, err.Error())
		e.finish(sctx, a, workspaceID, runID, "ok", trim(err.Error(), 300), "", "", len(sources), read)
	} else {
		pr.end("summarise", "done", 1, model)
		e.finish(sctx, a, workspaceID, runID, "ok", "", summary, model, len(sources), read)
	}

	notifyFindings(sctx, a, workspaceID, runID, topic, newCompanies)

	afterMu.RLock()
	hooks := append([]func(context.Context, *app.App, string, string, string){}, afterRun...)
	afterMu.RUnlock()
	for _, h := range hooks {
		h(sctx, a, workspaceID, runID, origin)
	}
}

// summarise asks the model to answer from the fetched pages only.
func (e *Engine) summarise(ctx context.Context, topic string, sources []ResearchSource) (string, string, error) {
	if e.AI == nil {
		return "", "", errors.New("no model is configured, so the pages were collected but not summarised")
	}

	readable := readableSources(sources)
	if len(readable) == 0 {
		return "", "", errors.New("none of the pages could be read, so there was nothing to summarise")
	}

	share := promptBudget() / len(readable)
	if share < 500 {
		share = 500
	}

	var b strings.Builder
	for _, s := range readable {
		fmt.Fprintf(&b, "\n\n=== SOURCE %d: %s\nURL: %s\n\n%s",
			s.Position+1, s.Title, s.URL, trim(s.Content, share))
	}

	result, err := e.AI.Generate(ctx, ai.TaskSummarise, ai.GenerateRequest{
		System: `You answer a question from the sources given to you, and from nothing else.

Rules:
- Use ONLY the numbered sources below. If they do not answer the question, say so plainly.
- Cite the sources you used as [1], [2] — the numbers are the SOURCE numbers.
- Do not invent facts, figures, dates or quotations. An unsourced claim here reads as research and is worse than saying you do not know.
- Be concise: a short answer, then the details that matter.`,
		Messages: []ai.Message{{
			Role:    "user",
			Content: fmt.Sprintf("Question: %s\n\nSources:%s", topic, b.String()),
		}},
		MaxTokens: 1200,
	})
	if err != nil {
		return "", "", err
	}

	return strings.TrimSpace(result.Text), result.Provenance.Provider, nil
}

func readableSources(sources []ResearchSource) []ResearchSource {
	var out []ResearchSource
	for _, s := range sources {
		if s.Fetched && s.Content != "" {
			out = append(out, s)
		}
	}
	return out
}

// finish records how a run ended — unless somebody stopped it first.
func (e *Engine) finish(ctx context.Context, a *app.App, workspaceID, runID, status, errText, summary, model string, found, read int) {
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE research_runs
		SET status = $3, error = $4, summary = $5, model = $6,
		    found_count = $7, read_count = $8, stage = '', finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status <> 'cancelled'`,
		workspaceID, runID, status, trim(errText, 500), summary, model, found, read); err != nil {
		e.Log.Error("research: could not finish the run", "run", runID, "err", err)
	}
}

func cancelled(ctx context.Context, a *app.App, workspaceID, runID string) bool {
	var status string
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT status FROM research_runs WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID).Scan(&status); err != nil {
		return false
	}
	return status == "cancelled"
}

// Cancel stops a run. The worker notices between pages.
func Cancel(ctx context.Context, a *app.App, workspaceID, runID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	res, err := a.SQLDB.ExecContext(sctx, `
		UPDATE research_runs SET status = 'cancelled', stage = '', finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status IN ('queued', 'running')`, workspaceID, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("that run is not going")
	}
	return nil
}

const runColumns = `id, topic, query, status, summary, model, error, found_count, read_count,
	requested_by, mode, urls::text, origin, coalesce(schedule_id, ''), stage, stages::text, cost::text, evidence_count,
	started_at, finished_at, created_at`

func scanRun(row interface{ Scan(...any) error }) (ResearchRun, error) {
	var r ResearchRun
	var started, finished sql.NullTime
	var urls, stages, cost string
	err := row.Scan(&r.ID, &r.Topic, &r.Query, &r.Status, &r.Summary, &r.Model, &r.Error,
		&r.FoundCount, &r.ReadCount, &r.RequestedBy, &r.Mode, &urls, &r.Origin, &r.ScheduleID,
		&r.Stage, &stages, &cost, &r.EvidenceCount, &started, &finished, &r.CreatedAt)
	if err != nil {
		return r, err
	}
	r.URLs = parsePgArray(urls)
	_ = json.Unmarshal([]byte(stages), &r.Stages)
	_ = json.Unmarshal([]byte(cost), &r.Cost)
	if r.Stages == nil {
		r.Stages = []ResearchStage{}
	}
	if r.Cost.Providers == nil {
		r.Cost.Providers = []string{}
	}
	if started.Valid {
		r.StartedAt = &started.Time
	}
	if finished.Valid {
		r.FinishedAt = &finished.Time
	}
	return r, nil
}

// Get returns one run and its sources. Page content only on request.
func Get(ctx context.Context, a *app.App, workspaceID, runID string, withContent bool) (*ResearchRun, []ResearchSource, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)

	r, err := scanRun(a.SQLDB.QueryRowContext(sctx, `
		SELECT `+runColumns+`
		FROM research_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, errors.New("no such research run")
	}
	if err != nil {
		return nil, nil, err
	}

	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, position, url, title, snippet, content, fetched, error
		FROM research_sources WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`,
		workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var sources []ResearchSource
	for rows.Next() {
		var s ResearchSource
		if err := rows.Scan(&s.ID, &s.Position, &s.URL, &s.Title, &s.Snippet, &s.Content, &s.Fetched, &s.Error); err != nil {
			return nil, nil, err
		}
		if !withContent {
			s.Content = ""
		}
		sources = append(sources, s)
	}
	return &r, sources, rows.Err()
}

// List returns the workspace's runs, newest first.
func List(ctx context.Context, a *app.App, workspaceID string, limit int) ([]ResearchRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)

	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT `+runColumns+`
		FROM research_runs WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT $2`,
		workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResearchRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		r.Summary = trim(r.Summary, 400)
		out = append(out, r)
	}
	return out, rows.Err()
}

// promptBudget is how many characters of source text one prompt may carry.
func promptBudget() int {
	if v := strings.TrimSpace(os.Getenv("RESEARCH_PROMPT_CHARS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1000 {
			return n
		}
	}
	return defaultPromptChars
}

// clean makes fetched text storable: no NUL bytes, valid UTF-8.
func clean(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// firstHeading is a page's first markdown heading, for a URL-list source that
// arrives with no title.
func firstHeading(md string) string {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "# "))
		}
	}
	return ""
}

// pgArray renders a text[] literal; parsePgArray reads one back. Only the
// shapes this package writes (URLs, names) are handled — quoted elements with
// escaped quotes and backslashes.
func pgArray(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts[i] = `"` + s + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func parsePgArray(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s == "{}" {
		return []string{}
	}
	s = s[1 : len(s)-1]
	var out []string
	var b strings.Builder
	inQ, esc := false, false
	for _, r := range s {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '"':
			inQ = !inQ
		case r == ',' && !inQ:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	out = append(out, b.String())
	return out
}
