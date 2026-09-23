package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
POST /api/hooks/{id} end to end against a real database (MH-381): the
signature per kind, the replay window, legacy sources, and the migration that
decides which sources are legacy.

	TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55470/moharrik_test?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app_test:test@localhost:55470/moharrik_test?sslmode=disable \
	go test -count=1 ./internal/notifications/ -run Hook -v
*/

type hookFixture struct {
	a    *app.App
	api  humatest.TestAPI
	ws   string
	user string
	ctx  context.Context // an owner's scope in ws, for the /api/w routes
}

func newHookFixture(t *testing.T) *hookFixture {
	t.Helper()
	owner, appURL := testdb.Setup(t, "notifications", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "hook-test-secret-hook-test-secret-hook-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")

	ctx := context.Background()
	k := togo.New()
	a := app.New(ctx, k)
	t.Cleanup(func() { k.Close() })

	f := &hookFixture{a: a}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	f.user = "u-" + suffix
	if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now()::text)`,
		f.user, f.user+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := a.SysDB.QueryRowContext(ctx, `INSERT INTO workspaces (name, slug, created_by) VALUES ('Hook Test', $1, $2) RETURNING id`,
		"hook-"+suffix, f.user).Scan(&f.ws); err != nil {
		t.Fatal(err)
	}
	f.ctx = tenant.With(ctx, &tenant.Scope{WorkspaceID: f.ws, UserID: f.user, Role: "owner", Via: "session"})
	_, f.api = humatest.New(t)
	Register(f.api, a)
	return f
}

type createdSource struct {
	Source WebhookSourceOut `json:"source"`
	Secret string           `json:"secret"`
}

func (f *hookFixture) create(t *testing.T, kind string) createdSource {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/webhook_sources", map[string]any{"name": kind + " source", "kind": kind})
	if res.Code != http.StatusCreated {
		t.Fatalf("create %s: %d %s", kind, res.Code, res.Body)
	}
	var c createdSource
	if err := json.Unmarshal(res.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.Source.LegacySigning {
		t.Fatalf("a new %s source must not be legacy_signing", kind)
	}
	return c
}

func (f *hookFixture) deliver(t *testing.T, id, body string, headers ...string) (int, HookResult) {
	t.Helper()
	args := []any{strings.NewReader(body)}
	for _, h := range headers {
		args = append(args, h)
	}
	res := f.api.Post("/api/hooks/"+id, args...)
	var out HookResult
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return res.Code, out
}

func (f *hookFixture) items(t *testing.T, sourceID string) []NotificationItemOut {
	t.Helper()
	res := f.api.GetCtx(f.ctx, "/api/w/notifications?source_id="+sourceID)
	if res.Code != http.StatusOK {
		t.Fatalf("list: %d %s", res.Code, res.Body)
	}
	var out struct {
		Items []NotificationItemOut `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Items
}

func (f *hookFixture) source(t *testing.T, id string) WebhookSourceOut {
	t.Helper()
	res := f.api.GetCtx(f.ctx, "/api/w/webhook_sources")
	var out struct {
		Sources []WebhookSourceOut `json:"sources"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	for _, s := range out.Sources {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("source %s not listed", id)
	return WebhookSourceOut{}
}

func genericHeaderArgs(secret, body string, at time.Time) []string {
	ts := strconv.FormatInt(at.Unix(), 10)
	return []string{"X-Webhook-Timestamp: " + ts, "X-Webhook-Signature: sha256=" + mac(secret, ts, ".", body)}
}

func TestHookGeneric(t *testing.T) {
	f := newHookFixture(t)
	src := f.create(t, "generic")
	body := `{"title":"Disk almost full","severity":"warning","url":"https://status.example/disk"}`

	h := genericHeaderArgs(src.Secret, body, time.Now())
	if code, r := f.deliver(t, src.Source.ID, body, h...); code != http.StatusAccepted || r.Duplicate {
		t.Fatalf("signed delivery: %d %+v", code, r)
	}
	// The same request again, inside the window: accepted, same row.
	if code, r := f.deliver(t, src.Source.ID, body, h...); code != http.StatusAccepted || !r.Duplicate {
		t.Fatalf("replay inside the window: %d %+v, want 202 duplicate", code, r)
	}
	items := f.items(t, src.Source.ID)
	if len(items) != 1 || items[0].Title != "Disk almost full" || items[0].Severity != "warning" || items[0].URL != "https://status.example/disk" {
		t.Fatalf("items = %+v", items)
	}

	// Captured and replayed after the window: refused, nothing stored, and the
	// owner can see why.
	old := genericHeaderArgs(src.Secret, `{"title":"old"}`, time.Now().Add(-10*time.Minute))
	if code, _ := f.deliver(t, src.Source.ID, `{"title":"old"}`, old...); code != http.StatusUnauthorized {
		t.Fatalf("old timestamp: %d, want 401", code)
	}
	if s := f.source(t, src.Source.ID); !strings.Contains(s.LastError, "replay window") {
		t.Fatalf("last_error = %q", s.LastError)
	}
	// Body-only (the pre-MH-381 scheme) on a new source: refused.
	if code, _ := f.deliver(t, src.Source.ID, body, "X-Webhook-Signature: sha256="+mac(src.Secret, body)); code != http.StatusUnauthorized {
		t.Fatalf("body-only signature on a new source: %d, want 401", code)
	}
	// Wrong secret, tampered body.
	if code, _ := f.deliver(t, src.Source.ID, body, genericHeaderArgs("not-the-secret-not-the-secret", body, time.Now())...); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", code)
	}
	if code, _ := f.deliver(t, src.Source.ID, strings.Replace(body, "warning", "error", 1), genericHeaderArgs(src.Secret, body, time.Now())...); code != http.StatusUnauthorized {
		t.Fatalf("tampered body: %d", code)
	}
	if n := len(f.items(t, src.Source.ID)); n != 1 {
		t.Fatalf("%d items after refusals, want 1", n)
	}

	// Verified JSON with no title: an item named after the source, not a 422.
	odd := `{"status":"ok"}`
	if code, _ := f.deliver(t, src.Source.ID, odd, genericHeaderArgs(src.Secret, odd, time.Now())...); code != http.StatusAccepted {
		t.Fatalf("untitled body: %d, want 202", code)
	}
}

func TestHookLegacySource(t *testing.T) {
	f := newHookFixture(t)
	src := f.create(t, "generic")
	// What 31_webhook_signing.sql does to a source that existed before MH-381.
	if _, err := f.a.SysDB.Exec(`UPDATE webhook_sources SET legacy_signing = true WHERE id = $1`, src.Source.ID); err != nil {
		t.Fatal(err)
	}
	if !f.source(t, src.Source.ID).LegacySigning {
		t.Fatal("legacy_signing not reported")
	}

	body := `{"title":"from an old integration"}`
	if code, r := f.deliver(t, src.Source.ID, body, "X-Webhook-Signature: sha256="+mac(src.Secret, body)); code != http.StatusAccepted || r.Duplicate {
		t.Fatalf("legacy body-only signature: %d %+v, want 202", code, r)
	}
	if s := f.source(t, src.Source.ID); s.LastLegacyAt == nil {
		t.Fatal("a legacy-verified delivery must stamp last_legacy_at")
	}
	// Timestamped deliveries work on a legacy source too.
	b2 := `{"title":"from a migrated sender"}`
	if code, _ := f.deliver(t, src.Source.ID, b2, genericHeaderArgs(src.Secret, b2, time.Now())...); code != http.StatusAccepted {
		t.Fatalf("timestamped on a legacy source: %d", code)
	}

	// The owner switches it off; it cannot be switched back on.
	res := f.api.PatchCtx(f.ctx, "/api/w/webhook_sources/"+src.Source.ID, map[string]any{"legacy_signing": false})
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), `"legacy_signing":true`) {
		t.Fatalf("switch off: %d %s", res.Code, res.Body)
	}
	if res := f.api.PatchCtx(f.ctx, "/api/w/webhook_sources/"+src.Source.ID, map[string]any{"legacy_signing": true}); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("switch back on: %d, want 422", res.Code)
	}
	b3 := `{"title":"replayed old-style"}`
	if code, _ := f.deliver(t, src.Source.ID, b3, "X-Webhook-Signature: sha256="+mac(src.Secret, b3)); code != http.StatusUnauthorized {
		t.Fatalf("body-only after switching off: %d, want 401", code)
	}
}

func TestHookGitHub(t *testing.T) {
	f := newHookFixture(t)
	src := f.create(t, "github")
	body := string(fixture(t, "github_workflow_run_failed.json"))
	sig := "X-Hub-Signature-256: sha256=" + mac(src.Secret, body)

	if code, _ := f.deliver(t, src.Source.ID, body, sig, "X-GitHub-Event: workflow_run", "X-GitHub-Delivery: d-1"); code != http.StatusAccepted {
		t.Fatalf("signed GitHub delivery: %d", code)
	}
	// A captured delivery replayed with a fresh (unsigned) delivery id is
	// still the same delivery: identity comes from the signed body.
	if code, r := f.deliver(t, src.Source.ID, body, sig, "X-GitHub-Event: workflow_run", "X-GitHub-Delivery: d-2"); code != http.StatusAccepted || !r.Duplicate {
		t.Fatalf("replay with a new delivery id: %d %+v, want duplicate", code, r)
	}
	items := f.items(t, src.Source.ID)
	if len(items) != 1 || items[0].Title != "Workflow CI failed on main in fadymondy/moharrik" || items[0].Severity != "error" ||
		items[0].URL != "https://github.com/fadymondy/moharrik/actions/runs/30433642" {
		t.Fatalf("items = %+v", items)
	}
	if code, _ := f.deliver(t, src.Source.ID, body, "X-Hub-Signature-256: sha256="+mac("wrong-secret-wrong-secret", body), "X-GitHub-Event: workflow_run"); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", code)
	}
	if code, _ := f.deliver(t, src.Source.ID, strings.Replace(body, "failure", "success", 1), sig, "X-GitHub-Event: workflow_run"); code != http.StatusUnauthorized {
		t.Fatalf("tampered body: %d", code)
	}
}

func TestHookSentry(t *testing.T) {
	f := newHookFixture(t)
	src := f.create(t, "sentry")
	if src.Secret != "" || src.Source.HasSecret {
		t.Fatal("a sentry source must not get a generated secret: Sentry generates its own")
	}
	body := string(fixture(t, "sentry_event_alert.json"))
	clientSecret := "b6a1f3c2d4e5f60718293a4b5c6d7e8f9a0b1c2d3e4f50617283940a1b2c3d4e"

	// No secret yet: refused, whatever it is signed with.
	if code, _ := f.deliver(t, src.Source.ID, body, "Sentry-Hook-Signature: "+mac(clientSecret, body), "Sentry-Hook-Resource: event_alert"); code != http.StatusUnauthorized {
		t.Fatalf("before the secret is set: %d, want 401", code)
	}
	if res := f.api.PostCtx(f.ctx, "/api/w/webhook_sources/"+src.Source.ID+"/rotate", map[string]any{}); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rotating a sentry source: %d, want 422", res.Code)
	}
	res := f.api.PutCtx(f.ctx, "/api/w/webhook_sources/"+src.Source.ID+"/secret", map[string]any{"secret": clientSecret})
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), clientSecret) || !strings.Contains(res.Body.String(), `"has_secret":true`) {
		t.Fatalf("set secret: %d %s", res.Code, res.Body)
	}

	if code, _ := f.deliver(t, src.Source.ID, body, "Sentry-Hook-Signature: "+mac(clientSecret, body), "Sentry-Hook-Resource: event_alert", "Request-ID: r-1"); code != http.StatusAccepted {
		t.Fatalf("signed Sentry delivery: %d", code)
	}
	items := f.items(t, src.Source.ID)
	if len(items) != 1 || items[0].Title != "Alert: ApiError: Unable to post message to Slack channel" || items[0].Severity != "error" ||
		!strings.HasPrefix(items[0].URL, "https://acme.sentry.io/issues/1170820242/") || items[0].Kind != "issue_alert" {
		t.Fatalf("items = %+v", items)
	}
	if code, _ := f.deliver(t, src.Source.ID, body, "Sentry-Hook-Signature: "+mac("wrong-secret-wrong-secret", body), "Sentry-Hook-Resource: event_alert"); code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", code)
	}
	if code, _ := f.deliver(t, src.Source.ID, body+"\n", "Sentry-Hook-Signature: "+mac(clientSecret, body), "Sentry-Hook-Resource: event_alert"); code != http.StatusUnauthorized {
		t.Fatalf("tampered body: %d", code)
	}
}

// The migration itself: existing sources become legacy, new ones are not, and
// a re-run changes nothing. Run in a scratch schema holding a pre-MH-381
// webhook_sources, so the real table is untouched.
func TestHookMigrationMarksExistingSourcesLegacy(t *testing.T) {
	f := newHookFixture(t)
	sql, err := os.ReadFile(filepath.Join("..", "db", "post", "31_webhook_signing.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	conn, err := f.a.SysDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	schema := "mh381_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	defer conn.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	for _, q := range []string{
		`CREATE SCHEMA ` + schema,
		`SET search_path = ` + schema,
		`CREATE TABLE webhook_sources (id text PRIMARY KEY, kind text NOT NULL DEFAULT 'generic', CHECK (kind IN ('generic', 'github', 'sentry')))`,
		`INSERT INTO webhook_sources (id, kind) VALUES ('old-generic', 'generic'), ('old-github', 'github')`,
		string(sql),
		`INSERT INTO webhook_sources (id) VALUES ('new-after-migration')`,
		string(sql), // idempotent: must not re-mark anything
		`INSERT INTO webhook_sources (id) VALUES ('new-after-rerun')`,
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%.60s: %v", q, err)
		}
	}
	rows, err := conn.QueryContext(ctx, `SELECT id, legacy_signing FROM webhook_sources ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var id string
		var legacy bool
		if err := rows.Scan(&id, &legacy); err != nil {
			t.Fatal(err)
		}
		got[id] = legacy
	}
	want := map[string]bool{"old-generic": true, "old-github": true, "new-after-migration": false, "new-after-rerun": false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s legacy_signing = %v, want %v", id, got[id], w)
		}
	}
	var checks int
	_ = conn.QueryRowContext(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid = 'webhook_sources'::regclass AND contype = 'c'`).Scan(&checks)
	if checks != 1 {
		t.Errorf("%d CHECK constraints on kind after two runs, want the original 1 (no duplicate added)", checks)
	}
	_, _ = conn.ExecContext(ctx, `RESET search_path`)
}

// A verified delivery is published to the workspace the moment it is stored
// (MH-378): the notifications page and every tab's bell update without a
// reload, and the feed's readers are alerted. A redelivery says nothing, and a
// client-role member (no notifications:read) hears nothing either way.
func TestHookPublishesAnEvent(t *testing.T) {
	f := newHookFixture(t)
	src := f.create(t, "generic")

	reader, stopR := f.a.Events.Subscribe(f.ws, realtime.Viewer{UserID: f.user, Role: "owner"})
	defer stopR()
	client, stopC := f.a.Events.Subscribe(f.ws, realtime.Viewer{UserID: "u-client", Role: "client"})
	defer stopC()

	body := `{"title":"Build failed on main","severity":"error"}`
	h := genericHeaderArgs(src.Secret, body, time.Now())
	if code, r := f.deliver(t, src.Source.ID, body, h...); code != http.StatusAccepted || r.Duplicate {
		t.Fatalf("signed delivery: %d %+v", code, r)
	}
	items := f.items(t, src.Source.ID)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}

	select {
	case raw := <-reader:
		var ev realtime.Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Kind != "notification" || ev.ItemID != items[0].ID || ev.Title != "Build failed on main" || ev.Severity != "error" ||
			ev.Source != "generic source" || !ev.Alert {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the delivery was stored but nothing was published")
	}

	// The same delivery again: accepted as a duplicate, and silent.
	if code, r := f.deliver(t, src.Source.ID, body, h...); code != http.StatusAccepted || !r.Duplicate {
		t.Fatalf("redelivery: %d %+v", code, r)
	}
	select {
	case raw := <-reader:
		t.Errorf("a redelivery published %s", raw)
	case <-time.After(300 * time.Millisecond):
	}
	select {
	case raw := <-client:
		t.Errorf("a client-role member received %s", raw)
	default:
	}
}
