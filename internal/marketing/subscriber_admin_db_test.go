package marketing_test

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Subscriber admin (MH-408), through the real routes on a real database.

  - every contact is reachable: 1,500 of them page through with an accurate
    total, and no row is repeated or skipped — including a batch that shares
    one created_at, which is what an import produces;
  - search and the per-list status filter, and counts that agree with them;
  - taking one address off one list leaves its other lists, the contact and
    its deals exactly where they were, and says who did it;
  - adding it back asks for consent again, as after a self-unsubscribe;
  - the export holds exactly the filtered view's rows, Arabic intact.
*/

type adminFixture struct {
	a    *app.App
	api  humatest.TestAPI
	ctx  context.Context
	ws   string
	list string
	user string
}

func newAdminFixture(t *testing.T, doubleOptIn bool) *adminFixture {
	t.Helper()
	a := testApp(t)
	wsID, listID := listIn(t, a, doubleOptIn)
	f := &adminFixture{a: a, ws: wsID, list: listID, user: "u-admin-" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	f.ctx = f.as("owner")
	_, f.api = humatest.New(t)
	marketing.Register(f.api, a)
	return f
}

func (f *adminFixture) as(role string) context.Context {
	return tenant.With(context.Background(), &tenant.Scope{
		WorkspaceID: f.ws, UserID: f.user, Email: role + "@team.test", Role: role, Via: "session",
	})
}

// newList makes another list in the fixture's workspace.
func (f *adminFixture) newList(t *testing.T, name string, doubleOptIn bool) string {
	t.Helper()
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `INSERT INTO mail_lists (workspace_id, name, double_opt_in) VALUES ($1, $2, $3) RETURNING id`,
		f.ws, name, doubleOptIn).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// contact writes one contact straight to the table and puts it on a list.
func (f *adminFixture) contact(t *testing.T, email, name, phone, company, listID, status string) string {
	t.Helper()
	var coID *string
	if company != "" {
		var id string
		if err := f.a.SQLDB.QueryRowContext(f.ctx, `INSERT INTO companies (workspace_id, name) VALUES ($1, $2) RETURNING id`, f.ws, company).Scan(&id); err != nil {
			t.Fatal(err)
		}
		coID = &id
	}
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO contacts (workspace_id, email, name, phone, company_id, source) VALUES ($1, $2, $3, $4, $5, 'import') RETURNING id`,
		f.ws, email, name, phone, coID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if listID != "" {
		f.subscribe(t, id, listID, status)
	}
	return id
}

func (f *adminFixture) subscribe(t *testing.T, contactID, listID, status string) {
	t.Helper()
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token, confirmed_at)
		VALUES ($1, $2, $3, $4, md5(random()::text) || md5(random()::text), CASE WHEN $4 = 'subscribed' THEN now() END)`,
		f.ws, listID, contactID, status); err != nil {
		t.Fatal(err)
	}
}

func (f *adminFixture) page(t *testing.T, ctx context.Context, q url.Values) marketing.ContactPage {
	t.Helper()
	res := f.api.GetCtx(ctx, "/api/w/contacts?"+q.Encode())
	if res.Code != http.StatusOK {
		t.Fatalf("GET /api/w/contacts?%s: %d %s", q.Encode(), res.Code, res.Body)
	}
	var p marketing.ContactPage
	if err := json.Unmarshal(res.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// walk follows next_cursor to the end and returns every contact id in order.
func (f *adminFixture) walk(t *testing.T, q url.Values, limit int) (ids []string, total int64, pages int) {
	t.Helper()
	q = cloneValues(q)
	q.Set("limit", strconv.Itoa(limit))
	for {
		p := f.page(t, f.ctx, q)
		pages++
		if pages == 1 {
			total = p.Total
		} else if p.Total != total {
			t.Fatalf("page %d reports total %d, page 1 said %d", pages, p.Total, total)
		}
		if len(p.Contacts) > limit {
			t.Fatalf("page %d has %d rows, over the limit of %d", pages, len(p.Contacts), limit)
		}
		for _, c := range p.Contacts {
			ids = append(ids, c.ID)
		}
		if p.NextCursor == "" {
			return ids, total, pages
		}
		if len(p.Contacts) != limit {
			t.Fatalf("page %d is short (%d of %d) but claims a next page", pages, len(p.Contacts), limit)
		}
		q.Set("cursor", p.NextCursor)
		if pages > 1000 {
			t.Fatal("the walk does not end")
		}
	}
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func TestContactsPage1500WithNoGapsOrDuplicates(t *testing.T) {
	f := newAdminFixture(t, false)

	// 1,000 in ONE statement — one transaction, so one created_at for all of
	// them, which is exactly what an import does and exactly what paging on
	// the timestamp alone gets wrong — then 500 more, one by one in time.
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO contacts (workspace_id, email, name, source)
		SELECT $1, 'bulk-' || g || '@example.test', 'Bulk ' || g, 'import' FROM generate_series(1, 1000) g`, f.ws); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO contacts (workspace_id, email, name, source, created_at)
		SELECT $1, 'later-' || g || '@example.test', 'Later ' || g, 'import', now() + (g || ' milliseconds')::interval
		FROM generate_series(1, 500) g`, f.ws); err != nil {
		t.Fatal(err)
	}
	// Every other one on the list, so the list filter pages too.
	if _, err := f.a.SQLDB.ExecContext(f.ctx, `
		INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token)
		SELECT $1, $2, c.id, 'subscribed', md5(c.id || random()::text) || md5(random()::text)
		FROM (SELECT id, row_number() OVER (ORDER BY email) AS n FROM contacts WHERE workspace_id = $1) c
		WHERE c.n % 2 = 0`, f.ws, f.list); err != nil {
		t.Fatal(err)
	}

	var want int64
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1`, f.ws).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if want != 1500 {
		t.Fatalf("seeded %d contacts, want 1500", want)
	}

	for _, limit := range []int{50, 200, 7} {
		ids, total, pages := f.walk(t, url.Values{}, limit)
		if total != 1500 {
			t.Fatalf("limit %d: total = %d, want 1500", limit, total)
		}
		if len(ids) != 1500 {
			t.Fatalf("limit %d: walked %d contacts over %d pages, want all 1500", limit, len(ids), pages)
		}
		seen := map[string]bool{}
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("limit %d: contact %s appeared twice", limit, id)
			}
			seen[id] = true
		}
	}

	// The order the pages come in IS the order of the table: newest first, id
	// breaking ties. Checked against the database, not against itself.
	ids, _, _ := f.walk(t, url.Values{}, 50)
	rows, err := f.a.SQLDB.QueryContext(f.ctx, `SELECT id FROM contacts WHERE workspace_id = $1 ORDER BY created_at DESC, id DESC`, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		if ids[i] != id {
			t.Fatalf("row %d: paged %s, table order has %s", i, ids[i], id)
		}
		i++
	}
	rows.Close()

	// The list filter pages through its half the same way.
	listIDs, listTotal, _ := f.walk(t, url.Values{"list_id": {f.list}}, 200)
	if listTotal != 750 || len(listIDs) != 750 {
		t.Fatalf("list view: total %d, walked %d, want 750/750", listTotal, len(listIDs))
	}

	// A contact added mid-walk lands on page one (newest first) and shifts
	// nothing: the cursor resumes after a row, not at an offset.
	q := url.Values{"limit": {"100"}}
	first := f.page(t, f.ctx, q)
	f.contact(t, "midwalk@example.test", "Mid Walk", "", "", "", "")
	q.Set("cursor", first.NextCursor)
	second := f.page(t, f.ctx, q)
	for _, c := range second.Contacts {
		for _, d := range first.Contacts {
			if c.ID == d.ID {
				t.Fatalf("an insert during paging repeated %s on the next page", c.ID)
			}
		}
	}
	if second.Total != 1501 {
		t.Fatalf("total after an insert = %d, want 1501", second.Total)
	}

	// The export has no cap either: every row on the list, not a first chunk.
	res := f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/export.csv")
	if res.Code != http.StatusOK {
		t.Fatalf("export: %d %s", res.Code, res.Body)
	}
	recs := readCSV(t, res.Body.Bytes())
	if len(recs)-1 != 750 {
		t.Fatalf("export has %d rows, the list has 750", len(recs)-1)
	}

	// A cursor this server did not issue is refused, not treated as "start".
	if res := f.api.GetCtx(f.ctx, "/api/w/contacts?cursor=not-a-cursor"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a forged cursor: %d, want 422", res.Code)
	}
	if res := f.api.GetCtx(f.ctx, "/api/w/contacts?limit=500"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a page over the maximum: %d, want 422 (never a silent clamp)", res.Code)
	}
}

func TestContactsSearchAndStatusFilter(t *testing.T) {
	f := newAdminFixture(t, false)
	other := f.newList(t, "Other", false)

	f.contact(t, "nour@acme.test", "Nour Hassan", "+20 100 111 2222", "Acme Trading", f.list, "subscribed")
	f.contact(t, "sara@example.test", "سارة أحمد", "", "شركة النيل", f.list, "pending")
	f.contact(t, "omar@example.test", "Omar", "+971 50 000 0000", "", f.list, "unsubscribed")
	f.contact(t, "bounce@example.test", "Bounce", "", "", f.list, "bounced")
	f.contact(t, "fifty@example.test", "50% Off Club", "", "", f.list, "subscribed")
	f.contact(t, "elsewhere@example.test", "Nour Elsewhere", "", "", other, "subscribed")
	f.contact(t, "nolist@example.test", "No List", "", "", "", "")

	emails := func(p marketing.ContactPage) []string {
		out := []string{}
		for _, c := range p.Contacts {
			out = append(out, c.Email)
		}
		return out
	}
	cases := []struct {
		name string
		q    url.Values
		want []string
	}{
		{"by name", url.Values{"q": {"nour"}}, []string{"elsewhere@example.test", "nour@acme.test"}},
		{"by email", url.Values{"q": {"ACME.TEST"}}, []string{"nour@acme.test"}},
		{"by phone", url.Values{"q": {"971 50"}}, []string{"omar@example.test"}},
		{"by company", url.Values{"q": {"trading"}}, []string{"nour@acme.test"}},
		{"by Arabic name", url.Values{"q": {"سارة"}}, []string{"sara@example.test"}},
		{"by Arabic company", url.Values{"q": {"النيل"}}, []string{"sara@example.test"}},
		{"a % is literal", url.Values{"q": {"50%"}}, []string{"fifty@example.test"}},
		{"an _ is literal", url.Values{"q": {"n_ur"}}, []string{}},
		{"list", url.Values{"list_id": {f.list}}, []string{"fifty@example.test", "bounce@example.test", "omar@example.test", "sara@example.test", "nour@acme.test"}},
		{"list + status", url.Values{"list_id": {f.list}, "status": {"subscribed"}}, []string{"fifty@example.test", "nour@acme.test"}},
		{"list + status + search", url.Values{"list_id": {f.list}, "status": {"subscribed"}, "q": {"nour"}}, []string{"nour@acme.test"}},
		{"other list + search", url.Values{"list_id": {other}, "q": {"nour"}}, []string{"elsewhere@example.test"}},
	}
	for _, c := range cases {
		p := f.page(t, f.ctx, c.q)
		got := emails(p)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		if p.Total != int64(len(c.want)) {
			t.Errorf("%s: total %d, want %d", c.name, p.Total, len(c.want))
		}
	}

	// Counts: one list, by status, and each one is what choosing it shows.
	p := f.page(t, f.ctx, url.Values{"list_id": {f.list}})
	if p.Counts == nil {
		t.Fatal("a list view has no counts")
	}
	want := marketing.StatusCounts{All: 5, Subscribed: 2, Pending: 1, Unsubscribed: 1, Bounced: 1}
	if *p.Counts != want {
		t.Fatalf("counts = %+v, want %+v", *p.Counts, want)
	}
	for status, n := range map[string]int64{"subscribed": 2, "pending": 1, "unsubscribed": 1, "bounced": 1} {
		if got := f.page(t, f.ctx, url.Values{"list_id": {f.list}, "status": {status}}).Total; got != n {
			t.Errorf("status %s: total %d, counts said %d", status, got, n)
		}
	}
	// Under a search, the counts follow the search.
	if c := f.page(t, f.ctx, url.Values{"list_id": {f.list}, "q": {"nour"}}).Counts; c == nil || c.All != 1 || c.Subscribed != 1 {
		t.Fatalf("counts under a search = %+v, want 1 subscribed", c)
	}
	if f.page(t, f.ctx, url.Values{}).Counts != nil {
		t.Fatal("the all-contacts view has no single list to count")
	}

	// A status means nothing without a list, and an unknown one is refused.
	if res := f.api.GetCtx(f.ctx, "/api/w/contacts?status=subscribed"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status without a list: %d, want 422", res.Code)
	}
	if res := f.api.GetCtx(f.ctx, "/api/w/contacts?list_id="+f.list+"&status=deleted"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown status: %d, want 422", res.Code)
	}
}

func TestAdminUnsubscribeLeavesOneListOnly(t *testing.T) {
	f := newAdminFixture(t, false)
	other := f.newList(t, "Product updates", false)
	id := f.contact(t, "keep@example.test", "Keep Me", "", "Acme", f.list, "subscribed")
	f.subscribe(t, id, other, "subscribed")

	// A deal on the contact: the thing deleting the contact used to destroy.
	if err := crm.EnsureStages(f.ctx, f.a.SQLDB, f.ws); err != nil {
		t.Fatal(err)
	}
	var dealID string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO deals (workspace_id, title, stage_id, contact_id, created_by)
		SELECT $1, 'Renewal', id, $2, 'test' FROM pipeline_stages WHERE workspace_id = $1 ORDER BY position LIMIT 1
		RETURNING id`, f.ws, id).Scan(&dealID); err != nil {
		t.Fatal(err)
	}

	res := f.api.PostCtx(f.ctx, "/api/w/contacts/"+id+"/lists/"+f.list+"/unsubscribe")
	if res.Code != http.StatusOK {
		t.Fatalf("unsubscribe: %d %s", res.Code, res.Body)
	}
	var out struct {
		Changed bool                 `json:"changed"`
		Contact marketing.ContactOut `json:"contact"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if !out.Changed {
		t.Fatal("the first unsubscribe reported no change")
	}
	byList := map[string]marketing.SubscriptionOut{}
	for _, s := range out.Contact.Subscriptions {
		byList[s.ListID] = s
	}
	if s := byList[f.list]; s.Status != marketing.StatusUnsubscribed || s.UnsubscribedAt == nil || !s.ByWorkspace {
		t.Fatalf("the list it was taken off: %+v", s)
	}
	if s := byList[other]; s.Status != marketing.StatusSubscribed || s.UnsubscribedAt != nil {
		t.Fatalf("the OTHER list was touched: %+v", s)
	}

	// The contact, the deal, and who did it.
	var contacts, deals int
	var by string
	_ = f.a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM contacts WHERE workspace_id = $1 AND id = $2`, f.ws, id).Scan(&contacts)
	_ = f.a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM deals WHERE workspace_id = $1 AND id = $2 AND contact_id = $3`, f.ws, dealID, id).Scan(&deals)
	_ = f.a.SQLDB.QueryRowContext(f.ctx, `SELECT coalesce(unsubscribed_by, '') FROM list_subscriptions WHERE workspace_id = $1 AND contact_id = $2 AND list_id = $3`,
		f.ws, id, f.list).Scan(&by)
	if contacts != 1 || deals != 1 {
		t.Fatalf("contact rows %d, deal rows %d — both must survive", contacts, deals)
	}
	if by != f.user {
		t.Fatalf("unsubscribed_by = %q, want the member %q", by, f.user)
	}
	var note string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		SELECT body FROM activities WHERE workspace_id = $1 AND contact_id = $2 AND created_by = $3`, f.ws, id, f.user).Scan(&note); err != nil {
		t.Fatalf("no timeline note says who did it: %v", err)
	}
	if !strings.Contains(note, "Newsletter") || !strings.Contains(note, "owner@team.test") {
		t.Fatalf("the timeline note does not name the list and the member: %q", note)
	}

	// Again: nothing changes, nothing is added, the first record stands.
	res = f.api.PostCtx(f.as("member"), "/api/w/contacts/"+id+"/lists/"+f.list+"/unsubscribe")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"changed":false`) {
		t.Fatalf("a repeat unsubscribe: %d %s", res.Code, res.Body)
	}
	var notes int
	_ = f.a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM activities WHERE workspace_id = $1 AND contact_id = $2`, f.ws, id).Scan(&notes)
	if notes != 1 {
		t.Fatalf("a repeat unsubscribe wrote %d timeline notes, want 1", notes)
	}

	// Not on that list, no such list, no such contact: 404, and nothing written.
	lone := f.contact(t, "lone@example.test", "", "", "", "", "")
	for _, path := range []string{
		"/api/w/contacts/" + lone + "/lists/" + f.list + "/unsubscribe",
		"/api/w/contacts/" + id + "/lists/no-such-list/unsubscribe",
		"/api/w/contacts/no-such-contact/lists/" + f.list + "/unsubscribe",
	} {
		if res := f.api.PostCtx(f.ctx, path); res.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", path, res.Code)
		}
	}
	// A client (read-only role) cannot.
	if res := f.api.PostCtx(f.as("client"), "/api/w/contacts/"+id+"/lists/"+other+"/unsubscribe"); res.Code != http.StatusForbidden {
		t.Fatalf("a client unsubscribing: %d, want 403", res.Code)
	}
}

func TestResubscribeAfterAdminUnsubscribeAsksConsentAgain(t *testing.T) {
	f := newAdminFixture(t, true) // double opt-in
	sctx := tenant.WithWorkspace(context.Background(), f.ws)

	// Subscribed properly: added, then confirmed through the email link.
	first, err := marketing.Subscribe(sctx, f.a.SQLDB, f.ws, f.list, "back@example.test", "Back Again", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if res := f.api.Post("/api/mail/confirm", map[string]any{"token": first.Token}); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "subscribed") {
		t.Fatalf("confirm: %d %s", res.Code, res.Body)
	}

	if res := f.api.PostCtx(f.ctx, "/api/w/contacts/"+first.ContactID+"/lists/"+f.list+"/unsubscribe"); res.Code != http.StatusOK {
		t.Fatalf("admin unsubscribe: %d %s", res.Code, res.Body)
	}

	// The same columns a self-unsubscribe writes. Compare against a real one.
	self, err := marketing.Subscribe(sctx, f.a.SQLDB, f.ws, f.list, "self@example.test", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	f.api.Post("/api/mail/confirm", map[string]any{"token": self.Token})
	if res := f.api.Post("/api/mail/unsubscribe", map[string]any{"token": self.Token}); res.Code != http.StatusOK {
		t.Fatalf("self unsubscribe: %d %s", res.Code, res.Body)
	}
	type state struct {
		Status                     string
		HasConfirmed, HasUnsub, By bool
	}
	read := func(contactID string) state {
		var s state
		if err := f.a.SQLDB.QueryRowContext(sctx, `
			SELECT status, confirmed_at IS NOT NULL, unsubscribed_at IS NOT NULL, unsubscribed_by IS NOT NULL
			FROM list_subscriptions WHERE workspace_id = $1 AND contact_id = $2 AND list_id = $3`,
			f.ws, contactID, f.list).Scan(&s.Status, &s.HasConfirmed, &s.HasUnsub, &s.By); err != nil {
			t.Fatal(err)
		}
		return s
	}
	admin, selfState := read(first.ContactID), read(self.ContactID)
	if admin.Status != selfState.Status || admin.HasConfirmed != selfState.HasConfirmed || admin.HasUnsub != selfState.HasUnsub {
		t.Fatalf("an admin unsubscribe %+v does not look like a self-unsubscribe %+v", admin, selfState)
	}
	if !admin.By || selfState.By {
		t.Fatalf("who did it: admin %v (want set), self %v (want empty)", admin.By, selfState.By)
	}

	// Back through the form: pending, a NEW token, a confirmation to send.
	again, err := marketing.Subscribe(sctx, f.a.SQLDB, f.ws, f.list, "back@example.test", "", "en", "form")
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != marketing.StatusPending || !again.NeedsConfirmation {
		t.Fatalf("re-subscribe after an admin unsubscribe: status %q, needs confirmation %v — consent must be asked again",
			again.Status, again.NeedsConfirmation)
	}
	if again.Token == first.Token {
		t.Fatal("the old token still works; the first confirmation link would re-subscribe them")
	}
	if s := read(first.ContactID); s.By || s.HasUnsub {
		t.Fatalf("back on the list but still marked as unsubscribed by a member: %+v", s)
	}
	// And the old confirmation link does nothing.
	if res := f.api.Post("/api/mail/confirm", map[string]any{"token": first.Token}); res.Code != http.StatusNotFound {
		t.Fatalf("the old confirmation link: %d, want 404", res.Code)
	}

	// The workspace's own add-contact route follows the same rule.
	res := f.api.PostCtx(f.ctx, "/api/w/contacts/"+self.ContactID+"/lists/"+f.list+"/unsubscribe")
	if res.Code != http.StatusOK {
		t.Fatalf("%d %s", res.Code, res.Body)
	}
	res = f.api.PostCtx(f.ctx, "/api/w/contacts", map[string]any{"email": "self@example.test", "list_id": f.list})
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"status":"pending"`) {
		t.Fatalf("re-adding from the workspace: %d %s — must be pending", res.Code, res.Body)
	}
}

func readCSV(t *testing.T, raw []byte) [][]string {
	t.Helper()
	if !bytes.HasPrefix(raw, []byte("\xEF\xBB\xBF")) {
		t.Fatalf("the export does not start with a UTF-8 BOM; Excel will garble Arabic. First bytes: %q", raw[:min(len(raw), 8)])
	}
	recs, err := csv.NewReader(bytes.NewReader(raw[3:])).ReadAll()
	if err != nil {
		t.Fatalf("the export is not valid CSV: %v", err)
	}
	if strings.Join(recs[0], ",") != strings.Join(marketing.ExportColumns, ",") {
		t.Fatalf("header = %v", recs[0])
	}
	return recs
}

func TestExportMatchesTheFilteredView(t *testing.T) {
	f := newAdminFixture(t, false)
	other := f.newList(t, "Other", false)

	f.contact(t, "fady@example.test", "فادي موندي", "+20 100 123 4567", "شركة النيل للتجارة", f.list, "subscribed")
	f.contact(t, "mona@example.test", "منى", "", "", f.list, "subscribed")
	f.contact(t, "evil@example.test", `=HYPERLINK("http://evil.test","click")`, "", "@SUM(1)", f.list, "subscribed")
	f.contact(t, "comma@example.test", `Last, First "Q"`, "", "", f.list, "subscribed")
	f.contact(t, "pending@example.test", "Pending Person", "", "", f.list, "pending")
	f.contact(t, "gone@example.test", "Gone", "", "", f.list, "unsubscribed")
	f.contact(t, "other@example.test", "Other List", "", "", other, "subscribed")

	for _, q := range []url.Values{
		{},
		{"status": {"subscribed"}},
		{"status": {"unsubscribed"}},
		{"q": {"فادي"}},
		{"status": {"subscribed"}, "q": {"example.test"}},
	} {
		view := cloneValues(q)
		view.Set("list_id", f.list)
		view.Set("limit", "200")
		p := f.page(t, f.ctx, view)

		res := f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/export.csv?"+q.Encode())
		if res.Code != http.StatusOK {
			t.Fatalf("export %s: %d %s", q.Encode(), res.Code, res.Body)
		}
		if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") || !strings.Contains(ct, "utf-8") {
			t.Fatalf("content type %q", ct)
		}
		if cd := res.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "filename*=UTF-8''Newsletter") {
			t.Fatalf("content disposition %q", cd)
		}
		recs := readCSV(t, res.Body.Bytes())[1:]
		if len(recs) != len(p.Contacts) || int64(len(recs)) != p.Total {
			t.Fatalf("filter %s: export has %d rows, the view shows %d of %d", q.Encode(), len(recs), len(p.Contacts), p.Total)
		}
		for i, c := range p.Contacts {
			if recs[i][1] != c.Email {
				t.Fatalf("filter %s row %d: export %q, view %q — same rows, same order", q.Encode(), i, recs[i][1], c.Email)
			}
		}
	}

	// Arabic survives the round trip, byte for byte, and the rest of the row
	// says what it should.
	res := f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/export.csv?q=fady")
	recs := readCSV(t, res.Body.Bytes())[1:]
	if len(recs) != 1 {
		t.Fatalf("search for fady exported %d rows", len(recs))
	}
	row := recs[0]
	if row[0] != "فادي موندي" || row[3] != "شركة النيل للتجارة" {
		t.Fatalf("Arabic did not survive: name %q company %q", row[0], row[3])
	}
	if row[2] != "+20 100 123 4567" {
		t.Fatalf("a phone number was mangled: %q", row[2])
	}
	if row[4] != "subscribed" || row[5] == "" || row[6] != "" || row[7] != "import" {
		t.Fatalf("status/stamps/source wrong: %v", row)
	}
	if _, err := time.Parse(time.RFC3339, row[5]); err != nil {
		t.Fatalf("subscribed_at is not RFC 3339: %q", row[5])
	}

	// A name that is a formula is exported as text, not run.
	res = f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/export.csv?q=evil")
	row = readCSV(t, res.Body.Bytes())[1]
	if !strings.HasPrefix(row[0], "'=") || !strings.HasPrefix(row[3], "'@") {
		t.Fatalf("formula injection not neutralised: name %q company %q", row[0], row[3])
	}
	// Quotes and commas are escaped, not split.
	res = f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/export.csv?q=comma")
	if row = readCSV(t, res.Body.Bytes())[1]; row[0] != `Last, First "Q"` {
		t.Fatalf("a comma or quote broke the row: %q", row[0])
	}

	// Who may: a manager may, a member may not (it is a bulk disclosure, not a
	// read), and an unknown list is a 404 before any byte of CSV goes out.
	if res := f.api.GetCtx(f.as("manager"), "/api/w/mail/lists/"+f.list+"/export.csv"); res.Code != http.StatusOK {
		t.Fatalf("a manager exporting: %d", res.Code)
	}
	if res := f.api.GetCtx(f.as("member"), "/api/w/mail/lists/"+f.list+"/export.csv"); res.Code != http.StatusForbidden {
		t.Fatalf("a member exporting: %d, want 403", res.Code)
	}
	if res := f.api.GetCtx(f.ctx, "/api/w/mail/lists/no-such-list/export.csv"); res.Code != http.StatusNotFound {
		t.Fatalf("an unknown list: %d, want 404", res.Code)
	}
	if res := f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/export.csv?status=nope"); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown status: %d, want 422", res.Code)
	}
}

func TestEditListNameAndDoubleOptIn(t *testing.T) {
	f := newAdminFixture(t, true)
	f.newList(t, "Taken", true)

	res := f.api.PatchCtx(f.ctx, "/api/w/mail/lists/"+f.list, map[string]any{"name": "  النشرة الشهرية  ", "double_opt_in": false})
	if res.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", res.Code, res.Body)
	}
	var out struct {
		List marketing.ListOut `json:"list"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.List.Name != "النشرة الشهرية" || out.List.DoubleOptIn {
		t.Fatalf("after patch: %+v", out.List)
	}

	// The new setting is what the next sign-up gets: straight in, no email.
	sctx := tenant.WithWorkspace(context.Background(), f.ws)
	r, err := marketing.Subscribe(sctx, f.a.SQLDB, f.ws, f.list, "direct@example.test", "", "ar", "form")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != marketing.StatusSubscribed || r.NeedsConfirmation {
		t.Fatalf("after turning double opt-in off: %q, confirm %v", r.Status, r.NeedsConfirmation)
	}

	// Only what was sent changes.
	res = f.api.PatchCtx(f.ctx, "/api/w/mail/lists/"+f.list, map[string]any{"double_opt_in": true})
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.List.Name != "النشرة الشهرية" || !out.List.DoubleOptIn || out.List.Subscribed != 1 {
		t.Fatalf("a partial patch changed more than it said: %+v", out.List)
	}

	if res := f.api.PatchCtx(f.ctx, "/api/w/mail/lists/"+f.list, map[string]any{"name": "Taken"}); res.Code != http.StatusConflict {
		t.Fatalf("a duplicate name: %d, want 409", res.Code)
	}
	if res := f.api.PatchCtx(f.ctx, "/api/w/mail/lists/"+f.list, map[string]any{"name": "   "}); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a blank name: %d, want 422", res.Code)
	}
	if res := f.api.PatchCtx(f.ctx, "/api/w/mail/lists/no-such-list", map[string]any{"name": "X"}); res.Code != http.StatusNotFound {
		t.Fatalf("an unknown list: %d, want 404", res.Code)
	}
	if res := f.api.PatchCtx(f.as("client"), "/api/w/mail/lists/"+f.list, map[string]any{"name": "X"}); res.Code != http.StatusForbidden {
		t.Fatalf("a client renaming: %d, want 403", res.Code)
	}
}
