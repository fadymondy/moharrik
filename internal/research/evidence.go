package research

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
The evidence store (MH-388): write, search, and the step from a quote to a
suggested CRM company.
*/

// Evidence is one stored quote or claim.
type Evidence struct {
	ID            string     `json:"id"`
	RunID         string     `json:"run_id"`
	RunTopic      string     `json:"run_topic"`
	SourceID      string     `json:"source_id"`
	Kind          string     `json:"kind" enum:"quote,claim"`
	Quote         string     `json:"quote" doc:"Verbatim text from the page."`
	Claim         string     `json:"claim" doc:"A model's one-line reading of the quote; empty when no model extracted it."`
	SourceURL     string     `json:"source_url"`
	SourceTitle   string     `json:"source_title"`
	SourceDomain  string     `json:"source_domain"`
	CompanyName   string     `json:"company_name"`
	CompanyDomain string     `json:"company_domain"`
	CompanyKey    string     `json:"company_key"`
	SignalType    string     `json:"signal_type"`
	Confidence    float64    `json:"confidence"`
	PublishedAt   *time.Time `json:"published_at"`
	CapturedAt    time.Time  `json:"captured_at"`
	Extractor     string     `json:"extractor" doc:"heuristic, or model:<provider>."`
}

// ── extraction ───────────────────────────────────────────────────────────

// extract turns the run's pages into evidence: a model when one is configured
// (claims whose quotes are checked against the page), the heuristic for every
// page the model did not cover. The note says which did what.
func (e *Engine) extract(ctx context.Context, plan Plan, sources []ResearchSource) ([]sourced, string) {
	var out []sourced
	covered := map[int]bool{}
	note := ""

	if e.AI != nil {
		if got, err := e.modelExtract(ctx, plan, sources); err != nil {
			note = "the model could not extract (" + trim(err.Error(), 120) + "), so sentences were picked from the pages instead"
		} else {
			for _, s := range got {
				covered[s.pos] = true
			}
			out = append(out, got...)
			note = fmt.Sprintf("%d claims by the model, each checked against its page", len(got))
		}
	} else {
		note = "no model is configured: the evidence is sentences copied from the pages"
	}

	for _, src := range sources {
		if covered[src.Position] {
			continue
		}
		for _, x := range ExtractHeuristic(plan, Page{URL: src.URL, Title: src.Title, Text: src.Content, Snippet: src.Snippet}) {
			out = append(out, sourced{pos: src.Position, x: x})
		}
	}
	return out, note
}

// sourced is extracted evidence and the position of its page in the run.
type sourced struct {
	pos int
	x   Extracted
}

var extractSchema = &ai.Schema{
	Name:        "research_evidence",
	Description: "Facts the pages state, each with the exact sentence that states it.",
	Type:        ai.TypeObject,
	Required:    []string{"findings"},
	Properties: map[string]*ai.Schema{
		"findings": {
			Type:     ai.TypeArray,
			MaxItems: 20,
			Items: &ai.Schema{
				Type:     ai.TypeObject,
				Required: []string{"source", "quote", "claim"},
				Properties: map[string]*ai.Schema{
					"source":         {Type: ai.TypeInteger, Description: "The SOURCE number the quote is from."},
					"quote":          {Type: ai.TypeString, Description: "The exact sentence, copied character for character."},
					"claim":          {Type: ai.TypeString, Description: "One short sentence: what the quote says, in plain words."},
					"company":        {Type: ai.TypeString, Description: "The organisation the quote is about, as named on the page. Empty if none."},
					"company_domain": {Type: ai.TypeString, Description: "Its website domain, only if the page shows it."},
					"signal_type":    {Type: ai.TypeString, Enum: append([]string{""}, SignalTypes...)},
				},
			},
		},
	},
}

func (e *Engine) modelExtract(ctx context.Context, plan Plan, sources []ResearchSource) ([]sourced, error) {
	readable := readableSources(sources)
	if len(readable) == 0 {
		return nil, errors.New("no page could be read")
	}
	share := promptBudget() / len(readable)
	if share < 500 {
		share = 500
	}
	byNumber := map[int]ResearchSource{}
	var b strings.Builder
	for _, s := range readable {
		byNumber[s.Position+1] = s
		fmt.Fprintf(&b, "\n\n=== SOURCE %d: %s\nURL: %s\n\n%s", s.Position+1, s.Title, s.URL, trim(s.Content, share))
	}
	res, err := e.AI.Generate(ctx, ai.TaskExtract, ai.GenerateRequest{
		System: `You pull evidence out of web pages for a business researcher.

Rules, and they are hard rules:
- Every finding's "quote" is a sentence copied EXACTLY from the numbered source. Do not shorten, fix or translate it.
- The "claim" says what the quote says and nothing more.
- Name a company only if the quote or its page names it. Leave it empty otherwise.
- signal_type is one of the listed values when the quote reports one, else "".
- At most 4 findings per source; only findings about the question or a company's news.`,
		Messages:  []ai.Message{{Role: "user", Content: "Question: " + strings.Join(append(plan.Phrases, plan.Keywords...), " ") + "\n\nSources:" + b.String()}},
		Schema:    extractSchema,
		MaxTokens: 2000,
	})
	if err != nil {
		return nil, err
	}
	items, _ := res.JSON["findings"].([]any)
	extractor := "model:" + res.Provenance.Provider
	var out []sourced
	perSource := map[int]int{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		n := toInt(m["source"])
		src, ok := byNumber[n]
		if !ok {
			continue
		}
		quote := strings.TrimSpace(str(m["quote"]))
		// The check that makes a model's claim evidence: the quote must be on
		// the page. A model that paraphrased, or invented, is dropped here.
		if len([]rune(quote)) < 20 || !onPage(quote, src.Content) || perSource[n] >= perPage {
			continue
		}
		perSource[n]++
		signal := str(m["signal_type"])
		if !validSignal(signal) {
			signal = ""
		}
		name := strings.TrimSpace(str(m["company"]))
		domain := RegistrableDomain(strings.TrimPrefix(strings.TrimPrefix(str(m["company_domain"]), "https://"), "http://"))
		if name == "" {
			pc := pageCompany(src.URL, src.Title)
			name, domain = pc.name, pc.domain
		}
		conf := 0.6 + 0.3*relevance(plan, quote)
		if signal != "" {
			conf += 0.1
		}
		if conf > 1 {
			conf = 1
		}
		out = append(out, sourced{pos: src.Position, x: Extracted{
			Kind: "claim", Quote: trim(quote, 1000), Claim: trim(strings.TrimSpace(str(m["claim"])), 500),
			CompanyName: trim(name, 120), CompanyDomain: domain, SignalType: signal, Confidence: round2(conf),
			PublishedAt: publishedDate(src.Content), Extractor: extractor,
		}})
	}
	return out, nil
}

// onPage reports whether a quote is on the page, ignoring spacing, case and
// the markdown the reader adds.
func onPage(quote, page string) bool {
	flat := func(s string) string {
		s = mdLink.ReplaceAllString(s, "$1")
		s = mdNoise.ReplaceAllString(s, " ")
		return strings.Join(strings.Fields(normalise(s)), " ")
	}
	return strings.Contains(flat(page), flat(quote))
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// ── storage ──────────────────────────────────────────────────────────────

// storeEvidence writes a run's evidence; the same quote from the same page
// once. Returns how many rows were stored.
func storeEvidence(ctx context.Context, db *sql.DB, ws, runID string, sources []ResearchSource, ev []sourced) (int, error) {
	byPos := map[int]ResearchSource{}
	for _, s := range sources {
		byPos[s.Position] = s
	}
	stored := 0
	var firstErr error
	for _, s := range ev {
		src := byPos[s.pos]
		x := s.x
		key := ""
		if strings.TrimSpace(x.CompanyName) != "" {
			key = CompanyKey(x.CompanyName, x.CompanyDomain)
		}
		res, err := db.ExecContext(ctx, `
			INSERT INTO research_evidence (workspace_id, run_id, source_id, kind, quote, claim, source_url, source_title, source_domain,
				company_name, company_domain, company_key, signal_type, confidence, published_at, extractor, content_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
			ON CONFLICT (workspace_id, run_id, content_hash) DO NOTHING`,
			ws, runID, nullable(src.ID), x.Kind, clean(x.Quote), clean(x.Claim), src.URL, trim(src.Title, 300), RegistrableDomain(hostOf(src.URL)),
			trim(clean(x.CompanyName), 120), x.CompanyDomain, trim(key, 200), x.SignalType, x.Confidence, x.PublishedAt, x.Extractor,
			evidenceHash(x.Quote, src.URL))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			stored++
		}
	}
	return stored, firstErr
}

// upsertCompanies turns the companies a run's evidence names into
// suggestions, and returns the ones that are NEW (never seen before). A
// company already in the CRM is recorded as linked, not suggested; a
// dismissed one stays dismissed.
func upsertCompanies(ctx context.Context, db *sql.DB, ws, runID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT company_key,
		       (array_agg(company_name ORDER BY confidence DESC, length(company_name)))[1],
		       coalesce(max(nullif(company_domain, '')), ''),
		       max(confidence),
		       coalesce(array_agg(DISTINCT signal_type) FILTER (WHERE signal_type <> ''), '{}')::text
		FROM research_evidence
		WHERE workspace_id = $1 AND run_id = $2 AND company_key <> ''
		GROUP BY company_key`, ws, runID)
	if err != nil {
		return nil, err
	}
	type co struct {
		key, name, domain, signals string
		strongest                  float64
	}
	var found []co
	for rows.Next() {
		var c co
		if err := rows.Scan(&c.key, &c.name, &c.domain, &c.strongest, &c.signals); err != nil {
			rows.Close()
			return nil, err
		}
		found = append(found, c)
	}
	rows.Close()

	var fresh []string
	for _, c := range found {
		// Already in the CRM, by domain or by name.
		var crmID sql.NullString
		_ = db.QueryRowContext(ctx, `
			SELECT id FROM companies WHERE workspace_id = $1
			  AND (($2 <> '' AND lower(domain) = lower($2)) OR lower(name) = lower($3))
			ORDER BY created_at LIMIT 1`, ws, c.domain, c.name).Scan(&crmID)
		status := "suggested"
		if crmID.Valid {
			status = "linked"
		}
		var inserted bool
		var id string
		err := db.QueryRowContext(ctx, `
			INSERT INTO research_companies (workspace_id, company_key, name, domain, status, crm_company_id,
				evidence_count, strongest, signal_types, first_run_id, last_run_id)
			VALUES ($1, $2, $3, $4, $5, $6,
				(SELECT count(*) FROM research_evidence WHERE workspace_id = $1 AND company_key = $2), $7, $8::text[], $9, $9)
			ON CONFLICT (workspace_id, company_key) DO UPDATE SET
				domain = CASE WHEN research_companies.domain = '' THEN EXCLUDED.domain ELSE research_companies.domain END,
				evidence_count = EXCLUDED.evidence_count,
				strongest = greatest(research_companies.strongest, EXCLUDED.strongest),
				signal_types = ARRAY(SELECT DISTINCT unnest(research_companies.signal_types || EXCLUDED.signal_types) ORDER BY 1),
				status = CASE WHEN research_companies.status = 'suggested' AND EXCLUDED.status = 'linked' THEN 'linked' ELSE research_companies.status END,
				crm_company_id = coalesce(research_companies.crm_company_id, EXCLUDED.crm_company_id),
				last_run_id = EXCLUDED.last_run_id, last_seen_at = now(), updated_at = now()
			RETURNING id, (xmax = 0)`,
			ws, c.key, trim(c.name, 120), c.domain, status, crmID, c.strongest, c.signals, runID).Scan(&id, &inserted)
		if err != nil {
			return fresh, err
		}
		if inserted && status == "suggested" {
			fresh = append(fresh, c.name)
		}
	}
	return fresh, nil
}

// strongConfidence is the bar a signal clears to be announced.
const strongConfidence = 0.6

// notifyFindings posts one notification per run when it found something worth
// a person's attention: a strong signal, or a company not seen before. A run
// that found only background reading stays quiet.
func notifyFindings(ctx context.Context, a *app.App, ws, runID, topic string, newCompanies []string) {
	var strong int
	var sample []string
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT coalesce(nullif(claim, ''), quote) FROM research_evidence
		WHERE workspace_id = $1 AND run_id = $2 AND signal_type <> '' AND confidence >= $3
		ORDER BY confidence DESC`, ws, runID, strongConfidence)
	if err != nil {
		return
	}
	for rows.Next() {
		var q string
		if rows.Scan(&q) == nil {
			strong++
			if len(sample) < 2 {
				sample = append(sample, "• "+trim(q, 180))
			}
		}
	}
	rows.Close()
	if strong == 0 && len(newCompanies) == 0 {
		return
	}

	var locale, slug string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT locale, slug FROM workspaces WHERE id = $1`, ws).Scan(&locale, &slug)
	ar := locale == "ar"
	title := fmt.Sprintf("Research: %d strong finding%s, %d new compan%s — %s", strong, plural(strong, "", "s"),
		len(newCompanies), plural(len(newCompanies), "y", "ies"), trim(topic, 80))
	source := "Research"
	if ar {
		title = fmt.Sprintf("البحث: %d نتيجة قوية و%d شركة جديدة — %s", strong, len(newCompanies), trim(topic, 80))
		source = "البحث"
	}
	body := strings.Join(sample, "\n")
	if len(newCompanies) > 0 {
		label := "New companies to review: "
		if ar {
			label = "شركات جديدة للمراجعة: "
		}
		names := newCompanies
		if len(names) > 5 {
			names = names[:5]
		}
		body = strings.TrimSpace(body + "\n" + label + strings.Join(names, ", "))
	}
	loc := "en"
	if ar {
		loc = "ar"
	}
	ext := "research:" + runID
	if err := notifications.Emit(ctx, a.SQLDB, ws, notifications.Internal{
		SourceName: source, ExternalID: ext, Kind: "research", Severity: "info",
		Title: title, Body: body, URL: publicBase() + "/" + loc + "/w/" + slug + "/research/" + runID,
	}); err != nil {
		a.Log.Warn("research: could not post the findings", "run", runID, "err", err)
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

// ── search ───────────────────────────────────────────────────────────────

// EvidenceQuery is find_evidence's filter.
type EvidenceQuery struct {
	Q          string
	Company    string // key, domain or name (contains)
	SignalType string
	Domain     string // source domain
	RunID      string
	Since      *time.Time
	Until      *time.Time
	Limit      int
	Offset     int
}

// EvidencePage is one page of results.
type EvidencePage struct {
	Evidence []Evidence `json:"evidence"`
	Total    int        `json:"total"`
	Limit    int        `json:"limit"`
	Offset   int        `json:"offset"`
}

const evidenceColumns = `e.id, e.run_id, coalesce(r.topic, ''), coalesce(e.source_id, ''), e.kind, e.quote, e.claim,
	e.source_url, e.source_title, e.source_domain, e.company_name, e.company_domain, e.company_key, e.signal_type,
	e.confidence, e.published_at, e.captured_at, e.extractor`

func scanEvidence(row interface{ Scan(...any) error }) (Evidence, error) {
	var e Evidence
	var pub sql.NullTime
	err := row.Scan(&e.ID, &e.RunID, &e.RunTopic, &e.SourceID, &e.Kind, &e.Quote, &e.Claim, &e.SourceURL, &e.SourceTitle,
		&e.SourceDomain, &e.CompanyName, &e.CompanyDomain, &e.CompanyKey, &e.SignalType, &e.Confidence, &pub, &e.CapturedAt, &e.Extractor)
	if pub.Valid {
		e.PublishedAt = &pub.Time
	}
	return e, err
}

// FindEvidence searches the workspace's evidence across every run. db is the
// app pool and ctx carries the workspace; every clause names workspace_id too.
func FindEvidence(ctx context.Context, db *sql.DB, ws string, q EvidenceQuery) (*EvidencePage, error) {
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 25
	}
	if q.Offset < 0 {
		q.Offset = 0
	}
	args := []any{ws}
	add := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	where := []string{"e.workspace_id = $1"}
	order := "e.captured_at DESC, e.confidence DESC"
	if s := strings.TrimSpace(q.Q); s != "" {
		// Full text first (every word, any order), with a plain substring as the
		// fallback for a fragment the tokenizer splits differently.
		p := add(s)
		like := add("%" + likeEscape(s) + "%")
		where = append(where, "(e.search @@ plainto_tsquery('simple', "+p+") OR e.quote ILIKE "+like+" OR e.claim ILIKE "+like+" OR e.company_name ILIKE "+like+")")
		order = "ts_rank(e.search, plainto_tsquery('simple', " + p + ")) DESC, " + order
	}
	if s := strings.TrimSpace(q.Company); s != "" {
		key := CompanyKey(s, "")
		if strings.Contains(s, ".") && !strings.Contains(s, " ") {
			key = CompanyKey("", s)
		}
		where = append(where, "(e.company_key = "+add(key)+" OR e.company_domain = "+add(strings.ToLower(s))+" OR e.company_name ILIKE "+add("%"+likeEscape(s)+"%")+")")
	}
	if s := strings.TrimSpace(q.SignalType); s != "" {
		where = append(where, "e.signal_type = "+add(s))
	}
	if s := strings.TrimSpace(q.Domain); s != "" {
		where = append(where, "e.source_domain = "+add(RegistrableDomain(strings.ToLower(s))))
	}
	if s := strings.TrimSpace(q.RunID); s != "" {
		where = append(where, "e.run_id = "+add(s))
	}
	if q.Since != nil {
		p := add(*q.Since)
		where = append(where, "(e.published_at >= "+p+" OR e.captured_at >= "+p+")")
	}
	if q.Until != nil {
		where = append(where, "e.captured_at <= "+add(*q.Until))
	}
	clause := strings.Join(where, " AND ")

	out := &EvidencePage{Evidence: []Evidence{}, Limit: q.Limit, Offset: q.Offset}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM research_evidence e WHERE `+clause, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT `+evidenceColumns+`
		FROM research_evidence e
		LEFT JOIN research_runs r ON r.workspace_id = e.workspace_id AND r.id = e.run_id
		WHERE `+clause+` ORDER BY `+order+` LIMIT `+add(q.Limit)+` OFFSET `+add(q.Offset), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanEvidence(rows)
		if err != nil {
			return nil, err
		}
		out.Evidence = append(out.Evidence, e)
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
