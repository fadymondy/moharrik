package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/leads"
	"github.com/fadymondy/moharrik/internal/rules"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
TestAutomationRules covers MH-427 end to end on the real stack, with only the
outside world faked (an Evolution server, an SMTP sink, a webhook receiver):

  - the three rules the card names, driven by real events through the public
    routes: a website inquiry → notify + open a deal; a signed Sentry delivery
    → WhatsApp the on-call member; a contact opting in → add to another list;
  - a test against a sample event renders every action and does nothing;
  - the loop guard: "when a deal is created, create a deal" stops after one,
    and two rules feeding each other stop too;
  - the rate limit, once-per-record, consent refusals, a member who may not
    write rules, the signed webhook and its private-address refusal, the
    schedule trigger, and the MCP tools.
*/

// ruleSMTPSink is a fake SMTP server that keeps every message it is given.
type ruleSMTPSink struct {
	port int
	mu   sync.Mutex
	got  []string // "to\n\ndata"
}

func startRuleSMTPSink(t *testing.T) *ruleSMTPSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &ruleSMTPSink{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				say := func(l string) { _, _ = c.Write([]byte(l + "\r\n")) }
				say("220 sink")
				to := ""
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					raw := strings.TrimSpace(line)
					cmd := strings.ToUpper(raw)
					switch {
					case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
						say("250 sink")
					case strings.HasPrefix(cmd, "RCPT TO:"):
						to = strings.Trim(raw[8:], "<> ")
						say("250 ok")
					case cmd == "DATA":
						say("354 go")
						var b strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil {
								return
							}
							if strings.TrimRight(l, "\r\n") == "." {
								break
							}
							b.WriteString(l)
						}
						s.mu.Lock()
						s.got = append(s.got, to+"\n\n"+b.String())
						s.mu.Unlock()
						say("250 queued")
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(c)
		}
	}()
	return s
}

func (s *ruleSMTPSink) mails() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func TestAutomationRules(t *testing.T) {
	t.Setenv("WHATSAPP_NOTIFY", "off")
	defer leads.SetLimiter(inbox.NewMemoryLimiter(), leads.DefaultInquiryLimits())()
	sf := bootSurface(t)
	alice, a, ws := sf.alice, sf.app, sf.ws
	ctx := context.Background()
	sctx := tenant.WithWorkspace(ctx, ws)

	// process runs the dispatcher for this workspace until nothing is left.
	process := func() int {
		t.Helper()
		total := 0
		for i := 0; i < 10; i++ {
			n, err := rules.ProcessWorkspace(ctx, a, ws)
			if err != nil {
				t.Fatal(err)
			}
			total += n
			if n == 0 {
				break
			}
		}
		return total
	}
	runsOf := func(ruleID string) []map[string]any {
		t.Helper()
		res := alice.must("GET", "/api/w/rule-runs?limit=100&rule_id="+ruleID, nil, 200)
		out := []map[string]any{}
		for _, r := range arr(res["runs"]) {
			out = append(out, obj(r))
		}
		return out
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := a.SysDB.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	var aliceID string
	if err := a.SysDB.QueryRow(`SELECT user_id FROM workspace_members WHERE workspace_id = $1`, ws).Scan(&aliceID); err != nil {
		t.Fatal(err)
	}

	// ---- the catalog and validation --------------------------------------------------
	cat := alice.must("GET", "/api/w/rules/catalog", nil, 200)
	if len(arr(cat["triggers"])) < 14 || len(arr(cat["actions"])) != 14 {
		t.Fatalf("catalog: %d triggers, %d actions", len(arr(cat["triggers"])), len(arr(cat["actions"])))
	}
	for _, bad := range []map[string]any{
		{"name": "x", "trigger": "no.such.event", "actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "t"}}}},
		{"name": "x", "trigger": "lead.created", "actions": []any{}},
		{"name": "x", "trigger": "lead.created", "conditions": []any{map[string]any{"field": "nope", "op": "equals", "value": "1"}},
			"actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "t"}}}},
		{"name": "x", "trigger": "lead.created", "actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{}}}},
		{"name": "x", "trigger": "schedule", "actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "t"}}}},
	} {
		if code, b := alice.do("POST", "/api/w/rules", bad); code != 422 {
			t.Errorf("an invalid rule %v: got %d %s", bad["trigger"], code, b)
		}
	}

	// ---- 1. new lead → notify + open a deal -----------------------------------------
	leadRule := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "New lead → notify and open a deal", "trigger": "lead.created",
		"conditions": []any{map[string]any{"field": "email", "op": "is_not_empty"}},
		"actions": []any{
			map[string]any{"type": "notify_members", "params": map[string]any{"members": "all", "title": "New lead: {{name}} ({{company}})", "body": "{{message}}"}},
			map[string]any{"type": "create_deal", "params": map[string]any{"title": "{{company}} — {{name}}", "value": "1500"}},
			map[string]any{"type": "add_note", "params": map[string]any{"body": "From the website: {{message}}", "deal_id": "{{deal_id}}"}},
		},
	}, 201)["rule"])
	leadRuleID := leadRule["id"].(string)
	if leadRule["enabled"] != false {
		t.Fatalf("a new rule should start switched off: %v", leadRule)
	}

	// A test against a sample event does nothing, and says what it would do.
	dealsBefore := count(`SELECT count(*) FROM deals WHERE workspace_id = $1`, ws)
	test := obj(alice.must("POST", "/api/w/rules/"+leadRuleID+"/test", map[string]any{"event": map[string]any{"name": "Mona", "company": "Globex"}}, 200)["run"])
	if test["status"] != "ok" || test["dry_run"] != true {
		t.Fatalf("test run: %v", test)
	}
	acts := arr(test["actions"])
	if len(acts) != 3 || obj(acts[0])["status"] != "would" || !strings.Contains(obj(acts[0])["summary"].(string), "New lead: Mona (Globex)") ||
		!strings.Contains(obj(acts[1])["summary"].(string), "Globex — Mona") {
		t.Fatalf("test run actions: %v", acts)
	}
	if n := count(`SELECT count(*) FROM deals WHERE workspace_id = $1`, ws); n != dealsBefore {
		t.Fatalf("a test opened a deal: %d -> %d", dealsBefore, n)
	}
	// A draft can be tested before it is saved; it is not logged.
	draft := obj(alice.must("POST", "/api/w/rules/test", map[string]any{"rule": map[string]any{
		"name": "draft", "trigger": "deal.won", "conditions": []any{map[string]any{"field": "value", "op": "gte", "value": "10000"}},
		"actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "Big win: {{title}}"}}},
	}}, 200)["run"])
	if draft["status"] != "skipped" || obj(arr(draft["conditions"])[0])["passed"] != false {
		t.Fatalf("a draft whose condition fails on the sample: %v", draft)
	}

	// Switched off, a real lead does nothing.
	inq := alice.must("POST", "/api/w/subscribe-forms", map[string]any{"kind": "inquiry", "name": "Site form", "allowed_origins": []string{"https://rules.test"}}, 201)
	inqKey := obj(inq["form"])["public_key"].(string)
	inquire := func(name, company string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"email": strings.ToLower(name) + "-" + rnd() + "@rules.test", "name": name, "company": company, "message": "We need a site"})
		req, _ := http.NewRequest("POST", sf.srv.URL+"/api/inquiry/"+inqKey, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://rules.test")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("inquiry: %d %s", res.StatusCode, body)
		}
	}
	inquire("Early", "Nobody")
	if n := process(); n != 0 {
		t.Fatalf("a switched-off rule ran %d times", n)
	}

	alice.must("PATCH", "/api/w/rules/"+leadRuleID, map[string]any{"enabled": true}, 200)
	inquire("Rana", "Acme")
	if n := process(); n != 1 {
		t.Fatalf("one lead should make one run, got %d", n)
	}
	runs := runsOf(leadRuleID)
	real := runs[0]
	if real["status"] != "ok" || real["dry_run"] != false || obj(real["event"])["company"] != "Acme" {
		t.Fatalf("the lead run: %v", real)
	}
	dealID := obj(arr(real["actions"])[1])["ref"].(string)
	var dealTitle string
	if err := a.SysDB.QueryRow(`SELECT title FROM deals WHERE workspace_id = $1 AND id = $2`, ws, dealID).Scan(&dealTitle); err != nil || dealTitle != "Acme — Rana" {
		t.Fatalf("the rule's deal: %q %v", dealTitle, err)
	}
	if n := count(`SELECT count(*) FROM notification_items WHERE workspace_id = $1 AND kind = 'rule' AND title = 'New lead: Rana (Acme)'`, ws); n != 1 {
		t.Fatalf("the rule's notification: %d", n)
	}
	if n := count(`SELECT count(*) FROM activities WHERE workspace_id = $1 AND deal_id = $2 AND kind = 'note'`, ws, dealID); n != 1 {
		t.Fatalf("the note on the rule's own deal (deal_id from the step before): %d", n)
	}
	// The deal was opened as alice and recorded in the audit log as the rule.
	if n := count(`SELECT count(*) FROM audit_log WHERE workspace_id = $1 AND entity_id = $2 AND actor_kind = 'agent' AND actor_id = $3`, ws, dealID, "rule:"+leadRuleID); n != 1 {
		t.Errorf("the audit log should name the rule as the deal's creator: %d", n)
	}
	if r := obj(alice.must("GET", "/api/w/rules/"+leadRuleID, nil, 200)["rule"]); r["run_count"].(float64) != 1 || r["last_status"] != "ok" {
		t.Errorf("the rule's last run: %v", r)
	}

	// ---- 2. Sentry error → WhatsApp the on-call member --------------------------------
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)
	const onCall = "201700003333"
	if _, err := a.SysDB.Exec(`INSERT INTO member_whatsapp (workspace_id, user_id, phone, verified_at, quiet_start, quiet_end, batch_minutes)
		VALUES ($1, $2, $3, now(), 0, 0, 0)`, ws, aliceID, onCall); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.SysDB.Exec(`DELETE FROM member_whatsapp WHERE workspace_id = $1`, ws) })
	sentryRule := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Sentry error → WhatsApp on-call", "trigger": "notification.received", "enabled": true,
		"conditions": []any{
			map[string]any{"field": "source_kind", "op": "equals", "value": "sentry"},
			map[string]any{"field": "severity", "op": "in", "value": "error, fatal"},
		},
		"actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"members": aliceID, "whatsapp": "true", "title": "Sentry: {{title}}", "body": "{{body}}"}}},
	}, 201)["rule"])
	sentryRuleID := sentryRule["id"].(string)
	src := alice.must("POST", "/api/w/webhook_sources", map[string]any{"name": "Sentry prod", "kind": "sentry"}, 201)
	// A Sentry source takes the integration's own Client Secret.
	srcID, srcSecret := obj(src["source"])["id"].(string), "sentry-client-secret-"+rnd()
	alice.must("PUT", "/api/w/webhook_sources/"+srcID+"/secret", map[string]any{"secret": srcSecret}, 200)
	deliver := func(level, title string) {
		t.Helper()
		b := []byte(`{"action":"created","data":{"error":{"title":"` + title + `","level":"` + level + `","culprit":"checkout.js","web_url":"https://sentry.test/1"}}}`)
		mac := hmac.New(sha256.New, []byte(srcSecret))
		mac.Write(b)
		req, _ := http.NewRequest("POST", sf.srv.URL+"/api/hooks/"+srcID, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sentry-Hook-Resource", "error")
		req.Header.Set("Sentry-Hook-Signature", hex.EncodeToString(mac.Sum(nil)))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 202 {
			t.Fatalf("sentry delivery: %d", res.StatusCode)
		}
	}
	deliver("warning", "Slow query")
	deliver("error", "TypeError in checkout")
	process()
	sruns := runsOf(sentryRuleID)
	var matched, skipped int
	for _, r := range sruns {
		switch r["status"] {
		case "ok":
			matched++
		case "skipped":
			skipped++
		}
	}
	if matched != 1 || skipped < 1 {
		t.Fatalf("sentry runs: %d ok, %d skipped: %v", matched, skipped, sruns)
	}
	if err := whatsapp.Collect(sctx, a, ws); err != nil {
		t.Fatal(err)
	}
	if _, err := whatsapp.FlushWorkspace(sctx, a, ws); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range sf.evo.sends() {
		if s.Number == onCall {
			got = append(got, s.Text)
		}
	}
	if len(got) != 1 || !strings.Contains(got[0], "Sentry: Error: TypeError in checkout") || strings.Contains(got[0], "Slow query") {
		t.Fatalf("the on-call member's WhatsApp: %q", got)
	}

	// ---- 3. a contact opts in → add them to another list ----------------------------------
	listA := obj(alice.must("POST", "/api/w/mail/lists", map[string]any{"name": "Newsletter " + rnd(), "double_opt_in": false}, 201)["list"])["id"].(string)
	listB := obj(alice.must("POST", "/api/w/mail/lists", map[string]any{"name": "Onboarding " + rnd(), "double_opt_in": false}, 201)["list"])["id"].(string)
	optRule := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Opted in → onboarding list", "trigger": "contact.opted_in", "enabled": true,
		"conditions": []any{map[string]any{"field": "channel", "op": "equals", "value": "email"}},
		"actions":    []any{map[string]any{"type": "add_to_list", "params": map[string]any{"list_id": listB, "contact_id": "{{contact_id}}"}}},
	}, 201)["rule"])
	optRuleID := optRule["id"].(string)
	subEmail := "sub-" + rnd() + "@rules.test"
	alice.must("POST", "/api/w/contacts", map[string]any{"email": subEmail, "list_id": listA}, 201)
	process()
	if n := count(`SELECT count(*) FROM list_subscriptions s JOIN contacts c ON c.id = s.contact_id
		WHERE s.workspace_id = $1 AND s.list_id = $2 AND c.email = $3 AND s.status = 'subscribed'`, ws, listB, subEmail); n != 1 {
		t.Fatalf("the contact should be on the onboarding list: %d", n)
	}
	// Joining list B was itself an opt-in — caused by this rule, so the rule
	// did not run on it again: the loop guard's record of that.
	oruns := runsOf(optRuleID)
	statuses := map[string]int{}
	for _, r := range oruns {
		statuses[r["status"].(string)]++
	}
	if statuses["ok"] != 1 || statuses["loop_blocked"] != 1 {
		t.Fatalf("opt-in rule runs: %v", statuses)
	}
	// Someone who unsubscribed from B is never put back by a rule.
	gone := "gone-" + rnd() + "@rules.test"
	goneID := obj(alice.must("POST", "/api/w/contacts", map[string]any{"email": gone, "list_id": listB}, 201)["contact"])["id"].(string)
	process() // their B opt-in: this rule does nothing useful but that is fine
	if _, err := a.SysDB.Exec(`UPDATE list_subscriptions SET status = 'unsubscribed', unsubscribed_at = now() WHERE workspace_id = $1 AND list_id = $2 AND contact_id = $3`, ws, listB, goneID); err != nil {
		t.Fatal(err)
	}
	alice.must("POST", "/api/w/contacts", map[string]any{"email": gone, "list_id": listA}, 201)
	process()
	if n := count(`SELECT count(*) FROM list_subscriptions WHERE workspace_id = $1 AND list_id = $2 AND contact_id = $3 AND status = 'unsubscribed'`, ws, listB, goneID); n != 1 {
		t.Fatalf("a rule re-subscribed someone who had unsubscribed")
	}
	last := runsOf(optRuleID)[0]
	if last["status"] != "ok" || obj(arr(last["actions"])[0])["status"] != "skipped" {
		t.Fatalf("the re-add should be skipped with its reason: %v", last)
	}
	alice.must("PATCH", "/api/w/rules/"+optRuleID, map[string]any{"enabled": false}, 200)

	// ---- the loop guard ---------------------------------------------------------------------
	loopRule := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Deal → another deal", "trigger": "deal.created", "enabled": true,
		"actions": []any{map[string]any{"type": "create_deal", "params": map[string]any{"title": "echo of {{title}}"}}},
	}, 201)["rule"])
	loopID := loopRule["id"].(string)
	alice.must("POST", "/api/w/deals", map[string]any{"title": "Seed deal"}, 201)
	process()
	if n := count(`SELECT count(*) FROM deals WHERE workspace_id = $1 AND title LIKE 'echo of %'`, ws); n != 1 {
		t.Fatalf("a self-triggering rule should make exactly one deal, made %d", n)
	}
	lruns := runsOf(loopID)
	if len(lruns) != 2 || lruns[0]["status"] != "loop_blocked" || lruns[1]["status"] != "ok" || lruns[0]["depth"].(float64) != 1 {
		t.Fatalf("loop runs: %v", lruns)
	}
	// Two rules feeding each other stop as well.
	pong := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Deal → another deal (2)", "trigger": "deal.created", "enabled": true,
		"actions": []any{map[string]any{"type": "create_deal", "params": map[string]any{"title": "pong of {{title}}"}}},
	}, 201)["rule"])
	alice.must("POST", "/api/w/deals", map[string]any{"title": "Ping"}, 201)
	process()
	echoes := count(`SELECT count(*) FROM deals WHERE workspace_id = $1 AND (title LIKE 'echo of %' OR title LIKE 'pong of %')`, ws)
	if echoes > 1+4 {
		t.Fatalf("two rules feeding each other did not stop: %d deals", echoes)
	}
	if n := count(`SELECT count(*) FROM automation_rule_events WHERE workspace_id = $1 AND processed_at IS NULL`, ws); n != 0 {
		t.Fatalf("%d events left unprocessed", n)
	}
	for _, id := range []string{loopID, pong["id"].(string)} {
		alice.must("PATCH", "/api/w/rules/"+id, map[string]any{"enabled": false}, 200)
	}

	// ---- the rate limit and once-per-record -------------------------------------------------
	brake := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Stage moves (braked)", "trigger": "deal.stage_changed", "enabled": true, "max_runs_per_hour": 1,
		"actions": []any{map[string]any{"type": "add_note", "params": map[string]any{"body": "moved to {{stage}}"}}},
	}, 201)["rule"])
	once := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Stage moves (once)", "trigger": "deal.stage_changed", "enabled": true, "once_per_subject": true,
		"actions": []any{map[string]any{"type": "add_note", "params": map[string]any{"body": "first move: {{stage}}"}}},
	}, 201)["rule"])
	stages := arr(alice.must("GET", "/api/w/pipeline/stages", nil, 200)["stages"])
	d2 := obj(alice.must("POST", "/api/w/deals", map[string]any{"title": "Mover"}, 201)["deal"])["id"].(string)
	alice.must("PATCH", "/api/w/deals/"+d2, map[string]any{"stage_id": obj(stages[1])["id"]}, 200)
	alice.must("PATCH", "/api/w/deals/"+d2, map[string]any{"stage_id": obj(stages[2])["id"]}, 200)
	process()
	br := runsOf(brake["id"].(string))
	if len(br) != 2 || br[0]["status"] != "rate_limited" || br[1]["status"] != "ok" {
		t.Fatalf("braked rule: %v", br)
	}
	on := runsOf(once["id"].(string))
	if len(on) != 2 || on[0]["status"] != "skipped" || !strings.Contains(on[0]["reason"].(string), "once per record") {
		t.Fatalf("once-per-record rule: %v", on)
	}

	// ---- consent and what a rule may not do ------------------------------------------------
	sink := startRuleSMTPSink(t)
	alice.must("POST", "/api/w/smtp-services", map[string]any{"name": "Sink", "host": "127.0.0.1", "port": sink.port,
		"from_email": "rules@mail.example", "tls_mode": "none"}, 201)
	stranger := obj(alice.must("POST", "/api/w/contacts", map[string]any{"email": "nowa-" + rnd() + "@rules.test", "name": "No WA"}, 201)["contact"])["id"].(string)
	consent := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Consent checks", "trigger": "contact.lifecycle_changed", "enabled": true,
		"actions": []any{
			map[string]any{"type": "send_email", "params": map[string]any{"to": "someone-" + rnd() + "@elsewhere.test", "subject": "Hi", "body": "Hello"}},
			map[string]any{"type": "whatsapp_contact", "params": map[string]any{"text": "Hello on WhatsApp"}},
			map[string]any{"type": "send_email", "params": map[string]any{"to": "{{email}}", "subject": "You are {{to_stage}} now", "body": "Welcome, {{name}}"}},
		},
	}, 201)["rule"])
	alice.must("PATCH", "/api/w/contacts/"+stranger, map[string]any{"lifecycle": "qualified"}, 200)
	process()
	cr := runsOf(consent["id"].(string))[0]
	ca := arr(cr["actions"])
	if cr["status"] != "partial" ||
		obj(ca[0])["status"] != "failed" || !strings.Contains(obj(ca[0])["error"].(string), "neither") ||
		obj(ca[1])["status"] != "failed" || !strings.Contains(obj(ca[1])["error"].(string), "opted in") ||
		obj(ca[2])["status"] != "ok" {
		t.Fatalf("consent run: %v", cr)
	}
	if m := sink.mails(); len(m) != 1 || !strings.Contains(m[0], "You are qualified now") {
		t.Fatalf("the one allowed email: %q", m)
	}
	alice.must("PATCH", "/api/w/rules/"+consent["id"].(string), map[string]any{"enabled": false}, 200)

	// A member (not a manager) reads rules but cannot write, switch or test them.
	bob := newClient(t, sf.srv.URL)
	bobEmail := "bob-" + rnd() + "@example.test"
	bob.must("POST", "/api/auth/register", map[string]string{"email": bobEmail, "password": "correct-horse-8"}, 201)
	if _, err := a.SysDB.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) SELECT $1, id, 'member' FROM users WHERE email = $2`, ws, bobEmail); err != nil {
		t.Fatal(err)
	}
	bob.ws = ws
	bob.must("GET", "/api/w/rules", nil, 200)
	newRule := map[string]any{"name": "x", "trigger": "lead.created", "actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "t"}}}}
	for _, try := range []struct {
		m, p string
		body any
	}{
		{"POST", "/api/w/rules", newRule},
		{"PATCH", "/api/w/rules/" + leadRuleID, map[string]any{"enabled": false}},
		{"PUT", "/api/w/rules/" + leadRuleID, newRule},
		{"POST", "/api/w/rules/" + leadRuleID + "/test", map[string]any{}},
		{"GET", "/api/w/rules/" + leadRuleID + "/secret", nil},
		{"DELETE", "/api/w/rules/" + leadRuleID, nil},
	} {
		if code, b := bob.do(try.m, try.p, try.body); code != 403 {
			t.Errorf("a member %s %s: %d %s", try.m, try.p, code, b)
		}
	}

	// ---- the signed webhook ---------------------------------------------------------------
	type delivery struct {
		sig, stamp string
		body       []byte
	}
	hooks := make(chan delivery, 4)
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hooks <- delivery{r.Header.Get("X-Moharrik-Signature"), r.Header.Get("X-Moharrik-Timestamp"), b}
		w.WriteHeader(204)
	}))
	defer recv.Close()
	hookRule := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Won → webhook", "trigger": "deal.won", "enabled": true,
		"actions": []any{map[string]any{"type": "webhook", "params": map[string]any{"url": recv.URL + "/moharrik"}}},
	}, 201)["rule"])
	hookID := hookRule["id"].(string)
	won := ""
	for _, st := range stages {
		if obj(st)["kind"] == "won" {
			won = obj(st)["id"].(string)
		}
	}
	d3 := obj(alice.must("POST", "/api/w/deals", map[string]any{"title": "Big one"}, 201)["deal"])["id"].(string)
	alice.must("PATCH", "/api/w/deals/"+d3, map[string]any{"stage_id": won}, 200)
	// Private addresses are refused unless allowed.
	rules.AllowPrivate = false
	process()
	if r := runsOf(hookID)[0]; r["status"] != "failed" || !strings.Contains(obj(arr(r["actions"])[0])["error"].(string), "private network") {
		t.Fatalf("a private webhook address should be refused: %v", r)
	}
	rules.AllowPrivate = true
	defer func() { rules.AllowPrivate = false }()
	d4 := obj(alice.must("POST", "/api/w/deals", map[string]any{"title": "Bigger one"}, 201)["deal"])["id"].(string)
	alice.must("PATCH", "/api/w/deals/"+d4, map[string]any{"stage_id": won}, 200)
	process()
	secret := alice.must("GET", "/api/w/rules/"+hookID+"/secret", nil, 200)["secret"].(string)
	select {
	case d := <-hooks:
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(d.stamp + "."))
		mac.Write(d.body)
		if d.sig != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			t.Fatalf("the webhook signature does not verify with the rule's secret")
		}
		var payload map[string]any
		_ = json.Unmarshal(d.body, &payload)
		if payload["event"] != "deal.won" || obj(payload["data"])["deal_id"] != d4 {
			t.Fatalf("webhook payload: %s", d.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the webhook was not called")
	}

	// ---- the schedule trigger ---------------------------------------------------------------
	sched := obj(alice.must("POST", "/api/w/rules", map[string]any{
		"name": "Every morning", "trigger": "schedule", "cron": "0 9 * * *", "timezone": "UTC", "enabled": true,
		"actions": []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "Good morning, it is {{weekday}}"}}},
	}, 201)["rule"])
	next, _ := time.Parse(time.RFC3339, sched["next_run_at"].(string))
	rules.Clock = func() time.Time { return next.Add(time.Minute) }
	defer func() { rules.Clock = time.Now }()
	rules.TickSchedules(ctx, a)
	process()
	sr := runsOf(sched["id"].(string))
	if len(sr) != 1 || sr[0]["status"] != "ok" || !strings.Contains(obj(arr(sr[0]["actions"])[0])["summary"].(string), "Good morning, it is "+next.Weekday().String()) {
		t.Fatalf("schedule run: %v", sr)
	}
	rules.TickSchedules(ctx, a) // not due again until tomorrow
	if n := process(); n != 0 {
		t.Fatalf("a schedule rule fired twice for one due time")
	}

	// ---- the MCP tools -----------------------------------------------------------------------
	agent, _ := mint(t, alice, "rules agent", []string{"automation:read", "automation:run"})
	listed := mcpJSON(t, agent, "list_rules", nil)
	if len(arr(listed["rules"])) < 8 {
		t.Fatalf("list_rules: %v", listed)
	}
	created := obj(mcpJSON(t, agent, "create_rule", map[string]any{
		"name": "Agent's rule", "trigger": "trend.topic",
		"conditions": []any{map[string]any{"field": "score", "op": "gte", "value": "60"}},
		"actions":    []any{map[string]any{"type": "notify_members", "params": map[string]any{"title": "Rising: {{title}}"}}},
	})["rule"])
	agentRule := created["id"].(string)
	if created["enabled"] != false {
		t.Fatalf("create_rule should leave it off: %v", created)
	}
	tr := obj(mcpJSON(t, agent, "test_rule", map[string]any{"id": agentRule, "event": map[string]any{"score": 75, "title": "Agents everywhere"}})["run"])
	if tr["status"] != "ok" || !strings.Contains(obj(arr(tr["actions"])[0])["summary"].(string), "Rising: Agents everywhere") {
		t.Fatalf("test_rule: %v", tr)
	}
	low := obj(mcpJSON(t, agent, "test_rule", map[string]any{"id": agentRule, "event": map[string]any{"score": 12}})["run"])
	if low["status"] != "skipped" {
		t.Fatalf("test_rule below the threshold: %v", low)
	}
	upd := obj(mcpJSON(t, agent, "update_rule", map[string]any{"id": agentRule, "enabled": true})["rule"])
	if upd["enabled"] != true {
		t.Fatalf("update_rule enabled: %v", upd)
	}
	upd = obj(mcpJSON(t, agent, "update_rule", map[string]any{"id": agentRule, "name": "Agent's rule (renamed)", "once_per_subject": true})["rule"])
	if upd["name"] != "Agent's rule (renamed)" || upd["once_per_subject"] != true || upd["enabled"] != true || len(arr(upd["conditions"])) != 1 {
		t.Fatalf("update_rule definition: %v", upd)
	}
	lr := mcpJSON(t, agent, "list_rule_runs", map[string]any{"rule_id": agentRule})
	if len(arr(lr["runs"])) != 2 {
		t.Fatalf("list_rule_runs: %v", lr)
	}
	if !strings.Contains(mcpRefused(t, agent, "create_rule", map[string]any{"name": "bad", "trigger": "nope", "actions": []any{}}), "not an event") {
		t.Error("create_rule with an unknown trigger")
	}
	reader, _ := mint(t, alice, "rules reader", []string{"automation:read"})
	if !strings.Contains(mcpRefused(t, reader, "create_rule", map[string]any{"name": "x"}), "automation:run") {
		t.Error("a read-only token created a rule")
	}
	// A trend story crossing 60 fires the agent's rule once.
	var storyID string
	if err := a.SysDB.QueryRow(`INSERT INTO trend_clusters (workspace_id, title, score) VALUES ($1, 'Agents everywhere', 42) RETURNING id`, ws).Scan(&storyID); err != nil {
		t.Fatal(err)
	}
	for _, s := range []float64{55, 64, 68, 71} {
		if _, err := a.SysDB.Exec(`UPDATE trend_clusters SET score = $3 WHERE workspace_id = $1 AND id = $2`, ws, storyID, s); err != nil {
			t.Fatal(err)
		}
	}
	process()
	trs := runsOf(agentRule)
	st := map[string]int{}
	for _, r := range trs {
		if r["dry_run"] == false {
			st[r["status"].(string)]++
		}
	}
	if st["ok"] != 1 {
		t.Fatalf("the trend rule should run once for the story: %v", st)
	}

	// Deleting a rule takes its log with it.
	alice.must("DELETE", "/api/w/rules/"+agentRule, nil, 204)
	if code, _ := alice.do("GET", "/api/w/rules/"+agentRule, nil); code != 404 {
		t.Fatalf("a deleted rule: %d", code)
	}
	if n := count(`SELECT count(*) FROM automation_rule_runs WHERE workspace_id = $1 AND rule_id = $2`, ws, agentRule); n != 0 {
		t.Fatalf("a deleted rule's runs stayed: %d", n)
	}
}
