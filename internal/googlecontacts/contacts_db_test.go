package googlecontacts_test

/*
Google People import/sync end to end (MH-585): the real stack (guard,
session, RLS pool) plus google's own OAuth flow against the fake in
internal/google/googletest, then Sync/Preview called directly (they are what
both the MCP tools and the REST routes call).

	TEST_DATABASE_URL=postgres://postgres:…@localhost:55432/moharrik_tak?sslmode=disable \
	TEST_APP_DATABASE_URL=postgres://moharrik_app:…@localhost:55432/moharrik_tak?sslmode=disable \
	go test -count=1 ./internal/googlecontacts/... -v
*/

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/googlecontacts"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/server"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

type fixture struct {
	t    *testing.T
	a    *app.App
	srv  *httptest.Server
	fake *googletest.Server
}

type client struct {
	f     *fixture
	http  *http.Client
	jar   *cookiejar.Jar
	csrf  string
	ws    string
	email string
	id    string
}

func rnd() string { return strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "") }

func newFixture(t *testing.T) *fixture {
	t.Helper()
	owner, appURL := testdb.Setup(t, "googlecontacts", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "googlecontacts-test-secret-0123456789ab")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")
	t.Setenv("GOOGLE_SYNC_INLINE", "off")
	t.Setenv("APP_ENV", "test")
	t.Setenv("APP_URL", "http://127.0.0.1")

	fake := googletest.New()
	t.Cleanup(fake.Close)
	t.Setenv("GOOGLE_API_BASE", fake.URL)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", fake.ClientID)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", fake.ClientSecret)
	t.Setenv("OAUTH_GOOGLE_CLIENT_ID", "")
	t.Setenv("OAUTH_GOOGLE_CLIENT_SECRET", "")

	a := server.Boot()
	t.Cleanup(func() { a.Kernel.Close() })
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	a.Kernel.Log, a.Log = lg, lg

	srv := httptest.NewServer(a.Kernel.Handler())
	t.Cleanup(srv.Close)
	t.Setenv("APP_URL", srv.URL)
	t.Setenv("PUBLIC_URL", "")
	return &fixture{t: t, a: a, srv: srv, fake: fake}
}

func (f *fixture) setAccount(sub, email string) {
	f.fake.Set(func(s *googletest.Server) {
		s.Account = googletest.Account{Sub: sub, Email: email}
	})
}

func (f *fixture) newUser(t *testing.T) *client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &client{f: f, jar: jar, http: &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	c.email = "gc-" + rnd() + "@example.test"
	out := c.must(t, "POST", "/api/auth/register", map[string]string{"email": c.email, "password": "correct-horse-9"}, 201)
	if u, ok := out["user"].(map[string]any); ok {
		c.id, _ = u["id"].(string)
	}
	return c
}

func (c *client) do(t *testing.T, method, path string, body any) (int, []byte, *http.Response) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	target := path
	if !strings.HasPrefix(path, "http") {
		target = c.f.srv.URL + path
	}
	req, _ := http.NewRequest(method, target, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.ws != "" {
		req.Header.Set("X-Workspace-ID", c.ws)
	}
	if method != http.MethodGet {
		if c.csrf == "" {
			r, err := c.http.Get(c.f.srv.URL + "/api/auth/csrf")
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Token string `json:"csrf_token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&out)
			r.Body.Close()
			c.csrf = out.Token
		}
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b, res
}

func (c *client) must(t *testing.T, method, path string, body any, want int) map[string]any {
	t.Helper()
	code, b, _ := c.do(t, method, path, body)
	if code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, code, want, b)
	}
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}

func (c *client) workspace(t *testing.T) string {
	t.Helper()
	ws := c.must(t, "POST", "/api/me/workspaces", map[string]string{"name": "GC " + rnd()}, 201)["workspace"].(map[string]any)
	c.ws = ws["id"].(string)
	return c.ws
}

// connect runs a full Contacts OAuth handshake against the fake and returns
// the resulting google_connections id.
func (c *client) connect(t *testing.T, query string) string {
	t.Helper()
	out := c.must(t, "GET", "/api/w/google/connect?"+query, nil, 200)
	authURL := out["url"].(string)
	loc, err := c.f.fake.Consent(authURL)
	if err != nil {
		t.Fatal(err)
	}
	code, b, res := c.do(t, "GET", loc, nil)
	if code != http.StatusFound {
		t.Fatalf("callback: got %d want 302: %s", code, b)
	}
	u, _ := url.Parse(res.Header.Get("Location"))
	if u.Query().Get("google") != google.ResultConnected {
		t.Fatalf("connect result: %s", u.Query().Get("google"))
	}
	l := c.must(t, "GET", "/api/w/google/connections", nil, 200)
	conns, _ := l["connections"].([]any)
	if len(conns) == 0 {
		t.Fatal("no connections after connect")
	}
	last := conns[len(conns)-1].(map[string]any)
	return last["id"].(string)
}

func withWorkspace(ctx context.Context, wsID string) context.Context {
	return tenant.WithWorkspace(ctx, wsID)
}

// ---- field mapping -------------------------------------------------------

func TestPersonToCardFieldMappingThroughSync(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsID := alice.workspace(t)
	f.setAccount("sub-sam", "sam-owner@example.test")
	connID := alice.connect(t, "product=contacts&monetary=true")

	f.fake.SetContactGroups("sub-sam", []googletest.ContactFields{
		{"resourceName": "contactGroups/friends1", "name": "Friends", "groupType": "USER_CONTACT_GROUP"},
	})
	f.fake.SetContact("sub-sam", "people/c1", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Sam", "familyName": "Malek", "displayName": "Sam Malek",
			"metadata": map[string]any{"primary": true}}},
		"nicknames":      []map[string]any{{"value": "Sammy"}},
		"biographies":    []map[string]any{{"value": "VIP client"}},
		"birthdays":      []map[string]any{{"date": map[string]any{"year": 1990, "month": 5, "day": 2}}},
		"phoneNumbers":   []map[string]any{{"value": "+201001112222", "formattedType": "Mobile"}},
		"emailAddresses": []map[string]any{{"value": "sam@example.test", "type": "work"}},
		"addresses":      []map[string]any{{"streetAddress": "1 Tahrir St", "city": "Cairo", "country": "Egypt"}},
		"organizations":  []map[string]any{{"name": "Acme", "title": "Engineer", "department": "R&D"}},
		"urls":           []map[string]any{{"value": "https://sam.example.test"}},
		"userDefined":    []map[string]any{{"key": "Employee ID", "value": "EMP-1"}},
		"memberships": []map[string]any{
			{"contactGroupMembership": map[string]any{"contactGroupResourceName": "contactGroups/friends1"}},
		},
	})

	ctx := withWorkspace(context.Background(), wsID)
	res, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Updated != 0 || res.FullResync != true {
		t.Fatalf("sync result: %+v", res)
	}

	var name, nickname, bio, bday, org, title, role, phone, email, meta string
	row := f.a.SQLDB.QueryRowContext(ctx, `
		SELECT c.name, c.nickname, c.biography, c.birthday::text, coalesce(co.name, ''), c.title, c.job_role,
		       c.phone, c.email, c.meta::text
		FROM contacts c
		LEFT JOIN companies co ON co.workspace_id = c.workspace_id AND co.id = c.company_id
		JOIN contact_identities ci ON ci.contact_id = c.id
		WHERE c.workspace_id = $1 AND ci.channel = 'other' AND ci.external_id = 'google:people/c1'`, wsID)
	if err := row.Scan(&name, &nickname, &bio, &bday, &org, &title, &role, &phone, &email, &meta); err != nil {
		t.Fatalf("committed contact not found: %v", err)
	}
	if name != "Sam Malek" || nickname != "Sammy" || bio != "VIP client" || bday != "1990-05-02" {
		t.Errorf("scalars: name=%q nickname=%q bio=%q bday=%q", name, nickname, bio, bday)
	}
	if org != "Acme" || title != "Engineer" || role != "R&D" {
		t.Errorf("org: org=%q title=%q role=%q", org, title, role)
	}
	if phone != "+201001112222" || email != "sam@example.test" {
		t.Errorf("identities: phone=%q email=%q", phone, email)
	}
	if !strings.Contains(meta, "Employee ID") || !strings.Contains(meta, "EMP-1") {
		t.Errorf("userDefined -> meta: %s", meta)
	}

	// Membership -> tag: contacts.tags is a plain text[] column (internal/db/post/16_crm.sql),
	// no join table involved. Categories reach it through the same
	// applyExtraFields concat CommitCards already uses for vCard import.
	var tags pgtext.Array
	err = f.a.SQLDB.QueryRowContext(ctx, `
		SELECT c.tags FROM contacts c
		JOIN contact_identities ci ON ci.contact_id = c.id
		WHERE c.workspace_id = $1 AND ci.channel = 'other' AND ci.external_id = 'google:people/c1'`, wsID).Scan(&tags)
	if err != nil || len(tags) != 1 || tags[0] != "Friends" {
		t.Errorf("membership -> tag: %v (err=%v)", []string(tags), err)
	}
}

// ---- matching --------------------------------------------------------------

func TestMatchesExistingContactByPhoneBeforeCreatingNew(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsID := alice.workspace(t)
	ctx := withWorkspace(context.Background(), wsID)

	// A contact that already exists in the workspace, not linked to Google yet.
	var existingID string
	if err := f.a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO contacts (workspace_id, name, phone) VALUES ($1, 'Nora Adel', '+201009998888') RETURNING id`,
		wsID).Scan(&existingID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SQLDB.ExecContext(ctx, `
		INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id, is_primary)
		VALUES ($1, $2, 'phone', '+201009998888', true)`,
		wsID, existingID); err != nil {
		t.Fatal(err)
	}

	f.setAccount("sub-nora", "nora-owner@example.test")
	connID := alice.connect(t, "product=contacts")
	f.fake.SetContact("sub-nora", "people/nora1", googletest.ContactFields{
		"names":        []map[string]any{{"givenName": "Nora", "familyName": "Adel"}},
		"phoneNumbers": []map[string]any{{"value": "+201009998888"}},
	})

	res, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 0 || res.Updated != 1 {
		t.Fatalf("matching by phone must merge, not create: %+v", res)
	}
	var count int
	if err := f.a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one contact after the merge, got %d", count)
	}
}

// ---- incremental sync -------------------------------------------------------

func TestIncrementalSyncOnlyAddsChangedPeople(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsID := alice.workspace(t)
	ctx := withWorkspace(context.Background(), wsID)
	f.setAccount("sub-inc", "inc-owner@example.test")
	connID := alice.connect(t, "product=contacts")

	f.fake.SetContact("sub-inc", "people/a", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Amir"}},
	})
	res1, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id)
	if err != nil {
		t.Fatal(err)
	}
	if res1.Added != 1 || !res1.FullResync {
		t.Fatalf("first sync must be a full resync adding one: %+v", res1)
	}

	// A second person appears; the first is untouched.
	f.fake.SetContact("sub-inc", "people/b", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Bella"}},
	})
	res2, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Added != 1 || res2.FullResync {
		t.Fatalf("incremental sync must add exactly the one changed person: %+v", res2)
	}
	var count int
	if err := f.a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 contacts total, got %d", count)
	}
}

func TestExpiredSyncTokenTriggersFullResync(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsID := alice.workspace(t)
	ctx := withWorkspace(context.Background(), wsID)
	f.setAccount("sub-exp", "exp-owner@example.test")
	connID := alice.connect(t, "product=contacts")

	f.fake.SetContact("sub-exp", "people/x", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Xena"}},
	})
	if _, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id); err != nil {
		t.Fatal(err)
	}
	f.fake.ExpireContactsSyncToken("sub-exp")
	f.fake.SetContact("sub-exp", "people/y", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Yara"}},
	})
	res, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FullResync {
		t.Fatalf("an expired sync token must force a full resync: %+v", res)
	}
	// A full resync re-lists everyone still present (x and y), so Added
	// reflects both — the point under test is FullResync flipping to true
	// and the sync completing without error rather than 410ing out.
	if res.Added < 1 {
		t.Fatalf("full resync must still import contacts: %+v", res)
	}
}

// ---- deleted-at-Google -------------------------------------------------------

func TestDeletedAtGoogleIsFlaggedNotHardDeleted(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsID := alice.workspace(t)
	ctx := withWorkspace(context.Background(), wsID)
	f.setAccount("sub-del", "del-owner@example.test")
	connID := alice.connect(t, "product=contacts")

	f.fake.SetContact("sub-del", "people/gone", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Ghost"}},
	})
	if _, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id); err != nil {
		t.Fatal(err)
	}
	f.fake.DeleteContact("sub-del", "people/gone")
	res, err := googlecontacts.Sync(ctx, f.a, wsID, connID, alice.id)
	if err != nil {
		t.Fatal(err)
	}
	if res.FlaggedDeleted != 1 {
		t.Fatalf("expected 1 flagged deletion: %+v", res)
	}
	var count int
	if err := f.a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("a deleted-at-Google contact must never be hard-deleted, got %d contacts", count)
	}
	var deletedAt sql.NullTime
	if err := f.a.SQLDB.QueryRowContext(ctx, `
		SELECT google_deleted_at FROM contacts c
		JOIN contact_identities ci ON ci.contact_id = c.id
		WHERE c.workspace_id = $1 AND ci.channel = 'other' AND ci.external_id = 'google:people/gone'`, wsID).
		Scan(&deletedAt); err != nil {
		t.Fatal(err)
	}
	if !deletedAt.Valid {
		t.Fatal("google_deleted_at must be set, not null")
	}
}

// ---- workspace isolation -----------------------------------------------------

func TestWorkspaceIsolation(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsA := alice.workspace(t)
	f.setAccount("sub-iso-a", "iso-a@example.test")
	connA := alice.connect(t, "product=contacts")
	f.fake.SetContact("sub-iso-a", "people/only-a", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "OnlyA"}},
	})
	ctxA := withWorkspace(context.Background(), wsA)
	if _, err := googlecontacts.Sync(ctxA, f.a, wsA, connA, alice.id); err != nil {
		t.Fatal(err)
	}

	wsB := alice.workspace(t)
	ctxB := withWorkspace(context.Background(), wsB)
	var countB int
	if err := f.a.SQLDB.QueryRowContext(ctxB, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsB).Scan(&countB); err != nil {
		t.Fatal(err)
	}
	if countB != 0 {
		t.Fatalf("workspace B must see none of workspace A's synced contacts, got %d", countB)
	}
}

// ---- Preview never writes ----------------------------------------------------

func TestPreviewClassifiesWithoutWriting(t *testing.T) {
	f := newFixture(t)
	alice := f.newUser(t)
	wsID := alice.workspace(t)
	ctx := withWorkspace(context.Background(), wsID)
	f.setAccount("sub-prev", "prev-owner@example.test")
	connID := alice.connect(t, "product=contacts")
	f.fake.SetContact("sub-prev", "people/p1", googletest.ContactFields{
		"names": []map[string]any{{"givenName": "Preview", "familyName": "Only"}},
	})

	pv, err := googlecontacts.Preview(ctx, f.a, wsID, connID)
	if err != nil {
		t.Fatal(err)
	}
	if pv.NewCount != 1 {
		t.Fatalf("expected exactly one new card classified: %+v", pv)
	}
	var count int
	if err := f.a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, wsID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("Preview must never write; found %d contacts", count)
	}
}
