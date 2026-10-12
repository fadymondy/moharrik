package research

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
A research run against a real database (MH-227).

Both behaviours under test are what Postgres does with what the run writes: a
text column refusing a NUL byte, and an UPDATE that must not overwrite a
cancelled row. A fake store could not show either.

	TEST_DATABASE_URL=postgres://postgres:…@localhost:55432/moharrik_test?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app:…@localhost:55432/moharrik_test?sslmode=disable \
	go test -count=1 ./internal/research/ -v
*/

func testApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "research", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "research-test-secret-research-test-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")

	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	return a
}

func testWorkspace(t *testing.T, a *app.App) string {
	t.Helper()
	var id string
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO workspaces (name, slug, created_by) VALUES ('Research Test', $1, 'research-test') RETURNING id`,
		"research-test-"+suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// These suites test the server doing the research; with no model key, auto
	// would hand the summary to an agent (agent_db_test.go covers that).
	if _, err := (&Engine{}).SetProvider(tenant.WithWorkspace(context.Background(), id), a.SQLDB, id, ProviderServer); err != nil {
		t.Fatal(err)
	}
	return id
}

// stubSearch answers like SearXNG with n distinct results.
func stubSearch(t *testing.T, n int) *Searcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type result struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		var out struct {
			Results []result `json:"results"`
		}
		for i := 0; i < n; i++ {
			out.Results = append(out.Results, result{
				URL:     "https://example.com/page-" + strconv.Itoa(i),
				Title:   "Page " + strconv.Itoa(i),
				Content: "snippet " + strconv.Itoa(i),
			})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return &Searcher{BaseURL: srv.URL, Client: srv.Client()}
}

// A page read as binary — a PDF is the common case — comes back with NUL bytes
// and invalid UTF-8. Postgres refused the INSERT, the source was only logged,
// and the run still counted it: "8 found · 5 read" over five stored rows.
func TestRunKeepsEverySourceEvenWhenAPageIsBinary(t *testing.T) {
	a := testApp(t)
	wsID := testWorkspace(t, a)

	reader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data":    map[string]string{"markdown": "%PDF-1.7\x00\x00 stream \xff\xfe text that matters"},
		})
	}))
	t.Cleanup(reader.Close)

	e := &Engine{
		Search: stubSearch(t, MaxFindings),
		Read:   &Reader{BaseURL: reader.URL, Client: reader.Client()},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	runID, err := e.Start(context.Background(), a, wsID, "research-test", "a question with binary pages")
	if err != nil {
		t.Fatal(err)
	}

	run := waitSettled(t, a, wsID, runID)
	_, sources, err := Get(context.Background(), a, wsID, runID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != MaxFindings {
		t.Fatalf("stored %d sources, want %d: a binary page was dropped", len(sources), MaxFindings)
	}
	read := 0
	for _, s := range sources {
		if s.Fetched {
			read++
			if s.Content == "" {
				t.Fatalf("source %d is marked read but kept no text", s.Position)
			}
		}
	}
	if read != MaxReads {
		t.Fatalf("stored %d read sources, want %d", read, MaxReads)
	}
	if run.FoundCount != len(sources) || run.ReadCount != read {
		t.Fatalf("the run counts %d found / %d read over %d stored / %d read",
			run.FoundCount, run.ReadCount, len(sources), read)
	}
}

// Stop is only noticed between pages, so a Stop during the model call used to
// be overwritten by finish: "Stopped" on screen, then "Finished" with an answer.
func TestFinishDoesNotOverwriteACancelledRun(t *testing.T) {
	a := testApp(t)
	wsID := testWorkspace(t, a)
	sctx := tenant.WithWorkspace(context.Background(), wsID)

	var runID string
	if err := a.SQLDB.QueryRowContext(sctx, `
		INSERT INTO research_runs (workspace_id, topic, query, status, requested_by, started_at)
		VALUES ($1, 'q', 'q', 'running', 'research-test', now()) RETURNING id`, wsID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := Cancel(context.Background(), a, wsID, runID); err != nil {
		t.Fatal(err)
	}

	e := &Engine{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	e.finish(sctx, a, wsID, runID, "ok", "", "an answer nobody waited for", "model", 8, 5)

	run, _, err := Get(context.Background(), a, wsID, runID, false)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "cancelled" || run.Summary != "" {
		t.Fatalf("a cancelled run became %q with summary %q", run.Status, run.Summary)
	}
}

func waitSettled(t *testing.T, a *app.App, wsID, runID string) *ResearchRun {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		run, _, err := Get(context.Background(), a, wsID, runID, false)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != "queued" && run.Status != "running" {
			return run
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the run did not settle within 30s")
	return nil
}
