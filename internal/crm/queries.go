package crm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/icp"
	"github.com/fadymondy/moharrik/internal/paging"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type scanner interface{ Scan(dest ...any) error }

// ---- stages ---------------------------------------------------------------

// EnsureStages seeds the default pipeline the first time a workspace touches
// the CRM — from a REST request or (internal/mcp's create_deal) an MCP tool
// call, whichever gets there first. The advisory lock makes two racing first
// touches seed once.
func EnsureStages(ctx context.Context, db *sql.DB, wsID string) error {
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pipeline_stages WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "crm-stages:"+wsID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM pipeline_stages WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	var locale string
	_ = tx.QueryRowContext(ctx, `SELECT locale FROM workspaces WHERE id = $1`, wsID).Scan(&locale)
	for i, st := range defaultStages {
		name := st.en
		if locale == "ar" {
			name = st.ar
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, $2, $3, $4)`,
			wsID, name, i, st.kind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func listStages(ctx context.Context, db *sql.DB, wsID string) ([]StageOut, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT st.id, st.name, st.position, st.kind, count(d.id),
		       -- The column total the board shows: in the workspace's currency only, as the
		       -- board always summed it. The whole stage, not just the cards sent (MH-467).
		       coalesce(sum(d.value_minor) FILTER (WHERE d.currency = (SELECT currency FROM workspaces WHERE id = $1)), 0)
		FROM pipeline_stages st
		LEFT JOIN deals d ON d.workspace_id = st.workspace_id AND d.stage_id = st.id
		WHERE st.workspace_id = $1
		GROUP BY st.id ORDER BY st.position, st.created_at`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []StageOut{}
	for rows.Next() {
		var s StageOut
		if err := rows.Scan(&s.ID, &s.Name, &s.Position, &s.Kind, &s.DealCount, &s.ValueMinor); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, rows.Err()
}

// ---- deals ----------------------------------------------------------------

const (
	dealCols = `d.id, d.title, d.value_minor, d.currency, d.stage_id, st.name, st.kind, d.position,
	       d.contact_id, ct.email, ct.name, ct.title, d.company_id, co.name,
	       d.owner_user_id, d.expected_close, d.closed_at, d.created_at`
	dealJoins = `
	JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
	LEFT JOIN contacts ct ON ct.workspace_id = d.workspace_id AND ct.id = d.contact_id
	LEFT JOIN companies co ON co.workspace_id = d.workspace_id AND co.id = d.company_id`
	dealSelect = `SELECT ` + dealCols + ` FROM deals d ` + dealJoins + ` WHERE d.workspace_id = $1`
)

func scanDeal(r scanner) (DealOut, error) {
	var d DealOut
	var contactID, cEmail, cName, cTitle, companyID, coName, owner sql.NullString
	var expected, closed sql.NullTime
	err := r.Scan(&d.ID, &d.Title, &d.ValueMinor, &d.Currency, &d.StageID, &d.StageName, &d.StageKind, &d.Position,
		&contactID, &cEmail, &cName, &cTitle, &companyID, &coName, &owner, &expected, &closed, &d.CreatedAt)
	if err != nil {
		return d, err
	}
	if contactID.Valid {
		d.Contact = &ContactRef{ID: contactID.String, Email: cEmail.String, Name: cName.String, Title: cTitle.String}
	}
	if companyID.Valid {
		d.Company = &Ref{ID: companyID.String, Name: coName.String}
	}
	d.OwnerUserID, d.ExpectedClose, d.ClosedAt = strp(owner), datep(expected), timep(closed)
	return d, nil
}

// MaxRelatedDeals bounds the deals a contact or company page lists. It is not
// silent: the page carries deals_total and says "Showing X of N" past it.
const MaxRelatedDeals = 500

// relatedDeals lists one record's deals; `where` narrows with $2 (e.g. "AND d.contact_id = $2").
// It returns the total as well, so a caller past MaxRelatedDeals can say so.
func relatedDeals(ctx context.Context, db *sql.DB, wsID, where, id string) ([]DealOut, int64, error) {
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM deals d WHERE d.workspace_id = $1 `+where, wsID, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	list, err := queryDeals(ctx, db, dealSelect+" "+where+" ORDER BY st.position, st.id, d.position, d.id LIMIT $3", wsID, id, MaxRelatedDeals)
	return list, total, err
}

func queryDeals(ctx context.Context, db *sql.DB, q string, args ...any) ([]DealOut, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []DealOut{}
	for rows.Next() {
		d, err := scanDeal(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func getDeal(ctx context.Context, db *sql.DB, wsID, id string) (*DealOut, error) {
	d, err := scanDeal(db.QueryRowContext(ctx, dealSelect+" AND d.id = $2", wsID, id))
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ---- companies ------------------------------------------------------------

const companySelect = `
	SELECT co.id, co.name, co.domain, co.industry, co.size, co.country, co.notes, co.owner_user_id, co.created_at, sc.score, coalesce(sc.reasons, '[]')::text,
	       (SELECT count(*) FROM contacts c WHERE c.workspace_id = co.workspace_id AND c.company_id = co.id),
	       (SELECT count(*) FROM deals d JOIN pipeline_stages st ON st.workspace_id = d.workspace_id AND st.id = d.stage_id
	         WHERE d.workspace_id = co.workspace_id AND d.company_id = co.id AND st.kind = 'open')
	FROM companies co LEFT JOIN icp_scores sc ON sc.workspace_id = co.workspace_id AND sc.company_id = co.id
	WHERE co.workspace_id = $1`

func scanCompany(r scanner) (CompanyOut, error) {
	var c CompanyOut
	var owner sql.NullString
	var score sql.NullInt64
	var reasons string
	err := r.Scan(&c.ID, &c.Name, &c.Domain, &c.Industry, &c.Size, &c.Country, &c.Notes, &owner, &c.CreatedAt, &score, &reasons, &c.ContactCount, &c.OpenDeals)
	c.OwnerUserID = strp(owner)
	c.Score, c.ScoreReasons = scoreOf(score, reasons)
	return c, err
}

// scoreOf decodes a stored score and its reasons.
func scoreOf(score sql.NullInt64, reasons string) (*int, []icp.Reason) {
	list := []icp.Reason{}
	_ = json.Unmarshal([]byte(reasons), &list)
	if !score.Valid {
		return nil, list
	}
	v := int(score.Int64)
	return &v, list
}

func getCompany(ctx context.Context, db *sql.DB, wsID, id string) (*CompanyOut, error) {
	c, err := scanCompany(db.QueryRowContext(ctx, companySelect+" AND co.id = $2", wsID, id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ---- contacts -------------------------------------------------------------

func getContact(ctx context.Context, db *sql.DB, wsID, id string) (*ContactDetail, error) {
	var c ContactDetail
	var companyID, coName, owner sql.NullString
	var tags, reasons string
	var score sql.NullInt64
	var lifecycleAt sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT c.id, c.email, c.name, c.locale, c.source, c.phone, c.title, array_to_json(c.tags)::text,
		       c.company_id, co.name, c.owner_user_id, c.created_at, c.lifecycle, c.lifecycle_at, sc.score, coalesce(sc.reasons, '[]')::text
		FROM contacts c
		LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id
		LEFT JOIN icp_scores sc ON sc.workspace_id = c.workspace_id AND sc.contact_id = c.id
		WHERE c.workspace_id = $1 AND c.id = $2`, wsID, id,
	).Scan(&c.ID, &c.Email, &c.Name, &c.Locale, &c.Source, &c.Phone, &c.Title, &tags, &companyID, &coName, &owner, &c.CreatedAt,
		&c.Lifecycle, &lifecycleAt, &score, &reasons)
	if err != nil {
		return nil, err
	}
	c.LifecycleAt = timep(lifecycleAt)
	c.Score, c.ScoreReasons = scoreOf(score, reasons)
	if c.LifecycleHistory, c.LifecycleHistoryTotal, err = icp.LifecycleHistory(ctx, db, wsID, id); err != nil {
		return nil, err
	}
	c.Tags = []string{}
	_ = json.Unmarshal([]byte(tags), &c.Tags)
	if companyID.Valid {
		c.Company = &Ref{ID: companyID.String, Name: coName.String}
	}
	c.OwnerUserID = strp(owner)
	c.Subscriptions = []ListSubscription{}
	rows, err := db.QueryContext(ctx, `
		SELECT s.id, s.list_id, l.name, s.status, s.confirmed_at, s.unsubscribed_at, s.unsubscribed_by IS NOT NULL
		FROM list_subscriptions s JOIN mail_lists l ON l.id = s.list_id AND l.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.contact_id = $2 ORDER BY lower(l.name)`, wsID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s ListSubscription
		if err := rows.Scan(&s.ID, &s.ListID, &s.ListName, &s.Status, &s.ConfirmedAt, &s.UnsubscribedAt, &s.ByWorkspace); err != nil {
			return nil, err
		}
		c.Subscriptions = append(c.Subscriptions, s)
	}
	return &c, rows.Err()
}

func companyContacts(ctx context.Context, db *sql.DB, wsID, companyID string) ([]ContactRef, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, email, name, title FROM contacts
		WHERE workspace_id = $1 AND company_id = $2 ORDER BY lower(coalesce(nullif(name, ''), email))`, wsID, companyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ContactRef{}
	for rows.Next() {
		var c ContactRef
		if err := rows.Scan(&c.ID, &c.Email, &c.Name, &c.Title); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// ---- activities -----------------------------------------------------------

const activitySelect = `
	SELECT a.id, a.kind, a.body, a.due_at, a.done_at,
	       a.contact_id, ct.email, ct.name, ct.title, a.company_id, co.name, a.deal_id, d.title,
	       a.created_by, a.created_at
	FROM activities a
	LEFT JOIN contacts ct ON ct.workspace_id = a.workspace_id AND ct.id = a.contact_id
	LEFT JOIN companies co ON co.workspace_id = a.workspace_id AND co.id = a.company_id
	LEFT JOIN deals d ON d.workspace_id = a.workspace_id AND d.id = a.deal_id
	WHERE a.workspace_id = $1`

func scanActivity(r scanner) (ActivityOut, error) {
	var a ActivityOut
	var due, done sql.NullTime
	var contactID, cEmail, cName, cTitle, companyID, coName, dealID, dTitle sql.NullString
	err := r.Scan(&a.ID, &a.Kind, &a.Body, &due, &done, &contactID, &cEmail, &cName, &cTitle,
		&companyID, &coName, &dealID, &dTitle, &a.CreatedBy, &a.CreatedAt)
	if err != nil {
		return a, err
	}
	a.DueAt, a.DoneAt = timep(due), timep(done)
	if contactID.Valid {
		a.Contact = &ContactRef{ID: contactID.String, Email: cEmail.String, Name: cName.String, Title: cTitle.String}
	}
	if companyID.Valid {
		a.Company = &Ref{ID: companyID.String, Name: coName.String}
	}
	if dealID.Valid {
		a.Deal = &Ref{ID: dealID.String, Name: dTitle.String}
	}
	return a, nil
}

// Timeline scopes. Each narrows activitySelect with $2 = the record's id.
const (
	// A contact's own activities plus those on its deals.
	timelineContact = `AND (a.contact_id = $2 OR a.deal_id IN (SELECT id FROM deals WHERE workspace_id = $1 AND contact_id = $2))`
	// A company's, its people's and its deals'.
	timelineCompany = `AND (a.company_id = $2
		OR a.contact_id IN (SELECT id FROM contacts WHERE workspace_id = $1 AND company_id = $2)
		OR a.deal_id IN (SELECT id FROM deals WHERE workspace_id = $1 AND company_id = $2))`
	timelineDeal = `AND a.deal_id = $2`
)

// timelinePage is one page of a record's timeline, newest first (MH-467: it
// used to be the newest 500 and nothing else). Keyset on (created_at, id), so a
// note added while somebody reads does not shift the next page. Open tasks are
// not buried under later notes because the UI groups them separately.
//
// With inbox (the caller may read the inbox), the page also carries the linked
// conversations in its stretch of time (ActivityPage.Conversations, MH-379).
func timelinePage(ctx context.Context, db *sql.DB, wsID, where, id, after string, limit int, inbox bool) (*ActivityPage, error) {
	cur, err := paging.Decode(after, 2)
	if err != nil {
		return nil, err
	}
	page := &ActivityPage{Activities: []ActivityOut{}, Limit: limit, Conversations: []ConversationRef{}}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM activities a WHERE a.workspace_id = $1 `+where, wsID, id).Scan(&page.Total); err != nil {
		return nil, err
	}
	args := []any{wsID, id}
	q := activitySelect + " " + where
	var cursorAt *time.Time
	if cur != nil {
		us, err := strconv.ParseInt(cur[0], 10, 64)
		if err != nil {
			return nil, paging.ErrBadCursor
		}
		at := time.UnixMicro(us).UTC()
		cursorAt = &at
		args = append(args, at, cur[1])
		q += ` AND (a.created_at, a.id) < ($3, $4)`
	}
	args = append(args, limit+1)
	q += ` ORDER BY a.created_at DESC, a.id DESC LIMIT $` + strconv.Itoa(len(args))
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		page.Activities = append(page.Activities, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var lastAt *time.Time
	if len(page.Activities) > limit {
		page.Activities = page.Activities[:limit]
		last := page.Activities[limit-1]
		page.NextCursor = paging.Encode(strconv.FormatInt(last.CreatedAt.UnixMicro(), 10), last.ID)
		// Microseconds, as the cursor carries, so this page's lower bound is
		// exactly the next page's upper one.
		at := time.UnixMicro(last.CreatedAt.UnixMicro()).UTC()
		lastAt = &at
	}
	if convWhere, ok := conversationScopes[where]; ok && inbox {
		if page.Conversations, err = conversationsInWindow(ctx, db, wsID, convWhere, id, cursorAt, lastAt); err != nil {
			return nil, err
		}
	}
	return page, nil
}

func getActivity(ctx context.Context, db *sql.DB, wsID, id string) (*ActivityOut, error) {
	a, err := scanActivity(db.QueryRowContext(ctx, activitySelect+" AND a.id = $2", wsID, id))
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func notFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// ---- conversations (MH-379) --------------------------------------------------

// ConversationRef is an inbox conversation as a CRM timeline shows it: enough
// to say who wrote, on which channel, how much, and to link to the thread.
type ConversationRef struct {
	ID            string      `json:"id"`
	Channel       string      `json:"channel"`
	Subject       string      `json:"subject"`
	Status        string      `json:"status"`
	CustomerName  string      `json:"customer_name"`
	Preview       string      `json:"preview" doc:"The most recent message, trimmed."`
	MessageCount  int64       `json:"message_count"`
	Contact       *ContactRef `json:"contact"`
	DealID        *string     `json:"deal_id"`
	LastMessageAt time.Time   `json:"last_message_at"`
	CreatedAt     time.Time   `json:"created_at"`
}

// Conversation scopes, keyed by the timeline scope they go with. Each narrows
// the select with $2 = the record's id.
var conversationScopes = map[string]string{
	// A contact's are the threads linked to it.
	timelineContact: `AND c.contact_id = $2`,
	// A company's are its people's.
	timelineCompany: `AND ct.company_id = $2`,
	// A deal's is the one it was opened from.
	timelineDeal: `AND c.deal_id = $2`,
}

// maxWindowConversations bounds one timeline page's conversations. A page's
// stretch of time holding more than this many threads for one record is not a
// case this product meets; the bound is here so it can never be unbounded.
const maxWindowConversations = 200

// conversationsInWindow lists a record's linked conversations whose latest
// message falls in one timeline page's stretch of time, newest first: before
// `newerThan` (the previous page's last activity; nil on the first page) and
// at or after `notBefore` (this page's last activity; nil on the last page, so
// it takes everything older). Consecutive pages' windows meet without overlap,
// so every conversation shows up on exactly one page.
func conversationsInWindow(ctx context.Context, db *sql.DB, wsID, where, id string, newerThan, notBefore *time.Time) ([]ConversationRef, error) {
	args := []any{wsID, id}
	if newerThan != nil {
		args = append(args, *newerThan)
		where += ` AND c.last_message_at < $` + strconv.Itoa(len(args))
	}
	if notBefore != nil {
		args = append(args, *notBefore)
		where += ` AND c.last_message_at >= $` + strconv.Itoa(len(args))
	}
	args = append(args, maxWindowConversations)
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, c.channel, c.subject, c.status, c.customer_name,
		       coalesce((SELECT m.body FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id
		                 ORDER BY m.created_at DESC LIMIT 1), ''),
		       (SELECT count(*) FROM messages m WHERE m.workspace_id = c.workspace_id AND m.conversation_id = c.id),
		       c.contact_id, ct.email, ct.name, ct.title, c.deal_id, c.last_message_at, c.created_at
		FROM conversations c
		LEFT JOIN contacts ct ON ct.workspace_id = c.workspace_id AND ct.id = c.contact_id
		WHERE c.workspace_id = $1 `+where+`
		ORDER BY c.last_message_at DESC, c.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []ConversationRef{}
	for rows.Next() {
		var c ConversationRef
		var contactID, cEmail, cName, cTitle, dealID sql.NullString
		if err := rows.Scan(&c.ID, &c.Channel, &c.Subject, &c.Status, &c.CustomerName, &c.Preview, &c.MessageCount,
			&contactID, &cEmail, &cName, &cTitle, &dealID, &c.LastMessageAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		if r := []rune(strings.Join(strings.Fields(c.Preview), " ")); len(r) > 140 {
			c.Preview = string(r[:140]) + "…"
		} else {
			c.Preview = string(r)
		}
		if contactID.Valid {
			c.Contact = &ContactRef{ID: contactID.String, Email: cEmail.String, Name: cName.String, Title: cTitle.String}
		}
		c.DealID = strp(dealID)
		list = append(list, c)
	}
	return list, rows.Err()
}

// canReadInbox is whether a caller's timeline pages carry conversations: a
// role that may read the CRM but not the inbox sees no threads, rather than
// an error.
//
// An MCP token is narrower than its member: it carries conversations only if
// it was also granted inbox:read. A token minted to work the CRM does not read
// customers' messages just because its member could (MH-393).
func canReadInbox(s *tenant.Scope) bool {
	if !authz.Can(s.Role, authz.InboxRead) {
		return false
	}
	if s.Via == "mcp" {
		for _, sc := range s.Scopes {
			if sc == "inbox:read" {
				return true
			}
		}
		return false
	}
	return true
}
