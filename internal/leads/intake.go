package leads

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/clientip"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The public half: POST /api/inquiry/{key}.

It is the subscribe form's endpoint with a different payload, and it goes
through the same machinery on purpose (MH-318):

  - marketing.ResolvePublicForm: the key must be an INQUIRY form's, the form
    must be on, and the browser Origin must be on its allow-list — which is
    EMPTY-DENIES-ALL, so a form nobody has configured accepts nothing.
  - The guard's public tier and the CORS middleware list /api/inquiry/ beside
    /api/subscribe/ and /api/chat/ (internal/server).
  - The answer is IDENTICAL in every accepted case: a new address, an address
    the workspace already has as a contact, and a bot that filled the
    honeypot. Anything else is an oracle for who is in somebody's CRM, or a
    signal to the bot that it was caught.
  - Rate limited per client address (per form) and per form, with the chat
    widget's limiter (internal/inbox/ratelimit.go) — the same all-or-nothing
    sliding windows, keyed on the address internal/clientip derives from the
    TRUSTED proxy chain, never a header the sender wrote.

What it does NOT do is send anything: unlike the subscribe form, no email goes
to the address the caller typed, so there is nothing here to mail-bomb with.
The cost of abuse is rows in one workspace's Leads list, which is what the
limits bound.
*/

// InquiryResponse is what the visitor's browser gets back. A named type for the
// reason marketing.SubscribeResponse gives: a stranger's website reads it.
type InquiryResponse struct {
	OK     bool   `json:"ok"`
	Thanks string `json:"thanks" doc:"What to show the visitor, in their language."`
}

type inquiryBody struct {
	Name    string `json:"name,omitempty" maxLength:"120"`
	Email   string `json:"email" maxLength:"320"`
	Phone   string `json:"phone,omitempty" maxLength:"40"`
	Company string `json:"company,omitempty" maxLength:"160"`
	Message string `json:"message" maxLength:"5000"`
	// Optional, as typed ("5,000", "$12k"). Converting the lead makes it the deal's value when it
	// reads as one amount in the workspace's currency (budget.go).
	Budget string `json:"budget,omitempty" maxLength:"80"`
	Locale  string `json:"locale,omitempty" maxLength:"35"`
	// The honeypot. The snippet hides it from people and from the tab order; a
	// bot filling every field fills it. Its submission is thanked and dropped.
	Nickname string `json:"nickname,omitempty" maxLength:"200" doc:"Honeypot: leave empty. A submission that fills it is accepted and discarded."`
	// First-touch attribution, captured by the embed snippet in the browser.
	UtmSource   string `json:"utm_source,omitempty" maxLength:"200"`
	UtmMedium   string `json:"utm_medium,omitempty" maxLength:"200"`
	UtmCampaign string `json:"utm_campaign,omitempty" maxLength:"200"`
	UtmTerm     string `json:"utm_term,omitempty" maxLength:"200"`
	UtmContent  string `json:"utm_content,omitempty" maxLength:"200"`
	Referrer    string `json:"referrer,omitempty" maxLength:"2000"`
	Gclid       string `json:"gclid,omitempty" maxLength:"300"`
	LandingPage string `json:"landing_page,omitempty" maxLength:"2000"`
}

// ---- rate limits -------------------------------------------------------------

// InquiryLimits are the numbers. A person sends an inquiry once, perhaps twice;
// the per-address limit leaves room for a second person behind the same office
// NAT and no more. The per-form ceiling is for traffic spread over many
// addresses, and is set far above what a small business's contact form sees.
type InquiryLimits struct {
	IPSubmits   inbox.Rate
	FormSubmits inbox.Rate
}

func DefaultInquiryLimits() InquiryLimits {
	return InquiryLimits{
		IPSubmits:   inbox.Rate{Limit: 5, Window: 10 * time.Minute},
		FormSubmits: inbox.Rate{Limit: 60, Window: 10 * time.Minute},
	}
}

var (
	limitsMu sync.RWMutex
	limiter  = inbox.NewMemoryLimiter()
	limits   = DefaultInquiryLimits()
)

// SetLimiter swaps the store and the numbers (a test's own clock, tighter
// numbers). Returns a function restoring the old ones.
func SetLimiter(l inbox.Limiter, lim InquiryLimits) (restore func()) {
	limitsMu.Lock()
	prevL, prevLim := limiter, limits
	limiter, limits = l, lim
	limitsMu.Unlock()
	return func() {
		limitsMu.Lock()
		limiter, limits = prevL, prevLim
		limitsMu.Unlock()
	}
}

func allowInquiry(formID, ipBucket string) error {
	limitsMu.RLock()
	l, lim := limiter, limits
	limitsMu.RUnlock()
	k := "inquiry:f:" + formID
	ok, retry := l.Take(time.Now(),
		inbox.Rule{Key: k + ":ip:" + ipBucket, Limit: lim.IPSubmits.Limit, Window: lim.IPSubmits.Window},
		inbox.Rule{Key: k, Limit: lim.FormSubmits.Limit, Window: lim.FormSubmits.Window},
	)
	if ok {
		return nil
	}
	secs := int((retry + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return huma.ErrorWithHeaders(
		huma.Error429TooManyRequests(fmt.Sprintf("too many messages from here — try again in %d seconds", secs)),
		map[string][]string{"Retry-After": {strconv.Itoa(secs)}},
	)
}

// ---- intake ----------------------------------------------------------------

// validEmail accepts a bare address with a dotted domain: what a person types
// into an email field, and nothing a display-name parser would also accept.
func validEmail(s string) bool {
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return false
	}
	d := EmailDomain(s)
	return d != "" && strings.Contains(d, ".") && !strings.HasPrefix(s, "@")
}

// Intake files one lead inside the form's workspace and returns its id. db is
// the APP pool; ctx carries the workspace.
func intake(ctx context.Context, db *sql.DB, form marketing.PublicForm, b inquiryBody, origin string) (string, error) {
	locale := strings.ToLower(strings.TrimSpace(b.Locale))
	if strings.HasPrefix(locale, "ar") {
		locale = "ar"
	} else {
		locale = "en"
	}
	var id string
	err := db.QueryRowContext(ctx, `
		INSERT INTO leads (workspace_id, form_id, form_name, name, email, phone, company, message, locale,
		                   utm_source, utm_medium, utm_campaign, utm_term, utm_content, referrer, gclid, landing_page, origin, budget)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id`,
		form.WorkspaceID, form.ID, form.Name, strings.TrimSpace(b.Name), strings.ToLower(strings.TrimSpace(b.Email)),
		strings.TrimSpace(b.Phone), strings.TrimSpace(b.Company), strings.TrimSpace(b.Message), locale,
		strings.TrimSpace(b.UtmSource), strings.TrimSpace(b.UtmMedium), strings.TrimSpace(b.UtmCampaign),
		strings.TrimSpace(b.UtmTerm), strings.TrimSpace(b.UtmContent), strings.TrimSpace(b.Referrer),
		strings.TrimSpace(b.Gclid), strings.TrimSpace(b.LandingPage), strings.TrimRight(strings.TrimSpace(origin), "/"),
		strings.TrimSpace(b.Budget),
	).Scan(&id)
	return id, err
}

// notify puts the new lead in the notification center, linked to its page, in
// the workspace's language. Best effort: the lead is already stored, and a
// visitor cannot act on a notification failing.
func notify(ctx context.Context, a *app.App, wsID, leadID string, b inquiryBody, formName string) {
	var slug, locale string
	if err := a.SysDB.QueryRowContext(ctx, `SELECT slug, locale FROM workspaces WHERE id = $1`, wsID).Scan(&slug, &locale); err != nil {
		a.Log.Warn("could not read the workspace for a lead notification", "workspace", wsID, "err", err)
		return
	}
	if locale != "ar" {
		locale = "en"
	}
	who := firstNonEmpty(strings.TrimSpace(b.Name), strings.ToLower(strings.TrimSpace(b.Email)))
	if c := strings.TrimSpace(b.Company); c != "" {
		who += " (" + c + ")"
	}
	title := "New lead: " + who
	source := "Leads"
	if locale == "ar" {
		title = "عميل محتمل جديد: " + who
		source = "العملاء المحتملون"
	}
	if f := strings.TrimSpace(formName); f != "" {
		source = f
	}
	err := notifications.Emit(ctx, a.SQLDB, wsID, notifications.Internal{
		SourceName: source,
		ExternalID: "lead:" + leadID,
		Kind:       "lead",
		Severity:   "info",
		Title:      title,
		Body:       b.Message,
		Actor:      strings.ToLower(strings.TrimSpace(b.Email)),
		URL:        publicBase() + "/" + locale + "/w/" + slug + "/leads/" + leadID,
	})
	if err != nil {
		a.Log.Warn("could not notify about a new lead", "workspace", wsID, "err", err)
		return
	}
	a.Events.Publish(wsID, realtime.Event{Kind: "lead"})
}

// defaultThanks is what a visitor is told when the form has no copy of its own.
func defaultThanks(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "ar") {
		return "شكرًا لك، وصلتنا رسالتك وسنتواصل معك قريبًا."
	}
	return "Thank you — your message has reached us, and we will be in touch soon."
}

func registerPublicInquiry(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID:   "public-inquiry",
		Method:        http.MethodPost,
		Path:          "/api/inquiry/{key}",
		DefaultStatus: http.StatusOK,
		Summary:       "Send an inquiry from a website (public: the form key plus the Origin allow-list are the authorization)",
		Description: "Files a lead in the form's workspace. email and message are required. The answer is the same whether or not " +
			"the address is already known. Rate limited per client address and per form; over a limit the answer is 429 with Retry-After (seconds).",
		Tags: []string{"Leads"},
		Responses: map[string]*huma.Response{
			"429": {Description: "Too many submissions. Retry-After says how many seconds to wait."},
		},
	}, func(ctx context.Context, in *struct {
		Key    string `path:"key"`
		Origin string `header:"Origin"`
		Body   inquiryBody
	}) (*struct{ Body InquiryResponse }, error) {
		form, err := marketing.ResolvePublicForm(ctx, a.SysDB, in.Key, marketing.FormInquiry, in.Origin)
		if err != nil {
			return nil, err
		}

		// After resolve, so an unknown key or a refused origin costs no limiter
		// entry; before anything is written, so a refused request writes nothing.
		ip := clientip.From(ctx)
		if ip == "" {
			ip = "unknown" // only outside the server's middleware (a test harness)
		}
		if err := allowInquiry(form.ID, clientip.Bucket(ip)); err != nil {
			return nil, err
		}

		thanks := form.ThanksEN
		if strings.HasPrefix(strings.ToLower(in.Body.Locale), "ar") && strings.TrimSpace(form.ThanksAR) != "" {
			thanks = form.ThanksAR
		}
		if strings.TrimSpace(thanks) == "" {
			thanks = defaultThanks(in.Body.Locale)
		}
		ok := &struct{ Body InquiryResponse }{InquiryResponse{OK: true, Thanks: thanks}}

		// The honeypot: thanked exactly like a person, filed nowhere. Checked
		// before validation, so what a bot put in the other fields cannot make
		// the answer differ either.
		if strings.TrimSpace(in.Body.Nickname) != "" {
			return ok, nil
		}

		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		if !validEmail(email) {
			return nil, huma.Error422UnprocessableEntity("that does not look like an email address")
		}
		if strings.TrimSpace(in.Body.Message) == "" {
			return nil, huma.Error422UnprocessableEntity("a message is required")
		}
		in.Body.Email = email

		sctx := tenant.WithWorkspace(ctx, form.WorkspaceID)
		id, err := intake(sctx, a.SQLDB, form, in.Body, in.Origin)
		if err != nil {
			a.Log.Error("inquiry intake failed", "workspace", form.WorkspaceID, "err", err)
			return nil, huma.Error500InternalServerError("could not record that message")
		}
		notify(sctx, a, form.WorkspaceID, id, in.Body, form.Name)

		// The SAME answer whether this address is new to the workspace or
		// already a contact there — see the package comment.
		return ok, nil
	})
}
