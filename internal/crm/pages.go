package crm

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/fadymondy/moharrik/internal/paging"
)

/*
The CRM's lists, every row of them (MH-467).

Deals and companies each ended in LIMIT 2000 and a record's timeline in LIMIT
500. Row 2,001 was not on the board, not in the list and not in any company
picker, and nothing on the page said it existed. They now page the way MH-408
made the contacts list page:

  - keyset paging with an opaque cursor naming the last row returned, and the
    row id as the tie-break so rows sharing a sort key are neither skipped
    nor repeated;
  - an accurate total counted by the same WHERE clause as the rows;
  - a page size that is refused over its maximum (the parameters declare it,
    so Huma answers 422), never clamped.

The board is the one list that pages PER STAGE: paging it as one list would
fill the first column and leave the rest empty. Each stage carries its own
next_cursor, and "load more" in a column asks for that stage alone.
*/

// Page sizes.
const (
	DefaultCompanyPage = 50
	MaxCompanyPage     = 200

	// Board: this many cards per stage on first load; list: this many rows.
	DefaultDealsPerStage = 100
	DefaultDealsPage     = 50
	MaxDealsPage         = 500

	DefaultTimelinePage = 50
	MaxTimelinePage     = 200
)

// CompanyPage is one page of the companies list.
type CompanyPage struct {
	Companies []CompanyOut `json:"companies"`
	// Total is every company matching the search — all pages, not this one.
	Total      int64  `json:"total"`
	Limit      int    `json:"limit"`
	NextCursor string `json:"next_cursor" doc:"Fetches the page after this one. Empty on the last page."`
}

// ActivityPage is one page of a record's timeline.
type ActivityPage struct {
	Activities []ActivityOut `json:"activities"`
	Total      int64         `json:"total"`
	Limit      int           `json:"limit"`
	NextCursor string        `json:"next_cursor"`
	// The inbox conversations linked to the record whose latest message falls
	// in this page's stretch of time (MH-379): newer than the previous page's
	// last activity, and — unless this is the last page — no older than this
	// page's last activity. Paged with the activities, so the timeline never
	// shows a conversation below activities it has not loaded yet. Empty without
	// inbox:read.
	Conversations []ConversationRef `json:"conversations"`
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// ListCompaniesPage is one page of the workspace's companies, A–Z, optionally
// narrowed by a search over name and domain.
func ListCompaniesPage(ctx context.Context, db *sql.DB, wsID, q, after string, limit int) (*CompanyPage, error) {
	cur, err := paging.Decode(after, 2)
	if err != nil {
		return nil, err
	}
	where := ""
	args := []any{wsID}
	if q = strings.TrimSpace(q); q != "" {
		if r := []rune(q); len(r) > 200 {
			q = string(r[:200])
		}
		args = append(args, "%"+likeEscape(q)+"%")
		where = ` AND (co.name ILIKE $2 OR co.domain ILIKE $2)`
	}
	page := &CompanyPage{Companies: []CompanyOut{}, Limit: limit}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM companies co WHERE co.workspace_id = $1`+where, args...).Scan(&page.Total); err != nil {
		return nil, err
	}
	if cur != nil {
		args = append(args, cur[0], cur[1])
		n := len(args)
		where += ` AND (lower(co.name), co.id) > ($` + strconv.Itoa(n-1) + `, $` + strconv.Itoa(n) + `)`
	}
	args = append(args, limit+1)
	// The sort key rides along as the first column, so the cursor is Postgres'
	// own lower() — Go's can disagree with it on some letters.
	sel := strings.Replace(companySelect, "SELECT co.id", "SELECT lower(co.name), co.id", 1)
	rows, err := db.QueryContext(ctx, sel+where+` ORDER BY lower(co.name), co.id LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		c, err := scanCompany(prefixed{rows, &key})
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
		page.Companies = append(page.Companies, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Companies) > limit {
		page.Companies = page.Companies[:limit]
		page.NextCursor = paging.Encode(keys[limit-1], page.Companies[limit-1].ID)
	}
	return page, nil
}

// prefixed scans one extra leading column (a sort key) before the row itself.
type prefixed struct {
	r   scanner
	key *string
}

func (p prefixed) Scan(dest ...any) error { return p.r.Scan(append([]any{p.key}, dest...)...) }

// ---- deals ------------------------------------------------------------------

// DealsPage is the pipeline and one page of its deals.
type DealsPage struct {
	Deals    []DealOut  `json:"deals"`
	Stages   []StageOut `json:"stages" doc:"Every stage, with its full deal count and value. On the board each also carries next_cursor when it has more deals than were sent."`
	Currency string     `json:"currency" doc:"The workspace's default currency."`
	// Total is every deal the view covers: the whole pipeline, or one stage.
	Total      int64  `json:"total"`
	Limit      int    `json:"limit"`
	NextCursor string `json:"next_cursor" doc:"List view and one-stage pages: the page after this one. Empty on the last page."`
}

// dealKey is the board and list order: stage, then card position, then id.
func dealKey(stages map[string]StageOut, d DealOut) string {
	st := stages[d.StageID]
	return paging.Encode(strconv.FormatInt(st.Position, 10), st.ID, strconv.FormatFloat(d.Position, 'g', -1, 64), d.ID)
}

func decodeDealKey(after string) ([]any, error) {
	cur, err := paging.Decode(after, 4)
	if err != nil || cur == nil {
		return nil, err
	}
	sp, err1 := strconv.ParseInt(cur[0], 10, 64)
	dp, err2 := strconv.ParseFloat(cur[2], 64)
	if err1 != nil || err2 != nil {
		return nil, paging.ErrBadCursor
	}
	return []any{sp, cur[1], dp, cur[3]}, nil
}

// DealsQuery is what GET /api/w/deals was asked for.
type DealsQuery struct {
	View    string // "board" (default) or "list"
	StageID string // board: one stage's next page
	Cursor  string
	Limit   int
}

// ListDealsPage answers the three shapes of the deals list:
//
//	board            the first Limit deals of EVERY stage, each stage with its next_cursor
//	board + stage_id the next Limit deals of one stage, after Cursor
//	list             Limit deals of the whole pipeline in board order, after Cursor
func ListDealsPage(ctx context.Context, db *sql.DB, wsID string, q DealsQuery) (*DealsPage, error) {
	after, err := decodeDealKey(q.Cursor)
	if err != nil {
		return nil, err
	}
	page := &DealsPage{Deals: []DealOut{}, Limit: q.Limit}
	if page.Stages, err = listStages(ctx, db, wsID); err != nil {
		return nil, err
	}
	_ = db.QueryRowContext(ctx, `SELECT currency FROM workspaces WHERE id = $1`, wsID).Scan(&page.Currency)
	byID := make(map[string]StageOut, len(page.Stages))
	for _, st := range page.Stages {
		byID[st.ID] = st
		page.Total += st.DealCount
	}

	switch {
	case q.View == "list" || q.StageID != "":
		args := []any{wsID}
		where := ""
		if q.StageID != "" {
			st, ok := byID[q.StageID]
			if !ok {
				return nil, ErrNoSuchStage
			}
			page.Total = st.DealCount
			args = append(args, q.StageID)
			where += ` AND d.stage_id = $2`
		}
		if after != nil {
			n := len(args)
			args = append(args, after...)
			where += ` AND (st.position, st.id, d.position, d.id) > ($` + strconv.Itoa(n+1) + `::bigint, $` + strconv.Itoa(n+2) +
				`, $` + strconv.Itoa(n+3) + `::double precision, $` + strconv.Itoa(n+4) + `)`
		}
		args = append(args, q.Limit+1)
		list, err := queryDeals(ctx, db, dealSelect+where+` ORDER BY st.position, st.id, d.position, d.id LIMIT $`+strconv.Itoa(len(args)), args...)
		if err != nil {
			return nil, err
		}
		if len(list) > q.Limit {
			list = list[:q.Limit]
			page.NextCursor = dealKey(byID, list[q.Limit-1])
		}
		page.Deals = list
	default:
		if after != nil {
			return nil, paging.ErrBadCursor // a board cursor belongs to one stage
		}
		// One more than the page per stage, to know which stages have more.
		list, err := queryDeals(ctx, db, `SELECT `+dealCols+` FROM (
			SELECT d.*, row_number() OVER (PARTITION BY d.stage_id ORDER BY d.position, d.id) AS rn
			FROM deals d WHERE d.workspace_id = $1) d `+dealJoins+`
			WHERE d.rn <= $2 ORDER BY st.position, st.id, d.position, d.id`, wsID, q.Limit+1)
		if err != nil {
			return nil, err
		}
		perStage := map[string]int{}
		for _, d := range list {
			if perStage[d.StageID] == q.Limit {
				// The (Limit+1)th of its stage: the stage has more, and the
				// cursor is the last card actually sent.
				for i := range page.Stages {
					if page.Stages[i].ID == d.StageID {
						page.Stages[i].NextCursor = dealKey(byID, page.Deals[len(page.Deals)-1])
					}
				}
				perStage[d.StageID]++
				continue
			}
			if perStage[d.StageID] > q.Limit {
				continue
			}
			perStage[d.StageID]++
			page.Deals = append(page.Deals, d)
		}
	}
	return page, nil
}
