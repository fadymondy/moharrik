package server

/*
TestListCapsAreGone — MH-467, end to end through the real stack.

Every list that used to stop at a fixed number without saying so is seeded past
that number, then walked page by page: the total must be exact, every row must
come back exactly once, and the LAST row — the one the old cap hid — must be
reachable. The two send paths are seeded past their 50,000 limit and must
refuse up front, leaving the campaign a draft.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run ListCaps -v
*/

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestListCapsAreGone(t *testing.T) {
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "list-caps-test-secret-list-caps-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")

	a := Boot()
	defer a.Kernel.Close()
	srv := httptest.NewServer(a.Kernel.Handler())
	defer srv.Close()

	alice := newClient(t, srv.URL)
	alice.must("POST", "/api/auth/register", map[string]string{"email": "caps-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	ws := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Caps " + rnd()}, 201)["workspace"].(map[string]any)
	wsID := ws["id"].(string)
	alice.ws = wsID
	var userID string
	if err := a.SysDB.QueryRow(`SELECT user_id FROM workspace_members WHERE workspace_id = $1`, wsID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.SysDB.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}

	// walk follows next_cursor until it runs out, collecting the ids under key.
	type pageOf struct {
		Total      int64            `json:"total"`
		NextCursor string           `json:"next_cursor"`
		Rows       []map[string]any `json:"-"`
	}
	walk := func(path, key string) (ids []string, total int64, pages int) {
		t.Helper()
		cursor := ""
		for {
			p := path
			if cursor != "" {
				p += "&cursor=" + url.QueryEscape(cursor)
			}
			code, b := alice.do("GET", p, nil)
			if code != 200 {
				t.Fatalf("GET %s: %d %s", p, code, b)
			}
			var pg pageOf
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(b, &pg)
			_ = json.Unmarshal(b, &raw)
			var rows []map[string]any
			_ = json.Unmarshal(raw[key], &rows)
			for _, r := range rows {
				ids = append(ids, r["id"].(string))
			}
			total, pages = pg.Total, pages+1
			if pg.NextCursor == "" {
				return
			}
			if pages > 100 {
				t.Fatalf("%s: more than 100 pages, the cursor is not advancing", path)
			}
			cursor = pg.NextCursor
		}
	}
	assertAll := func(what string, ids []string, total int64, want int, last string) {
		t.Helper()
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Errorf("%s: %s came back twice", what, id)
			}
			seen[id] = true
		}
		if total != int64(want) || len(seen) != want {
			t.Errorf("%s: total %d, %d distinct rows; want %d of each", what, total, len(seen), want)
		}
		if !seen[last] {
			t.Errorf("%s: the last row (%s) is not reachable", what, last)
		}
	}

	// A list with one contact, for campaigns to point at.
	listID := alice.must("POST", "/api/w/mail/lists", map[string]any{"name": "Caps list", "double_opt_in": false}, 201)["list"].(map[string]any)["id"].(string)

	t.Run("campaigns: 501 past the old 500", func(t *testing.T) {
		// One shared created_at for 400 of them: the id tie-break is what keeps
		// them apart, the same trap an import sets for contacts.
		exec(`INSERT INTO campaigns (workspace_id, list_id, subject, body, created_by, created_at)
			SELECT $1, $2, 'Caps campaign ' || g, 'body', $3, CASE WHEN g <= 400 THEN '2026-01-01T00:00:00Z'::timestamptz ELSE now() - g * interval '1 second' END
			FROM generate_series(1, 501) g`, wsID, listID, userID)
		var oldest string
		if err := a.SysDB.QueryRow(`SELECT id FROM campaigns WHERE workspace_id = $1 ORDER BY created_at, id LIMIT 1`, wsID).Scan(&oldest); err != nil {
			t.Fatal(err)
		}
		ids, total, pages := walk("/api/w/campaigns?limit=200", "campaigns")
		assertAll("campaigns", ids, total, 501, oldest)
		if pages != 3 {
			t.Errorf("501 campaigns at 200 a page took %d pages, want 3", pages)
		}
		if code, b := alice.do("GET", "/api/w/campaigns?limit=201", nil); code != 422 {
			t.Errorf("limit=201 must be refused, not clamped: %d %s", code, b)
		}
		body := alice.must("GET", "/api/w/campaigns", nil, 200)
		if body["max_audience"] != float64(50000) || body["limit"] != float64(50) {
			t.Errorf("the default page and the stated send limit: %v / %v", body["limit"], body["max_audience"])
		}
	})

	t.Run("MCP list_campaigns: past the old 200", func(t *testing.T) {
		tok := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "caps", "scopes": []string{"campaigns:read"}}, 201)
		bot := newClient(t, srv.URL)
		bot.bearer = tok["plaintext"].(string)
		cursor, seen, pages := "", 0, 0
		for {
			args := map[string]any{"limit": 200}
			if cursor != "" {
				args["cursor"] = cursor
			}
			code, text, isErr := mcpCall(bot, "/api/mcp", "list_campaigns", args)
			if code != 200 || isErr {
				t.Fatalf("list_campaigns: %d %v %s", code, isErr, text)
			}
			pages++
			if !strings.HasPrefix(text, "Showing ") || !strings.Contains(text, " of 501 campaign(s)") {
				t.Fatalf("the reply must state the total: %.120s", text)
			}
			seen += strings.Count(text, "Caps campaign ")
			i := strings.Index(text, "cursor=")
			if i < 0 {
				break
			}
			cursor = strings.TrimSpace(text[i+len("cursor="):])
		}
		if seen != 501 || pages != 3 {
			t.Errorf("MCP paged %d campaigns over %d pages, want 501 over 3", seen, pages)
		}
		if _, text, isErr := mcpCall(bot, "/api/mcp", "list_campaigns", map[string]any{"limit": 201}); !isErr || !strings.Contains(text, "at most 200") {
			t.Errorf("limit 201 over MCP must be refused: %v %s", isErr, text)
		}
	})

	t.Run("companies: 2,001 past the old 2,000", func(t *testing.T) {
		// 1,000 share one name: only the id tells them apart.
		exec(`INSERT INTO companies (workspace_id, name) SELECT $1, CASE WHEN g <= 1000 THEN 'Same Name Co' ELSE 'Co ' || lpad(g::text, 5, '0') END
			FROM generate_series(1, 2001) g`, wsID)
		var last string
		if err := a.SysDB.QueryRow(`SELECT id FROM companies WHERE workspace_id = $1 ORDER BY lower(name) DESC, id DESC LIMIT 1`, wsID).Scan(&last); err != nil {
			t.Fatal(err)
		}
		ids, total, _ := walk("/api/w/companies?limit=200", "companies")
		assertAll("companies", ids, total, 2001, last)
		if code, _ := alice.do("GET", "/api/w/companies?limit=201", nil); code != 422 {
			t.Errorf("limit=201 must be refused, got %d", code)
		}
		// Search has its own exact total.
		if b := alice.must("GET", "/api/w/companies?q=same+name", nil, 200); b["total"] != float64(1000) {
			t.Errorf("search total %v, want 1000", b["total"])
		}
	})

	t.Run("deals: 2,001 past the old 2,000, on the board and in the list", func(t *testing.T) {
		stages := alice.must("GET", "/api/w/pipeline/stages", nil, 200)["stages"].([]any)
		first := stages[0].(map[string]any)["id"].(string)
		second := stages[1].(map[string]any)["id"].(string)
		// 1,990 in the first stage, most at the SAME position; 11 in the second.
		exec(`INSERT INTO deals (workspace_id, title, stage_id, position, created_by)
			SELECT $1, 'Caps deal ' || g, CASE WHEN g <= 1990 THEN $2 ELSE $3 END, CASE WHEN g <= 1500 THEN 1 ELSE g END, $4
			FROM generate_series(1, 2001) g`, wsID, first, second, userID)
		var last string
		if err := a.SysDB.QueryRow(`SELECT id FROM deals WHERE workspace_id = $1 AND stage_id = $2 ORDER BY position DESC, id DESC LIMIT 1`, wsID, first).Scan(&last); err != nil {
			t.Fatal(err)
		}

		ids, total, _ := walk("/api/w/deals?view=list&limit=500", "deals")
		assertAll("deals (list)", ids, total, 2001, last)

		// The board: the first page of every stage, then one stage's next pages.
		board := alice.must("GET", "/api/w/deals?limit=100", nil, 200)
		if board["total"] != float64(2001) {
			t.Errorf("board total %v, want 2001", board["total"])
		}
		var firstCursor string
		for _, s := range board["stages"].([]any) {
			st := s.(map[string]any)
			switch st["id"] {
			case first:
				firstCursor, _ = st["next_cursor"].(string)
				if st["deal_count"] != float64(1990) {
					t.Errorf("first stage deal_count %v, want 1990", st["deal_count"])
				}
			case second:
				if c, _ := st["next_cursor"].(string); c != "" {
					t.Error("a stage with 11 deals has no next page at 100 a stage")
				}
			}
		}
		if n := len(board["deals"].([]any)); n != 111 {
			t.Errorf("board sent %d deals, want 100 of the first stage and all 11 of the second", n)
		}
		if firstCursor == "" {
			t.Fatal("the first stage has 1,990 deals and no next_cursor")
		}
		stageIDs, stageTotal, _ := walk("/api/w/deals?stage_id="+first+"&limit=500", "deals")
		assertAll("deals (one stage)", stageIDs, stageTotal, 1990, last)
		if code, _ := alice.do("GET", "/api/w/deals?limit=501", nil); code != 422 {
			t.Errorf("limit=501 must be refused, got %d", code)
		}
		if code, _ := alice.do("GET", "/api/w/deals?stage_id=not-a-stage", nil); code != 404 {
			t.Errorf("an unknown stage must be 404, got %d", code)
		}
	})

	t.Run("timeline: 501 past the old 500", func(t *testing.T) {
		ctID := alice.must("POST", "/api/w/contacts", map[string]any{"email": "timeline-" + rnd() + "@example.test"}, 201)["contact"].(map[string]any)["id"].(string)
		// All 501 at one instant: the id tie-break again.
		exec(`INSERT INTO activities (workspace_id, kind, body, contact_id, created_by, created_at)
			SELECT $1, 'note', 'Caps note ' || g, $2, $3, '2026-02-02T00:00:00Z' FROM generate_series(1, 501) g`, wsID, ctID, userID)
		var last string
		if err := a.SysDB.QueryRow(`SELECT id FROM activities WHERE workspace_id = $1 AND contact_id = $2 ORDER BY created_at, id LIMIT 1`, wsID, ctID).Scan(&last); err != nil {
			t.Fatal(err)
		}
		page := alice.must("GET", "/api/w/contacts/"+ctID, nil, 200)
		if _, ok := page["activities"].([]any); !ok {
			t.Fatalf("the contact page has no activities: %v", keys(page))
		}
		if page["activities_total"] != float64(501) || page["activities_next_cursor"] == "" || len(page["activities"].([]any)) != 50 {
			t.Errorf("the contact page must carry the first 50, the total and a cursor: %v / %v / %d",
				page["activities_total"], page["activities_next_cursor"], len(page["activities"].([]any)))
		}
		ids, total, _ := walk("/api/w/activities?contact_id="+ctID+"&limit=200", "activities")
		assertAll("timeline", ids, total, 501, last)
		if code, _ := alice.do("GET", "/api/w/activities?contact_id="+ctID+"&limit=201", nil); code != 422 {
			t.Errorf("limit=201 must be refused, got %d", code)
		}
		if code, _ := alice.do("GET", "/api/w/activities", nil); code != 422 {
			t.Errorf("a timeline of nothing must be refused, got %d", code)
		}
	})

	t.Run("MCP list_companies and list_deals state their totals and page", func(t *testing.T) {
		// MH-393: both answer in JSON — total, the rows with their ids, and
		// next_cursor — so an agent can act on a row it read.
		tok := alice.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "caps-crm", "scopes": []string{"contacts:read"}}, 201)
		bot := newClient(t, srv.URL)
		bot.bearer = tok["plaintext"].(string)
		for _, c := range []struct {
			tool, rows  string
			limit, want int
		}{
			{"list_companies", "companies", 200, 2001},
			{"list_deals", "deals", 500, 2001},
		} {
			cursor := ""
			seen := map[string]bool{}
			for pages := 0; pages < 50; pages++ {
				args := map[string]any{"limit": c.limit}
				if cursor != "" {
					args["cursor"] = cursor
				}
				_, text, isErr := mcpCall(bot, "/api/mcp", c.tool, args)
				var page struct {
					Total      int64            `json:"total"`
					NextCursor string           `json:"next_cursor"`
					Companies  []map[string]any `json:"companies"`
					Deals      []map[string]any `json:"deals"`
				}
				if isErr || json.Unmarshal([]byte(text), &page) != nil || page.Total != int64(c.want) {
					t.Fatalf("%s: %v %.160s", c.tool, isErr, text)
				}
				rows := page.Companies
				if c.rows == "deals" {
					rows = page.Deals
				}
				for _, r := range rows {
					id, _ := r["id"].(string)
					if id == "" || seen[id] {
						t.Fatalf("%s: a row with no id, or twice: %v", c.tool, r)
					}
					seen[id] = true
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
			if len(seen) != c.want {
				t.Errorf("%s paged %d rows, want %d", c.tool, len(seen), c.want)
			}
		}
	})

	t.Run("sends over 50,000 are refused up front", func(t *testing.T) {
		big := alice.must("POST", "/api/w/mail/lists", map[string]any{"name": "Big list", "double_opt_in": false}, 201)["list"].(map[string]any)["id"].(string)
		exec(`INSERT INTO contacts (workspace_id, email, phone, whatsapp_opt_in_at)
			SELECT $1, 'big-' || g || '@example.test', '+2016' || lpad(g::text, 8, '0'), now() FROM generate_series(1, 50001) g`, wsID)
		exec(`INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token)
			SELECT $1, $2, c.id, 'subscribed', 'big-' || c.id FROM contacts c WHERE c.workspace_id = $1 AND c.email LIKE 'big-%'`, wsID, big)
		// Somewhere to send to, so the refusal is the audience and not the setup.
		exec(`INSERT INTO smtp_settings (workspace_id, host, port, from_email) VALUES ($1, '127.0.0.1', 1, 'caps@example.test')`, wsID)
		exec(`INSERT INTO whatsapp_links (workspace_id, instance, hook_id, state) VALUES ($1, 'caps-'||$1, 'caps-hook-'||$1, 'open')`, wsID)

		for _, channel := range []string{"email", "whatsapp"} {
			c := alice.must("POST", "/api/w/campaigns", map[string]any{"list_id": big, "subject": "Big " + channel, "body": "Hello", "channel": channel}, 201)["campaign"].(map[string]any)
			code, b := alice.do("POST", "/api/w/campaigns/"+c["id"].(string)+"/send", nil)
			if code != 409 || !strings.Contains(string(b), "50001") || !strings.Contains(string(b), "50000") {
				t.Errorf("%s send to 50,001: %d %s; want 409 naming both numbers", channel, code, b)
			}
			var status string
			var recipients int64
			if err := a.SysDB.QueryRow(`SELECT status, recipient_count FROM campaigns WHERE id = $1`, c["id"]).Scan(&status, &recipients); err != nil {
				t.Fatal(err)
			}
			if status != "draft" || recipients != 0 {
				t.Errorf("%s: a refused send left the campaign %s with %d recipients; want an untouched draft", channel, status, recipients)
			}
		}
	})
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
