package marketing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

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

// ErrNoWhatsApp is returned when the workspace has not linked a number.
var ErrNoWhatsApp = errors.New("link a WhatsApp number for this workspace before broadcasting")

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
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, c.phone, c.name `+whatsappAudienceFrom+`
		ORDER BY s.created_at
		LIMIT 50000`, wsID, listID)
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
		  AND coalesce(c.phone, '') <> ''`

// CountWhatsAppAudience is how many contacts a broadcast to a list would reach
// right now — the number shown before anyone presses send.
func CountWhatsAppAudience(ctx context.Context, db *sql.DB, wsID, listID string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT count(*) `+whatsappAudienceFrom, wsID, listID).Scan(&n)
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

// StartWhatsAppSend broadcasts a campaign and returns as soon as it is
// accepted, the same shape as the email path.
//
// The number is resolved BEFORE anything is marked sending, so a workspace with
// no linked WhatsApp is told now rather than discovering it halfway through a
// list with some contacts already messaged.
func StartWhatsAppSend(ctx context.Context, a *app.App, wsID, campaignID string) error {
	if _, err := whatsapp.Load(ctx, a, wsID); err != nil {
		return ErrNoWhatsApp
	}

	var listID, bodyText, locale string
	err := a.SQLDB.QueryRowContext(ctx, `
		UPDATE campaigns SET status = 'sending', sent_count = 0, failed_count = 0, last_error = '', updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND channel = 'whatsapp' AND status IN ('draft', 'failed')
		RETURNING list_id, body, locale`, wsID, campaignID).Scan(&listID, &bodyText, &locale)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotSendable
	}
	if err != nil {
		return err
	}

	audience, err := WhatsAppAudience(ctx, a.SQLDB, wsID, listID)
	if err != nil {
		_, _ = a.SQLDB.ExecContext(ctx, `
			UPDATE campaigns SET status = 'failed', last_error = 'could not read recipients', updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, campaignID)
		return err
	}
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE campaigns SET recipient_count = $3, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, int64(len(audience)))

	go func() {
		bg := tenant.WithWorkspace(context.Background(), wsID)
		var sent, failed int64
		lastErr := ""

		for i, r := range audience {
			text := bodyText + "\n\n" + stopHint(locale)
			if _, err := whatsapp.Send(bg, a, wsID, r.Phone, text); err != nil {
				failed++
				lastErr = err.Error()
			} else {
				sent++
			}

			// Paced. A burst of messages from one number is what automated-abuse
			// detection looks for, and the cost of being wrong is the
			// workspace's only number.
			if i < len(audience)-1 {
				time.Sleep(whatsappPace)
			}
			if (i+1)%20 == 0 {
				_, _ = a.SQLDB.ExecContext(bg, `
					UPDATE campaigns SET sent_count = $3, failed_count = $4, updated_at = now()
					WHERE workspace_id = $1 AND id = $2`, wsID, campaignID, sent, failed)
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
	}()

	return nil
}

// ErrNotSendable is a campaign that is not a WhatsApp draft.
var ErrNotSendable = errors.New("this campaign is not a WhatsApp draft")

// whatsappPace is the gap between messages in a broadcast.
const whatsappPace = 1200 * time.Millisecond

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
