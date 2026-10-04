package contactcontext

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ContactReport is generate_contact_report's result, already persisted.
type ContactReport struct {
	ID         string `json:"id"`
	Version    int    `json:"version"`
	Markdown   string `json:"markdown"`
	NoProvider bool   `json:"no_provider"`
	Provider   string `json:"provider,omitempty"`
	Model      string `json:"model,omitempty"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
}

// timelineEntry is one dated fact the report can cite by id.
type timelineEntry struct {
	when   Time
	kind   string
	id     string
	convID string
	text   string
}

// Time is a thin alias so the struct above reads cleanly; it is exactly
// time.Time.
type Time = time.Time

// GenerateReport builds a new versioned report for a contact: relationship
// summary, timeline, topics, open promises/asks, sentiment, last touch and a
// recommended next step. Every factual line is built FROM the contact's own
// data (messages/notes/deals), each carrying its source id/date, so a claim
// can always be traced back — this holds whether or not an LLM is available.
//
// Without a configured AI provider (aiClient == nil, or ai.ErrNoProvider),
// GenerateReport takes the deterministic template path: report.NoProvider is
// set true, and every section is assembled directly from the data with no
// generation call at all — MH-586 requires the feature to work end-to-end
// with zero AI configured, not merely to degrate gracefully.
//
// With a provider, the same gathered facts are handed to it as the ONLY
// source of truth, with an explicit instruction not to invent anything
// outside them; the deterministic timeline/citations section is still
// appended verbatim underneath the model's narrative, so nothing the report
// claims is ever ungrounded even if the model drifts.
func GenerateReport(ctx context.Context, a *app.App, aiClient *ai.Client, workspaceID, contactID, createdBy string) (*ContactReport, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	brief, err := loadContactBrief(ctx, a, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	entries, err := gatherTimeline(sctx, a, workspaceID, contactID)
	if err != nil {
		return nil, err
	}

	var md string
	noProvider := true
	provider, model := "", ""

	if aiClient != nil {
		prompt := buildReportPrompt(brief, entries)
		res, genErr := aiClient.Generate(ctx, ai.TaskSummarise, ai.GenerateRequest{
			System: "You write a relationship report for a CRM contact using ONLY the numbered facts given. " +
				"Cover: relationship summary, key topics, open promises/asks, sentiment, last touch, and a recommended " +
				"next step. Cite facts inline like [3]. Never state a name, date, amount or commitment that is not in " +
				"the numbered facts. Write in markdown with ## section headings.",
			Messages:    []ai.Message{{Role: "user", Content: prompt}},
			MaxTokens:   1200,
			Temperature: 0.2,
		})
		if genErr == nil {
			md = res.Text
			noProvider = false
			provider, model = res.Provenance.Provider, res.Provenance.Model
		} else if !errors.Is(genErr, ai.ErrNoProvider) {
			return nil, fmt.Errorf("contactcontext: generate_contact_report generation: %w", genErr)
		}
	}
	if md == "" {
		md = deterministicReport(brief, entries)
	}
	md += "\n\n" + citedTimelineSection(entries)

	var id string
	var version int
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO contact_context_reports (workspace_id, contact_id, version, markdown, provider, model, no_model, created_by)
		SELECT $1, $2, coalesce((SELECT max(version) FROM contact_context_reports WHERE workspace_id=$1 AND contact_id=$2), 0) + 1,
		       $3, $4, $5, $6, $7
		RETURNING id, version`,
		workspaceID, contactID, md, provider, model, noProvider, createdBy,
	).Scan(&id, &version); err != nil {
		return nil, fmt.Errorf("saving the report: %w", err)
	}

	return &ContactReport{ID: id, Version: version, Markdown: md, NoProvider: noProvider, Provider: provider, Model: model}, nil
}

// ListReports returns a contact's reports, newest version first.
func ListReports(ctx context.Context, a *app.App, workspaceID, contactID string) ([]ContactReport, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, version, markdown, no_model, provider, model, created_at FROM contact_context_reports
		WHERE workspace_id=$1 AND contact_id=$2 ORDER BY version DESC`, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ContactReport{}
	for rows.Next() {
		var r ContactReport
		var created time.Time
		if err := rows.Scan(&r.ID, &r.Version, &r.Markdown, &r.NoProvider, &r.Provider, &r.Model, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = &created
		out = append(out, r)
	}
	return out, rows.Err()
}

func gatherTimeline(sctx context.Context, a *app.App, workspaceID, contactID string) ([]timelineEntry, error) {
	var entries []timelineEntry

	msgRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT m.id, m.created_at, m.direction, m.body, m.conversation_id FROM messages m
		JOIN conversations c ON c.workspace_id=m.workspace_id AND c.id=m.conversation_id
		WHERE m.workspace_id=$1 AND c.contact_id=$2 ORDER BY m.created_at DESC LIMIT 60`,
		workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	for msgRows.Next() {
		var id, direction, body, convID string
		var when time.Time
		if err := msgRows.Scan(&id, &when, &direction, &body, &convID); err != nil {
			msgRows.Close()
			return nil, err
		}
		who := "them"
		if direction == "out" {
			who = "us"
		}
		entries = append(entries, timelineEntry{when: when, kind: "message", id: id, convID: convID, text: who + ": " + trimTo(body, 240)})
	}
	msgRows.Close()

	noteRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, created_at, coalesce(title,''), content FROM contact_context_notes
		WHERE workspace_id=$1 AND contact_id=$2 ORDER BY created_at DESC LIMIT 30`, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	for noteRows.Next() {
		var id, title, content string
		var when time.Time
		if err := noteRows.Scan(&id, &when, &title, &content); err != nil {
			noteRows.Close()
			return nil, err
		}
		text := content
		if title != "" {
			text = title + ": " + content
		}
		entries = append(entries, timelineEntry{when: when, kind: "note", id: id, text: trimTo(text, 240)})
	}
	noteRows.Close()

	dealRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, created_at, title, stage_id, closed_at FROM deals WHERE workspace_id=$1 AND contact_id=$2
		ORDER BY created_at DESC LIMIT 20`, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	for dealRows.Next() {
		var id, title, stage string
		var when time.Time
		var closedAt *time.Time
		if err := dealRows.Scan(&id, &when, &title, &stage, &closedAt); err != nil {
			dealRows.Close()
			return nil, err
		}
		status := "open, stage " + stage
		if closedAt != nil {
			status = "closed " + closedAt.Format("2006-01-02")
		}
		entries = append(entries, timelineEntry{when: when, kind: "deal", id: id, text: "Deal " + title + " (" + status + ")"})
	}
	dealRows.Close()

	sort.Slice(entries, func(i, j int) bool { return entries[i].when.After(entries[j].when) })
	if len(entries) > 60 {
		entries = entries[:60]
	}
	return entries, nil
}

func citedTimelineSection(entries []timelineEntry) string {
	var b strings.Builder
	b.WriteString("## Timeline (cited)\n\n")
	if len(entries) == 0 {
		b.WriteString("No messages, notes or deals recorded for this contact yet.\n")
		return b.String()
	}
	for i, e := range entries {
		fmt.Fprintf(&b, "%d. **%s** [%s:%s] %s\n", i+1, e.when.Format("2006-01-02 15:04"), e.kind, e.id, e.text)
	}
	return b.String()
}

// deterministicReport is the zero-AI path: every section built directly from
// entries, no generation call at all.
func deterministicReport(brief ContactBrief, entries []timelineEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Relationship report: %s\n\n", brief.Name)
	b.WriteString("_Generated without an AI provider configured — every line below is computed directly from the data on record, not written by a model._\n\n")

	b.WriteString("## Relationship summary\n\n")
	msgCount, noteCount, dealCount := 0, 0, 0
	var last *timelineEntry
	for i := range entries {
		switch entries[i].kind {
		case "message":
			msgCount++
		case "note":
			noteCount++
		case "deal":
			dealCount++
		}
		if last == nil || entries[i].when.After(last.when) {
			e := entries[i]
			last = &e
		}
	}
	fmt.Fprintf(&b, "%d messages, %d notes and %d deals are on record for %s.\n\n", msgCount, noteCount, dealCount, brief.Name)

	b.WriteString("## Last touch\n\n")
	if last != nil {
		fmt.Fprintf(&b, "%s — %s [%s:%s]\n\n", last.when.Format("2006-01-02 15:04"), last.text, last.kind, last.id)
	} else {
		b.WriteString("No contact on record yet.\n\n")
	}

	b.WriteString("## Open promises / asks\n\n")
	b.WriteString("No AI provider is configured, so promises/asks are not extracted from free text automatically — " +
		"review the timeline below directly, or add them as notes with add_contact_context.\n\n")

	b.WriteString("## Sentiment\n\n")
	b.WriteString("Not assessed — sentiment scoring needs a configured AI provider.\n\n")

	b.WriteString("## Recommended next step\n\n")
	if last == nil {
		b.WriteString("No history yet — reach out to open the relationship.\n\n")
	} else if last.kind == "message" && strings.HasPrefix(last.text, "them:") {
		b.WriteString("Their last message has not been followed up — reply.\n\n")
	} else {
		b.WriteString("Follow up: the most recent activity was from our side; check whether a response is due.\n\n")
	}
	return b.String()
}

func buildReportPrompt(brief ContactBrief, entries []timelineEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Contact: %s", brief.Name)
	if brief.Email != "" {
		fmt.Fprintf(&b, " <%s>", brief.Email)
	}
	b.WriteString("\n\nFacts (numbered, most recent first):\n")
	for i, e := range entries {
		fmt.Fprintf(&b, "[%d] %s (%s) %s\n", i+1, e.when.Format("2006-01-02 15:04"), e.kind, e.text)
	}
	if len(entries) == 0 {
		b.WriteString("(none — say plainly there is no history to report on)\n")
	}
	return b.String()
}
