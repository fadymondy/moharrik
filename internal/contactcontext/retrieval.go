package contactcontext

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// searchStopwords are dropped when turning a natural-language question into
// a tsquery: ask_about_contact's questions are full sentences ("What did
// Sara ask about?"), and plainto_tsquery's implicit AND between every word
// means a single filler word with no match in the text (here: "ask" itself,
// since the 'simple' config has no stemming and the note says "asked") zeroes
// out an otherwise-relevant result. This list only affects which words
// participate in the OR search below — it never touches what gets indexed.
var searchStopwords = map[string]bool{
	"what": true, "when": true, "where": true, "who": true, "why": true, "how": true,
	"did": true, "do": true, "does": true, "is": true, "are": true, "was": true, "were": true,
	"the": true, "a": true, "an": true, "to": true, "of": true, "in": true, "on": true, "at": true,
	"and": true, "or": true, "about": true, "that": true, "this": true, "please": true,
	"tell": true, "me": true, "us": true, "you": true, "for": true, "with": true, "has": true, "have": true,
}

// orTsQuery turns free text into an OR'd 'simple'-config tsquery ("sara | renewing | plan"),
// so a question needs only one meaningful word to match rather than all of them.
// Returns "" when nothing but stopwords/short tokens remain, in which case the
// caller falls back to plainto_tsquery's stricter AND behaviour.
func orTsQuery(q string) string {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var terms []string
	seen := map[string]bool{}
	for _, f := range fields {
		if len([]rune(f)) <= 2 || searchStopwords[f] || seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " | ")
}

// Passage is one retrieved piece of a contact's context, with enough on it to
// cite and to jump to in the UI.
type Passage struct {
	Kind      string    `json:"kind"` // "message" | "note" | "deal" | "activity" | "field"
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	When      time.Time `json:"when,omitempty"`
	Rank      float64   `json:"rank"`
	// ConversationID is set for kind="message", so the UI can jump to it.
	ConversationID string `json:"conversation_id,omitempty"`
}

const maxPassages = 12

// Retrieve runs a 'simple'-config full-text search (MH-586: no vector DB,
// this repo has no embedding infra to reuse — see parser.go's package doc)
// across everything that makes up a contact's context: notes/files
// (contact_context_notes.tsv), every channel's message history
// (messages.search_simple, joined through conversations.contact_id), deals
// and activities (ILIKE, since those tables carry no tsvector column and
// adding one is out of scope for two free-text fields), and the MH-583 meta
// fields already on contacts. Ranked passages, most relevant first, capped at
// maxPassages so a prompt built from this never grows unbounded.
func Retrieve(ctx context.Context, a *app.App, workspaceID, contactID, query string) ([]Passage, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	tsq := orTsQuery(q)
	tsqExpr := "plainto_tsquery('simple', $3)"
	tsqArg := q
	if tsq != "" {
		tsqExpr = "to_tsquery('simple', $3)"
		tsqArg = tsq
	}
	out := []Passage{}

	noteRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, coalesce(title,'') || CASE WHEN title <> '' THEN ': ' ELSE '' END || content, created_at,
		       ts_rank(tsv, `+tsqExpr+`)
		FROM contact_context_notes
		WHERE workspace_id=$1 AND contact_id=$2 AND tsv @@ `+tsqExpr+`
		ORDER BY ts_rank(tsv, `+tsqExpr+`) DESC LIMIT 8`,
		workspaceID, contactID, tsqArg)
	if err != nil {
		return nil, err
	}
	for noteRows.Next() {
		var p Passage
		if err := noteRows.Scan(&p.ID, &p.Text, &p.When, &p.Rank); err != nil {
			noteRows.Close()
			return nil, err
		}
		p.Kind = "note"
		out = append(out, p)
	}
	noteRows.Close()

	msgRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT m.id, m.body, m.created_at, m.conversation_id,
		       ts_rank(m.search_simple, `+tsqExpr+`)
		FROM messages m
		JOIN conversations c ON c.workspace_id = m.workspace_id AND c.id = m.conversation_id
		WHERE m.workspace_id=$1 AND c.contact_id=$2 AND m.search_simple @@ `+tsqExpr+`
		ORDER BY ts_rank(m.search_simple, `+tsqExpr+`) DESC LIMIT 8`,
		workspaceID, contactID, tsqArg)
	if err != nil {
		return nil, err
	}
	for msgRows.Next() {
		var p Passage
		if err := msgRows.Scan(&p.ID, &p.Text, &p.When, &p.ConversationID, &p.Rank); err != nil {
			msgRows.Close()
			return nil, err
		}
		p.Kind = "message"
		out = append(out, p)
	}
	msgRows.Close()

	// Deals/activities have no tsvector column (two free-text fields; not
	// worth a generated column for), so they get a plain ILIKE — but ANY of
	// the same meaningful terms the tsquery above used, not the raw sentence,
	// for the same reason: a literal question rarely appears verbatim.
	likeTerms := "{" + strings.ReplaceAll(tsqArg, " | ", ",") + "}"
	if tsq == "" {
		likeTerms = "{" + q + "}"
	}

	dealRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, title, created_at FROM deals
		WHERE workspace_id=$1 AND contact_id=$2
		  AND title ILIKE ANY (SELECT '%' || t || '%' FROM unnest($3::text[]) t) LIMIT 5`,
		workspaceID, contactID, likeTerms)
	if err != nil {
		return nil, err
	}
	for dealRows.Next() {
		var p Passage
		if err := dealRows.Scan(&p.ID, &p.Text, &p.When); err != nil {
			dealRows.Close()
			return nil, err
		}
		p.Kind = "deal"
		p.Rank = 0.5
		out = append(out, p)
	}
	dealRows.Close()

	actRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, body, created_at FROM activities
		WHERE workspace_id=$1 AND contact_id=$2 AND body ILIKE '%' || $3 || '%' LIMIT 5`,
		workspaceID, contactID, q)
	if err != nil {
		return nil, err
	}
	for actRows.Next() {
		var p Passage
		if err := actRows.Scan(&p.ID, &p.Text, &p.When); err != nil {
			actRows.Close()
			return nil, err
		}
		p.Kind = "activity"
		p.Rank = 0.5
		out = append(out, p)
	}
	actRows.Close()

	// Sort by rank desc (stable enough: ties keep source-group order) and cap.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Rank > out[j-1].Rank; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if len(out) > maxPassages {
		out = out[:maxPassages]
	}
	return out, nil
}

// ContextSummary gathers the non-search-dependent parts of a contact's
// context for get_contact_context: recent notes/imports/messages regardless
// of any query, plus the MH-583 meta fields, so a caller can see "what do we
// know" without having to phrase it as a search.
type ContextSummary struct {
	Contact       ContactBrief `json:"contact"`
	RecentNotes   []Passage    `json:"recent_notes"`
	RecentMessages []Passage   `json:"recent_messages"`
	Imports       []ImportRow  `json:"imports"`
	OpenDeals     []Passage    `json:"open_deals"`
}

// ContactBrief is the minimal contact identity a report/answer cites.
type ContactBrief struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
}

func loadContactBrief(ctx context.Context, a *app.App, workspaceID, contactID string) (ContactBrief, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var b ContactBrief
	b.ID = contactID
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT name, coalesce(email,''), coalesce(phone,'') FROM contacts WHERE workspace_id=$1 AND id=$2`,
		workspaceID, contactID).Scan(&b.Name, &b.Email, &b.Phone); err != nil {
		return b, err
	}
	return b, nil
}

// GetContextSummary backs get_contact_context and the "Ask about this
// contact" panel's initial load.
func GetContextSummary(ctx context.Context, a *app.App, workspaceID, contactID string) (*ContextSummary, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	brief, err := loadContactBrief(ctx, a, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	s := &ContextSummary{Contact: brief}

	noteRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, coalesce(title,'') || CASE WHEN title <> '' THEN ': ' ELSE '' END || content, created_at
		FROM contact_context_notes WHERE workspace_id=$1 AND contact_id=$2 ORDER BY created_at DESC LIMIT 10`,
		workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	for noteRows.Next() {
		var p Passage
		if err := noteRows.Scan(&p.ID, &p.Text, &p.When); err != nil {
			noteRows.Close()
			return nil, err
		}
		p.Kind = "note"
		s.RecentNotes = append(s.RecentNotes, p)
	}
	noteRows.Close()

	msgRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT m.id, m.body, m.created_at, m.conversation_id FROM messages m
		JOIN conversations c ON c.workspace_id=m.workspace_id AND c.id=m.conversation_id
		WHERE m.workspace_id=$1 AND c.contact_id=$2 ORDER BY m.created_at DESC LIMIT 20`,
		workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	for msgRows.Next() {
		var p Passage
		if err := msgRows.Scan(&p.ID, &p.Text, &p.When, &p.ConversationID); err != nil {
			msgRows.Close()
			return nil, err
		}
		p.Kind = "message"
		s.RecentMessages = append(s.RecentMessages, p)
	}
	msgRows.Close()

	imports, err := ListImports(ctx, a, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	s.Imports = imports

	dealRows, err := a.SQLDB.QueryContext(sctx, `
		SELECT id, title, created_at FROM deals WHERE workspace_id=$1 AND contact_id=$2 AND closed_at IS NULL
		ORDER BY created_at DESC LIMIT 10`, workspaceID, contactID)
	if err != nil {
		return nil, err
	}
	for dealRows.Next() {
		var p Passage
		if err := dealRows.Scan(&p.ID, &p.Text, &p.When); err != nil {
			dealRows.Close()
			return nil, err
		}
		p.Kind = "deal"
		s.OpenDeals = append(s.OpenDeals, p)
	}
	dealRows.Close()

	return s, nil
}

// AddNote backs add_contact_context: append a note, or a small text/markdown
// file's content, as a new contact_context_notes row. importID is empty for
// hand-added notes; RemoveImport also cleans up any note tagged with an
// import's id.
func AddNote(ctx context.Context, a *app.App, workspaceID, contactID, kind, title, content, filename, createdBy string) (string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	// The FK on (workspace_id, contact_id) would refuse this anyway, but its
	// Postgres error text can carry the rejected contact_id — check first so
	// a foreign contact_id fails with the same generic message loadContactBrief
	// already returns everywhere else, not a driver error that might echo it.
	if _, err := loadContactBrief(ctx, a, workspaceID, contactID); err != nil {
		return "", err
	}
	if kind != "note" && kind != "file" {
		kind = "note"
	}
	var id string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO contact_context_notes (workspace_id, contact_id, kind, title, content, filename, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		workspaceID, contactID, kind, trimTo(title, 200), trimTo(content, maxBody), trimTo(filename, 200), createdBy,
	).Scan(&id); err != nil {
		return "", err
	}
	return id, nil
}
