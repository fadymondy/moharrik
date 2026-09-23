package marketing_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/humatest"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Edit, preview and test send (MH-407), through the real routes on a real
database. What these pin down:

  - an edit is refused (409) the moment sending has started, and allowed for a
    draft or a failed campaign waiting to be retried;
  - the preview is the send path's own rendering, in the campaign's language,
    right-to-left for Arabic, and the WhatsApp preview carries the stop line;
  - a test send changes nothing on the campaign, and is rate limited.
*/

// ---- fixture -------------------------------------------------------------

type editFixture struct {
	a     *app.App
	api   humatest.TestAPI
	ctx   context.Context
	ws    string
	list  string
	email string
}

func newEditFixture(t *testing.T) *editFixture {
	t.Helper()
	a := testApp(t)
	wsID, listID := listIn(t, a, false)
	f := &editFixture{a: a, ws: wsID, list: listID, email: "owner-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.test"}
	f.ctx = tenant.With(context.Background(), &tenant.Scope{
		WorkspaceID: wsID, UserID: "u-test", Email: f.email, Role: "owner", Via: "session",
	})
	_, f.api = humatest.New(t)
	marketing.Register(f.api, a)
	// Every test starts with a clean test-send allowance.
	t.Cleanup(marketing.SetTestLimit(marketing.DefaultTestLimit))
	return f
}

func (f *editFixture) create(t *testing.T, channel, subject, body, locale string) string {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/campaigns", map[string]any{
		"list_id": f.list, "channel": channel, "subject": subject, "body": body, "locale": locale,
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var out struct {
		Campaign marketing.CampaignOut `json:"campaign"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Campaign.ID
}

func (f *editFixture) setStatus(t *testing.T, id, status string) {
	t.Helper()
	if _, err := f.a.SQLDB.ExecContext(f.ctx,
		`UPDATE campaigns SET status = $3 WHERE workspace_id = $1 AND id = $2`, f.ws, id, status); err != nil {
		t.Fatal(err)
	}
}

// row is every column a send writes. A test send must leave all of them alone.
type row struct {
	Status                        string
	Recipients, Sent, Failed      int64
	LastError                     string
	SentAt, UpdatedAt             string
	Subject, Body, Locale, Channel string
}

func (f *editFixture) row(t *testing.T, id string) row {
	t.Helper()
	var r row
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		SELECT status, recipient_count, sent_count, failed_count, last_error, coalesce(sent_at::text, ''), updated_at::text, subject, body, locale, channel
		FROM campaigns WHERE workspace_id = $1 AND id = $2`, f.ws, id).Scan(
		&r.Status, &r.Recipients, &r.Sent, &r.Failed, &r.LastError, &r.SentAt, &r.UpdatedAt,
		&r.Subject, &r.Body, &r.Locale, &r.Channel); err != nil {
		t.Fatal(err)
	}
	return r
}

// withSink saves an SMTP service pointing at a local sink and returns it.
func (f *editFixture) withSink(t *testing.T) *sink {
	t.Helper()
	s := startSink(t)
	res := f.api.PutCtx(f.ctx, "/api/w/mail/smtp", map[string]any{
		"host": "127.0.0.1", "port": s.port, "from_name": "Acme", "from_email": "news@acme.test", "tls_mode": "none",
	})
	if res.Code != http.StatusOK {
		t.Fatalf("save smtp: %d %s", res.Code, res.Body)
	}
	return s
}

// ---- a throwaway SMTP server ---------------------------------------------

type sinkMail struct {
	To   string
	Data string
}

type sink struct {
	port int
	mu   sync.Mutex
	got  []sinkMail
}

func startSink(t *testing.T) *sink {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &sink{port: ln.Addr().(*net.TCPAddr).Port}
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

func (s *sink) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(line string) { _, _ = c.Write([]byte(line + "\r\n")) }
	say("220 sink")
	var m sinkMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 sink")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.To = strings.Trim(strings.TrimSpace(line)[8:], "<> ")
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
			m.Data = b.String()
			s.mu.Lock()
			s.got = append(s.got, m)
			s.mu.Unlock()
			m = sinkMail{}
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (s *sink) mails() []sinkMail {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sinkMail(nil), s.got...)
}

// htmlPart pulls the HTML alternative out of a delivered message.
func htmlPart(t *testing.T, data string) string {
	t.Helper()
	data = strings.ReplaceAll(data, "\r\n", "\n")
	i := strings.Index(data, "Content-Type: text/html; charset=utf-8\n\n")
	if i < 0 {
		t.Fatalf("no HTML part in:\n%s", data)
	}
	rest := data[i+len("Content-Type: text/html; charset=utf-8\n\n"):]
	j := strings.Index(rest, "\n--mhr-alt-")
	if j < 0 {
		t.Fatal("unterminated HTML part")
	}
	return rest[:j]
}

// ---- edit ------------------------------------------------------------------

func TestCampaignEditRefusedOnceSending(t *testing.T) {
	f := newEditFixture(t)
	id := f.create(t, "email", "Septmber update", "Helo", "en")

	put := func(id string, body map[string]any) (int, string) {
		res := f.api.PutCtx(f.ctx, "/api/w/campaigns/"+id, body)
		return res.Code, res.Body.String()
	}
	fix := map[string]any{"list_id": f.list, "channel": "email", "subject": "September update", "body": "Hello", "locale": "ar"}

	// A draft: the typo is fixed in place.
	if code, body := put(id, fix); code != http.StatusOK {
		t.Fatalf("editing a draft: %d %s", code, body)
	}
	r := f.row(t, id)
	if r.Subject != "September update" || r.Body != "Hello" || r.Locale != "ar" || r.Status != marketing.CampaignDraft {
		t.Fatalf("edit did not land: %+v", r)
	}

	// A failed campaign is retryable (StartSend claims draft|failed), so it can be fixed first.
	f.setStatus(t, id, marketing.CampaignFailed)
	if code, body := put(id, fix); code != http.StatusOK {
		t.Fatalf("editing a failed campaign: %d %s", code, body)
	}

	// Once sending has started, what the list gets is fixed.
	for _, st := range []string{marketing.CampaignSending, marketing.CampaignSent} {
		f.setStatus(t, id, st)
		code, body := put(id, map[string]any{"list_id": f.list, "subject": "Changed mid-send", "body": "x"})
		if code != http.StatusConflict {
			t.Fatalf("editing a %s campaign: %d %s, want 409", st, code, body)
		}
		if !strings.Contains(body, "can no longer be changed") {
			t.Fatalf("the refusal should say why in plain words: %s", body)
		}
		if got := f.row(t, id).Subject; got != "September update" {
			t.Fatalf("a refused edit changed the subject to %q", got)
		}
	}

	// Unknown campaign, and a list from nowhere.
	if code, _ := put("00000000-0000-0000-0000-000000000000", fix); code != http.StatusNotFound {
		t.Fatalf("unknown campaign: %d, want 404", code)
	}
	id2 := f.create(t, "email", "Other", "x", "en")
	if code, _ := put(id2, map[string]any{"list_id": "no-such-list", "subject": "Other", "body": "x"}); code != http.StatusNotFound {
		t.Fatalf("unknown list: %d, want 404", code)
	}
}

func TestCampaignEditRaceWithSendClaim(t *testing.T) {
	// The edit and the send claim are both single UPDATEs guarded by the same
	// status predicate: after StartSend has claimed the row, an edit loses.
	f := newEditFixture(t)
	f.withSink(t)
	id := f.create(t, "email", "Race", "body", "en")
	svc, err := marketing.LoadSMTP(f.ctx, f.a.SQLDB, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := marketing.StartSend(f.ctx, f.a, f.ws, id, svc); err != nil {
		t.Fatal(err)
	}
	err = marketing.UpdateCampaign(f.ctx, f.a.SQLDB, f.ws, id, marketing.CampaignInput{ListID: f.list, Subject: "Late", Body: "late"})
	var locked *marketing.LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("edit after the claim: %v, want a LockedError", err)
	}
	f.waitDone(t, id)
}

// waitDone lets a background send finish before the database goes away.
func (f *editFixture) waitDone(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if f.row(t, id).Status != marketing.CampaignSending {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the send never finished")
}

// ---- preview ---------------------------------------------------------------

func (f *editFixture) preview(t *testing.T, body map[string]any) marketing.Preview {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/campaigns/preview", body)
	if res.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", res.Code, res.Body)
	}
	var p marketing.Preview
	if err := json.Unmarshal(res.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCampaignPreviewEnglishAndArabic(t *testing.T) {
	f := newEditFixture(t)

	en := f.preview(t, map[string]any{"channel": "email", "subject": "Hello", "body": "# Hi\n\nWe shipped **editing**.", "locale": "en"})
	if en.Dir != "ltr" || en.Subject != "Hello" {
		t.Fatalf("en preview: dir %q subject %q", en.Dir, en.Subject)
	}
	for _, want := range []string{`<html dir="ltr" lang="en">`, `<strong style="font-weight:600;">editing</strong>`, ">Unsubscribe</a>", "token=preview"} {
		if !strings.Contains(en.HTML, want) {
			t.Errorf("en HTML is missing %q", want)
		}
	}
	if !strings.Contains(en.Text, "Unsubscribe: ") {
		t.Errorf("en text part has no unsubscribe line: %q", en.Text)
	}

	ar := f.preview(t, map[string]any{"channel": "email", "subject": "مرحبا", "body": "أطلقنا **التحرير**.", "locale": "ar"})
	if ar.Dir != "rtl" {
		t.Fatalf("ar preview dir = %q, want rtl", ar.Dir)
	}
	for _, want := range []string{`<html dir="rtl" lang="ar">`, "text-align:right", "إلغاء الاشتراك", "IBM Plex Sans Arabic", "مرحبا"} {
		if !strings.Contains(ar.HTML, want) {
			t.Errorf("ar HTML is missing %q", want)
		}
	}
}

func TestCampaignPreviewIsWhatTheSendDelivers(t *testing.T) {
	// Not "looks like": the preview HTML and the delivered HTML differ only in
	// the subscriber's unsubscribe token.
	f := newEditFixture(t)
	s := f.withSink(t)
	subj, body := "Launch", "# Hello\n\n- one\n- two\n\n[Read](https://moharrik.com)"
	id := f.create(t, "email", subj, body, "ar")

	res, err := marketing.Subscribe(f.ctx, f.a.SQLDB, f.ws, f.list, "reader@example.test", "", "ar", "form")
	if err != nil {
		t.Fatal(err)
	}
	var token string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `SELECT token FROM list_subscriptions WHERE workspace_id = $1 AND contact_id = $2`,
		f.ws, res.ContactID).Scan(&token); err != nil {
		t.Fatal(err)
	}

	svc, err := marketing.LoadSMTP(f.ctx, f.a.SQLDB, f.ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := marketing.StartSend(f.ctx, f.a, f.ws, id, svc); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(s.mails()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if len(s.mails()) == 0 {
		t.Fatal("the campaign was not delivered")
	}
	delivered := htmlPart(t, s.mails()[0].Data)
	f.waitDone(t, id)

	p := f.preview(t, map[string]any{"channel": "email", "subject": subj, "body": body, "locale": "ar"})
	if want := strings.ReplaceAll(delivered, token, "preview"); p.HTML != want {
		t.Fatalf("the preview is not what was delivered.\npreview:\n%s\n\ndelivered:\n%s", p.HTML, want)
	}
}

func TestWhatsAppPreviewCarriesTheStopLine(t *testing.T) {
	f := newEditFixture(t)
	en := f.preview(t, map[string]any{"channel": "whatsapp", "subject": "internal", "body": "Sale today.", "locale": "en"})
	if en.Text != "Sale today.\n\nTo stop these messages, reply STOP." || en.HTML != "" || en.Subject != "" {
		t.Fatalf("en WhatsApp preview = %+v", en)
	}
	ar := f.preview(t, map[string]any{"channel": "whatsapp", "body": "تخفيضات اليوم.", "locale": "ar"})
	if ar.Text != "تخفيضات اليوم.\n\nللإيقاف، أرسل: إيقاف" || ar.Dir != "rtl" {
		t.Fatalf("ar WhatsApp preview = %+v", ar)
	}
	// And the broadcast itself uses the same function.
	if marketing.WhatsAppText("ar", "تخفيضات اليوم.") != ar.Text {
		t.Fatal("the preview and the broadcast text disagree")
	}
}

// ---- test send -------------------------------------------------------------

func TestCampaignTestSendChangesNothing(t *testing.T) {
	f := newEditFixture(t)
	s := f.withSink(t)
	id := f.create(t, "email", "September update", "# Hi\n\nBody.", "en")
	before := f.row(t, id)

	// No address: it goes to the member.
	res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{})
	if res.Code != http.StatusOK {
		t.Fatalf("test send: %d %s", res.Code, res.Body)
	}
	var out marketing.TestResult
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if out.To != f.email || out.Subject != "[TEST] September update" {
		t.Fatalf("test result = %+v", out)
	}
	// A typed address.
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{"to": "colleague@example.test"}); res.Code != http.StatusOK {
		t.Fatalf("test send to a typed address: %d %s", res.Code, res.Body)
	}
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{"to": "not an address"}); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("test send to garbage: %d, want 422", res.Code)
	}

	mails := s.mails()
	if len(mails) != 2 || mails[0].To != f.email || mails[1].To != "colleague@example.test" {
		t.Fatalf("delivered: %+v", mails)
	}
	if !strings.Contains(mails[0].Data, "Subject: [TEST] September update\r\n") {
		t.Fatalf("the test subject is not prefixed:\n%s", mails[0].Data)
	}
	// The heading inside is the real subject, as the list will read it.
	if !strings.Contains(htmlPart(t, mails[0].Data), ">September update</h1>") {
		t.Fatal("the email heading should be the real subject")
	}

	if after := f.row(t, id); after != before {
		t.Fatalf("a test send changed the campaign:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestCampaignTestSendIsRateLimited(t *testing.T) {
	f := newEditFixture(t)
	f.withSink(t)
	t.Cleanup(marketing.SetTestLimit(marketing.Rate{Limit: 2, Window: time.Hour}))
	id := f.create(t, "email", "Limited", "x", "en")
	other := f.create(t, "email", "Other", "x", "en")

	for i := 0; i < 2; i++ {
		if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{}); res.Code != http.StatusOK {
			t.Fatalf("test %d: %d %s", i+1, res.Code, res.Body)
		}
	}
	res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{})
	if res.Code != http.StatusTooManyRequests {
		t.Fatalf("third test: %d, want 429", res.Code)
	}
	if ra, _ := strconv.Atoi(res.Header().Get("Retry-After")); ra < 3000 {
		t.Fatalf("Retry-After = %q, want about an hour", res.Header().Get("Retry-After"))
	}
	// The limit is per campaign.
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+other+"/test", map[string]any{}); res.Code != http.StatusOK {
		t.Fatalf("another campaign's test: %d %s", res.Code, res.Body)
	}
}

func TestCampaignTestSendRefusedOnceSending(t *testing.T) {
	f := newEditFixture(t)
	s := f.withSink(t)
	id := f.create(t, "email", "Gone", "x", "en")
	f.setStatus(t, id, marketing.CampaignSending)
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{}); res.Code != http.StatusConflict {
		t.Fatalf("test of a sending campaign: %d, want 409", res.Code)
	}
	if len(s.mails()) != 0 {
		t.Fatal("a refused test still sent mail")
	}
}

func TestWhatsAppTestSendGoesOnlyToTheLinkedNumber(t *testing.T) {
	f := newEditFixture(t)
	id := f.create(t, "whatsapp", "Promo", "Sale today.", "en")

	// Not linked: said plainly, nothing sent.
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{}); res.Code != http.StatusConflict {
		t.Fatalf("unlinked WhatsApp test: %d %s, want 409", res.Code, res.Body)
	}

	// A fake Evolution server.
	var mu sync.Mutex
	var sent []map[string]string
	evo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]string
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		sent = append(sent, m)
		mu.Unlock()
		_, _ = w.Write([]byte(`{"key":{"id":"TEST1"}}`))
	}))
	t.Cleanup(evo.Close)
	t.Setenv("EVOLUTION_API_URL", evo.URL)
	t.Setenv("EVOLUTION_API_KEY", "k")
	if _, err := whatsapp.Ensure(f.ctx, f.a, f.ws, "u-test"); err != nil {
		t.Fatal(err)
	}
	whatsapp.SetState(f.ctx, f.a, f.ws, "open", "201000000001@s.whatsapp.net")

	// Never to a typed number.
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{"to": "201999999999"}); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("WhatsApp test to a typed number: %d, want 422", res.Code)
	}

	before := f.row(t, id)
	res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/test", map[string]any{})
	if res.Code != http.StatusOK {
		t.Fatalf("WhatsApp test: %d %s", res.Code, res.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0]["number"] != "201000000001" || sent[0]["text"] != "Sale today.\n\nTo stop these messages, reply STOP." {
		t.Fatalf("Evolution received %+v", sent)
	}
	if after := f.row(t, id); after != before {
		t.Fatalf("a WhatsApp test changed the campaign:\nbefore %+v\nafter  %+v", before, after)
	}
}
