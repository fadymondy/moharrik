package research

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
A research run (MH-227): search, read, summarise — and keep the evidence.

	SearXNG   finds the pages
	Firecrawl reads them, in a real browser, so JavaScript pages come back as text
	a model    writes the answer, from those pages only

Three rules shape this file.

	THE SOURCES OUTLIVE THE SUMMARY. Every page found is stored with its URL and
	its position, whether or not it could be read. A summary with no sources is
	a model's opinion; the gap between "found" and "read" is what tells you
	whether an answer is thin because nothing was written or because everything
	was behind a paywall.

	THE MODEL IS OPTIONAL. No provider configured is a supported way to run
	this: you get the pages and read them yourself. It must not be an error,
	because the search and the reading are most of the value.

	NOTHING IS INVENTED. The model is given the fetched text and told to answer
	from it, and the answer is stored next to the pages it came from. An
	unsourced summary here would be worse than none — it reads as research.
*/

const (
	// How many search results a run keeps. Past this the tail is noise and the
	// model's context is spent on it.
	MaxFindings = 8

	// How many of them are actually fetched. Reading is the slow, expensive
	// part; the rest keep their title, link and snippet.
	MaxReads = 5

	// Per source. Enough of an article to answer from, small enough that a
	// run does not become the biggest row in the database.
	maxSourceChars = 12000

	// Per PROMPT, across every source together. This is the one that matters:
	// bounding each source alone leaves the TOTAL unbounded, so five long pages
	// produced a 13,700-token prompt and every model with an 8k window refused
	// it — at the END of the run, after all the reading was already paid for.
	//
	// ~24k characters is roughly 6k tokens, leaving room for the system prompt
	// and the answer inside a conservative 8k window. A model with a larger one
	// simply gets a shorter prompt than it could take, which costs nothing.
	defaultPromptChars = 24000

	// The whole run. A search, five browser renders and one model call.
	RunDeadline = 5 * time.Minute

	// One run at a time per workspace: each one drives a shared search
	// instance and a shared browser pool, and a queue of them started by
	// accident is how those fall over.
	maxLiveRuns = 1
)

// ErrBusy is one-run-at-a-time answering.
var ErrBusy = errors.New("this workspace is already running some research")

// ResearchRun is the record of one question.
type ResearchRun struct {
	ID          string     `json:"id"`
	Topic       string     `json:"topic"`
	Query       string     `json:"query"`
	Status      string     `json:"status"`
	Summary     string     `json:"summary"`
	Model       string     `json:"model"`
	Error       string     `json:"error"`
	FoundCount  int        `json:"found_count"`
	ReadCount   int        `json:"read_count"`
	RequestedBy string     `json:"requested_by"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// ResearchSource is one page that went into an answer.
type ResearchSource struct {
	Position int    `json:"position"`
	URL      string `json:"url"`
	Title    string `json:"title"`
	Snippet  string `json:"snippet"`
	Content  string `json:"content,omitempty"`
	Fetched  bool   `json:"fetched"`
	Error    string `json:"error,omitempty"`
}

// Engine holds what a run needs. Built once at boot.
type Engine struct {
	Search *Searcher
	Read   *Reader
	AI     *ai.Client
	Log    *slog.Logger
}

// NewEngine wires the tiers from the environment. Each is independently
// optional except search, which a run cannot do without — that is reported when
// a run is started rather than at boot, so a deployment that never researches
// is not held up by it.
func NewEngine(log *slog.Logger) *Engine {
	e := &Engine{Search: NewSearcher(), Read: NewReader(), Log: log}

	// The model is best-effort at construction: a bad AI_PROVIDERS should not
	// stop the app booting, and a run says so plainly when it has no model.
	//
	// An EMPTY configuration is valid to ai.New — it returns a client whose
	// every call fails with "no provider". Keeping that client would make the
	// engine report that it can summarise and then fail at the end of every
	// run, after the reading is already paid for. So no providers means no
	// client, and the capability this reports is the one it has.
	cfg, err := ai.LoadConfig()
	if err != nil {
		log.Warn("research: AI config unreadable, running without a model", "err", err)
		return e
	}
	if len(cfg.Providers) == 0 {
		log.Info("research: no model configured (AI_PROVIDERS is empty); runs will collect sources without summarising")
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

// Start records the run and does the work in the background, returning as soon
// as the row exists — a search, five page renders and a model call take far
// longer than a request should.
func (e *Engine) Start(ctx context.Context, a *app.App, workspaceID, userID, topic string) (string, error) {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return "", errors.New("a research run needs a question")
	}
	if len([]rune(topic)) > 500 {
		return "", errors.New("that question is too long")
	}
	if e.Search == nil {
		return "", ErrNoSearch
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)

	var live int
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM research_runs
		WHERE workspace_id = $1 AND status IN ('queued', 'running')`, workspaceID).Scan(&live); err != nil {
		return "", err
	}
	if live >= maxLiveRuns {
		return "", ErrBusy
	}

	var runID string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO research_runs (workspace_id, topic, query, status, requested_by)
		VALUES ($1, $2, $2, 'queued', $3) RETURNING id`,
		workspaceID, topic, userID).Scan(&runID); err != nil {
		return "", err
	}

	go e.execute(a, workspaceID, runID, topic)
	return runID, nil
}

// execute is the run itself.
func (e *Engine) execute(a *app.App, workspaceID, runID, topic string) {
	ctx, cancel := context.WithTimeout(context.Background(), RunDeadline)
	defer cancel()
	sctx := tenant.WithWorkspace(ctx, workspaceID)

	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE research_runs SET status = 'running', started_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'queued'`, workspaceID, runID)

	findings, err := e.Search.Search(ctx, topic, MaxFindings)
	if err != nil {
		e.finish(sctx, a, workspaceID, runID, "failed", err.Error(), "", "", 0, 0)
		return
	}
	if len(findings) == 0 {
		// Not a failure: "nothing is being said about this" is an answer, and
		// recording it as an error would make the next person retry it.
		e.finish(sctx, a, workspaceID, runID, "ok", "", "Nothing was found for that question.", "", 0, 0)
		return
	}

	sources := make([]ResearchSource, 0, len(findings))
	read := 0

	for i, f := range findings {
		src := ResearchSource{Position: i, URL: clean(f.URL), Title: clean(f.Title), Snippet: clean(f.Snippet)}

		// Only the first few are fetched; the rest stand on their snippet.
		if e.Read != nil && i < MaxReads {
			md, err := e.Read.Read(ctx, f.URL)
			if err != nil {
				// Kept, with the reason. A page that could not be read is part
				// of the honest picture, not something to hide.
				src.Error = trim(clean(err.Error()), 300)
			} else {
				src.Content = trim(clean(md), maxSourceChars)
				src.Fetched = true
				read++
			}
		} else if e.Read == nil {
			src.Error = "no reader is configured for this deployment"
		}

		sources = append(sources, src)

		if _, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO research_sources (workspace_id, run_id, position, url, title, snippet, content, fetched, error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			workspaceID, runID, src.Position, src.URL, trim(src.Title, 300),
			trim(src.Snippet, 1000), src.Content, src.Fetched, src.Error); err != nil {
			e.Log.Error("research: could not store a source", "run", runID, "err", err)
		}

		// Cancelled between pages: stop rather than finish a run nobody wants.
		if cancelled(sctx, a, workspaceID, runID) {
			return
		}
	}

	summary, model, err := e.summarise(ctx, topic, sources)
	if err != nil {
		// The pages are already stored, so the run is USABLE even though the
		// model failed. Recording it as failed would throw away the half that
		// worked; the error is kept next to the sources instead.
		e.finish(sctx, a, workspaceID, runID, "ok", trim(err.Error(), 300), "", "", len(sources), read)
		return
	}

	e.finish(sctx, a, workspaceID, runID, "ok", "", summary, model, len(sources), read)
}

// summarise asks the model to answer from the fetched pages only.
func (e *Engine) summarise(ctx context.Context, topic string, sources []ResearchSource) (string, string, error) {
	if e.AI == nil {
		return "", "", errors.New("no model is configured, so the pages were collected but not summarised")
	}

	var readable []ResearchSource
	for _, s := range sources {
		if s.Fetched && s.Content != "" {
			readable = append(readable, s)
		}
	}
	if len(readable) == 0 {
		return "", "", errors.New("none of the pages could be read, so there was nothing to summarise")
	}

	// An equal share each, rather than first-come-first-served: spending the
	// whole budget on source one would leave the rest contributing nothing,
	// and the answer would quietly rest on fewer pages than the run claims.
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

// finish records how a run ended — unless somebody stopped it first.
//
// Cancel is only noticed BETWEEN pages, so a Stop pressed during the search or
// the model call used to be overwritten here: the run said "Stopped" on screen,
// then quietly became "Finished" with an answer nobody waited for. A cancelled
// run stays cancelled.
func (e *Engine) finish(ctx context.Context, a *app.App, workspaceID, runID, status, errText, summary, model string, found, read int) {
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE research_runs
		SET status = $3, error = $4, summary = $5, model = $6,
		    found_count = $7, read_count = $8, finished_at = now(), updated_at = now()
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
		UPDATE research_runs SET status = 'cancelled', finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status IN ('queued', 'running')`, workspaceID, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("that run is not going")
	}
	return nil
}

// Get returns one run and its sources.
//
// The page content is NOT returned by default: a run holds up to five articles,
// and a list that carried them all would be megabytes for a reader who wants
// the answer and the links.
func Get(ctx context.Context, a *app.App, workspaceID, runID string, withContent bool) (*ResearchRun, []ResearchSource, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)

	var r ResearchRun
	var started, finished sql.NullTime
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT id, topic, query, status, summary, model, error, found_count, read_count,
		       requested_by, started_at, finished_at, created_at
		FROM research_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID).
		Scan(&r.ID, &r.Topic, &r.Query, &r.Status, &r.Summary, &r.Model, &r.Error,
			&r.FoundCount, &r.ReadCount, &r.RequestedBy, &started, &finished, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, errors.New("no such research run")
	}
	if err != nil {
		return nil, nil, err
	}
	if started.Valid {
		r.StartedAt = &started.Time
	}
	if finished.Valid {
		r.FinishedAt = &finished.Time
	}

	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT position, url, title, snippet, content, fetched, error
		FROM research_sources WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`,
		workspaceID, runID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var sources []ResearchSource
	for rows.Next() {
		var s ResearchSource
		if err := rows.Scan(&s.Position, &s.URL, &s.Title, &s.Snippet, &s.Content, &s.Fetched, &s.Error); err != nil {
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
		SELECT id, topic, query, status, summary, model, error, found_count, read_count,
		       requested_by, started_at, finished_at, created_at
		FROM research_runs WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT $2`,
		workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResearchRun
	for rows.Next() {
		var r ResearchRun
		var started, finished sql.NullTime
		if err := rows.Scan(&r.ID, &r.Topic, &r.Query, &r.Status, &r.Summary, &r.Model, &r.Error,
			&r.FoundCount, &r.ReadCount, &r.RequestedBy, &started, &finished, &r.CreatedAt); err != nil {
			return nil, err
		}
		if started.Valid {
			r.StartedAt = &started.Time
		}
		if finished.Valid {
			r.FinishedAt = &finished.Time
		}
		// The summary is a paragraph; a list of 25 does not need all of it.
		r.Summary = trim(r.Summary, 400)
		out = append(out, r)
	}
	return out, rows.Err()
}

// promptBudget is how many characters of source text one summary may carry.
//
// Configurable because it is really a property of the MODEL, which this code
// cannot see: a 128k-context model can take far more than a local 8k one, and
// a budget that fits both would waste the first. RESEARCH_PROMPT_CHARS lets a
// deployment say what its model can actually hold.
func promptBudget() int {
	if v := strings.TrimSpace(os.Getenv("RESEARCH_PROMPT_CHARS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1000 {
			return n
		}
	}
	return defaultPromptChars
}

// clean makes fetched text storable. Postgres refuses a text value holding a
// NUL byte or invalid UTF-8, and a page read as binary (a PDF, a mislabelled
// download) carries both: the INSERT failed, the source was only logged, and the
// run then reported more pages found and read than it kept. Dropping the bytes
// Postgres cannot hold keeps the source, which is the point of storing them.
func clean(s string) string {
	return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
