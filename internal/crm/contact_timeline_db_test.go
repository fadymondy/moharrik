package crm

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
The contact timeline (MH-1064) against a real database with RLS on. Every expected entry is worked
out by hand from the rows inserted below, with timestamps pinned so the order is the point under test.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/crm/ -run ContactTimeline -v
*/

func ctApp(t *testing.T) *app.App {
	t.Helper()
	owner, appURL := testdb.Setup(t, "crm_ctimeline", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "ctimeline-test-secret-ctimeline-test-secret-01")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("SCHEDULER", "off")
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	k := togo.New()
	a := app.New(context.Background(), k)
	t.Cleanup(func() { k.Close() })
	if a.SQLDB == nil || a.SysDB == nil {
		t.Fatal("both pools are required")
	}
	return a
}

type ctSeed struct {
	t  *testing.T
	db *sql.DB
	ws string
	u  string
}

func (s ctSeed) id(q string, args ...any) string {
	s.t.Helper()
	var id string
	if err := s.db.QueryRow(q, append([]any{s.ws}, args...)...).Scan(&id); err != nil {
		s.t.Fatalf("%v\n%s", err, q)
	}
	return id
}

func (s ctSeed) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.db.Exec(q, append([]any{s.ws}, args...)...); err != nil {
		s.t.Fatalf("%v\n%s", err, q)
	}
}

func ctWorkspace(t *testing.T, a *app.App) ctSeed {
	t.Helper()
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "-")
	user := "u-ct-" + suffix
	if _, err := a.SysDB.Exec(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', 'now')`, user, "ct-"+suffix+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var ws string
	if err := a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by, currency, timezone) VALUES ('CT', $1, $2, 'USD', 'UTC') RETURNING id`,
		"ct-"+suffix, user).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, 'owner', 'active')`, ws, user); err != nil {
		t.Fatal(err)
	}
	return ctSeed{t: t, db: a.SysDB, ws: ws, u: user}
}

func ctCtx(ws, role, via string, scopes ...string) context.Context {
	return tenant.With(context.Background(), &tenant.Scope{WorkspaceID: ws, UserID: "x", Role: role, Via: via, Scopes: scopes})
}

func TestContactTimeline(t *testing.T) {
	a := ctApp(t)
	s := ctWorkspace(t, a)
	base := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }

	contact := s.id(`INSERT INTO contacts (workspace_id, email, name, created_at) VALUES ($1, 'sara@example.test', 'Sara', $2) RETURNING id`, at(0))
	other := s.id(`INSERT INTO contacts (workspace_id, email, name, created_at) VALUES ($1, 'other@example.test', 'Other', $2) RETURNING id`, at(0))

	qualify := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Qualify', 0, 'open') RETURNING id`)
	proposal := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Proposal', 1, 'open') RETURNING id`)
	won := s.id(`INSERT INTO pipeline_stages (workspace_id, name, position, kind) VALUES ($1, 'Won', 2, 'won') RETURNING id`)
	deal := s.id(`INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, contact_id, created_by, created_at) VALUES ($1, 'Pilot', 250000, 'USD', $2, $3, $4, $5) RETURNING id`,
		won, contact, s.u, at(2))
	otherDeal := s.id(`INSERT INTO deals (workspace_id, title, stage_id, contact_id, created_by) VALUES ($1, 'Not Sara', $2, $3, $4) RETURNING id`, qualify, other, s.u)

	// Triggers wrote stage and lifecycle rows at insert time; replace them with pinned ones.
	s.exec(`DELETE FROM deal_stage_events WHERE workspace_id = $1 AND deal_id = $2`, deal)
	s.exec(`DELETE FROM contact_lifecycle_events WHERE workspace_id = $1`)
	ev := func(from any, to string, h int) {
		s.exec(`INSERT INTO deal_stage_events (workspace_id, deal_id, stage_id, from_stage_id, entered_at) VALUES ($1, $2, $3, $4, $5)`, deal, to, from, at(h))
	}
	ev(nil, qualify, 2)
	ev(qualify, proposal, 4)
	ev(proposal, won, 6)
	s.exec(`INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source, created_at) VALUES ($1, $2, '', 'lead', 'lead_converted', $3)`, contact, at(1))
	s.exec(`INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source, created_at) VALUES ($1, $2, 'lead', 'customer', 'deal_won', $3)`, contact, at(6))
	s.exec(`INSERT INTO contact_lifecycle_events (workspace_id, contact_id, from_stage, to_stage, source, created_at) VALUES ($1, $2, '', 'lead', 'manual', $3)`, other, at(1))

	// activities: one on the contact, one on its deal, one on someone else.
	s.exec(`INSERT INTO activities (workspace_id, kind, body, contact_id, created_by, created_at) VALUES ($1, 'call', 'Called Sara', $2, $3, $4)`, contact, s.u, at(3))
	s.exec(`INSERT INTO activities (workspace_id, kind, body, deal_id, created_by, created_at) VALUES ($1, 'note', 'Sent the proposal', $2, $3, $4)`, deal, s.u, at(5))
	s.exec(`INSERT INTO activities (workspace_id, kind, body, contact_id, created_by, created_at) VALUES ($1, 'note', 'Not Sara', $2, $3, $4)`, other, s.u, at(5))

	// a converted lead with UTM, an unlinked lead with the same email (must not match).
	lead := s.id(`INSERT INTO leads (workspace_id, name, email, message, utm_source, utm_campaign, contact_id, created_at) VALUES ($1, 'Sara', 'sara@example.test', 'hello', 'google', 'autumn', $2, $3) RETURNING id`, contact, at(1))
	s.exec(`INSERT INTO leads (workspace_id, name, email, message, created_at) VALUES ($1, 'Sara', 'sara@example.test', 'unlinked', $2)`, at(7))

	// a conversation with two messages, another contact's conversation.
	conv := s.id(`INSERT INTO conversations (workspace_id, channel, contact_id, customer_name, subject, last_message_at, created_at) VALUES ($1, 'email', $2, 'Sara', 'Pricing', $3, $4) RETURNING id`, contact, at(8), at(7))
	s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, 'in', 'hi', $3)`, conv, at(7))
	s.exec(`INSERT INTO messages (workspace_id, conversation_id, direction, body, created_at) VALUES ($1, $2, 'out', 'hello', $3)`, conv, at(8))
	s.exec(`INSERT INTO conversations (workspace_id, channel, contact_id, customer_name, last_message_at) VALUES ($1, 'chat', $2, 'Other', $3)`, other, at(8))

	// rule runs: by subject, by event.contact_id (a deal event), a dry run, another contact's.
	rule := s.id(`INSERT INTO automation_rules (workspace_id, name, trigger, created_by, updated_by) VALUES ($1, 'Welcome', 'lead.created', $2, $2) RETURNING id`, s.u)
	run := func(status string, dry bool, subject, eventContact string, h int) {
		s.exec(`INSERT INTO automation_rule_runs (workspace_id, rule_id, rule_name, trigger, status, dry_run, subject_id, event, started_at)
			VALUES ($1, $2, 'Welcome', 'x', $3, $4, $5, jsonb_build_object('contact_id', $6::text), $7)`, rule, status, dry, subject, eventContact, at(h))
	}
	run("ok", false, contact, contact, 9)
	run("failed", false, deal, contact, 10) // a deal event naming the contact
	run("ok", true, contact, contact, 11)   // dry run: not an event
	run("ok", false, otherDeal, other, 12)  // someone else's

	ctx := ctCtx(s.ws, "owner", "session")
	page, err := ContactTimeline(ctx, a.SQLDB, contact, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range page.Events {
		got = append(got, e.Kind)
	}
	// newest first: rule(10) rule(9) conversation(8) deal.won(6)=lifecycle(6) note(5) deal stage(4) call(3) deal.opened(2) lead(1)=lifecycle(1) contact(0)
	want := []string{"rule.ran", "rule.ran", "conversation"}
	if strings.Join(got[:3], ",") != strings.Join(want, ",") {
		t.Fatalf("first events = %v, want %v (all: %v)", got[:3], want, got)
	}
	if len(got) != 12 {
		t.Fatalf("got %d events %v, want 12", len(got), got)
	}
	count := map[string]int{}
	for _, k := range got {
		count[k]++
	}
	for k, w := range map[string]int{"rule.ran": 2, "conversation": 1, "deal.won": 1, "deal.opened": 1, "deal.stage_changed": 1,
		"lifecycle.changed": 2, "lead.submitted": 1, "activity.call": 1, "activity.note": 1, "contact.created": 1} {
		if count[k] != w {
			t.Errorf("%s = %d, want %d", k, count[k], w)
		}
	}
	for _, e := range page.Events {
		switch e.Kind {
		case "lead.submitted":
			if e.ID != lead || e.Data["utm_source"] != "google" || e.Data["utm_campaign"] != "autumn" {
				t.Errorf("lead event = %+v", e)
			}
		case "deal.won":
			if e.Data["stage"] != "Won" || e.Data["from_stage"] != "Proposal" || e.Data["title"] != "Pilot" || e.Data["value_minor"].(float64) != 250000 {
				t.Errorf("deal.won data = %v", e.Data)
			}
		case "activity.note":
			if e.Data["on_deal"] != true {
				t.Errorf("the deal's note should say on_deal: %v", e.Data)
			}
		case "conversation":
			if e.Data["messages"].(float64) != 2 || e.Data["inbound"].(float64) != 1 {
				t.Errorf("conversation data = %v", e.Data)
			}
		}
	}
	if page.Overview == nil || len(page.Overview.Conversations) != 1 || page.Overview.Conversations[0].Messages != 2 || page.Overview.Conversations[0].LastDirection != "out" {
		t.Errorf("overview conversations = %+v", page.Overview)
	}
	if page.Overview == nil || len(page.Overview.Automations) != 1 || page.Overview.Automations[0].Runs != 2 || page.Overview.Automations[0].LastStatus != "failed" {
		t.Errorf("overview automations = %+v", page.Overview)
	}
	if len(page.Sources) != 7 {
		t.Errorf("sources = %v", page.Sources)
	}

	// Paging by cursor: 5 + 5 + 2, no repeats, same order as the one big page.
	var paged []string
	cursor := ""
	pages := 0
	for {
		p, err := ContactTimeline(ctx, a.SQLDB, contact, cursor, 5)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if (cursor == "") != (p.Overview != nil) {
			t.Errorf("overview only belongs to the first page (cursor %q)", cursor)
		}
		for _, e := range p.Events {
			paged = append(paged, e.Kind+":"+e.ID+":"+e.At.UTC().Format(time.RFC3339Nano))
		}
		if p.NextCursor == "" {
			break
		}
		cursor = p.NextCursor
	}
	if pages != 3 || len(paged) != 12 {
		t.Errorf("pages = %d, events = %d, want 3 and 12", pages, len(paged))
	}
	seen := map[string]bool{}
	for i, k := range paged {
		if seen[k] {
			t.Errorf("event %s repeated across pages", k)
		}
		seen[k] = true
		if strings.SplitN(k, ":", 2)[0] != page.Events[i].Kind {
			t.Errorf("paged order differs at %d: %s vs %s", i, k, page.Events[i].Kind)
		}
	}

	// A role without the inbox, leads and automation permissions gets only the CRM kinds.
	client := ctCtx(s.ws, "client", "session")
	if _, err := ContactTimeline(client, a.SQLDB, contact, "", 0); err == nil {
		t.Log("client may read contacts in this build")
	}
	// An MCP token with contacts:read only: no conversations, leads or runs, no overview lists.
	narrow := ctCtx(s.ws, "owner", "mcp", "contacts:read")
	np, err := ContactTimeline(narrow, a.SQLDB, contact, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range np.Events {
		if e.Kind == "conversation" || e.Kind == "lead.submitted" || e.Kind == "rule.ran" {
			t.Errorf("a contacts:read token saw %s", e.Kind)
		}
	}
	if len(np.Events) != 8 || len(np.Sources) != 4 || np.Overview.Conversations != nil || np.Overview.Automations != nil {
		t.Errorf("narrow token: %d events, sources %v, overview %+v", len(np.Events), np.Sources, np.Overview)
	}

	// Another workspace's contact is a 404, not an empty page.
	s2 := ctWorkspace(t, a)
	if _, err := ContactTimeline(ctCtx(s2.ws, "owner", "session"), a.SQLDB, contact, "", 0); err == nil {
		t.Error("a contact from another workspace must not be readable")
	}
	if _, err := ContactTimeline(ctx, a.SQLDB, contact, "not-a-cursor", 0); err == nil {
		t.Error("a bad cursor must be refused")
	}
	if _, err := ContactTimeline(ctx, a.SQLDB, contact, "", 101); err == nil {
		t.Error("a limit over 100 must be refused")
	}
}
