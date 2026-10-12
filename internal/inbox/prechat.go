package inbox

/*
The chat widget's pre-chat form: who the visitor is, before the first message.

A chat that ends with nothing but a browser token cannot be followed up once
the visitor closes the tab. So a widget with require_contact (the default)
refuses to OPEN a conversation without a name and an email or a phone number
with its country code. Continuing a thread (visitor_token) never asks again:
the conversation already holds what was given, and the widget remembers it on
the visitor's browser so a returning visitor is not asked twice.

What they gave is stored on the conversation (customer_name / _email /
_phone) and filed in the CRM: an existing contact with that email or number is
linked, otherwise one is created. contacts.FindOrCreate, not Upsert — a
visitor proves nothing about owning the address they typed, so an existing
contact is only linked, never edited or merged. The link is 'auto', which a
member can undo like any other automatic link.

The API stays backwards compatible: the same fields as before plus an
optional phone, the same answer. A widget with require_contact off behaves
exactly as it did.
*/

import (
	"regexp"
	"context"
	"database/sql"
	"net/mail"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/contacts"
	"github.com/fadymondy/moharrik/internal/marketing"
)

// visitorContact is what the pre-chat form gave, normalised: the email
// lower-cased, the phone as +digits.
type visitorContact struct {
	Name, Email, Phone string
}

func (v visitorContact) any() bool { return v.Email != "" || v.Phone != "" }

// checkVisitorContact validates what a visitor gave when opening a
// conversation. With required set, a name and a valid email or phone are a
// must; without it, whatever is valid is kept and the rest dropped, which is
// what the widget did before the form existed (bar the phone, which is new).
func checkVisitorContact(name, email, phone string, required bool) (visitorContact, error) {
	v := visitorContact{Name: strings.Join(strings.Fields(name), " ")}
	if len([]rune(v.Name)) > 120 {
		v.Name = string([]rune(v.Name)[:120])
	}

	email = strings.TrimSpace(email)
	emailOK := false
	if email != "" {
		if a, err := mail.ParseAddress(email); err == nil && a.Address == email && len(email) <= 254 && visitorEmail.MatchString(email) {
			v.Email, emailOK = strings.ToLower(email), true
		}
	}
	phone = asciiDigits(strings.TrimSpace(phone))
	phoneOK := false
	if phone != "" {
		// "With its country code": a leading + or 00. A local number alone
		// cannot be messaged on WhatsApp from anywhere.
		intl := strings.HasPrefix(phone, "+") || strings.HasPrefix(phone, "00")
		if strings.HasPrefix(phone, "00") {
			phone = "+" + phone[2:]
		}
		if p, err := marketing.NormalizePhone(phone); err == nil && p != "" && intl {
			v.Phone, phoneOK = p, true
		}
	}

	if !required {
		if !emailOK {
			// As before the form: an address that does not parse is still the
			// visitor's words, kept for whoever answers; it just never links.
			v.Email = strings.TrimSpace(email)
		}
		return v, nil
	}
	if v.Name == "" {
		return v, huma.Error422UnprocessableEntity("contact_required: your name is required")
	}
	if email != "" && !emailOK {
		return v, huma.Error422UnprocessableEntity("contact_required: that email address is not valid")
	}
	if phone != "" && !phoneOK {
		return v, huma.Error422UnprocessableEntity("contact_required: give the phone number with its country code, e.g. +20 100 123 4567")
	}
	if !v.any() {
		return v, huma.Error422UnprocessableEntity("contact_required: an email or a phone number is required")
	}
	return v, nil
}

// linkChatContact files the visitor in the CRM and links the conversation to
// them, unless a member already linked it. Only for a validated form
// (require_contact): with the form off, chat stays unlinked as it always was. ctx must carry the workspace (the
// app pool's RLS). Best effort for the visitor: a CRM failure never loses
// their message, it only leaves the thread unlinked for a member to link.
// visitorEmail is a plain address — a dot-atom, a dotted domain, a letter TLD —
// the same shape the widget checks before it sends (chat-sdk checkVisitor).
var visitorEmail = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(?:[.][A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+)*@(?:[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?[.])+[A-Za-z]{2,}$")

func linkChatContact(ctx context.Context, db *sql.DB, wsID, convID string, v visitorContact, locale string) (string, error) {
	if !v.any() {
		return "", nil
	}
	if !strings.HasPrefix(strings.ToLower(locale), "ar") {
		locale = "en"
	} else {
		locale = "ar"
	}
	link := func() (string, error) {
		res, err := contacts.FindOrCreate(ctx, db, wsID, contacts.Candidate{
			Email: v.Email, Phone: v.Phone, Name: v.Name, Locale: locale, Source: "chat",
		})
		if err != nil {
			return "", err
		}
		_, err = db.ExecContext(ctx, `
			UPDATE conversations SET contact_id = $3, contact_link = 'auto', updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND contact_id IS NULL AND contact_link <> 'manual'`, wsID, convID, res.ContactID)
		return res.ContactID, err
	}
	id, err := link()
	if err != nil {
		// The contact found may be deleted or merged before the link lands
		// (a foreign key miss): look again, once.
		id, err = link()
	}
	return id, err
}
