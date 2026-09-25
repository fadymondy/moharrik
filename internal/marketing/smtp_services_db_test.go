package marketing_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/mcp"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Several SMTP services per workspace (MH-412), through the real routes on a
real database, against two fake mail servers that log what they were given.

What these pin down:
  - a campaign goes out through the service it names while a confirmation
    email, sent at the same time, goes through the default — each provider's
    own log shows only its own mail, and the AUTH it was given;
  - a workspace's legacy smtp_settings row becomes its default service, once,
    and a deleted service is not brought back by the next migrate;
  - no password (plain or sealed) and, over MCP, no host, username or sender,
    appears in any response.
*/

// authSink is a fake SMTP server that offers AUTH PLAIN and logs, per
// message, who authenticated, the envelope and the data.
type authSink struct {
	port int
	mu   sync.Mutex
	got  []authMail
}

type authMail struct {
	User, From, To, Data string
}

func startAuthSink(t *testing.T) *authSink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &authSink{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *authSink) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	say("220 sink")
	var m authMail
	user := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		raw := strings.TrimSpace(line)
		cmd := strings.ToUpper(raw)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			say("250-sink")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			parts := strings.Fields(raw)
			if len(parts) == 3 {
				if dec, err := base64.StdEncoding.DecodeString(parts[2]); err == nil {
					f := strings.Split(string(dec), "\x00")
					if len(f) == 3 {
						user = f[1] + ":" + f[2]
					}
				}
			}
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m.From = strings.Trim(raw[10:], "<> ")
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.To = strings.Trim(raw[8:], "<> ")
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
			m.Data, m.User = b.String(), user
			s.mu.Lock()
			s.got = append(s.got, m)
			s.mu.Unlock()
			m = authMail{}
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (s *authSink) mails() []authMail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authMail(nil), s.got...)
}

func (s *authSink) waitFor(t *testing.T, n int) []authMail {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := s.mails(); len(got) >= n {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the sink got %d mail(s), want %d", len(s.mails()), n)
	return nil
}

func (f *editFixture) addService(t *testing.T, name string, port int, user, pass, from string) marketing.SMTPService {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/smtp-services", map[string]any{
		"name": name, "host": "127.0.0.1", "port": port, "username": user, "password": pass,
		"from_email": from, "from_name": name, "tls_mode": "none",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("add %s: %d %s", name, res.Code, res.Body)
	}
	var out struct {
		Service marketing.SMTPService `json:"service"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return out.Service
}

func TestSMTPCampaignThroughBWhileConfirmationThroughA(t *testing.T) {
	f := newEditFixture(t)
	sinkA, sinkB := startAuthSink(t), startAuthSink(t)
	svcA := f.addService(t, "Transactional A", sinkA.port, "user-a", "pass-a-SECRET", "hello@acme.test")
	svcB := f.addService(t, "Bulk B", sinkB.port, "user-b", "pass-b-SECRET", "news@acme.test")
	if !svcA.IsDefault || !svcA.IsCampaignDefault || svcB.IsDefault || svcB.IsCampaignDefault {
		t.Fatalf("the first service takes both roles, the second none: A=%+v B=%+v", svcA, svcB)
	}

	// Two subscribers on the (no double opt-in) list, and a campaign naming B.
	for _, e := range []string{"one@mail.example", "two@mail.example"} {
		if res := f.api.PostCtx(f.ctx, "/api/w/contacts", map[string]any{"email": e, "list_id": f.list}); res.Code != http.StatusCreated {
			t.Fatalf("add contact: %d %s", res.Code, res.Body)
		}
	}
	res := f.api.PostCtx(f.ctx, "/api/w/campaigns", map[string]any{
		"list_id": f.list, "subject": "Bulk news", "body": "Hello", "smtp_service_id": svcB.ID,
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var created struct {
		Campaign marketing.CampaignOut `json:"campaign"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Campaign.SMTPServiceID == nil || *created.Campaign.SMTPServiceID != svcB.ID || created.Campaign.SMTPServiceName != "Bulk B" {
		t.Fatalf("the campaign should name B: %+v", created.Campaign)
	}
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+created.Campaign.ID+"/send", map[string]any{}); res.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", res.Code, res.Body)
	}

	// Meanwhile somebody subscribes to a double-opt-in list: a confirmation.
	sctx := tenant.WithWorkspace(context.Background(), f.ws)
	var doiList string
	if err := f.a.SQLDB.QueryRowContext(sctx, `INSERT INTO mail_lists (workspace_id, name, double_opt_in) VALUES ($1, 'Confirmed', true) RETURNING id`, f.ws).Scan(&doiList); err != nil {
		t.Fatal(err)
	}
	res = f.api.PostCtx(f.ctx, "/api/w/contacts", map[string]any{"email": "new@mail.example", "list_id": doiList})
	if res.Code != http.StatusCreated || !strings.Contains(res.Body.String(), `"confirmation":"sent"`) {
		t.Fatalf("subscribe with confirmation: %d %s", res.Code, res.Body)
	}
	f.waitDone(t, created.Campaign.ID)

	gotB := sinkB.waitFor(t, 2)
	gotA := sinkA.waitFor(t, 1)
	if len(gotA) != 1 || len(gotB) != 2 {
		t.Fatalf("A got %d, B got %d; want 1 and 2", len(gotA), len(gotB))
	}
	for _, m := range gotB {
		if m.User != "user-b:pass-b-SECRET" || m.From != "news@acme.test" || !strings.Contains(m.Data, "Subject: Bulk news") {
			t.Errorf("B's log: %+v", m)
		}
	}
	if m := gotA[0]; m.User != "user-a:pass-a-SECRET" || m.From != "hello@acme.test" || m.To != "new@mail.example" || !strings.Contains(m.Data, "Confirm") {
		t.Errorf("A's log should hold only the confirmation: %+v", m)
	}

	// A test of the campaign goes through the campaign's service too.
	c2 := f.create(t, "email", "Second", "Body", "en")
	if res := f.api.PutCtx(f.ctx, "/api/w/campaigns/"+c2, map[string]any{
		"list_id": f.list, "subject": "Second", "body": "Body", "smtp_service_id": svcB.ID,
	}); res.Code != http.StatusOK {
		t.Fatalf("edit: %d %s", res.Code, res.Body)
	}
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+c2+"/test", map[string]any{"to": "me@mail.example"}); res.Code != http.StatusOK {
		t.Fatalf("test: %d %s", res.Code, res.Body)
	}
	if got := sinkB.waitFor(t, 3); !strings.Contains(got[2].Data, "[TEST] Second") {
		t.Errorf("the campaign test should go through B: %+v", got[2])
	}
	if len(sinkA.mails()) != 1 {
		t.Errorf("A received a campaign test")
	}

	// Making B the campaign default moves a campaign that names none.
	if res := f.api.PostCtx(f.ctx, "/api/w/smtp-services/"+svcB.ID+"/default", map[string]any{"role": "campaign"}); res.Code != http.StatusOK {
		t.Fatalf("default: %d %s", res.Code, res.Body)
	}
	c3 := f.create(t, "email", "Third", "Body", "en")
	svc, err := marketing.LoadCampaignSMTP(f.ctx, f.a.SQLDB, f.ws, c3)
	if err != nil || svc.ServiceID != svcB.ID {
		t.Fatalf("a campaign naming no service should follow the campaign default (B): %v %+v", err, svc)
	}
	if d, _ := marketing.LoadSMTP(f.ctx, f.a.SQLDB, f.ws); d == nil || d.ServiceID != svcA.ID {
		t.Fatal("transactional mail must stay on A")
	}

	// The default cannot go while another service remains; B can, and the
	// campaign that named it returns to the campaign default.
	if res := f.api.DeleteCtx(f.ctx, "/api/w/smtp-services/"+svcA.ID); res.Code != http.StatusConflict {
		t.Fatalf("deleting the default with others left: %d %s", res.Code, res.Body)
	}
	if res := f.api.DeleteCtx(f.ctx, "/api/w/smtp-services/"+svcB.ID); res.Code != http.StatusNoContent {
		t.Fatalf("delete B: %d %s", res.Code, res.Body)
	}
	svc, err = marketing.LoadCampaignSMTP(f.ctx, f.a.SQLDB, f.ws, c2)
	if err != nil || svc.ServiceID != svcA.ID {
		t.Fatalf("a campaign whose service was deleted falls back to the default: %v %+v", err, svc)
	}
}

func TestSMTPNoCredentialInAnyResponse(t *testing.T) {
	f := newEditFixture(t)
	sink := startAuthSink(t)
	const pass = "PW-never-returned-7c1e"
	const user = "smtp-user-9d2f"
	svc := f.addService(t, "Only", sink.port, user, pass, "hi@acme.test")
	var sealed string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `SELECT password_sealed FROM smtp_services WHERE workspace_id = $1 AND id = $2`, f.ws, svc.ID).Scan(&sealed); err != nil || sealed == "" || strings.Contains(sealed, pass) {
		t.Fatalf("the password must be stored sealed: %v %q", err, sealed)
	}

	var bodies []string
	keep := func(code int, b string, want int, what string) {
		t.Helper()
		if code != want {
			t.Fatalf("%s: %d %s", what, code, b)
		}
		bodies = append(bodies, b)
	}
	r := f.api.GetCtx(f.ctx, "/api/w/smtp-services")
	keep(r.Code, r.Body.String(), 200, "list")
	r = f.api.GetCtx(f.ctx, "/api/w/smtp-services/options")
	keep(r.Code, r.Body.String(), 200, "options")
	r = f.api.PutCtx(f.ctx, "/api/w/smtp-services/"+svc.ID, map[string]any{
		"name": "Only", "host": "127.0.0.1", "port": sink.port, "username": user, "from_email": "hi@acme.test", "tls_mode": "none",
	})
	keep(r.Code, r.Body.String(), 200, "update keeping the password")
	r = f.api.PostCtx(f.ctx, "/api/w/smtp-services/"+svc.ID+"/test", map[string]any{"to": "me@mail.example"})
	keep(r.Code, r.Body.String(), 200, "test")
	r = f.api.PostCtx(f.ctx, "/api/w/smtp-services/"+svc.ID+"/default", map[string]any{"role": "transactional"})
	keep(r.Code, r.Body.String(), 200, "default")
	r = f.api.GetCtx(f.ctx, "/api/w/mail/smtp")
	keep(r.Code, r.Body.String(), 200, "legacy get")
	r = f.api.GetCtx(f.ctx, "/api/w/campaigns")
	keep(r.Code, r.Body.String(), 200, "campaigns")

	// The kept password still authenticates: the update did not clear it.
	if got := sink.waitFor(t, 1); got[0].User != user+":"+pass {
		t.Fatalf("the stored password was not kept: %+v", got[0])
	}
	r = f.api.GetCtx(f.ctx, "/api/w/smtp-services")
	keep(r.Code, r.Body.String(), 200, "list after the test")
	if last := bodies[len(bodies)-1]; !strings.Contains(last, `"has_password":true`) || !strings.Contains(last, `"last_test_ok":true`) {
		t.Errorf("the list should say a password is stored and the test worked: %s", last)
	}

	// Over MCP: names and status only.
	var tool *mcp.Tool
	for _, tl := range mcp.MarketingTools() {
		if tl.Name == "list_smtp_services" {
			tl := tl
			tool = &tl
		}
	}
	if tool == nil {
		t.Fatal("list_smtp_services is missing")
	}
	text, err := tool.Run(f.ctx, f.a, json.RawMessage(`{}`))
	var listOut struct {
		Count int `json:"count"`
		Items []struct {
			Name       string `json:"name"`
			LastTestOK bool   `json:"last_test_ok"`
		} `json:"items"`
	}
	if jsonErr := json.Unmarshal([]byte(text), &listOut); err != nil || jsonErr != nil || listOut.Count != 1 ||
		listOut.Items[0].Name != "Only" || !listOut.Items[0].LastTestOK {
		t.Fatalf("list_smtp_services: %v %v %s", err, jsonErr, text)
	}
	for _, secret := range []string{user, "127.0.0.1", "hi@acme.test"} {
		if strings.Contains(text, secret) {
			t.Errorf("list_smtp_services leaked %q: %s", secret, text)
		}
	}
	bodies = append(bodies, text)

	for _, b := range bodies {
		if strings.Contains(b, pass) || strings.Contains(b, sealed) || strings.Contains(b, "password_sealed") {
			t.Errorf("a response carried the password: %s", b)
		}
	}
}

func TestSMTPLegacyRowBecomesTheDefaultOnce(t *testing.T) {
	f := newEditFixture(t)
	sink := startAuthSink(t)
	// A workspace as it was before MH-412: one smtp_settings row, written the
	// way the old PUT wrote it (sealed password).
	if res := f.api.PutCtx(f.ctx, "/api/w/mail/smtp", map[string]any{
		"host": "127.0.0.1", "port": sink.port, "username": "legacy", "password": "legacy-pass", "from_email": "old@acme.test", "tls_mode": "none",
	}); res.Code != 200 {
		t.Fatalf("legacy put: %d %s", res.Code, res.Body)
	}
	var sealed string
	if err := f.a.SysDB.QueryRow(`SELECT password_sealed FROM smtp_services WHERE workspace_id = $1`, f.ws).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	// Move it into the legacy table, as a pre-migration database holds it.
	for _, q := range []string{
		`DELETE FROM smtp_services WHERE workspace_id = $1`,
		`INSERT INTO smtp_settings (workspace_id, host, port, username, password_sealed, from_name, from_email, tls_mode)
		 VALUES ($1, '127.0.0.1', ` + itoa(sink.port) + `, 'legacy', '` + sealed + `', 'Old', 'old@acme.test', 'none')`,
	} {
		if _, err := f.a.SysDB.Exec(q, f.ws); err != nil {
			t.Fatal(err)
		}
	}
	post, err := os.ReadFile("../db/post/115_smtp_services.sql")
	if err != nil {
		t.Fatal(err)
	}
	migrate := func() {
		t.Helper()
		if _, err := f.a.SysDB.Exec(string(post)); err != nil {
			t.Fatal(err)
		}
	}
	migrate()
	migrate() // idempotent: still one row

	var n int
	var isDef, isCamp bool
	var name string
	if err := f.a.SysDB.QueryRow(`SELECT count(*), bool_and(is_default), bool_and(is_campaign_default), min(name) FROM smtp_services WHERE workspace_id = $1`, f.ws).Scan(&n, &isDef, &isCamp, &name); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !isDef || !isCamp || name != "Default" {
		t.Fatalf("the legacy row should become one Default service with both roles: n=%d def=%v camp=%v name=%q", n, isDef, isCamp, name)
	}

	// Everything that sent mail before still does, through the same server.
	if res := f.api.PostCtx(f.ctx, "/api/w/mail/smtp/test", map[string]any{"to": "me@mail.example"}); res.Code != 200 {
		t.Fatalf("legacy test after migration: %d %s", res.Code, res.Body)
	}
	if got := sink.waitFor(t, 1); got[0].User != "legacy:legacy-pass" || got[0].From != "old@acme.test" {
		t.Fatalf("the migrated service should send as before: %+v", got[0])
	}

	// Deleted by its workspace, it stays deleted across the next migrate.
	if res := f.api.DeleteCtx(f.ctx, "/api/w/mail/smtp"); res.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", res.Code, res.Body)
	}
	migrate()
	if err := f.a.SysDB.QueryRow(`SELECT count(*) FROM smtp_services WHERE workspace_id = $1`, f.ws).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a deleted service came back on migrate: %d %v", n, err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
