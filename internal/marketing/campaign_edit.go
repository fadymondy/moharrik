package marketing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Editing, previewing and test-sending a campaign before it goes out (MH-407).

Three rules shape this file:

 1. A campaign is editable exactly while it could still be CLAIMED for sending:
    draft, or failed (StartSend and StartWhatsAppSend retry a failed one). Once
    the claim has flipped it to sending, what the list receives is already
    fixed, so an edit is refused rather than silently changing half a send.
    The edit and the claim are single UPDATEs on the same row, so Postgres'
    row lock decides which one wins; there is no read-then-write gap.

 2. A preview is rendered by the SAME functions the send path uses
    (campaignEmail and WhatsAppText), not a lookalike. A preview that drifts
    from what is delivered is worse than no preview, because it is trusted.

 3. A test send is not a send. It writes nothing to the campaign — no status,
    no counters, no last_error — and it goes to the member themselves (or, for
    email, one address they type), never to the list. It is rate limited per
    campaign, because an endpoint that emails any typed address through the
    workspace's own SMTP is otherwise a relay.
*/

// claimable is the status predicate shared by the send claims and the edit: a
// campaign can be edited exactly while it could still be sent.
const claimable = `status IN ('draft', 'failed')`

// Editable reports whether a campaign in this status may still be changed.
func Editable(status string) bool {
	return status == CampaignDraft || status == CampaignFailed
}

// ErrCampaignNotFound is a campaign id this workspace does not have.
var ErrCampaignNotFound = errors.New("campaign not found")

// ErrListNotFound is a list id this workspace does not have.
var ErrListNotFound = errors.New("list not found")

// LockedError is an edit or test of a campaign that has already gone out.
type LockedError struct{ Status string }

func (e *LockedError) Error() string {
	switch e.Status {
	case CampaignSending:
		return "this campaign is already sending, so it can no longer be changed"
	case CampaignSent:
		return "this campaign has already been sent, so it can no longer be changed"
	}
	return "this campaign is " + e.Status + " and can no longer be changed"
}

// CampaignInput is everything that can be set on a draft.
type CampaignInput struct {
	ListID  string
	Channel string
	Subject string
	Body    string
	Locale  string
	// SMTPServiceID is the service an email campaign goes out through
	// (MH-412). nil = the campaign default.
	SMTPServiceID *string
	// AudienceLocale narrows the send to one language (MH-413); "" = all.
	AudienceLocale string
}

// UpdateCampaign rewrites a draft (or a failed, retryable) campaign.
func UpdateCampaign(ctx context.Context, db *sql.DB, wsID, id string, in CampaignInput) error {
	subject := strings.TrimSpace(in.Subject)
	if subject == "" || strings.TrimSpace(in.Body) == "" {
		return errors.New("a subject and a body are required")
	}
	res, err := db.ExecContext(ctx, `
		UPDATE campaigns c SET list_id = l.id, channel = $4, subject = $5, body = $6, locale = $7,
		  smtp_service_id = $8, audience_locale = $9, updated_at = now()
		FROM mail_lists l
		WHERE c.workspace_id = $1 AND c.id = $2
		  AND l.workspace_id = $1 AND l.id = $3
		  AND c.`+claimable,
		wsID, id, in.ListID, normChannel(in.Channel), subject, in.Body, normLocale(in.Locale), in.SMTPServiceID, AudienceLocale(in.AudienceLocale))
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}

	// Nothing changed: say which of the three reasons it was.
	var status string
	err = db.QueryRowContext(ctx, `SELECT status FROM campaigns WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCampaignNotFound
	}
	if err != nil {
		return err
	}
	if !Editable(status) {
		return &LockedError{Status: status}
	}
	return ErrListNotFound
}

// ---- rendering: one path for the send, the preview and the test ----------

// previewToken stands in for a subscriber's token in a preview or a test: the
// footer is the real one, but its link unsubscribes nobody.
const previewToken = "preview"

// campaignEmail is one recipient's email, exactly as StartSend delivers it.
// The caller fills in To.
func campaignEmail(base, locale, subject, body, token string) Message {
	unsubPage := base + "/" + locale + "/mail/unsubscribe?token=" + url.QueryEscape(token)
	oneClick := base + "/api/mail/unsubscribe?token=" + url.QueryEscape(token)
	text, htmlBody := RenderCampaign(locale, subject, body, unsubPage)
	return Message{Subject: subject, Text: text, HTML: htmlBody, Headers: UnsubscribeHeaders(oneClick)}
}

// WhatsAppText is the broadcast message exactly as each contact receives it:
// the body, then the way to stop.
func WhatsAppText(locale, body string) string {
	return body + "\n\n" + stopHint(locale)
}

// Preview is a campaign rendered as its recipients get it.
type Preview struct {
	Channel string `json:"channel"`
	Locale  string `json:"locale"`
	Dir     string `json:"dir" doc:"ltr | rtl"`
	Subject string `json:"subject" doc:"The inbox subject line. Empty for WhatsApp, which has none."`
	HTML    string `json:"html,omitempty" doc:"The email as a whole HTML document. Email only."`
	Text    string `json:"text" doc:"The plain-text part of the email, or the WhatsApp message as delivered (stop line included)."`
}

// RenderPreview renders a campaign, saved or not, through the send path.
func RenderPreview(channel, locale, subject, body string) Preview {
	channel, locale = normChannel(channel), normLocale(locale)
	p := Preview{Channel: channel, Locale: locale, Dir: "ltr"}
	if locale == "ar" {
		p.Dir = "rtl"
	}
	if channel == "whatsapp" {
		p.Text = WhatsAppText(locale, body)
		return p
	}
	m := campaignEmail(publicBase(), locale, strings.TrimSpace(subject), body, previewToken)
	p.Subject, p.HTML, p.Text = m.Subject, m.HTML, m.Text
	return p
}

// ---- test sends ----------------------------------------------------------

// TestSubjectPrefix marks a test email in the inbox it lands in.
const TestSubjectPrefix = "[TEST] "

// ErrBadAddress is a test address that is not an email address.
var ErrBadAddress = errors.New("that is not a valid email address")

// ErrWhatsAppTestTarget is a WhatsApp test aimed at anyone but the workspace.
var ErrWhatsAppTestTarget = errors.New("a WhatsApp test goes only to this workspace's own linked number; it cannot be sent to another number")

// RateLimitError is a test refused because the campaign has had its share.
type RateLimitError struct {
	Limit      int
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	mins := int((e.RetryAfter + time.Minute - 1) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return fmt.Sprintf("this campaign has had %d test sends in the last hour; try again in %d minutes", e.Limit, mins)
}

// TestResult says where a test went.
type TestResult struct {
	Channel string `json:"channel"`
	To      string `json:"to"`
	Subject string `json:"subject,omitempty"`
}

// SendTest delivers one copy of a campaign to the member (or, for email, to an
// address they typed). It touches no campaign column.
//
// For WhatsApp the only target is the workspace's own linked number. Members
// have no phone number on record, and a typed number would be a way to
// message somebody who never agreed to it — the one thing broadcasts are
// built to prevent.
func SendTest(ctx context.Context, a *app.App, wsID, campaignID, memberEmail, to string) (*TestResult, error) {
	var channel, subject, body, locale, status string
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT channel, subject, body, locale, status FROM campaigns WHERE workspace_id = $1 AND id = $2`,
		wsID, campaignID).Scan(&channel, &subject, &body, &locale, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCampaignNotFound
	}
	if err != nil {
		return nil, err
	}
	if !Editable(status) {
		return nil, &LockedError{Status: status}
	}

	if channel == "whatsapp" {
		if strings.TrimSpace(to) != "" {
			return nil, ErrWhatsAppTestTarget
		}
		l, err := whatsapp.Load(ctx, a, wsID)
		if err != nil || l.State != "open" || l.Phone == "" {
			return nil, ErrNoWhatsApp
		}
		if err := takeTest(campaignID); err != nil {
			return nil, err
		}
		if _, err := whatsapp.Send(ctx, a, wsID, l.Phone, WhatsAppText(locale, body)); err != nil {
			return nil, err
		}
		return &TestResult{Channel: "whatsapp", To: l.Phone}, nil
	}

	target := strings.TrimSpace(to)
	if target == "" {
		target = memberEmail
	}
	addr, err := mail.ParseAddress(target)
	if err != nil {
		return nil, ErrBadAddress
	}
	// Through the campaign's own service (MH-412), so the test arrives the
	// way the list's copies will.
	svc, err := LoadCampaignSMTP(ctx, a.SQLDB, wsID, campaignID)
	if err != nil {
		return nil, err
	}
	if err := takeTest(campaignID); err != nil {
		return nil, err
	}
	// The heading inside the email stays the real subject: only the inbox
	// line says TEST, so the body is still exactly what the list will read.
	m := campaignEmail(publicBase(), locale, subject, body, previewToken)
	m.To, m.Subject = addr.Address, TestSubjectPrefix+subject
	if err := Send(ctx, svc, m); err != nil {
		return nil, err
	}
	return &TestResult{Channel: "email", To: addr.Address, Subject: m.Subject}, nil
}

// ---- the test-send limit ---------------------------------------------------

// Rate is a limit on test sends: at most Limit per Window, per campaign.
type Rate struct {
	Limit  int
	Window time.Duration
}

// DefaultTestLimit: ten tests an hour for one campaign is a writer checking
// every fix they make, and far too few to use the endpoint as a relay.
var DefaultTestLimit = Rate{Limit: 10, Window: time.Hour}

var (
	testMu    sync.Mutex
	testRate  = DefaultTestLimit
	testSends = map[string][]time.Time{}
)

// SetTestLimit swaps the limit and clears the counts (tests). Returns a
// function restoring the old limit.
func SetTestLimit(r Rate) (restore func()) {
	testMu.Lock()
	prev := testRate
	testRate, testSends = r, map[string][]time.Time{}
	testMu.Unlock()
	return func() {
		testMu.Lock()
		testRate, testSends = prev, map[string][]time.Time{}
		testMu.Unlock()
	}
}

// takeTest records one test of a campaign, or refuses it. Per process and in
// memory, like the chat limiter: best-effort abuse control on a single
// instance, not an audit trail. An attempt counts whether or not the mail
// server then accepts it — each one reached the mail server.
func takeTest(campaignID string) error {
	testMu.Lock()
	defer testMu.Unlock()
	now := time.Now()

	// Forget campaigns nobody has tested within a window, so the map does not
	// keep an entry per campaign for ever.
	for k, hits := range testSends {
		if len(hits) == 0 || now.Sub(hits[len(hits)-1]) >= testRate.Window {
			delete(testSends, k)
		}
	}

	recent := testSends[campaignID][:0:0]
	for _, at := range testSends[campaignID] {
		if now.Sub(at) < testRate.Window {
			recent = append(recent, at)
		}
	}
	if len(recent) >= testRate.Limit {
		testSends[campaignID] = recent
		return &RateLimitError{Limit: testRate.Limit, RetryAfter: recent[len(recent)-testRate.Limit].Add(testRate.Window).Sub(now)}
	}
	testSends[campaignID] = append(recent, now)
	return nil
}
