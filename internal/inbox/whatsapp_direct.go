package inbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Messaging a CRM contact on WhatsApp first (MH-409).

A reply answers somebody who wrote in; this starts or continues a thread from
the workspace's side, so it carries the MH-320 rule in full: the contact must
have opted in to WhatsApp (whatsapp_opt_in_at) and must not have asked to stop
(whatsapp_opt_out_at). Email consent is not WhatsApp consent, and the absence
of an opt-in is never one.

A message to a contact who has no WhatsApp thread yet is the agent reaching out
to a customer unprompted, so it takes two calls, the way fadymondy.com's
whatsapp_send does: the first returns a one-time code bound to that contact and
that exact text; only the same call repeated with the code sends it. A contact
with a thread already continues it in one call.

Sends go through whatsapp.SendAs, so they are paced with every other message
from the number (replies and broadcasts alike) and counted in the day's log.
*/

// DirectConfirmTTL is how long a send code stays usable.
const DirectConfirmTTL = 10 * time.Minute

// maxWhatsAppText is WhatsApp's own limit on one text message.
const maxWhatsAppText = 4096

// DirectSend is the outcome of SendWhatsAppToContact.
type DirectSend struct {
	// ConfirmationRequired: nothing was sent. Repeat the call with Code.
	ConfirmationRequired bool       `json:"confirmation_required"`
	Code                 string     `json:"code,omitempty"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	Summary              string     `json:"summary"`
	// Sent: the message went out and is on ConversationID's thread.
	Sent           bool        `json:"sent"`
	NewThread      bool        `json:"new_thread"`
	ConversationID string      `json:"conversation_id,omitempty"`
	Message        *MessageOut `json:"message,omitempty"`
	ContactID      string      `json:"contact_id"`
}

// ErrDirectCode covers unknown, expired, used and mismatched codes alike: the
// answer does not say which, so a guesser learns nothing.
var ErrDirectCode = errors.New("that code is not valid for this contact and text, has expired or was already used")

// SendWhatsAppToContact sends text to one CRM contact on WhatsApp, as the
// caller. code is empty on the first call for a new thread.
func SendWhatsAppToContact(ctx context.Context, a *app.App, contactID, text, code string) (*DirectSend, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	contactID, text, code = strings.TrimSpace(contactID), strings.TrimSpace(text), strings.TrimSpace(code)
	if contactID == "" {
		return nil, huma.Error422UnprocessableEntity("contact_id is required")
	}
	if text == "" {
		return nil, huma.Error422UnprocessableEntity("a message is required")
	}
	if len([]rune(text)) > maxWhatsAppText {
		return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("a WhatsApp message is at most %d characters", maxWhatsAppText))
	}

	var name, email, phone string
	var optIn, optOut sql.NullTime
	err = a.SQLDB.QueryRowContext(ctx, `
		SELECT name, email, coalesce(phone, ''), whatsapp_opt_in_at, whatsapp_opt_out_at
		FROM contacts WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, contactID,
	).Scan(&name, &email, &phone, &optIn, &optOut)
	if notFound(err) {
		return nil, huma.Error404NotFound("contact not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the contact")
	}
	// MH-320: the consent is WhatsApp's own, and a STOP outranks it.
	switch {
	case optOut.Valid:
		return nil, huma.Error409Conflict("this contact asked to stop WhatsApp messages, so none can be sent to them")
	case !optIn.Valid:
		return nil, huma.Error409Conflict("this contact has not opted in to WhatsApp messages; email consent is not WhatsApp consent")
	}
	digits := whatsapp.Number(phone)
	if len(digits) < 8 {
		return nil, huma.Error422UnprocessableEntity("this contact has no usable phone number")
	}
	// Refuse now rather than after a code has been issued for a send that
	// could never happen.
	if !whatsapp.LoadConfig().Configured() {
		return nil, whatsappError(whatsapp.ErrNotConfigured)
	}
	if _, err := whatsapp.Load(ctx, a, s.WorkspaceID); err != nil {
		return nil, whatsappError(err)
	}

	res := &DirectSend{ContactID: contactID,
		Summary: fmt.Sprintf("send %d characters on WhatsApp to %s (+%s)", len([]rune(text)), firstNonEmpty(name, email, "the contact"), digits)}

	var convID string
	err = a.SQLDB.QueryRowContext(ctx, `
		SELECT id FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
		s.WorkspaceID, digits).Scan(&convID)
	if err != nil && !notFound(err) {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	res.NewThread = convID == ""

	if res.NewThread {
		if code == "" {
			c, exp, err := issueDirectCode(ctx, a, s.WorkspaceID, s.UserID, contactID, text)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not issue a confirmation code")
			}
			res.ConfirmationRequired, res.Code, res.ExpiresAt = true, c, &exp
			return res, nil
		}
		if err := redeemDirectCode(ctx, a, s.WorkspaceID, contactID, text, code); err != nil {
			if errors.Is(err, ErrDirectCode) {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return nil, huma.Error500InternalServerError("could not check the code")
		}
	}

	sentID, err := whatsapp.SendAs(ctx, a, s.WorkspaceID, digits, text, whatsapp.KindDirect)
	if err != nil {
		return nil, whatsappError(err)
	}
	if res.NewThread {
		// The same row FileWhatsApp would have made had they written first,
		// so their answer lands on this thread. Linked to the contact it was
		// sent to, and marked manual (MH-379): it is a member's deliberate
		// choice, which the automatic phone match must not undo.
		err := a.SQLDB.QueryRowContext(ctx, `
			INSERT INTO conversations (workspace_id, channel, customer_name, customer_email, customer_phone, contact_id, contact_link,
			                           subject, last_message_at, unread)
			VALUES ($1, 'whatsapp', $2, $3, $4, $5, 'manual', $6, now(), false)
			ON CONFLICT (workspace_id, customer_phone) WHERE customer_phone <> '' AND channel = 'whatsapp' DO NOTHING
			RETURNING id`,
			s.WorkspaceID, trimTo(name, 120), email, digits, contactID, trim(text, 80)).Scan(&convID)
		if notFound(err) {
			// They wrote in between the check and now: continue their thread.
			err = a.SQLDB.QueryRowContext(ctx, `
				SELECT id FROM conversations WHERE workspace_id = $1 AND channel = 'whatsapp' AND customer_phone = $2`,
				s.WorkspaceID, digits).Scan(&convID)
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("the message was sent, but its thread could not be opened")
		}
	}
	m, err := recordOutbound(ctx, a, s.WorkspaceID, s.UserID, s.Email, convID, text, sentID)
	if err != nil {
		return nil, err
	}
	res.Sent, res.ConversationID, res.Message = true, convID, m
	return res, nil
}

func directCodeHash(wsID, code string) string {
	sum := sha256.Sum256([]byte("whatsapp-direct:" + wsID + ":" + code))
	return hex.EncodeToString(sum[:])
}

func directTextHash(contactID, text string) string {
	sum := sha256.Sum256([]byte(contactID + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

func issueDirectCode(ctx context.Context, a *app.App, wsID, userID, contactID, text string) (string, time.Time, error) {
	exp := time.Now().Add(DirectConfirmTTL)
	for attempt := 0; attempt < 5; attempt++ {
		n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
		if err != nil {
			return "", exp, err
		}
		code := fmt.Sprintf("%06d", n.Int64())
		// A code whose predecessor expired or was used can be drawn again; a
		// live one cannot, so this re-draws on a clash.
		res, err := a.SQLDB.ExecContext(ctx, `
			INSERT INTO whatsapp_confirmations (workspace_id, code_hash, contact_id, text_hash, requested_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, code_hash) DO UPDATE SET contact_id = EXCLUDED.contact_id, text_hash = EXCLUDED.text_hash,
			  requested_by = EXCLUDED.requested_by, expires_at = EXCLUDED.expires_at, used_at = NULL, created_at = now()
			WHERE whatsapp_confirmations.expires_at < now() OR whatsapp_confirmations.used_at IS NOT NULL`,
			wsID, directCodeHash(wsID, code), contactID, directTextHash(contactID, text), userID, exp)
		if err != nil {
			return "", exp, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return code, exp, nil
		}
	}
	return "", exp, errors.New("no free code")
}

// redeemDirectCode consumes the code in one statement, so two calls racing
// with the same code cannot both send.
func redeemDirectCode(ctx context.Context, a *app.App, wsID, contactID, text, code string) error {
	if len(code) != 6 {
		return ErrDirectCode
	}
	var id string
	err := a.SQLDB.QueryRowContext(ctx, `
		UPDATE whatsapp_confirmations SET used_at = now()
		WHERE workspace_id = $1 AND code_hash = $2 AND contact_id = $3 AND text_hash = $4
		  AND used_at IS NULL AND expires_at > now()
		RETURNING id`, wsID, directCodeHash(wsID, code), contactID, directTextHash(contactID, text)).Scan(&id)
	if notFound(err) {
		return ErrDirectCode
	}
	return err
}
