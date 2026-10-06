package contactcontext

// MH-1169: the AI context brief. One markdown field per contact, company and
// lead that says who they are, how we speak to them, and what matters to
// them. The team writes it (or has it drafted from the record's own history,
// then edits it); every AI path that talks about or to the record reads it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// BriefKind is the record a brief belongs to.
type BriefKind string

const (
	BriefContact BriefKind = "contact"
	BriefCompany BriefKind = "company"
	BriefLead    BriefKind = "lead"
)

// MaxBriefLen caps a saved brief: long enough for a real profile, short
// enough to sit in every prompt about the record.
const MaxBriefLen = 20000

// ErrBriefNotFound is a record that is not in this workspace.
var ErrBriefNotFound = errors.New("record not found")

func (k BriefKind) table() (string, error) {
	switch k {
	case BriefContact:
		return "contacts", nil
	case BriefCompany:
		return "companies", nil
	case BriefLead:
		return "leads", nil
	}
	return "", fmt.Errorf("unknown record kind %q", k)
}

// Brief is a record's saved context.
type Brief struct {
	Markdown  string     `json:"markdown"`
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy string     `json:"updated_by"`
}

// BriefDraft is a generated brief, not yet saved: the member reviews it first.
type BriefDraft struct {
	Markdown   string `json:"markdown"`
	NoProvider bool   `json:"no_provider"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	Facts      int    `json:"facts" doc:"How many dated facts the draft was built from."`
}

// GetBrief reads a record's brief.
func GetBrief(ctx context.Context, a *app.App, workspaceID string, kind BriefKind, id string) (*Brief, error) {
	table, err := kind.table()
	if err != nil {
		return nil, err
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var b Brief
	err = a.SQLDB.QueryRowContext(sctx,
		`SELECT ai_context, ai_context_updated_at, ai_context_updated_by FROM `+table+` WHERE workspace_id=$1 AND id=$2`,
		workspaceID, id).Scan(&b.Markdown, &b.UpdatedAt, &b.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBriefNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// SaveBrief replaces a record's brief. An empty markdown clears it.
func SaveBrief(ctx context.Context, a *app.App, workspaceID string, kind BriefKind, id, markdown, userID string) (*Brief, error) {
	table, err := kind.table()
	if err != nil {
		return nil, err
	}
	markdown = strings.TrimSpace(markdown)
	if len(markdown) > MaxBriefLen {
		return nil, fmt.Errorf("the context is too long: keep it under %d characters", MaxBriefLen)
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var b Brief
	err = a.SQLDB.QueryRowContext(sctx,
		`UPDATE `+table+` SET ai_context=$3, ai_context_updated_at=now(), ai_context_updated_by=$4
		 WHERE workspace_id=$1 AND id=$2
		 RETURNING ai_context, ai_context_updated_at, ai_context_updated_by`,
		workspaceID, id, markdown, userID).Scan(&b.Markdown, &b.UpdatedAt, &b.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBriefNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// briefFacts is what a draft is built from: a header of known fields, and
// dated facts newest first.
type briefFacts struct {
	subject string
	fields  [][2]string
	entries []timelineEntry
	current string // the brief already saved, which the draft refines
}

// DraftBrief writes a brief from the record's own data. With no AI provider
// it returns a template filled with the known fields, with the parts only a
// person can know left as prompts to fill in. Nothing is saved.
func DraftBrief(ctx context.Context, a *app.App, aiClient *ai.Client, workspaceID string, kind BriefKind, id string) (*BriefDraft, error) {
	f, err := gatherBriefFacts(ctx, a, workspaceID, kind, id)
	if err != nil {
		return nil, err
	}
	d := &BriefDraft{NoProvider: true, Facts: len(f.entries)}
	if aiClient != nil {
		res, genErr := aiClient.Generate(ctx, ai.TaskSummarise, ai.GenerateRequest{
			System: "You write the context brief a CRM keeps about one " + string(kind) + ", so that an AI assistant " +
				"knows how this team speaks to them. Use ONLY the fields and numbered facts given. Write markdown with " +
				"exactly these ## sections: Who they are; Relationship so far; How we speak to them (language, tone, " +
				"formality, preferred channel, how they are greeted and addressed — infer it from how our own messages " +
				"read); What matters to them; Open threads; Do and don't. Keep it short and concrete, bullets over prose. " +
				"Where the facts say nothing, write a short italic prompt like _Add …_ instead of guessing. Never invent " +
				"a name, date, amount or promise. If an existing brief is given, keep what it says unless a fact " +
				"contradicts it, and write in the language most of the messages use.",
			Messages:    []ai.Message{{Role: "user", Content: buildBriefPrompt(f)}},
			MaxTokens:   1400,
			Temperature: 0.2,
		})
		if genErr == nil {
			d.Markdown = strings.TrimSpace(res.Text)
			d.NoProvider = false
			d.Provider, d.Model = res.Provenance.Provider, res.Provenance.Model
		} else if !errors.Is(genErr, ai.ErrNoProvider) {
			return nil, fmt.Errorf("drafting the context: %w", genErr)
		}
	}
	if d.Markdown == "" {
		d.Markdown = templateBrief(f)
	}
	return d, nil
}

func gatherBriefFacts(ctx context.Context, a *app.App, workspaceID string, kind BriefKind, id string) (*briefFacts, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	f := &briefFacts{}
	notFound := func(err error) error {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBriefNotFound
		}
		return err
	}
	switch kind {
	case BriefContact:
		var name, email, phone, title, role, company, lifecycle, notes, bio, locale string
		if err := a.SQLDB.QueryRowContext(sctx, `
			SELECT c.name, coalesce(c.email,''), coalesce(c.phone,''), coalesce(c.title,''), coalesce(c.job_role,''),
			       coalesce(co.name,''), coalesce(c.lifecycle,''), coalesce(c.notes,''), coalesce(c.biography,''),
			       coalesce(c.locale,''), c.ai_context
			FROM contacts c LEFT JOIN companies co ON co.workspace_id=c.workspace_id AND co.id=c.company_id
			WHERE c.workspace_id=$1 AND c.id=$2`, workspaceID, id).
			Scan(&name, &email, &phone, &title, &role, &company, &lifecycle, &notes, &bio, &locale, &f.current); err != nil {
			return nil, notFound(err)
		}
		f.subject = "Contact: " + firstOf(name, email)
		f.fields = nonEmpty([][2]string{{"Name", name}, {"Email", email}, {"Phone", phone}, {"Title", firstOf(title, role)},
			{"Company", company}, {"Lifecycle stage", lifecycle}, {"Language on file", locale}, {"Biography", bio}, {"Notes", notes}})
		entries, err := gatherTimeline(sctx, a, workspaceID, id)
		if err != nil {
			return nil, err
		}
		acts, err := activityEntries(sctx, a, `workspace_id=$1 AND contact_id=$2`, workspaceID, id)
		if err != nil {
			return nil, err
		}
		f.entries = newestFirst(append(entries, acts...), 70)

	case BriefCompany:
		var name, domain, industry, size, country, notes string
		if err := a.SQLDB.QueryRowContext(sctx, `
			SELECT name, domain, industry, size, coalesce(country,''), notes, ai_context
			FROM companies WHERE workspace_id=$1 AND id=$2`, workspaceID, id).
			Scan(&name, &domain, &industry, &size, &country, &notes, &f.current); err != nil {
			return nil, notFound(err)
		}
		f.subject = "Company: " + name
		people, err := companyPeople(sctx, a, workspaceID, id)
		if err != nil {
			return nil, err
		}
		f.fields = nonEmpty([][2]string{{"Name", name}, {"Domain", domain}, {"Industry", industry}, {"Size", size},
			{"Country", country}, {"Notes", notes}, {"People", strings.Join(people.labels, "; ")}})
		var entries []timelineEntry
		for _, pid := range people.ids {
			e, err := gatherTimeline(sctx, a, workspaceID, pid)
			if err != nil {
				return nil, err
			}
			entries = append(entries, e...)
		}
		acts, err := activityEntries(sctx, a, `workspace_id=$1 AND company_id=$2`, workspaceID, id)
		if err != nil {
			return nil, err
		}
		deals, err := companyDeals(sctx, a, workspaceID, id)
		if err != nil {
			return nil, err
		}
		f.entries = newestFirst(append(append(entries, acts...), deals...), 70)

	case BriefLead:
		var name, email, phone, company, message, budget, form, locale, contactID string
		var created time.Time
		if err := a.SQLDB.QueryRowContext(sctx, `
			SELECT name, email, phone, company, message, coalesce(budget,''), form_name, locale,
			       coalesce(contact_id,''), created_at, ai_context
			FROM leads WHERE workspace_id=$1 AND id=$2`, workspaceID, id).
			Scan(&name, &email, &phone, &company, &message, &budget, &form, &locale, &contactID, &created, &f.current); err != nil {
			return nil, notFound(err)
		}
		f.subject = "Lead: " + firstOf(name, email)
		f.fields = nonEmpty([][2]string{{"Name", name}, {"Email", email}, {"Phone", phone}, {"Company", company},
			{"Budget", budget}, {"Form", form}, {"Language of the form", locale}})
		f.entries = []timelineEntry{{when: created, kind: "lead", id: id, text: "them: " + trimTo(message, 1200)}}
		if contactID != "" {
			e, err := gatherTimeline(sctx, a, workspaceID, contactID)
			if err != nil {
				return nil, err
			}
			f.entries = newestFirst(append(f.entries, e...), 70)
		}

	default:
		return nil, fmt.Errorf("unknown record kind %q", kind)
	}
	return f, nil
}

func activityEntries(sctx context.Context, a *app.App, where, workspaceID, id string) ([]timelineEntry, error) {
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, created_at, kind, body, done_at IS NOT NULL FROM activities
		WHERE `+where+` ORDER BY created_at DESC LIMIT 30`, workspaceID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []timelineEntry
	for rows.Next() {
		var aid, kind, body string
		var when time.Time
		var done bool
		if err := rows.Scan(&aid, &when, &kind, &body, &done); err != nil {
			return nil, err
		}
		if done {
			kind += ", done"
		}
		out = append(out, timelineEntry{when: when, kind: kind, id: aid, text: trimTo(body, 240)})
	}
	return out, rows.Err()
}

type people struct{ ids, labels []string }

func companyPeople(sctx context.Context, a *app.App, workspaceID, companyID string) (people, error) {
	var p people
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, name, coalesce(email,''), coalesce(title,'') FROM contacts
		WHERE workspace_id=$1 AND company_id=$2 ORDER BY updated_at DESC LIMIT 8`, workspaceID, companyID)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, email, title string
		if err := rows.Scan(&id, &name, &email, &title); err != nil {
			return p, err
		}
		label := firstOf(name, email)
		if title != "" {
			label += " (" + title + ")"
		}
		p.ids = append(p.ids, id)
		p.labels = append(p.labels, label)
	}
	return p, rows.Err()
}

func companyDeals(sctx context.Context, a *app.App, workspaceID, companyID string) ([]timelineEntry, error) {
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT d.id, d.created_at, d.title, coalesce(s.name,''), d.closed_at FROM deals d
		LEFT JOIN pipeline_stages s ON s.workspace_id=d.workspace_id AND s.id=d.stage_id
		WHERE d.workspace_id=$1 AND d.company_id=$2 ORDER BY d.created_at DESC LIMIT 20`, workspaceID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []timelineEntry
	for rows.Next() {
		var id, title, stage string
		var when time.Time
		var closed *time.Time
		if err := rows.Scan(&id, &when, &title, &stage, &closed); err != nil {
			return nil, err
		}
		status := "stage " + stage
		if closed != nil {
			status += ", closed " + closed.Format("2006-01-02")
		}
		out = append(out, timelineEntry{when: when, kind: "deal", id: id, text: "Deal " + title + " (" + status + ")"})
	}
	return out, rows.Err()
}

func buildBriefPrompt(f *briefFacts) string {
	var b strings.Builder
	b.WriteString(f.subject + "\n\nFields:\n")
	for _, kv := range f.fields {
		fmt.Fprintf(&b, "- %s: %s\n", kv[0], trimTo(kv[1], 600))
	}
	b.WriteString("\nFacts (numbered, most recent first; \"us\" is this team, \"them\" is the customer):\n")
	for i, e := range f.entries {
		fmt.Fprintf(&b, "[%d] %s (%s) %s\n", i+1, e.when.Format("2006-01-02"), e.kind, e.text)
	}
	if len(f.entries) == 0 {
		b.WriteString("(none yet)\n")
	}
	if strings.TrimSpace(f.current) != "" {
		b.WriteString("\nThe brief saved today, to refine:\n" + trimTo(f.current, 6000) + "\n")
	}
	return b.String()
}

// templateBrief is the no-AI draft: the known fields filled in, the rest left
// as prompts for the member.
func templateBrief(f *briefFacts) string {
	if strings.TrimSpace(f.current) != "" {
		return f.current
	}
	var b strings.Builder
	b.WriteString("## Who they are\n")
	for _, kv := range f.fields {
		fmt.Fprintf(&b, "- **%s:** %s\n", kv[0], strings.ReplaceAll(trimTo(kv[1], 400), "\n", " "))
	}
	b.WriteString("\n## Relationship so far\n")
	if len(f.entries) == 0 {
		b.WriteString("_Nothing logged yet._\n")
	}
	for i, e := range f.entries {
		if i == 8 {
			break
		}
		fmt.Fprintf(&b, "- %s · %s · %s\n", e.when.Format("2006-01-02"), e.kind, strings.ReplaceAll(e.text, "\n", " "))
	}
	b.WriteString("\n## How we speak to them\n- _Add the language, tone and formality: e.g. Arabic, warm, first name._\n" +
		"- _Add the channel they answer on, and how they like to be greeted._\n" +
		"\n## What matters to them\n- _Add their goals, budget and what they worry about._\n" +
		"\n## Open threads\n- _Add anything promised or waiting on either side._\n" +
		"\n## Do and don't\n- _Add what works with them, and what to avoid._\n")
	return b.String()
}

// briefFor is the saved brief to hand an AI prompt about a contact: the
// contact's own, then their company's. Empty when neither has one.
func briefFor(ctx context.Context, a *app.App, workspaceID, contactID string) string {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var own, company string
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT c.ai_context, coalesce(co.ai_context,'') FROM contacts c
		LEFT JOIN companies co ON co.workspace_id=c.workspace_id AND co.id=c.company_id
		WHERE c.workspace_id=$1 AND c.id=$2`, workspaceID, contactID).Scan(&own, &company); err != nil {
		return ""
	}
	var b strings.Builder
	if own = strings.TrimSpace(own); own != "" {
		b.WriteString("Context the team keeps about this contact:\n" + trimTo(own, 6000) + "\n\n")
	}
	if company = strings.TrimSpace(company); company != "" {
		b.WriteString("Context the team keeps about their company:\n" + trimTo(company, 3000) + "\n\n")
	}
	return b.String()
}

func newestFirst(entries []timelineEntry, limit int) []timelineEntry {
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].when.After(entries[j].when) })
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func nonEmpty(kv [][2]string) [][2]string {
	out := kv[:0]
	for _, p := range kv {
		if strings.TrimSpace(p[1]) != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstOf(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
