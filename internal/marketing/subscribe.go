package marketing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

/*
Subscribing somebody to a list — the one copy of it (MH-318).

Both the workspace endpoint (a person adding a contact) and the public form (a
visitor typing their address on a customer's website) come through here. That
is deliberate: consent is the part of this system it would be worst to get
subtly different in two places, and "the public form skipped double opt-in"
is exactly the kind of difference that only shows up in a spam complaint.

The rules, in one place:

  - An address that is already subscribed is left alone. Re-adding it must not
    reset it to pending and make a subscriber confirm again.
  - An address that is pending, unsubscribed or bounced goes back to pending
    with a FRESH token. A re-add must not quietly skip consent, and the old
    token must stop working.
  - A list with double opt-in off subscribes immediately. That is the list
    owner's decision to have made, and it is recorded on the list.
*/

// SubscribeResult is what happened, in terms the caller can act on.
type SubscribeResult struct {
	ContactID string
	Status    string
	Token     string
	ListName  string
	// NeedsConfirmation is true when a confirmation email should be sent for
	// this token. It is false for an address that was already subscribed, so a
	// re-submission does not mail somebody who is already in.
	NeedsConfirmation bool
	// AlreadySubscribed is true when nothing changed. The PUBLIC endpoint must
	// not reveal this to the visitor — see the note there — but the workspace
	// UI shows it.
	AlreadySubscribed bool
}

// ErrNoSuchList is returned when the list does not exist in this workspace.
var ErrNoSuchList = errors.New("list not found")

// ConfirmationCooldown is how often one address can be sent a confirmation for
// the same list.
//
// This exists because the public form makes the endpoint a way to send email to
// an address the CALLER chose. Without it, replaying one submission is a way to
// mail-bomb a stranger from a domain they trust. The row is still updated; only
// the sending is held back.
const ConfirmationCooldown = 5 * time.Minute

// Subscribe adds or re-subscribes an address, applying the list's opt-in rule.
//
// db must already be scoped to the workspace (tenant.WithWorkspace on the
// context, or the owner pool with workspace_id supplied) — this function does
// not decide which workspace it is in, because the public caller resolves that
// from a key and the workspace caller from a session.
func Subscribe(ctx context.Context, db *sql.DB, wsID, listID, email, name, locale, source string) (SubscribeResult, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if source == "" {
		source = "manual"
	}

	var res SubscribeResult
	var doubleOptIn bool
	err := db.QueryRowContext(ctx,
		`SELECT name, double_opt_in FROM mail_lists WHERE workspace_id = $1 AND id = $2`,
		wsID, listID).Scan(&res.ListName, &doubleOptIn)
	if errors.Is(err, sql.ErrNoRows) {
		return res, ErrNoSuchList
	}
	if err != nil {
		return res, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback() //nolint:errcheck

	if err := tx.QueryRowContext(ctx, `
		INSERT INTO contacts (workspace_id, email, name, locale, source) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, email) DO UPDATE SET
		  name = CASE WHEN EXCLUDED.name = '' THEN contacts.name ELSE EXCLUDED.name END,
		  updated_at = now()
		RETURNING id`, wsID, email, name, locale, source).Scan(&res.ContactID); err != nil {
		return res, err
	}

	var subID, status, token string
	var updated time.Time
	err = tx.QueryRowContext(ctx, `
		SELECT id, status, token, updated_at FROM list_subscriptions
		WHERE workspace_id = $1 AND list_id = $2 AND contact_id = $3`,
		wsID, listID, res.ContactID).Scan(&subID, &status, &token, &updated)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		status = StatusPending
		if !doubleOptIn {
			status = StatusSubscribed
		}
		token = newToken()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO list_subscriptions (workspace_id, list_id, contact_id, status, token, confirmed_at)
			VALUES ($1, $2, $3, $4, $5, CASE WHEN $4 = 'subscribed' THEN now() END)`,
			wsID, listID, res.ContactID, status, token); err != nil {
			return res, err
		}
		res.NeedsConfirmation = status == StatusPending

	case err != nil:
		return res, err

	case status == StatusSubscribed:
		// Already in. Nothing changes, and no email goes out — re-submitting a
		// form must not mail a subscriber.
		res.AlreadySubscribed = true

	default:
		// Pending, unsubscribed or bounced. Back to pending with a fresh token:
		// a re-add must not skip consent, and the old link must stop working.
		wasPending := status == StatusPending
		status = StatusPending
		if !doubleOptIn {
			status = StatusSubscribed
		}
		token = newToken()
		if _, err := tx.ExecContext(ctx, `
			UPDATE list_subscriptions SET status = $3, token = $4, unsubscribed_at = NULL, unsubscribed_by = NULL,
			  confirmed_at = CASE WHEN $3 = 'subscribed' THEN now() ELSE confirmed_at END, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, wsID, subID, status, token); err != nil {
			return res, err
		}
		res.NeedsConfirmation = status == StatusPending
		// The cooldown only applies to an address that was ALREADY waiting to
		// confirm: that is the case someone can replay. A previously
		// unsubscribed address asking to come back gets its email immediately.
		if wasPending && time.Since(updated) < ConfirmationCooldown {
			res.NeedsConfirmation = false
		}
	}

	if err := tx.Commit(); err != nil {
		return res, err
	}
	res.Status, res.Token = status, token
	return res, nil
}
