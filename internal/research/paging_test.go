package research

import (
	"strconv"
	"testing"
	"time"
)

/*
MH-603: find_evidence and list_company_suggestions page by cursor
(FindEvidencePage / ListSuggestionsPage), not limit/offset. This is a
keyset walk over a fabricated set of rows: every row is seen exactly once,
across as many pages as it takes, in a stable order.
*/

func TestFindEvidencePagesByCursorWithNoOverlapOrGaps(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	wctx := memberCtx(ws, "u", "owner")
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	runID := "run-paging-" + suffix
	if _, err := a.SQLDB.ExecContext(wctx, `
		INSERT INTO research_runs (id, workspace_id, topic, status) VALUES ($1, $2, 'paging test', 'ok')`,
		runID, ws); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	const n = 7
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := "ev-paging-" + suffix + "-" + strconv.Itoa(i)
		ids[id] = true
		capturedAt := base.Add(time.Duration(i) * time.Minute)
		quote := "quote number " + strconv.Itoa(i) + " about the fintech market"
		if _, err := a.SQLDB.ExecContext(wctx, `
			INSERT INTO research_evidence (id, workspace_id, run_id, kind, quote, source_url, source_domain, captured_at, content_hash)
			VALUES ($1, $2, $3, 'quote', $4, $5, 'news.test', $6, $7)`,
			id, ws, runID, quote, "https://news.test/"+id, capturedAt, "hash-"+id); err != nil {
			t.Fatalf("insert evidence %s: %v", id, err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := FindEvidencePage(wctx, a.SQLDB, ws, EvidenceQuery{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatalf("FindEvidencePage: %v", err)
		}
		if page.Total != n {
			t.Fatalf("total = %d, want %d", page.Total, n)
		}
		pages++
		if pages > n {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
		for _, e := range page.Evidence {
			if seen[e.ID] {
				t.Fatalf("evidence %s seen twice: pages overlap", e.ID)
			}
			seen[e.ID] = true
		}
		if page.NextCursor == "" {
			if len(page.Evidence) == 0 && len(seen) < n {
				t.Fatalf("stopped early: saw %d of %d", len(seen), n)
			}
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != n {
		t.Fatalf("saw %d of %d evidence rows across %d pages: %v", len(seen), n, pages, seen)
	}
	for id := range ids {
		if !seen[id] {
			t.Errorf("evidence %s was never returned", id)
		}
	}
	if pages < 2 {
		t.Errorf("expected more than one page for %d rows at limit 3, got %d", n, pages)
	}
}

func TestListSuggestionsPagesByCursorWithNoOverlapOrGaps(t *testing.T) {
	a := testApp(t)
	ws := testWorkspace(t, a)
	wctx := memberCtx(ws, "u", "owner")
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)

	const n = 7
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		id := "sg-paging-" + suffix + "-" + strconv.Itoa(i)
		ids[id] = true
		lastSeen := base.Add(time.Duration(i) * time.Hour)
		if _, err := a.SQLDB.ExecContext(wctx, `
			INSERT INTO research_companies (id, workspace_id, company_key, name, status, evidence_count, strongest, last_seen_at)
			VALUES ($1, $2, $3, $4, 'suggested', 1, 0.5, $5)`,
			id, ws, "key-"+id, "Company "+strconv.Itoa(i), lastSeen); err != nil {
			t.Fatalf("insert suggestion %s: %v", id, err)
		}
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := ListSuggestionsPage(wctx, a.SQLDB, ws, "", cursor, 3)
		if err != nil {
			t.Fatalf("ListSuggestionsPage: %v", err)
		}
		if page.Total != n {
			t.Fatalf("total = %d, want %d", page.Total, n)
		}
		pages++
		if pages > n {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
		for _, s := range page.Suggestions {
			if seen[s.ID] {
				t.Fatalf("suggestion %s seen twice: pages overlap", s.ID)
			}
			seen[s.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != n {
		t.Fatalf("saw %d of %d suggestions across %d pages: %v", len(seen), n, pages, seen)
	}
	for id := range ids {
		if !seen[id] {
			t.Errorf("suggestion %s was never returned", id)
		}
	}
	if pages < 2 {
		t.Errorf("expected more than one page for %d rows at limit 3, got %d", n, pages)
	}
}
