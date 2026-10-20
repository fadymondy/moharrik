package marketing

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Broadcasting on WhatsApp (MH-320).

One rule shapes everything here:

	WHATSAPP CONSENT IS NOT EMAIL CONSENT.

A contact who subscribed to a mailing list agreed to receive email. They did
not agree to a message on their personal phone, which is a different thing and
is answered by a person. So a broadcast needs BOTH: the list says who the
audience is, and whatsapp_opt_in_at says whether we may reach them this way.

The second rule follows from the first: a broadcast must carry a way to stop.
Email campaigns have a one-click unsubscribe header; WhatsApp has nothing of
the kind, so the only way out is a reply, and the inbound hook has to honour
it (see HandleStopWord). Sending broadcasts nobody can stop is precisely the
behaviour that gets a number reported and banned — which takes the workspace's
only number with it.
*/

// WhatsAppRecipient is one contact a broadcast will reach.
type WhatsAppRecipient struct {
	ContactID string
	Phone     string
	Name      string
}

// ErrNoWhatsApp is returned when the workspace has not linked a number (or the
// number the campaign was set to send from is no longer linked).
var ErrNoWhatsApp = errors.New("link a WhatsApp number for this workspace before broadcasting")

// CampaignNumberID resolves the number a WhatsApp campaign is set to send
// from: a link id, the number itself or its label. "" stays "", which means
// the workspace's default number at the moment the campaign sends.
func CampaignNumberID(ctx context.Context, a *app.App, wsID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	l, err := whatsapp.Resolve(ctx, a, wsID, ref)
	if errors.Is(err, whatsapp.ErrNoLink) {
		return "", huma.Error404NotFound("no WhatsApp number in this workspace matches " + ref)
	}
	if err != nil {
		return "", huma.Error500InternalServerError("could not read the WhatsApp numbers")
	}
	return l.ID, nil
}

// WhatsAppAudience returns who may be broadcast to for a list.
//
// Deliberately conservative, and each condition is a decision:
//
//	on the list             — the list is the segment; this is who was targeted
//	not unsubscribed        — an unsubscribe is "stop sending me this", and it
//	                          is honoured across channels rather than read as
//	                          being about email only
//	whatsapp_opt_in_at set  — the consent itself; NULL is never consent
//	whatsapp_opt_out_at null— they have not said STOP
//	phone present           — nothing to send to otherwise
func WhatsAppAudience(ctx context.Context, db *sql.DB, wsID, listID string) ([]WhatsAppRecipient, error) {
	return WhatsAppAudienceFor(ctx, db, wsID, listID, "")
}

// WhatsAppAudienceFor is WhatsAppAudience narrowed to contacts in one
// language (MH-413); "" is everyone.
func WhatsAppAudienceFor(ctx context.Context, db *sql.DB, wsID, listID, locale string) ([]WhatsAppRecipient, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, c.phone, c.name `+whatsappAudienceFrom+`
		ORDER BY s.created_at, s.id
		LIMIT $4`, wsID, listID, AudienceLocale(locale), MaxSendAudience+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WhatsAppRecipient
	for rows.Next() {
		var r WhatsAppRecipient
		if err := rows.Scan(&r.ContactID, &r.Phone, &r.Name); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// whatsappAudienceFrom is the audience rule, written ONCE so the count shown
// before sending and the list actually sent to can never disagree.
const whatsappAudienceFrom = `
		FROM list_subscriptions s
		JOIN contacts c ON c.id = s.contact_id AND c.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1
		  AND s.list_id = $2
		  AND s.status <> 'unsubscribed'
		  AND c.whatsapp_opt_in_at IS NOT NULL
		  AND c.whatsapp_opt_out_at IS NULL
		  AND coalesce(c.phone, '') <> ''
		  AND ($3 = '' OR c.locale = $3)`

// CountWhatsAppAudience is how many contacts a broadcast to a list would reach
// right now — the number shown before anyone presses send.
func CountWhatsAppAudience(ctx context.Context, db *sql.DB, wsID, listID string) (int64, error) {
	return CountWhatsAppAudienceFor(ctx, db, wsID, listID, "")
}

// CountWhatsAppAudienceFor counts one language's share of it (MH-413).
func CountWhatsAppAudienceFor(ctx context.Context, db *sql.DB, wsID, listID, locale string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT count(*) `+whatsappAudienceFrom, wsID, listID, AudienceLocale(locale)).Scan(&n)
	return n, err
}

// ErrWhatsAppOptedOut is an opt-in attempted for a contact who said STOP.
var ErrWhatsAppOptedOut = errors.New("this contact asked to stop WhatsApp messages; only an explicit reversal can let them back in")

// ErrNoSuchContact is a contact id this workspace does not have.
var ErrNoSuchContact = errors.New("contact not found")

// RecordWhatsAppOptIn is SetWhatsAppOptIn for a person acting in the
// workspace: unlike the form path, it TELLS the caller when nothing happened
// because the contact had opted out, so the UI can offer the explicit
// reversal instead of pretending the tick took.
func RecordWhatsAppOptIn(ctx context.Context, db *sql.DB, wsID, contactID string) error {
	var optedOut bool
	err := db.QueryRowContext(ctx, `
		SELECT whatsapp_opt_out_at IS NOT NULL FROM contacts WHERE workspace_id = $1 AND id = $2`,
		wsID, contactID).Scan(&optedOut)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchContact
	}
	if err != nil {
		return err
	}
	if optedOut {
		return ErrWhatsAppOptedOut
	}
	return SetWhatsAppOptIn(ctx, db, wsID, contactID)
}

// WhatsAppOptInSources are how a contact can have agreed to WhatsApp messages:
// they asked publicly to be contacted on WhatsApp (a post with their number),
// they messaged first, they said so (a call, a meeting), or a form.
var WhatsAppOptInSources = []string{"public_request", "inbound_message", "verbal", "form"}

// WhatsAppOptInEvidence is the proof behind a recorded opt-in.
type WhatsAppOptInEvidence struct {
	Source string
	// URL is a link to the post or message where they asked; Note is the
	// rest (what they wrote, who heard it). At least one of the two.
	URL  string
	Note string
	// UserID is the member recording it (for a token, the member it acts for).
	UserID string
}

// ErrOptInEvidence is evidence that does not hold up; the message says why.
type ErrOptInEvidence struct{ Msg string }

func (e ErrOptInEvidence) Error() string { return e.Msg }

// Validate trims ev and checks it is evidence: a known source, and a link or a
// note to show for it — a public request needs the link, since the post is
// the proof.
func (ev *WhatsAppOptInEvidence) Validate() error {
	ev.Source, ev.URL, ev.Note = strings.TrimSpace(ev.Source), strings.TrimSpace(ev.URL), strings.TrimSpace(ev.Note)
	if !slices.Contains(WhatsAppOptInSources, ev.Source) {
		return ErrOptInEvidence{"source must be one of " + strings.Join(WhatsAppOptInSources, ", ")}
	}
	if ev.URL != "" {
		u, err := url.Parse(ev.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return ErrOptInEvidence{"evidence_url must be an http(s) link"}
		}
		if len(ev.URL) > 2000 {
			return ErrOptInEvidence{"evidence_url is at most 2000 characters"}
		}
	}
	if len([]rune(ev.Note)) > 1000 {
		return ErrOptInEvidence{"note is at most 1000 characters"}
	}
	switch {
	case ev.Source == "public_request" && ev.URL == "":
		return ErrOptInEvidence{"a public_request opt-in needs evidence_url: the link to the post where they asked to be contacted on WhatsApp"}
	case ev.URL == "" && ev.Note == "":
		return ErrOptInEvidence{"an opt-in needs evidence: evidence_url, note, or both"}
	}
	return nil
}

// RecordWhatsAppOptInWithEvidence records an opt-in with how it was given and
// the proof. Like RecordWhatsAppOptIn it refuses a contact who said STOP
// (ErrWhatsAppOptedOut): only the explicit reversal lets them back in.
//
// The time is set to now even for a contact already opted in: the time, the
// source and the evidence describe the same act of recording. The earlier
// values stay in the audit log (contacts is audited).
func RecordWhatsAppOptInWithEvidence(ctx context.Context, db *sql.DB, wsID, contactID string, ev WhatsAppOptInEvidence) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	var by any
	if ev.UserID != "" {
		by = ev.UserID
	}
	// One statement, conditioned on the STOP, so a STOP that lands between a
	// read and this write still wins.
	res, err := db.ExecContext(ctx, `
		UPDATE contacts SET whatsapp_opt_in_at = now(), whatsapp_opt_in_source = $3, whatsapp_opt_in_evidence_url = $4,
		       whatsapp_opt_in_note = $5, whatsapp_opt_in_by = $6, updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND whatsapp_opt_out_at IS NULL`,
		wsID, contactID, ev.Source, ev.URL, ev.Note, by)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var exists bool
	if err := db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM contacts WHERE workspace_id = $1 AND id = $2)`, wsID, contactID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNoSuchContact
	}
	return ErrWhatsAppOptedOut
}

// WhatsAppOptInRecord is a contact's recorded opt-in as the contact page and
// the MCP tool show it. Source is empty for an opt-in recorded without one.
type WhatsAppOptInRecord struct {
	At          *time.Time `json:"at"`
	Source      string     `json:"source" doc:"public_request | inbound_message | verbal | form; empty when recorded without one"`
	EvidenceURL string     `json:"evidence_url"`
	Note        string     `json:"note"`
	By          string     `json:"by" doc:"The email of the member who recorded it; empty when unknown"`
}

// WhatsAppOptInOf reads a contact's recorded opt-in. The member's email comes
// from the owner pool (the app role has no grant on users), confined to this
// workspace's members.
func WhatsAppOptInOf(ctx context.Context, a *app.App, wsID, contactID string) (*WhatsAppOptInRecord, error) {
	var r WhatsAppOptInRecord
	var at sql.NullTime
	var by sql.NullString
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT whatsapp_opt_in_at, whatsapp_opt_in_source, whatsapp_opt_in_evidence_url, whatsapp_opt_in_note, whatsapp_opt_in_by
		FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, contactID,
	).Scan(&at, &r.Source, &r.EvidenceURL, &r.Note, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSuchContact
	}
	if err != nil {
		return nil, err
	}
	if at.Valid {
		r.At = &at.Time
	}
	if by.Valid && a.SysDB != nil {
		err := a.SysDB.QueryRowContext(ctx, `
			SELECT u.email FROM users u JOIN workspace_members m ON m.user_id = u.id
			WHERE m.workspace_id = $1 AND u.id = $2`, wsID, by.String).Scan(&r.By)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return &r, nil
}

// SetWhatsAppOptIn records that a contact agreed to be messaged on WhatsApp.
//
// It will NOT override an opt-out. Someone who said STOP has to be re-added
// deliberately (ClearWhatsAppOptOut), not by a form quietly re-ticking a box on
// their behalf — which is exactly how an opt-out gets lost.
func SetWhatsAppOptIn(ctx context.Context, db *sql.DB, wsID, contactID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE contacts SET whatsapp_opt_in_at = coalesce(whatsapp_opt_in_at, now()), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND whatsapp_opt_out_at IS NULL`, wsID, contactID)
	return err
}

// SetWhatsAppOptOut records a STOP. The opt-in is left in place: "they never
// agreed" and "they asked us to stop" are different facts, and flattening them
// loses the second one.
func SetWhatsAppOptOut(ctx context.Context, db *sql.DB, wsID, contactID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE contacts SET whatsapp_opt_out_at = coalesce(whatsapp_opt_out_at, now()), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, contactID)
	return err
}

// ClearWhatsAppOptOut lets a contact back in. Explicit by design: only a
// deliberate action reverses a STOP.
func ClearWhatsAppOptOut(ctx context.Context, db *sql.DB, wsID, contactID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE contacts SET whatsapp_opt_out_at = NULL, whatsapp_opt_in_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, contactID)
	return err
}

/*
stopWords are the replies that mean "stop".

Both languages, because the product is bilingual and somebody who received an
Arabic broadcast will answer in Arabic. Matched on the whole trimmed message,
not as a substring: "don't stop sending these" must not unsubscribe anybody.
*/
var stopWords = map[string]bool{
	"stop": true, "unsubscribe": true, "end": true, "quit": true, "cancel": true,
	"إيقاف": true, "توقف": true, "الغاء": true, "إلغاء": true, "ايقاف": true,
}

// IsStopWord reports whether an inbound message is a request to stop.
func IsStopWord(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.Trim(t, ".!،؟?")
	return stopWords[strings.TrimSpace(t)]
}

// StopConfirmation is what the contact is told, once, in their language.
func StopConfirmation(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "ar") {
		return "تم. لن نرسل إليك رسائل أخرى على واتساب."
	}
	return "Done. You will not receive any more WhatsApp messages from us."
}

// HandleStopWord honours a STOP from an inbound WhatsApp message.
//
// It matches the contact by phone, records the opt-out, and reports whether it
// did — so the caller can answer once, confirming it. It returns false for a
// message that was not a stop word, and for a phone nobody in this workspace
// knows.
func HandleStopWord(ctx context.Context, a *app.App, wsID, phone, text string) (bool, error) {
	if !IsStopWord(text) {
		return false, nil
	}

	sctx := tenant.WithWorkspace(ctx, wsID)
	digits := OnlyDigits(phone)
	if digits == "" {
		return false, nil
	}

	// Matched on the digits, because the same number is written a dozen ways
	// and the one that arrives from WhatsApp is rarely the one that was typed
	// into the CRM.
	var contactID, locale string
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT id, locale FROM contacts
		WHERE workspace_id = $1
		  AND regexp_replace(coalesce(phone, ''), '[^0-9]', '', 'g') = $2
		LIMIT 1`, wsID, digits).Scan(&contactID, &locale)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := SetWhatsAppOptOut(sctx, a.SQLDB, wsID, contactID); err != nil {
		return false, err
	}
	PublishContacts(a, wsID)
	return true, nil
}

// OnlyDigits strips a phone number down to what can be compared.
func OnlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NormalizePhone puts a phone number in E.164 (a leading +, nothing but
// digits after it) — the shape contacts.phone is stored in (MH-561) and the
// one OnlyDigits already assumes when comparing two numbers. "" in, "" out;
// anything left that is not 8-15 digits (E.164's own range) is refused rather
// than stored malformed.
func NormalizePhone(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	digits := OnlyDigits(raw)
	if len(digits) < 8 || len(digits) > 15 {
		return "", errors.New("phone numbers are 8-15 digits, with the country code")
	}
	return "+" + digits, nil
}

// StartWhatsAppSend broadcasts a campaign and returns as soon as it is
// accepted, the same shape as the email path.
//
// The number is resolved BEFORE anything is marked sending, so a workspace with
// no linked WhatsApp is told now rather than discovering it halfway through a
// list with some contacts already messaged.
func StartWhatsAppSend(ctx context.Context, a *app.App, wsID, campaignID string) error {
	// The number it sends from: its own pick, else the default. A picked number
	// that has since been unlinked stops it here, never sends from another.
	var linkID string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT whatsapp_link_id FROM campaigns WHERE workspace_id = $1 AND id = $2`,
		wsID, campaignID).Scan(&linkID)
	if linkID != "" {
		ctx = whatsapp.WithLink(ctx, linkID)
	}
	l, err := whatsapp.Load(ctx, a, wsID)
	if err != nil {
		return ErrNoWhatsApp
	}
	linkID = l.ID
	// Over the stated limit is refused before the claim (MH-467).
	if err := checkAudience(ctx, a.SQLDB, wsID, campaignID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	var listID, bodyText, locale, audienceLocale string
	err = a.SQLDB.QueryRowContext(ctx, `
		UPDATE campaigns SET status = 'sending', sent_count = 0, failed_count = 0, last_error = '', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND channel = 'whatsapp' AND `+claimable+`
		RETURNING list_id, body, locale, audience_locale`, wsID, campaignID).Scan(&listID, &bodyText, &locale, &audienceLocale)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotSendable
	}
	if err != nil {
		return err
	}

	audience, err := WhatsAppAudienceFor(ctx, a.SQLDB, wsID, listID, audienceLocale)
	if err != nil {
		_, _ = a.SQLDB.ExecContext(ctx, `
			UPDATE campaigns SET status = 'failed', last_error = 'could not read recipients', updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, campaignID)
		return err
	}
	// Grew past the limit between the check and the claim: fail, never truncate.
	if len(audience) > MaxSendAudience {
		tooLarge := &AudienceTooLargeError{Audience: int64(len(audience)), Max: MaxSendAudience}
		_, _ = a.SQLDB.ExecContext(ctx, `
			UPDATE campaigns SET status = 'failed', last_error = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, tooLarge.Error())
		return tooLarge
	}
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE campaigns SET recipient_count = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, int64(len(audience)))
	PublishCampaigns(a, wsID, campaignID)

	go func() {
		bg := whatsapp.WithLink(tenant.WithWorkspace(context.Background(), wsID), linkID)
		var sent, failed int64
		lastErr := ""

		for i, r := range audience {
			// The same text the preview shows (campaign_edit.go).
			text := WhatsAppText(locale, bodyText)
			if _, err := whatsapp.SendAs(bg, a, wsID, r.Phone, text, whatsapp.KindBroadcast); errors.Is(err, whatsapp.ErrDailyCap) {
				// The number's daily limit is reached: everyone left is not
				// sent today, rather than tried one by one against the cap.
				failed += int64(len(audience) - i)
				lastErr = err.Error()
				break
			} else if err != nil {
				failed++
				lastErr = err.Error()
			} else {
				sent++
			}

			// Paced. A burst of messages from one number is what automated-abuse
			// detection looks for, and the cost of being wrong is the
			// number itself.
			if i < len(audience)-1 {
				time.Sleep(whatsappPace)
			}
			if (i+1)%20 == 0 {
				_, _ = a.SQLDB.ExecContext(bg, `
					UPDATE campaigns SET sent_count = $3, failed_count = $4, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, sent, failed)
				PublishCampaigns(a, wsID, campaignID)
			}
		}

		status := "sent"
		if sent == 0 && failed > 0 {
			status = "failed"
		}
		_, _ = a.SQLDB.ExecContext(bg, `
			UPDATE campaigns SET status = $3, sent_count = $4, failed_count = $5, last_error = $6,
			  sent_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, status, sent, failed, trimErr(lastErr))
		PublishCampaigns(a, wsID, campaignID)
	}()

	return nil
}

// ErrNotSendable is a campaign that is not a WhatsApp draft.
var ErrNotSendable = errors.New("this campaign is not a WhatsApp draft")

// whatsappPace is the gap between messages in a broadcast.
const whatsappPace = whatsapp.Pace

// stopHint is the way out, on every broadcast. Email has a header for this;
// WhatsApp has only the message itself, so it has to be in the text.
func stopHint(locale string) string {
	if strings.HasPrefix(strings.ToLower(locale), "ar") {
		return "للإيقاف، أرسل: إيقاف"
	}
	return "To stop these messages, reply STOP."
}

func trimErr(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}
