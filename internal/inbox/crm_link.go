package inbox

/*
The inbox reaches the CRM (MH-379).

A conversation used to end in the inbox: PATCH contact_id existed, but nothing
called it, and nothing a customer said on chat ever became a contact, a deal or
a line on anyone's timeline. This file is the other half:

  - autoLink: an email or WhatsApp thread whose sender the CRM already knows by
    that EXACT address or number is linked when the message is filed. Phone
    numbers are compared as digits only, the normalisation MH-320's STOP
    handling uses (marketing.OnlyDigits), so "+20 101 234 5678" and the
    "201012345678" WhatsApp delivers are the same number. Chat is deliberately
    NOT auto-linked: a chat visitor types whatever email they like, and
    matching on it would file a stranger's words on a customer's record.
  - guessVisitor: what the visitor gave (name, email, phone), from the
    conversation and from what they wrote, to prefill "create a contact".
  - replyLanguage: which language the customer is answered in — the canned
    replies (MH-383) insert that variant.
  - Transcript: the thread as a readable text — who said what, and when — which
    opening a deal attaches as the deal's first activity.
*/

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/realtime"
)

// VisitorGuess prefills "create a contact" from what the visitor gave.
type VisitorGuess struct {
	Name   string `json:"name"`
	Email  string `json:"email" doc:"The address they gave, or the first one they wrote in a message."`
	Phone  string `json:"phone" doc:"Their WhatsApp number, or the first phone number they wrote in a message."`
	Locale string `json:"locale" enum:"en,ar" doc:"The language they write in."`
}

// conversationDetail is GET /api/w/conversations/{id}.
type conversationDetail struct {
	Conversation  ConversationOut `json:"conversation"`
	Messages      []MessageOut    `json:"messages"`
	Visitor       VisitorGuess    `json:"visitor" doc:"What the visitor gave, to prefill a new contact."`
	ReplyLanguage string          `json:"reply_language" enum:"en,ar" doc:"The language to answer in: the canned replies insert this variant."`
}

// reopenOnInbound is the SET fragment every inbound path uses: a customer
// writing reopens a closed thread and wakes a snoozed one (MH-383), and an
// open thread has neither a snooze time nor a close time.
const reopenOnInbound = `status = CASE WHEN status IN ('closed', 'snoozed') THEN 'open' ELSE status END,
	snoozed_until = NULL, closed_at = NULL`

// ---- automatic link -----------------------------------------------------------

// autoLink links an email or WhatsApp conversation to the one contact whose
// email or phone digits match exactly. It never overrides a member: a thread
// that is linked already, or that a member linked or unlinked by hand, is left
// alone. Two contacts sharing a number is ambiguous, and links nothing.
//
// ctx must carry the workspace (the app pool's RLS).
func autoLink(ctx context.Context, db *sql.DB, wsID, convID string) error {
	var channel, email, phone, link string
	var contactID sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT channel, customer_email, customer_phone, contact_link, contact_id
		FROM conversations WHERE workspace_id = $1 AND id = $2`, wsID, convID).
		Scan(&channel, &email, &phone, &link, &contactID)
	if err != nil {
		return err
	}
	if contactID.Valid || link == "manual" {
		return nil
	}
	var rows *sql.Rows
	switch channel {
	case "email":
		email = strings.ToLower(strings.TrimSpace(email))
		if email == "" {
			return nil
		}
		rows, err = db.QueryContext(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND lower(email) = $2 LIMIT 2`, wsID, email)
	case "whatsapp":
		digits := marketing.OnlyDigits(phone)
		// A handful of digits is an extension, not a number worth matching on.
		if len(digits) < 7 {
			return nil
		}
		rows, err = db.QueryContext(ctx, `
			SELECT id FROM contacts
			WHERE workspace_id = $1 AND phone <> '' AND regexp_replace(phone, '[^0-9]', '', 'g') = $2 LIMIT 2`, wsID, digits)
	default:
		return nil
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(ids) != 1 {
		return nil
	}
	_, err = db.ExecContext(ctx, `
		UPDATE conversations SET contact_id = $3, contact_link = 'auto', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND contact_id IS NULL AND contact_link <> 'manual'`, wsID, convID, ids[0])
	return err
}

// ---- what the visitor gave ------------------------------------------------------

var (
	emailInText = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	phoneInText = regexp.MustCompile(`\+?\d[\d\s().\-]{5,20}\d`)
)

// asciiDigits turns Arabic-Indic and Eastern Arabic-Indic digits into 0-9, so
// a number typed on an Arabic keyboard is still found.
func asciiDigits(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '٠' && r <= '٩':
			return '0' + (r - '٠')
		case r >= '۰' && r <= '۹':
			return '0' + (r - '۰')
		}
		return r
	}, s)
}

// phoneFrom finds the first phone number in a text: 8 to 15 digits, the
// range E.164 allows once the separators are gone.
func phoneFrom(text string) string {
	for _, m := range phoneInText.FindAllString(asciiDigits(text), -1) {
		if n := len(marketing.OnlyDigits(m)); n >= 8 && n <= 15 {
			return strings.TrimSpace(m)
		}
	}
	return ""
}

// guessVisitor is what the visitor gave: the fields the conversation holds,
// and failing those, the first email and phone number they wrote.
func guessVisitor(c ConversationOut, msgs []MessageOut, lang string) VisitorGuess {
	g := VisitorGuess{Name: strings.TrimSpace(c.CustomerName), Email: strings.ToLower(strings.TrimSpace(c.CustomerEmail)), Locale: lang}
	if p := strings.TrimSpace(c.CustomerPhone); p != "" {
		g.Phone = p
		// WhatsApp delivers the number without its plus.
		if !strings.HasPrefix(p, "+") && marketing.OnlyDigits(p) == p {
			g.Phone = "+" + p
		}
	}
	for _, m := range msgs {
		if m.Direction != "in" {
			continue
		}
		if g.Email == "" {
			if e := emailInText.FindString(m.Body); e != "" {
				g.Email = strings.ToLower(e)
			}
		}
		if g.Phone == "" {
			g.Phone = phoneFrom(m.Body)
		}
	}
	return g
}

// ---- the language to answer in ----------------------------------------------------

// scriptOf says which script a text is mostly written in: "ar", "en", or ""
// when it has no letters at all.
func scriptOf(text string) string {
	var arabic, latin int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Arabic, r) && unicode.IsLetter(r):
			arabic++
		case unicode.Is(unicode.Latin, r):
			latin++
		}
	}
	switch {
	case arabic == 0 && latin == 0:
		return ""
	case arabic >= latin:
		return "ar"
	}
	return "en"
}

// replyLanguage picks the language a customer is answered in, strongest
// signal first: the language the chat widget ran in; the script of what they
// last wrote; the linked contact's language; the workspace's. It is never the
// member's own UI language — someone reading the inbox in English still
// answers an Arabic customer in Arabic.
func replyLanguage(chatLocale, lastInbound, contactLocale, wsLocale string) string {
	if l := strings.ToLower(strings.TrimSpace(chatLocale)); l != "" {
		if strings.HasPrefix(l, "ar") {
			return "ar"
		}
		return "en"
	}
	if s := scriptOf(lastInbound); s != "" {
		return s
	}
	for _, l := range []string{contactLocale, wsLocale} {
		switch strings.ToLower(strings.TrimSpace(l)) {
		case "ar":
			return "ar"
		case "en":
			return "en"
		}
	}
	return "en"
}

func replyLanguageFor(ctx context.Context, db *sql.DB, wsID string, c ConversationOut, msgs []MessageOut) (string, error) {
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Direction == "in" {
			last = msgs[i].Body
			break
		}
	}
	var contactLocale, wsLocale string
	if c.ContactID != nil {
		if err := db.QueryRowContext(ctx, `SELECT locale FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, *c.ContactID).Scan(&contactLocale); err != nil && !notFound(err) {
			return "", err
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT locale FROM workspaces WHERE id = $1`, wsID).Scan(&wsLocale); err != nil && !notFound(err) {
		return "", err
	}
	return replyLanguage(c.Locale, last, contactLocale, wsLocale), nil
}

// ---- the transcript -----------------------------------------------------------------

// maxTranscript bounds the activity a transcript becomes. A long thread is cut
// with a line saying how much more there is; the whole thing stays in the inbox.
const maxTranscript = 20000

// Transcript renders a thread as plain text for a CRM activity: a header
// saying who, on which channel, from which page, then every message with its
// speaker and time. Times are in the workspace's time zone, which the header
// names. Staff are named by their email (users hold nothing else); the
// customer by the name they gave, or "Visitor".
func Transcript(c ConversationOut, msgs []MessageOut, authors map[string]string, lang string, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	ar := lang == "ar"
	channel := map[string][2]string{
		"chat":     {"Chat", "محادثة الدردشة"},
		"email":    {"Email", "البريد الإلكتروني"},
		"whatsapp": {"WhatsApp", "واتساب"},
	}[c.Channel]
	if channel[0] == "" {
		channel = [2]string{c.Channel, c.Channel}
	}
	customer := strings.TrimSpace(c.CustomerName)
	if customer == "" {
		customer = map[bool]string{false: "Visitor", true: "الزائر"}[ar]
	}
	contactBits := []string{}
	for _, v := range []string{c.CustomerEmail, c.CustomerPhone} {
		if v = strings.TrimSpace(v); v != "" {
			contactBits = append(contactBits, v)
		}
	}
	stamp := func(t time.Time) string { return t.In(loc).Format("2006-01-02 15:04") }

	var b strings.Builder
	if ar {
		fmt.Fprintf(&b, "نص %s مع %s", channel[1], customer)
	} else {
		fmt.Fprintf(&b, "%s transcript with %s", channel[0], customer)
	}
	if len(contactBits) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(contactBits, ", "))
	}
	b.WriteString("\n")
	if s := strings.TrimSpace(c.Subject); s != "" && c.Channel == "email" {
		if ar {
			fmt.Fprintf(&b, "الموضوع: %s\n", s)
		} else {
			fmt.Fprintf(&b, "Subject: %s\n", s)
		}
	}
	if c.PageURL != "" {
		if ar {
			fmt.Fprintf(&b, "بدأت من: %s\n", c.PageURL)
		} else {
			fmt.Fprintf(&b, "Started on: %s\n", c.PageURL)
		}
	}
	if ar {
		fmt.Fprintf(&b, "الأوقات بتوقيت %s\n", loc.String())
	} else {
		fmt.Fprintf(&b, "Times are %s\n", loc.String())
	}

	for i, m := range msgs {
		speaker := customer
		if m.Direction == "out" {
			who := ""
			if m.AuthorUserID != nil {
				who = authors[*m.AuthorUserID]
			}
			if who == "" {
				who = map[bool]string{false: "Team", true: "الفريق"}[ar]
			}
			speaker = who + map[bool]string{false: " (team)", true: " (الفريق)"}[ar]
		}
		entry := fmt.Sprintf("\n[%s] %s:\n%s\n", stamp(m.CreatedAt), speaker, strings.TrimSpace(m.Body))
		if len([]rune(b.String()))+len([]rune(entry)) > maxTranscript {
			rest := len(msgs) - i
			if ar {
				fmt.Fprintf(&b, "\n… و%d رسالة أخرى — المحادثة كاملة في صندوق الوارد.\n", rest)
			} else {
				fmt.Fprintf(&b, "\n… and %d more messages — the whole thread is in the inbox.\n", rest)
			}
			break
		}
		b.WriteString(entry)
	}
	return strings.TrimRight(b.String(), "\n")
}

// memberEmails maps the workspace's members to their emails, for naming who
// replied in a transcript. On the owner pool: the app role has no grant on
// users at all (cmd/migrate), and the join is confined to this workspace's
// own members.
func memberEmails(ctx context.Context, a *app.App, wsID string) map[string]string {
	out := map[string]string{}
	if a == nil || a.SysDB == nil {
		return out
	}
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT u.id, u.email FROM users u JOIN workspace_members m ON m.user_id = u.id
		WHERE m.workspace_id = $1`, wsID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, email string
		if rows.Scan(&id, &email) == nil {
			out[id] = email
		}
	}
	return out
}

// ---- routes ---------------------------------------------------------------------------

// crmScope is inbox:write AND contacts:write: these routes change both.
func crmScope(ctx context.Context) (string, string, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return "", "", err
	}
	if _, err := scope(ctx, authz.ContactsWrite); err != nil {
		return "", "", err
	}
	return s.WorkspaceID, s.UserID, nil
}

type contactResult struct {
	ContactID      string          `json:"contact_id"`
	ContactCreated bool            `json:"contact_created" doc:"false when the email already belonged to a contact, which was linked instead (its blank name and phone filled in)."`
	Conversation   ConversationOut `json:"conversation"`
}

type dealResult struct {
	DealID       string          `json:"deal_id"`
	ActivityID   string          `json:"activity_id" doc:"The transcript, the deal's first activity."`
	Conversation ConversationOut `json:"conversation"`
}

func registerCRM(api huma.API, a *app.App) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-create-contact", Method: http.MethodPost, Path: "/api/w/conversations/{id}/contact",
		DefaultStatus: http.StatusCreated, Summary: "Create a CRM contact from a conversation and link it", Tags: tags,
		Description: "Deduped on email: an address the workspace already has is linked rather than duplicated, and only its blank " +
			"name and phone are filled in. A company that already has the email's domain is attached; none is created.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Email  string `json:"email" minLength:"3" maxLength:"254"`
			Name   string `json:"name,omitempty" maxLength:"120"`
			Phone  string `json:"phone,omitempty" maxLength:"40"`
			Locale string `json:"locale,omitempty" enum:"en,ar"`
		}
	}) (*body[contactResult], error) {
		wsID, userID, err := crmScope(ctx)
		if err != nil {
			return nil, err
		}
		addr, err := mail.ParseAddress(strings.TrimSpace(in.Body.Email))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("that is not a valid email address")
		}
		email := strings.ToLower(addr.Address)
		locale := "en"
		if in.Body.Locale == "ar" {
			locale = "ar"
		}
		tx, err := db().BeginTx(ctx, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not create the contact")
		}
		defer tx.Rollback() //nolint:errcheck
		var exists string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE workspace_id = $1 AND id = $2 FOR UPDATE`, wsID, in.ID).Scan(&exists); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("conversation not found")
			}
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		res := contactResult{}
		// xmax = 0 is true only for a row this statement inserted.
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO contacts (workspace_id, email, name, phone, locale, source) VALUES ($1, $2, $3, $4, $5, 'inbox')
			ON CONFLICT (workspace_id, email) DO UPDATE SET
			  name = CASE WHEN contacts.name = '' THEN EXCLUDED.name ELSE contacts.name END,
			  phone = CASE WHEN contacts.phone = '' THEN EXCLUDED.phone ELSE contacts.phone END,
			  updated_at = now()
			RETURNING id, (xmax = 0)`,
			wsID, email, strings.TrimSpace(in.Body.Name), strings.TrimSpace(in.Body.Phone), locale,
		).Scan(&res.ContactID, &res.ContactCreated); err != nil {
			return nil, huma.Error500InternalServerError("could not create the contact")
		}
		// A company the workspace already keeps under this domain; never a new one.
		if at := strings.LastIndex(email, "@"); at > 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE contacts SET company_id = (SELECT id FROM companies WHERE workspace_id = $1 AND lower(domain) = $3 LIMIT 1), updated_at = now()
				WHERE workspace_id = $1 AND id = $2 AND company_id IS NULL
				  AND EXISTS (SELECT 1 FROM companies WHERE workspace_id = $1 AND lower(domain) = $3)`,
				wsID, res.ContactID, email[at+1:]); err != nil {
				return nil, huma.Error500InternalServerError("could not create the contact")
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE conversations SET contact_id = $3, contact_link = 'manual', updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, in.ID, res.ContactID); err != nil {
			return nil, huma.Error500InternalServerError("could not link the contact")
		}
		if err := tx.Commit(); err != nil {
			return nil, huma.Error500InternalServerError("could not create the contact")
		}
		if res.Conversation, err = readConversation(ctx, db(), wsID, in.ID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		changed(ctx, a, wsID, realtime.Event{Kind: "conversation", ConversationID: in.ID, AuthorUserID: userID})
		return out(res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-open-deal", Method: http.MethodPost, Path: "/api/w/conversations/{id}/deal",
		DefaultStatus: http.StatusCreated, Summary: "Open a deal from a conversation, with the transcript as its first activity", Tags: tags,
		Description: "The deal goes on the linked contact and their company when there is one. One deal per conversation: a second " +
			"attempt answers 409 naming the first.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Title      string `json:"title,omitempty" maxLength:"160" doc:"Defaults to who wrote and on which channel."`
			StageID    string `json:"stage_id,omitempty" doc:"Defaults to the pipeline's first open stage."`
			ValueMinor int64  `json:"value_minor,omitempty" minimum:"0" doc:"In the currency's minor unit."`
			Currency   string `json:"currency,omitempty" pattern:"^[A-Z]{3}$" doc:"Defaults to the workspace's."`
		}
	}) (*body[dealResult], error) {
		wsID, userID, err := crmScope(ctx)
		if err != nil {
			return nil, err
		}
		// Outside the transaction: EnsureStages takes its own lock and commits.
		if err := crm.EnsureStages(ctx, db(), wsID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the pipeline")
		}
		c, err := readConversation(ctx, db(), wsID, in.ID)
		if notFound(err) {
			return nil, huma.Error404NotFound("conversation not found")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		msgs, err := thread(ctx, db(), wsID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the thread")
		}
		if len(msgs) == 0 {
			return nil, huma.Error422UnprocessableEntity("this conversation has no messages to attach")
		}
		var wsLocale, currency, zone string
		if err := db().QueryRowContext(ctx, `SELECT locale, currency, timezone FROM workspaces WHERE id = $1`, wsID).Scan(&wsLocale, &currency, &zone); err != nil {
			return nil, huma.Error500InternalServerError("could not read the workspace")
		}
		if in.Body.Currency != "" {
			currency = in.Body.Currency
		}
		loc, err := time.LoadLocation(strings.TrimSpace(zone))
		if err != nil || strings.TrimSpace(zone) == "" {
			loc = time.UTC
		}
		lang := "en"
		if wsLocale == "ar" {
			lang = "ar"
		}
		transcript := Transcript(c, msgs, memberEmails(ctx, a, wsID), lang, loc)

		title := strings.TrimSpace(in.Body.Title)
		if title == "" {
			who := firstNonEmpty(strings.TrimSpace(c.CustomerName), c.CustomerEmail, c.CustomerPhone)
			if c.Contact != nil {
				who = firstNonEmpty(c.Contact.Name, c.Contact.Email)
			}
			if who == "" {
				who = map[bool]string{false: "Visitor", true: "زائر"}[lang == "ar"]
			}
			label := map[string][2]string{"chat": {"chat", "دردشة"}, "email": {"email", "بريد إلكتروني"}, "whatsapp": {"WhatsApp", "واتساب"}}[c.Channel]
			if lang == "ar" {
				title = who + " — " + label[1]
			} else {
				title = who + " — " + label[0]
			}
		}
		if r := []rune(title); len(r) > 160 {
			title = string(r[:160])
		}

		tx, err := db().BeginTx(ctx, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not open the deal")
		}
		defer tx.Rollback() //nolint:errcheck
		// FOR UPDATE: two members clicking at once open one deal, not two.
		var existing, contactID sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT deal_id, contact_id FROM conversations WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
			wsID, in.ID).Scan(&existing, &contactID); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("conversation not found")
			}
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		if existing.Valid {
			return nil, huma.Error409Conflict("this conversation already opened a deal: " + existing.String)
		}
		var companyID sql.NullString
		if contactID.Valid {
			if err := tx.QueryRowContext(ctx, `SELECT company_id FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, contactID.String).Scan(&companyID); err != nil && !notFound(err) {
				return nil, huma.Error500InternalServerError("could not read the contact")
			}
		}
		stageID := strings.TrimSpace(in.Body.StageID)
		if stageID == "" {
			if err := tx.QueryRowContext(ctx, `SELECT id FROM pipeline_stages WHERE workspace_id = $1 AND kind = 'open'
				ORDER BY position, created_at LIMIT 1`, wsID).Scan(&stageID); err != nil {
				return nil, huma.Error422UnprocessableEntity("the pipeline has no open stage to put a deal in")
			}
		}
		var stageKind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM pipeline_stages WHERE workspace_id = $1 AND id = $2`, wsID, stageID).Scan(&stageKind); err != nil {
			if notFound(err) {
				return nil, huma.Error422UnprocessableEntity("that stage is not in this workspace's pipeline")
			}
			return nil, huma.Error500InternalServerError("could not read the pipeline")
		}
		res := dealResult{}
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO deals (workspace_id, title, value_minor, currency, stage_id, position, contact_id, company_id, owner_user_id, created_by, closed_at)
			VALUES ($1, $2, $3, $4, $5, (SELECT coalesce(max(position), 0) + 1 FROM deals WHERE workspace_id = $1 AND stage_id = $5),
			        $6, $7, $8, $8, CASE WHEN $9 <> 'open' THEN now() END)
			RETURNING id`,
			wsID, title, in.Body.ValueMinor, currency, stageID, contactID, companyID, userID, stageKind,
		).Scan(&res.DealID); err != nil {
			return nil, huma.Error500InternalServerError("could not open the deal")
		}
		// The transcript is the deal's first activity, dated when the customer
		// first wrote rather than when the deal was opened.
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO activities (workspace_id, kind, body, contact_id, company_id, deal_id, created_by, created_at)
			VALUES ($1, 'note', $2, $3, $4, $5, $6, $7)
			RETURNING id`,
			wsID, transcript, contactID, companyID, res.DealID, userID, msgs[0].CreatedAt,
		).Scan(&res.ActivityID); err != nil {
			return nil, huma.Error500InternalServerError("could not attach the transcript")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE conversations SET deal_id = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			wsID, in.ID, res.DealID); err != nil {
			return nil, huma.Error500InternalServerError("could not link the deal")
		}
		if err := tx.Commit(); err != nil {
			return nil, huma.Error500InternalServerError("could not open the deal")
		}
		if res.Conversation, err = readConversation(ctx, db(), wsID, in.ID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the conversation")
		}
		changed(ctx, a, wsID, realtime.Event{Kind: "conversation", ConversationID: in.ID, AuthorUserID: userID})
		return out(res), nil
	})
}
