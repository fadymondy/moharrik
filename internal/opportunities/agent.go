package opportunities

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/prospects"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The Opportunity Finder with the caller's agent as the provider ("bring your
own agent").

Before this, finding opportunities needed the server to search (SEARXNG_URL)
and, for good extraction, a model key. Without them run_discovery and
start_research refused and the Opportunities page sat empty. Now the work
can go the other way: an MCP client (Claude, Codex, …) asks for a task
(opportunity_tasks), does the web research with its own model and tools, and
posts structured candidates back (submit_opportunities / submit_research).
Moharrik only hands out the work, validates, sanitises, dedupes against the
CRM, scores against the ICP (prospects.Score — no model), stores with
provenance, and links to the CRM when a person accepts.

What never happens here: a model call, a web fetch, or a message to anyone.
An agent's opener is stored as a draft (prospect_drafts) and nothing sends it.
*/

// Limits on what one credential may submit.
const (
	MaxCandidatesPerCall    = 25
	MaxFindingsPerCall      = 60
	MaxEvidencePerCandidate = 5
	MaxSubmitsPerHour       = 60
	MaxItemsPerHour         = 400
	minQuoteRunes           = 20
)

// ErrRateLimited is the per-credential submission limit.
var ErrRateLimited = errors.New("this token has submitted too much in the last hour; wait and try again")

// ErrNeedsResearchRead: a summary task hands over the pages and evidence a run
// collected, which only a credential with research:read may read.
var ErrNeedsResearchRead = errors.New("summary tasks carry collected research: this token needs the research:read scope")

// SignalDefinitions say what each signal type means, for the agent.
var SignalDefinitions = map[string]string{
	"funding":     "raised money: a seed/Series round, a grant, debt financing, an acquisition of the company",
	"hiring":      "is hiring for roles that suggest a need the workspace's offer meets (job posts, 'we're hiring')",
	"launch":      "launched a product, service, app, market or website",
	"expansion":   "is expanding: a new office, country, city, branch, warehouse or large customer",
	"partnership": "announced a partnership, integration, reseller or channel deal",
	"leadership":  "appointed or changed a leader (CEO, CTO, head of …) — new leaders buy",
	"technology":  "is adopting or replacing technology (digital transformation, migration, new stack, RFP)",
	"regulatory":  "faces a new regulation, licence, compliance deadline or government programme",
}

// Claimant is the caller as an agent: its credential and what it says it is.
func Claimant(s *tenant.Scope, agent, model string) research.Claimant {
	id := s.TokenID
	if id == "" {
		id = "user:" + s.UserID
	}
	agent = research.SanitizeText(agent, 80)
	if agent == "" {
		agent = research.SanitizeText(s.TokenName, 80)
	}
	return research.Claimant{TokenID: id, Agent: agent, Model: research.SanitizeText(model, 80)}
}

// ── the brief ────────────────────────────────────────────────────────────

// TaskBrief is everything an agent needs to do one task without asking.
type TaskBrief struct {
	Task          research.AgentTask      `json:"task"`
	Goal          string                  `json:"goal"`
	SearchQueries []string                `json:"search_queries" doc:"Run these (and your own variations) with your web tools."`
	ICP           ICPBrief                `json:"icp"`
	Signals       map[string]string       `json:"signals" doc:"signal_type -> what it means."`
	Dedupe        DedupeHints             `json:"dedupe" doc:"Already known: do not submit these again (Moharrik also dedupes)."`
	Pages         []research.SummaryPage  `json:"pages,omitempty" doc:"summary tasks: the pages Moharrik collected for the run, numbered as sources."`
	Evidence      []research.SummaryQuote `json:"evidence,omitempty" doc:"summary tasks: sentences Moharrik already pulled from those pages."`
	Submit        SubmitGuide             `json:"submit"`
}

// ICPBrief is the workspace's ideal customer, as the agent should judge fit.
type ICPBrief struct {
	Offer           string             `json:"offer"`
	Industries      []string           `json:"industries"`
	Sizes           []string           `json:"sizes"`
	Regions         []string           `json:"regions"`
	Keywords        []string           `json:"keywords"`
	ExcludeKeywords []string           `json:"exclude_keywords"`
	SignalWeights   map[string]float64 `json:"signal_weights" doc:"0..3; the signals that matter most score highest."`
	Configured      bool               `json:"configured"`
}

// DedupeHints are the companies the workspace already knows.
type DedupeHints struct {
	CRMCompanies []KnownCompany `json:"crm_companies"`
	Known        []KnownCompany `json:"known" doc:"Companies research already found: suggested, prospects, accepted or dismissed."`
	Note         string         `json:"note"`
}

// KnownCompany is one company the workspace already has.
type KnownCompany struct {
	Name   string `json:"name"`
	Domain string `json:"domain,omitempty"`
	Status string `json:"status,omitempty"`
}

// SubmitGuide says how to hand the results back.
type SubmitGuide struct {
	Tool         string         `json:"tool"`
	Then         string         `json:"then"`
	Limits       map[string]int `json:"limits"`
	Rules        []string       `json:"rules"`
	OutputSchema map[string]any `json:"output_schema" doc:"JSON Schema of one candidate (submit_opportunities) or one finding (submit_research)."`
}

// CandidateSchema is one submit_opportunities candidate, as JSON Schema.
func CandidateSchema() map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return map[string]any{
		"type":     "object",
		"required": []string{"company", "evidence"},
		"properties": map[string]any{
			"company": str("The company's name, as it calls itself."),
			"domain":  str("Its website domain, e.g. acme.com. Strongly recommended: it is how Moharrik dedupes."),
			"person": map[string]any{"type": "object", "description": "Who to talk to there, if a public page names them.",
				"properties": map[string]any{
					"name": str("Full name."), "role": str("Their title/role."), "email": str("A public business email, only if published."),
					"linkedin": str("Their LinkedIn profile URL."), "phone": str("A public business phone, only if published."),
					"profile_url": str("Another public page naming them (team page, X, GitHub)."),
				}},
			"signal_type":      map[string]any{"type": "string", "enum": append([]string{""}, research.SignalTypes...)},
			"why_now":          str("One or two sentences: why this company needs the offer NOW, from the evidence."),
			"suggested_opener": str("A short first message (a DRAFT — Moharrik never sends it)."),
			"opener_subject":   str("Optional subject line for the opener."),
			"locale":           map[string]any{"type": "string", "enum": []string{"en", "ar"}, "description": "The opener's language."},
			"confidence":       map[string]any{"type": "number", "minimum": 0, "maximum": 1, "description": "How sure you are it fits (Moharrik caps unverified evidence at 0.7)."},
			"evidence": map[string]any{"type": "array", "minItems": 1, "maxItems": MaxEvidencePerCandidate,
				"description": "Where you read it. At least one, each a public URL and a sentence copied from that page.",
				"items": map[string]any{"type": "object", "required": []string{"url", "quote"}, "properties": map[string]any{
					"url":          str("The public page (http/https)."),
					"quote":        str("A sentence copied exactly from that page (at least 20 characters)."),
					"title":        str("The page's title."),
					"published_at": str("YYYY-MM-DD, when the page says it was published."),
				}}},
		},
	}
}

// FindingSchema is one submit_research finding, as JSON Schema.
func FindingSchema() map[string]any {
	str := func(d string) map[string]any { return map[string]any{"type": "string", "description": d} }
	return map[string]any{
		"type":     "object",
		"required": []string{"url"},
		"properties": map[string]any{
			"url":          str("The public page (http/https) you read."),
			"title":        str("Its title."),
			"quote":        str("A sentence copied exactly from the page (at least 20 characters). Omit to record the page as a source only."),
			"claim":        str("One line: what the quote says."),
			"company":      str("The organisation the quote is about, if any."),
			"domain":       str("Its website domain, if known."),
			"signal_type":  map[string]any{"type": "string", "enum": append([]string{""}, research.SignalTypes...)},
			"published_at": str("YYYY-MM-DD."),
		},
	}
}

// SummarySchema is what submit_research takes for a summary task, as JSON Schema.
func SummarySchema() map[string]any {
	return map[string]any{
		"type":     "object",
		"required": []string{"task_id", "answer"},
		"properties": map[string]any{
			"task_id": map[string]any{"type": "string"},
			"answer": map[string]any{"type": "string", "description": "The answer to the question from these pages only, citing them as [1], [2] by their source number. " +
				"Say plainly what the pages do not answer."},
			"insights": map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string"},
				"description": "Optional: up to 10 one-line takeaways (who to talk to, what changed, what to do next), each citing a source."},
			"findings": map[string]any{"type": "array", "items": FindingSchema(),
				"description": "Optional: more evidence you read on these pages (or others) that Moharrik missed."},
		},
	}
}

// Brief builds the brief for one claimed task.
func Brief(ctx context.Context, db *sql.DB, s *tenant.Scope, t research.AgentTask) (*TaskBrief, error) {
	p, err := prospects.GetProfile(ctx, db, s.WorkspaceID)
	if err != nil {
		return nil, err
	}
	b := &TaskBrief{Task: t, Signals: SignalDefinitions,
		ICP: ICPBrief{Offer: p.Offer, Industries: nz(p.Industries), Sizes: nz(p.Sizes), Regions: nz(p.Regions), Keywords: nz(p.Keywords),
			ExcludeKeywords: nz(p.ExcludeKeywords), SignalWeights: weights(p.SignalWeights), Configured: p.Configured}}
	plan := research.PlanQuery(t.Topic)
	seen := map[string]bool{}
	for _, q := range append([]string{t.Topic}, plan.Queries...) {
		if q = strings.TrimSpace(q); q != "" && !seen[strings.ToLower(q)] {
			seen[strings.ToLower(q)] = true
			b.SearchQueries = append(b.SearchQueries, q)
		}
	}
	if t.Mode == "urls" {
		b.SearchQueries = []string{}
	}
	switch t.Kind {
	case "summary":
		if !canSee(s, authz.ResearchRead) {
			return nil, ErrNeedsResearchRead
		}
		pages, quotes, err := research.SummaryMaterial(ctx, db, s.WorkspaceID, t.RunID)
		if err != nil {
			return nil, err
		}
		b.SearchQueries, b.Pages, b.Evidence = []string{}, pages, quotes
		b.Goal = "Moharrik already searched and read the web for this question; it has no model to think with. Read the pages below and " +
			"answer the question from them only, citing them as [source]. Then add a few insights. Question: " + t.Topic
		b.Submit = SubmitGuide{Tool: "submit_research", OutputSchema: SummarySchema(),
			Limits: map[string]int{"answer_chars": 8000, "insights": 10, "findings_per_call": MaxFindingsPerCall, "submits_per_hour": MaxSubmitsPerHour}}
	case "discovery":
		b.Goal = "Find companies that match the ICP and show a buying signal now. For each, name the person to talk to if a public page " +
			"names them, say why now, and suggest a short opener. Every candidate needs at least one public URL with a sentence copied from it."
		b.Submit = SubmitGuide{Tool: "submit_opportunities", OutputSchema: CandidateSchema(),
			Limits: map[string]int{"candidates_per_call": MaxCandidatesPerCall, "evidence_per_candidate": MaxEvidencePerCandidate,
				"submits_per_hour": MaxSubmitsPerHour, "items_per_hour": MaxItemsPerHour}}
	default:
		b.Goal = "Answer the question from public sources. Record each page you relied on with a sentence copied from it, and the companies " +
			"and signals it reports. Give a short answer that cites the sources."
		if t.Mode == "urls" {
			b.Goal = "Read exactly these pages (task.urls) and answer the question from them only: " + t.Topic
		}
		b.Submit = SubmitGuide{Tool: "submit_research", OutputSchema: FindingSchema(),
			Limits: map[string]int{"findings_per_call": MaxFindingsPerCall, "submits_per_hour": MaxSubmitsPerHour, "items_per_hour": MaxItemsPerHour}}
	}
	b.Submit.Then = "complete_task with this task_id when you are done (or fail_task with the reason). Several submits per task are fine."
	if t.Kind == "summary" {
		b.Submit.Then = "complete_task with this task_id once the answer is in (or fail_task with the reason)."
		b.Submit.Rules = []string{
			"Answer from these pages only; cite each claim as [source]. Where they do not answer, say so.",
			"Do not contact anyone, and do not invent names, emails or numbers.",
			"Tell Moharrik who you are: pass agent.name and agent.model.",
		}
		return b, nil
	}
	b.Submit.Rules = []string{
		"Use only public pages you actually read; copy quotes exactly. No URL, no candidate.",
		"Never invent emails or phone numbers; leave them out unless a page publishes them.",
		"Do not contact anyone. Openers are stored as drafts for a person; Moharrik never sends them.",
		"Skip companies in dedupe.crm_companies and dedupe.known, and anything matching icp.exclude_keywords.",
		"Tell Moharrik who you are: pass agent.name and agent.model.",
	}

	b.Dedupe = DedupeHints{CRMCompanies: []KnownCompany{}, Known: []KnownCompany{},
		Note: "Moharrik dedupes again on submit — by domain and name against the CRM, and by email against contacts and leads."}
	if canSee(s, authz.ContactsRead) {
		rows, err := db.QueryContext(ctx, `SELECT name, domain FROM companies WHERE workspace_id = $1 ORDER BY updated_at DESC LIMIT 200`, s.WorkspaceID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k KnownCompany
			if rows.Scan(&k.Name, &k.Domain) == nil {
				b.Dedupe.CRMCompanies = append(b.Dedupe.CRMCompanies, k)
			}
		}
		rows.Close()
	}
	if canSee(s, authz.ResearchRead) {
		rows, err := db.QueryContext(ctx, `SELECT s.name, s.domain, coalesce(p.status, s.status) FROM research_companies s
			LEFT JOIN prospects p ON p.workspace_id = s.workspace_id AND p.suggestion_id = s.id
			WHERE s.workspace_id = $1 ORDER BY s.last_seen_at DESC LIMIT 300`, s.WorkspaceID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k KnownCompany
			if rows.Scan(&k.Name, &k.Domain, &k.Status) == nil {
				b.Dedupe.Known = append(b.Dedupe.Known, k)
			}
		}
		rows.Close()
	}
	return b, nil
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func weights(w map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range prospects.DefaultWeights {
		out[k] = v
	}
	for k, v := range w {
		out[k] = v
	}
	return out
}

// TakeTasks is opportunity_tasks: the caller's open tasks plus waiting ones,
// leased to it. With nothing waiting and create=true, the ICP's next
// discovery query is queued and handed out, so "find opportunities" always has
// work while the profile names something to look for.
func TakeTasks(ctx context.Context, db *sql.DB, s *tenant.Scope, c research.Claimant, kind string, limit int, create bool) ([]TaskBrief, string, error) {
	if !canSee(s, authz.ResearchRead) {
		// A summary brief carries the pages and evidence a run collected:
		// research:read material. Without it, only the other kinds.
		switch kind {
		case "summary":
			return nil, "", ErrNeedsResearchRead
		case "":
			kind = research.KindNotSummary
		}
	}
	tasks, err := research.ClaimAgentTasks(ctx, db, s.WorkspaceID, c, kind, limit)
	if err != nil {
		return nil, "", err
	}
	note := ""
	if len(tasks) == 0 && create && (kind == "" || kind == research.KindNotSummary || kind == "discovery") {
		if _, err := prospects.QueueDiscovery(ctx, db, s.WorkspaceID, s.UserID, "assistant"); err != nil {
			if errors.Is(err, prospects.ErrNotConfigured) {
				return []TaskBrief{}, "Nothing to do: the ICP names nothing to look for. Set it with set_icp_profile (industries, regions, keywords) " +
					"or capability_profile (queries), then ask again.", nil
			}
			return nil, "", err
		}
		if tasks, err = research.ClaimAgentTasks(ctx, db, s.WorkspaceID, c, kind, limit); err != nil {
			return nil, "", err
		}
		note = "The queue was empty, so the ICP's next discovery query was queued for you."
	}
	out := make([]TaskBrief, 0, len(tasks))
	for _, t := range tasks {
		b, err := Brief(ctx, db, s, t)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *b)
	}
	if len(out) == 0 && note == "" {
		note = "No tasks are waiting."
		if n := research.CollectingRuns(ctx, db, s.WorkspaceID); n > 0 {
			note = fmt.Sprintf("No tasks are waiting yet: Moharrik is still collecting pages for %d research run(s); each becomes a "+
				"summary task when it finishes. Ask again in a minute.", n)
		}
	}
	return out, note, nil
}

// ── submissions ──────────────────────────────────────────────────────────

// Candidate is one opportunity an agent found.
type Candidate struct {
	Company         string              `json:"company"`
	Domain          string              `json:"domain"`
	Person          *CandidatePerson    `json:"person"`
	SignalType      string              `json:"signal_type"`
	WhyNow          string              `json:"why_now"`
	SuggestedOpener string              `json:"suggested_opener"`
	OpenerSubject   string              `json:"opener_subject"`
	Locale          string              `json:"locale"`
	Confidence      float64             `json:"confidence"`
	Evidence        []CandidateEvidence `json:"evidence"`
}

// CandidatePerson is who to talk to.
type CandidatePerson struct {
	Name       string `json:"name"`
	Role       string `json:"role"`
	Email      string `json:"email"`
	LinkedIn   string `json:"linkedin"`
	Phone      string `json:"phone"`
	ProfileURL string `json:"profile_url"`
}

// CandidateEvidence is where the agent read it.
type CandidateEvidence struct {
	URL         string `json:"url"`
	Quote       string `json:"quote"`
	Title       string `json:"title"`
	PublishedAt string `json:"published_at"`
}

// Finding is one submit_research finding.
type Finding struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Quote       string `json:"quote"`
	Claim       string `json:"claim"`
	Company     string `json:"company"`
	Domain      string `json:"domain"`
	SignalType  string `json:"signal_type"`
	PublishedAt string `json:"published_at"`
}

// Rejection is an item refused, and why.
type Rejection struct {
	Index  int      `json:"index"`
	Name   string   `json:"name,omitempty"`
	Errors []string `json:"errors"`
}

// CandidateResult is what became of one accepted candidate.
type CandidateResult struct {
	Index        int     `json:"index"`
	Company      string  `json:"company"`
	Outcome      string  `json:"outcome" enum:"created,updated,in_crm,already_decided,merged"`
	ProspectID   string  `json:"prospect_id,omitempty"`
	Score        float64 `json:"score,omitempty"`
	CRMCompanyID string  `json:"crm_company_id,omitempty"`
	ContactID    string  `json:"existing_contact_id,omitempty" doc:"A CRM contact already has this person's email."`
	LeadID       string  `json:"existing_lead_id,omitempty" doc:"A lead already has this person's email."`
	DraftID      string  `json:"draft_id,omitempty" doc:"The opener, stored as a draft. Never sent."`
}

// SubmitResult is the answer to submit_opportunities.
type SubmitResult struct {
	TaskID   string            `json:"task_id"`
	RunID    string            `json:"run_id"`
	Received int               `json:"received"`
	Stored   int               `json:"stored" doc:"New evidence rows."`
	Results  []CandidateResult `json:"results"`
	Rejected []Rejection       `json:"rejected"`
	Sent     bool              `json:"sent" doc:"Always false: nothing is sent to anyone."`
	Next     string            `json:"next"`
}

var phoneRe = regexp.MustCompile(`^\+?[0-9][0-9 ()\-.]{5,30}$`)

func parseDay(s string) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > 10 {
		s = s[:10]
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, fmt.Errorf("published_at %q is not YYYY-MM-DD", s)
	}
	if t.After(time.Now().Add(48 * time.Hour)) {
		return nil, fmt.Errorf("published_at %q is in the future", s)
	}
	return &t, nil
}

// cleanDomain reads a domain or a website address as a registrable domain on a public host.
func cleanDomain(d string) (string, error) {
	d = strings.TrimSpace(d)
	if d == "" {
		return "", nil
	}
	if !strings.Contains(d, "://") {
		d = "https://" + d
	}
	u, err := research.PublicURL(d)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	return research.RegistrableDomain(parsed.Hostname()), nil
}

// validCandidate cleans one candidate and returns its evidence as findings,
// or the reasons it is refused.
func validCandidate(c *Candidate) ([]research.AgentFinding, []string) {
	var errs []string
	c.Company = research.SanitizeText(c.Company, 120)
	c.WhyNow = research.SanitizeText(c.WhyNow, 600)
	c.SuggestedOpener = research.SanitizeText(c.SuggestedOpener, 2000)
	c.OpenerSubject = research.SanitizeText(c.OpenerSubject, 200)
	c.SignalType = strings.ToLower(strings.TrimSpace(c.SignalType))
	if c.Company == "" {
		errs = append(errs, "company is required")
	}
	if !research.ValidSignal(c.SignalType) {
		errs = append(errs, fmt.Sprintf("signal_type %q is not one of %s", c.SignalType, strings.Join(research.SignalTypes, ", ")))
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		errs = append(errs, "confidence is 0 to 1")
	}
	if c.Locale != "" && c.Locale != "en" && c.Locale != "ar" {
		errs = append(errs, "locale is en or ar")
	}
	if d, err := cleanDomain(c.Domain); err != nil {
		errs = append(errs, "domain: "+err.Error())
	} else {
		c.Domain = d
	}
	if p := c.Person; p != nil {
		p.Name = research.SanitizeText(p.Name, 120)
		p.Role = research.SanitizeText(p.Role, 120)
		if e := strings.TrimSpace(p.Email); e != "" {
			a, err := mail.ParseAddress(e)
			if err != nil || len(e) > 254 || a.Address != e {
				errs = append(errs, fmt.Sprintf("person.email %q is not an email address", research.SanitizeText(e, 80)))
			}
			p.Email = strings.ToLower(e)
		}
		if l := strings.TrimSpace(p.LinkedIn); l != "" {
			u, err := research.PublicURL(l)
			if err != nil || !strings.Contains(strings.ToLower(u), "linkedin.com/") {
				errs = append(errs, "person.linkedin must be a linkedin.com URL")
			}
			p.LinkedIn = u
		}
		if ph := strings.TrimSpace(p.Phone); ph != "" {
			if !phoneRe.MatchString(ph) {
				errs = append(errs, fmt.Sprintf("person.phone %q is not a phone number", research.SanitizeText(ph, 40)))
			}
			p.Phone = ph
		}
		if pu := strings.TrimSpace(p.ProfileURL); pu != "" {
			u, err := research.PublicURL(pu)
			if err != nil {
				errs = append(errs, "person.profile_url: "+err.Error())
			}
			p.ProfileURL = u
		}
	}
	if len(c.Evidence) == 0 {
		errs = append(errs, "evidence is required: at least one public URL with a sentence copied from it")
	}
	if len(c.Evidence) > MaxEvidencePerCandidate {
		errs = append(errs, fmt.Sprintf("at most %d evidence items per candidate", MaxEvidencePerCandidate))
	}
	var out []research.AgentFinding
	for i, e := range c.Evidence {
		u, err := research.PublicURL(e.URL)
		if err != nil {
			errs = append(errs, fmt.Sprintf("evidence[%d].url: %s", i, err))
			continue
		}
		q := research.SanitizeText(e.Quote, 1000)
		if len([]rune(q)) < minQuoteRunes {
			errs = append(errs, fmt.Sprintf("evidence[%d].quote must be a sentence copied from the page (at least %d characters)", i, minQuoteRunes))
			continue
		}
		pub, err := parseDay(e.PublishedAt)
		if err != nil {
			errs = append(errs, fmt.Sprintf("evidence[%d]: %s", i, err))
			continue
		}
		out = append(out, research.AgentFinding{URL: u, Title: research.SanitizeText(e.Title, 300), Quote: q, Claim: c.WhyNow,
			CompanyName: c.Company, CompanyDomain: c.Domain, SignalType: c.SignalType, Confidence: c.Confidence, PublishedAt: pub})
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// reserve books a submission against the credential's hourly quota before
// anything is written: under a per-credential lock it counts the last hour
// and inserts this submission's row, so concurrent or failing calls cannot
// slip past the limit. settle fills in what became of it.
func reserve(ctx context.Context, db *sql.DB, s *tenant.Scope, c research.Claimant, taskID, kind string, items int) (string, error) {
	ws := s.WorkspaceID
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "agent-submit:"+ws+":"+c.TokenID); err != nil {
		return "", err
	}
	var calls, sent int
	if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(received), 0) FROM agent_submissions
		WHERE workspace_id = $1 AND token_id = $2 AND created_at > now() - interval '1 hour'`, ws, c.TokenID).Scan(&calls, &sent); err != nil {
		return "", err
	}
	if calls >= MaxSubmitsPerHour || sent+items > MaxItemsPerHour {
		return "", ErrRateLimited
	}
	var id string
	if err := tx.QueryRowContext(ctx, `INSERT INTO agent_submissions (workspace_id, task_id, kind, token_id, token_name, via, user_id, agent_name, agent_model, received)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		ws, taskID, kind, c.TokenID, research.SanitizeText(s.TokenName, 120), s.Via, s.UserID, trimRunes(c.Agent, 80), trimRunes(c.Model, 80), items).Scan(&id); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// settle records what became of a reserved submission.
func settle(ctx context.Context, db *sql.DB, ws, id string, stored, dups, rejected int) {
	_, _ = db.ExecContext(ctx, `UPDATE agent_submissions SET stored = $3, duplicates = $4, rejected = $5 WHERE workspace_id = $1 AND id = $2`,
		ws, id, stored, dups, rejected)
}

func trimRunes(v string, n int) string {
	if r := []rune(strings.TrimSpace(v)); len(r) > n {
		return string(r[:n])
	}
	return strings.TrimSpace(v)
}

func mayRun(s *tenant.Scope) error {
	if !authz.Can(s.Role, authz.ResearchRun) {
		return errors.New("your role cannot run research in this workspace")
	}
	return nil
}

// SubmitOpportunities validates, dedupes, scores and stores an agent's candidates for a task.
func SubmitOpportunities(ctx context.Context, db *sql.DB, s *tenant.Scope, c research.Claimant, taskID string, cands []Candidate) (*SubmitResult, error) {
	if err := mayRun(s); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, errors.New("candidates is empty: send at least one")
	}
	if len(cands) > MaxCandidatesPerCall {
		return nil, fmt.Errorf("at most %d candidates per call; send the rest in another call", MaxCandidatesPerCall)
	}
	ws := s.WorkspaceID
	t, err := research.HoldAgentTask(ctx, db, ws, taskID, c)
	if err != nil {
		return nil, err
	}
	sub, err := reserve(ctx, db, s, c, t.ID, "opportunities", len(cands))
	if err != nil {
		return nil, err
	}
	res := &SubmitResult{TaskID: t.ID, Received: len(cands), Results: []CandidateResult{}, Rejected: []Rejection{}}

	type ok struct {
		i   int
		c   Candidate
		key string
	}
	var good []ok
	var findings []research.AgentFinding
	firstByKey := map[string]int{}
	for i := range cands {
		cand := cands[i]
		f, errs := validCandidate(&cand)
		if errs != nil {
			res.Rejected = append(res.Rejected, Rejection{Index: i, Name: cand.Company, Errors: errs})
			continue
		}
		key := research.CompanyKey(cand.Company, cand.Domain)
		findings = append(findings, f...)
		if _, dup := firstByKey[key]; dup {
			res.Results = append(res.Results, CandidateResult{Index: i, Company: cand.Company, Outcome: "merged"})
			continue
		}
		firstByKey[key] = i
		good = append(good, ok{i, cand, key})
	}
	if len(findings) > 0 {
		runID, err := research.AgentRun(ctx, db, ws, t, c)
		if err != nil {
			return nil, err
		}
		res.RunID = runID
		if res.Stored, _, err = research.StoreAgentFindings(ctx, db, ws, runID, c, findings); err != nil {
			return nil, err
		}
		for _, g := range good {
			r := CandidateResult{Index: g.i, Company: g.c.Company}
			var status, crm string
			if err := db.QueryRowContext(ctx, `SELECT status, coalesce(crm_company_id, '') FROM research_companies WHERE workspace_id = $1 AND company_key = $2`,
				ws, g.key).Scan(&status, &crm); err != nil {
				return nil, err
			}
			person := g.c.Person
			if person == nil {
				person = &CandidatePerson{}
			}
			if person.Email != "" && canSee(s, authz.ContactsRead) {
				_ = db.QueryRowContext(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND lower(email) = $2`, ws, person.Email).Scan(&r.ContactID)
			}
			if person.Email != "" && canSee(s, authz.LeadsRead) {
				_ = db.QueryRowContext(ctx, `SELECT id FROM leads WHERE workspace_id = $1 AND lower(email) = $2 ORDER BY created_at DESC LIMIT 1`, ws, person.Email).Scan(&r.LeadID)
			}
			switch status {
			case "linked":
				r.Outcome = "in_crm"
				if canSee(s, authz.ContactsRead) {
					r.CRMCompanyID = crm
				}
				res.Results = append(res.Results, r)
				continue
			case "accepted", "dismissed":
				r.Outcome = "already_decided"
				res.Results = append(res.Results, r)
				continue
			}
			source := firstNonEmpty(person.ProfileURL, person.LinkedIn)
			if source == "" && person.Name != "" {
				source = g.c.Evidence[0].URL
			}
			pr, err := prospects.UpsertAgentProspect(ctx, db, ws, prospects.AgentProspect{RunID: res.RunID, CompanyKey: g.key, WhyNow: g.c.WhyNow,
				Contact:  prospects.ProspectContact{Name: person.Name, Title: person.Role, Email: person.Email, SourceURL: source},
				Channels: prospects.ContactChannels{Email: person.Email, LinkedIn: person.LinkedIn, Phone: person.Phone, Website: website(g.c.Domain), Other: person.ProfileURL}})
			if errors.Is(err, prospects.ErrNotSuggested) {
				r.Outcome = "already_decided"
				res.Results = append(res.Results, r)
				continue
			}
			if err != nil {
				return nil, err
			}
			r.ProspectID, r.Score = pr.ProspectID, pr.Score
			switch {
			case pr.Created:
				r.Outcome = "created"
			case pr.Status == "new":
				r.Outcome = "updated"
			default:
				r.Outcome = "already_decided"
			}
			if g.c.SuggestedOpener != "" && pr.Status == "new" {
				d, err := prospects.SaveAgentDraft(ctx, db, ws, s.UserID, pr.ProspectID, g.c.Locale, g.c.OpenerSubject, g.c.SuggestedOpener, research.AgentLabel(c))
				if err != nil {
					return nil, err
				}
				r.DraftID = d.ID
			}
			res.Results = append(res.Results, r)
		}
	}
	accepted := res.Received - len(res.Rejected)
	if err := research.CountAgentSubmitted(ctx, db, ws, t.ID, res.Received, accepted); err != nil {
		return nil, err
	}
	dups := 0
	for _, r := range res.Results {
		if r.Outcome != "created" {
			dups++
		}
	}
	settle(ctx, db, ws, sub, res.Stored, dups, len(res.Rejected))
	res.Next = "Send more with submit_opportunities, then complete_task(task_id) to finish. Prospects are on find_opportunities; a person accepts them with mark_opportunity."
	return res, nil
}

func website(domain string) string {
	if domain == "" {
		return ""
	}
	return "https://" + domain
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// ResearchResult is the answer to submit_research.
type ResearchResult struct {
	TaskID       string      `json:"task_id"`
	RunID        string      `json:"run_id"`
	Received     int         `json:"received"`
	Stored       int         `json:"stored" doc:"New evidence rows."`
	NewCompanies []string    `json:"new_companies" doc:"Companies the evidence names that research had never seen."`
	Rejected     []Rejection `json:"rejected"`
	Next         string      `json:"next"`
}

// SubmitResearch stores an agent's answer and findings for a task.
func SubmitResearch(ctx context.Context, db *sql.DB, s *tenant.Scope, c research.Claimant, taskID, answer string, insights []string, in []Finding) (*ResearchResult, error) {
	if err := mayRun(s); err != nil {
		return nil, err
	}
	answer = research.SanitizeText(answer, 8000)
	if len(insights) > 10 {
		return nil, errors.New("at most 10 insights")
	}
	var lines []string
	for _, x := range insights {
		if x = strings.TrimSpace(research.SanitizeText(x, 400)); x != "" {
			lines = append(lines, "- "+x)
		}
	}
	if len(lines) > 0 {
		if answer == "" {
			return nil, errors.New("insights go with an answer")
		}
		answer = strings.TrimSpace(answer) + "\n\nInsights:\n" + strings.Join(lines, "\n")
	}
	if len(in) == 0 && answer == "" {
		return nil, errors.New("send findings, an answer, or both")
	}
	if len(in) > MaxFindingsPerCall {
		return nil, fmt.Errorf("at most %d findings per call; send the rest in another call", MaxFindingsPerCall)
	}
	ws := s.WorkspaceID
	t, err := research.HoldAgentTask(ctx, db, ws, taskID, c)
	if err != nil {
		return nil, err
	}
	sub, err := reserve(ctx, db, s, c, t.ID, "research", len(in))
	if err != nil {
		return nil, err
	}
	if t.Kind == "summary" && answer == "" && research.RunSummary(ctx, db, ws, t.RunID) == "" {
		return nil, errors.New("a summary task needs the answer: send answer (and findings if you like)")
	}
	res := &ResearchResult{TaskID: t.ID, Received: len(in), NewCompanies: []string{}, Rejected: []Rejection{}}
	var findings []research.AgentFinding
	for i, f := range in {
		var errs []string
		u, err := research.PublicURL(f.URL)
		if err != nil {
			errs = append(errs, "url: "+err.Error())
		}
		q := research.SanitizeText(f.Quote, 1000)
		if q != "" && len([]rune(q)) < minQuoteRunes {
			errs = append(errs, fmt.Sprintf("quote must be a sentence copied from the page (at least %d characters)", minQuoteRunes))
		}
		sig := strings.ToLower(strings.TrimSpace(f.SignalType))
		if !research.ValidSignal(sig) {
			errs = append(errs, fmt.Sprintf("signal_type %q is not one of %s", sig, strings.Join(research.SignalTypes, ", ")))
		}
		pub, err := parseDay(f.PublishedAt)
		if err != nil {
			errs = append(errs, err.Error())
		}
		domain, err := cleanDomain(f.Domain)
		if err != nil {
			errs = append(errs, "domain: "+err.Error())
		}
		if len(errs) > 0 {
			res.Rejected = append(res.Rejected, Rejection{Index: i, Errors: errs})
			continue
		}
		findings = append(findings, research.AgentFinding{URL: u, Title: research.SanitizeText(f.Title, 300), Quote: q,
			Claim: research.SanitizeText(f.Claim, 500), CompanyName: research.SanitizeText(f.Company, 120), CompanyDomain: domain,
			SignalType: sig, PublishedAt: pub})
	}
	if len(findings) > 0 || answer != "" {
		runID, err := research.AgentRun(ctx, db, ws, t, c)
		if err != nil {
			return nil, err
		}
		res.RunID = runID
		if len(findings) > 0 {
			stored, fresh, err := research.StoreAgentFindings(ctx, db, ws, runID, c, findings)
			if err != nil {
				return nil, err
			}
			res.Stored = stored
			if fresh != nil {
				res.NewCompanies = fresh
			}
		}
		if answer != "" {
			set := research.SetAgentSummary(ctx, db, ws, runID, answer)
			if t.Kind == "summary" {
				set = research.SetAgentRunSummary(ctx, db, ws, runID, answer, c)
			}
			if set != nil {
				return nil, set
			}
		}
	}
	if err := research.CountAgentSubmitted(ctx, db, ws, t.ID, res.Received, res.Received-len(res.Rejected)); err != nil {
		return nil, err
	}
	settle(ctx, db, ws, sub, res.Stored, res.Received-len(res.Rejected)-res.Stored, len(res.Rejected))
	res.Next = "complete_task(task_id) when done. The run reads back with get_research(run_id); evidence with find_evidence."
	switch t.Kind {
	case "discovery":
		res.Next = "complete_task(task_id) when done: the companies your findings name then become scored prospects (find_opportunities)."
	case "summary":
		res.Next = "complete_task(task_id): the run now shows your summary (get_research(run_id))."
	}
	return res, nil
}

// CloseTask is complete_task / fail_task.
func CloseTask(ctx context.Context, a *app.App, s *tenant.Scope, c research.Claimant, taskID, status, note string) (*research.AgentTask, error) {
	if err := mayRun(s); err != nil {
		return nil, err
	}
	return research.CloseAgentTask(ctx, a, s.WorkspaceID, taskID, c, status, note)
}

// ── what a person pastes ─────────────────────────────────────────────────

// MCPServerURL is set by the server to the MCP endpoint people connect to
// (mcp.MCPServerURL; that package imports this one, so it is wired at boot).
var MCPServerURL func() string

// MCPURL is this deployment's MCP endpoint.
func MCPURL() string {
	if MCPServerURL != nil {
		if u := MCPServerURL(); u != "" {
			return u
		}
	}
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v + "/api/mcp"
		}
	}
	return "http://localhost:8120/api/mcp"
}

// AgentTools are the MCP tools an assistant uses as the workspace's AI, in order.
var AgentTools = []string{"agent_tasks", "submit_research", "opportunity_tasks", "submit_opportunities", "complete_task", "fail_task", "find_opportunities"}

// AgentPrompt is what a person gives their AI assistant to be the workspace's
// AI: summarise the research Moharrik collected, then run the finder.
func AgentPrompt(workspace string) string {
	return "You are the AI for my Moharrik workspace \"" + workspace + "\" (it has no model key of its own). Use the Moharrik MCP tools:\n" +
		"1. Call agent_tasks with agent {name, model} set to who you are, and do every task it hands you:\n" +
		"   - summary: Moharrik already searched and read the pages. Answer task.task.topic from task.pages only, citing them as [source], " +
		"and send it with submit_research(task_id, answer, insights).\n" +
		"   - research: research the question with your own web search and page reading, then submit_research with the answer and the " +
		"findings (each a public URL and a sentence copied exactly from it).\n" +
		"   - discovery: as in step 2.\n" +
		"   Close each task with complete_task (or fail_task with the reason). Repeat until agent_tasks returns nothing.\n" +
		"2. Then run my Opportunity Finder: call opportunity_tasks. For each task run its search_queries (and your own variations), judge every " +
		"company against task.icp, skip anything in task.dedupe, and keep only companies with a real, recent signal. Submit them with " +
		"submit_opportunities: every candidate needs at least one public URL and a sentence copied exactly from that page. Name the person " +
		"to talk to only if a public page names them; never guess emails. Then complete_task.\n" +
		"3. Show me the result: the summaries (get_research) and the top prospects (find_opportunities). Do not contact anyone — openers are " +
		"drafts for me to review."
}
