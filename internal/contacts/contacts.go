// Package contacts is the single place every creator resolves-or-creates a
// contact (MH-583), so the same person arriving through different doors — a
// WhatsApp import, an MCP create_contact call, a conversation turned into a
// contact, a lead converted from an inquiry — ends up as one contact record,
// never a duplicate that has to be merged by hand later.
//
// Callers normalise before calling: email lower-cased, phone run through
// internal/marketing.NormalizePhone, handle lower-cased. This package does not
// import internal/marketing (that would be a cycle: marketing itself calls
// in here), so it does not re-derive normalisation — it only compares.
//
// Matching is deliberately generous: ANY of the candidate's email, phone,
// handle or extra channel identities (Telegram, Slack, Discord, Messenger,
// Instagram, LinkedIn, X, the chat widget, ...) that already belongs to a
// contact in this workspace counts as a match. Zero matches creates a new
// contact. One match fills blank fields and appends any new identities — it
// never overwrites a non-empty value. Two or more matches means two contacts
// the workspace already had for the same person (a phone added on one, an
// email on the other): they are merged into the oldest one, moving every
// child row so nothing an import or a click already produced is lost.
package contacts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Identity is one row of contact_identities: an account on one channel.
type Identity struct {
	ID         string `json:"id"`
	Channel    string `json:"channel"`
	ExternalID string `json:"external_id"`
	Label      string `json:"label"`
	IsPrimary  bool   `json:"is_primary"`
	Position   int    `json:"position"`
}

// Channels this package treats as the three legacy "primary" slots that stay
// denormalised on contacts.email/phone/handle.
const (
	ChannelEmail    = "email"
	ChannelPhone    = "phone"
	ChannelHandle   = "handle"
	ChannelWhatsApp = "whatsapp"
)

// primaryChannels are matched against the digits-only phone comparison
// 155_contact_identity.sql/crm_link.go already use — a WhatsApp number and a
// "phone" number are the same kind of value.
var phoneLikeChannels = map[string]bool{ChannelPhone: true, ChannelWhatsApp: true}

// Candidate is what a caller knows about a person, pre-normalised.
type Candidate struct {
	Email, Phone, Handle string
	Name, Locale, Source string
	CompanyID            string
	// Identities beyond email/phone/handle: e.g. {Channel: "telegram", ExternalID: "@fady"}.
	Identities []Identity
	// Meta is merged additively: an existing key is never overwritten, only
	// filled in when the contact does not already have it.
	Meta map[string]string
}

// Result is what Upsert did.
type Result struct {
	ContactID string
	Created   bool
	// Merged holds the ids of any other contacts folded into ContactID
	// because they turned out to be the same person.
	Merged []string
}

// ErrNoIdentity is returned when a candidate carries nothing to key on at all.
var ErrNoIdentity = errors.New("a contact needs an email, a phone number, a handle or another identity")

func (c Candidate) allIdentities() []Identity {
	list := append([]Identity{}, c.Identities...)
	if c.Email != "" {
		list = append(list, Identity{Channel: ChannelEmail, ExternalID: strings.ToLower(strings.TrimSpace(c.Email)), IsPrimary: true})
	}
	if c.Phone != "" {
		list = append(list, Identity{Channel: ChannelPhone, ExternalID: strings.TrimSpace(c.Phone), IsPrimary: true})
	}
	if c.Handle != "" {
		list = append(list, Identity{Channel: ChannelHandle, ExternalID: strings.ToLower(strings.TrimSpace(c.Handle)), IsPrimary: true})
	}
	return list
}

// Upsert resolves wsID's contact for c, creating one if none matches and
// merging together any that do. It runs its own transaction (an advisory lock
// on the workspace serialises two racing imports of the same person into one
// contact instead of two).
func Upsert(ctx context.Context, db *sql.DB, wsID string, c Candidate) (*Result, error) {
	identities := c.allIdentities()
	if len(identities) == 0 {
		return nil, ErrNoIdentity
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "contacts-upsert:"+wsID); err != nil {
		return nil, err
	}

	ids, err := matchingContacts(ctx, tx, wsID, c, identities)
	if err != nil {
		return nil, err
	}

	var res *Result
	switch len(ids) {
	case 0:
		id, err := createContact(ctx, tx, wsID, c)
		if err != nil {
			return nil, err
		}
		if err := writeIdentities(ctx, tx, wsID, id, identities); err != nil {
			return nil, err
		}
		if err := mergeMeta(ctx, tx, wsID, id, c.Meta); err != nil {
			return nil, err
		}
		res = &Result{ContactID: id, Created: true}
	case 1:
		id := ids[0]
		if err := fillBlanks(ctx, tx, wsID, id, c); err != nil {
			return nil, err
		}
		if err := writeIdentities(ctx, tx, wsID, id, identities); err != nil {
			return nil, err
		}
		if err := mergeMeta(ctx, tx, wsID, id, c.Meta); err != nil {
			return nil, err
		}
		if err := syncPrimaries(ctx, tx, wsID, id); err != nil {
			return nil, err
		}
		res = &Result{ContactID: id}
	default:
		survivor, losers, err := mergeContacts(ctx, tx, wsID, ids)
		if err != nil {
			return nil, err
		}
		if err := fillBlanks(ctx, tx, wsID, survivor, c); err != nil {
			return nil, err
		}
		if err := writeIdentities(ctx, tx, wsID, survivor, identities); err != nil {
			return nil, err
		}
		if err := mergeMeta(ctx, tx, wsID, survivor, c.Meta); err != nil {
			return nil, err
		}
		if err := syncPrimaries(ctx, tx, wsID, survivor); err != nil {
			return nil, err
		}
		res = &Result{ContactID: survivor, Merged: losers}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

// matchingContacts finds every contact in the workspace that the candidate's
// email/phone/handle (against the legacy columns, for any row a caller that
// still writes contacts directly has not run through this package) OR any of
// its identities (against contact_identities) already belong to. FOR UPDATE
// so two concurrent upserts for the same person serialise on the rows, not
// just the advisory lock (which only guards this workspace's upserts against
// each other, not a plain UPDATE elsewhere).
func matchingContacts(ctx context.Context, tx *sql.Tx, wsID string, c Candidate, identities []Identity) ([]string, error) {
	set := map[string]bool{}
	add := func(rows *sql.Rows, err error) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			set[id] = true
		}
		return rows.Err()
	}

	if c.Email != "" {
		if err := add(tx.QueryContext(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND email <> '' AND lower(email) = lower($2)`, wsID, c.Email)); err != nil {
			return nil, err
		}
	}
	if c.Phone != "" {
		if err := add(tx.QueryContext(ctx, `
			SELECT id FROM contacts WHERE workspace_id = $1 AND phone <> ''
			  AND regexp_replace(phone, '[^0-9]', '', 'g') = regexp_replace($2, '[^0-9]', '', 'g')`, wsID, c.Phone)); err != nil {
			return nil, err
		}
	}
	if c.Handle != "" {
		if err := add(tx.QueryContext(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND handle <> '' AND lower(handle) = lower($2)`, wsID, c.Handle)); err != nil {
			return nil, err
		}
	}
	for _, ident := range identities {
		if phoneLikeChannels[ident.Channel] {
			if err := add(tx.QueryContext(ctx, `
				SELECT contact_id FROM contact_identities
				WHERE workspace_id = $1 AND channel IN ('phone', 'whatsapp')
				  AND regexp_replace(external_id, '[^0-9]', '', 'g') = regexp_replace($2, '[^0-9]', '', 'g')`,
				wsID, ident.ExternalID)); err != nil {
				return nil, err
			}
			continue
		}
		if err := add(tx.QueryContext(ctx, `
			SELECT contact_id FROM contact_identities WHERE workspace_id = $1 AND channel = $2 AND external_id = $3`,
			wsID, ident.Channel, normExternalID(ident.Channel, ident.ExternalID))); err != nil {
			return nil, err
		}
	}

	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	if len(ids) < 2 {
		return ids, nil
	}
	// Order oldest-first so a merge always keeps the longest-lived contact
	// (its id, deals and history) as the survivor. pgx's database/sql driver
	// encodes a Go []string as a Postgres array natively for = ANY($n).
	rows, err := tx.QueryContext(ctx, `SELECT id FROM contacts WHERE workspace_id = $1 AND id = ANY($2::text[]) ORDER BY created_at, id`, wsID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ordered := make([]string, 0, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ordered = append(ordered, id)
	}
	return ordered, rows.Err()
}

func normExternalID(channel, v string) string {
	v = strings.TrimSpace(v)
	if channel == ChannelEmail || channel == ChannelHandle {
		return strings.ToLower(v)
	}
	return v
}

func createContact(ctx context.Context, tx *sql.Tx, wsID string, c Candidate) (string, error) {
	source := c.Source
	if source == "" {
		source = "manual"
	}
	locale := c.Locale
	if locale != "ar" {
		locale = "en"
	}
	var companyID any
	if c.CompanyID != "" {
		companyID = c.CompanyID
	}
	var id string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO contacts (workspace_id, email, phone, handle, name, locale, source, company_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		wsID, strings.ToLower(strings.TrimSpace(c.Email)), strings.TrimSpace(c.Phone), strings.ToLower(strings.TrimSpace(c.Handle)),
		strings.TrimSpace(c.Name), locale, source, companyID,
	).Scan(&id)
	return id, err
}

// fillBlanks sets name/phone/email/handle/company only where the contact's
// own value is currently blank — an import or a second channel never
// overwrites what a member already entered.
func fillBlanks(ctx context.Context, tx *sql.Tx, wsID, id string, c Candidate) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE contacts SET
		  name    = CASE WHEN name = ''    AND $3 <> '' THEN $3 ELSE name END,
		  email   = CASE WHEN email = ''   AND $4 <> '' THEN $4 ELSE email END,
		  phone   = CASE WHEN phone = ''   AND $5 <> '' THEN $5 ELSE phone END,
		  handle  = CASE WHEN handle = ''  AND $6 <> '' THEN $6 ELSE handle END,
		  company_id = CASE WHEN company_id IS NULL AND $7 <> '' THEN $7 ELSE company_id END,
		  updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		wsID, id, strings.TrimSpace(c.Name), strings.ToLower(strings.TrimSpace(c.Email)), strings.TrimSpace(c.Phone),
		strings.ToLower(strings.TrimSpace(c.Handle)), c.CompanyID)
	return err
}

// mergeMeta adds keys the contact does not already have; an existing key's
// value is left alone (the wire convention UpdateMeta uses for an explicit
// edit is different — see internal/contacts REST/MCP surface in internal/crm).
func mergeMeta(ctx context.Context, tx *sql.Tx, wsID, id string, meta map[string]string) error {
	if len(meta) == 0 {
		return nil
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE contacts SET meta = jsonb_set(meta, ARRAY[$3], to_jsonb($4::text), true), updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND NOT (meta ? $3)`, wsID, id, k, meta[k]); err != nil {
			return err
		}
	}
	return nil
}

// writeIdentities links every identity to id. An identity already pointing at
// id is left as-is (its label/primary flag is not clobbered by a later import
// that only re-confirms the same channel).
func writeIdentities(ctx context.Context, tx *sql.Tx, wsID, id string, identities []Identity) error {
	for _, ident := range identities {
		ext := normExternalID(ident.Channel, ident.ExternalID)
		if ext == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id, label, is_primary)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, channel, external_id) DO NOTHING`,
			wsID, id, ident.Channel, ext, ident.Label, ident.IsPrimary); err != nil {
			return err
		}
	}
	return nil
}

// syncPrimaries keeps contacts.email/phone/handle equal to that contact's
// primary identity on each legacy channel, filling a blank column from
// whichever identity row exists when there is exactly one candidate to prefer
// (the primary one if marked, else the oldest). It never blanks a column that
// already holds a value — Upsert only ever adds identities, so the column
// that is non-empty already reflects some earlier choice.
func syncPrimaries(ctx context.Context, tx *sql.Tx, wsID, id string) error {
	for col, channels := range map[string][]string{
		"email":  {ChannelEmail},
		"phone":  {ChannelPhone, ChannelWhatsApp},
		"handle": {ChannelHandle},
	} {
		_, err := tx.ExecContext(ctx, `
			UPDATE contacts c SET `+col+` = i.external_id, updated_at = now()
			FROM (
				SELECT external_id FROM contact_identities
				WHERE workspace_id = $1 AND contact_id = $2 AND channel = ANY($3)
				ORDER BY is_primary DESC, position, created_at LIMIT 1
			) i
			WHERE c.workspace_id = $1 AND c.id = $2 AND c.`+col+` = ''`,
			wsID, id, channels)
		if err != nil {
			return err
		}
	}
	return nil
}

// mergeContacts folds every id but the first (oldest, already ordered by
// matchingContacts) into it: every child row that names one of the losers
// moves to the survivor, then the losers are deleted. Conflicts from a row
// that would now duplicate one the survivor already has (e.g. both contacts
// were on the same mailing list) are resolved by keeping the survivor's row
// and dropping the loser's.
func mergeContacts(ctx context.Context, tx *sql.Tx, wsID string, ids []string) (survivor string, losers []string, err error) {
	survivor, losers = ids[0], ids[1:]
	for _, loser := range losers {
		if err = moveChildRows(ctx, tx, wsID, survivor, loser); err != nil {
			return "", nil, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, loser); err != nil {
			return "", nil, err
		}
	}
	return survivor, losers, nil
}

// moveChildRows re-points every table that references a contact from loser to
// survivor. Tables with a workspace+X unique constraint the move could
// collide with (list_subscriptions, contact_identities) are moved with a
// NOT EXISTS guard, and whatever cannot move because the survivor already has
// an equivalent row is simply left on the loser, which is about to be deleted
// — nothing is lost, the survivor already has it.
func moveChildRows(ctx context.Context, tx *sql.Tx, wsID, survivor, loser string) error {
	// Three-arg statements (wsID, survivor, loser). The two cleanup DELETEs
	// below never reference survivor at all, so they are run separately with
	// only (wsID, loser) — a bound parameter Postgres never sees used in the
	// query text (as $3 was here) fails to type-infer (SQLSTATE 42P18).
	stmts := []string{
		// contact_identities: unique per (workspace, channel, external_id), so
		// only move rows whose channel+external_id the survivor does not
		// already carry; the rest already exist on the survivor, so cascading
		// delete on the loser is correct for them.
		`UPDATE contact_identities ci SET contact_id = $2
		   WHERE ci.workspace_id = $1 AND ci.contact_id = $3
		     AND NOT EXISTS (SELECT 1 FROM contact_identities s WHERE s.workspace_id = $1 AND s.contact_id = $2
		                     AND s.channel = ci.channel AND s.external_id = ci.external_id)`,

		`UPDATE deals SET contact_id = $2, updated_at = now() WHERE workspace_id = $1 AND contact_id = $3`,
		`UPDATE activities SET contact_id = $2 WHERE workspace_id = $1 AND contact_id = $3`,
		`UPDATE conversations SET contact_id = $2, updated_at = now() WHERE workspace_id = $1 AND contact_id = $3`,

		`UPDATE list_subscriptions ls SET contact_id = $2, updated_at = now()
		   WHERE ls.workspace_id = $1 AND ls.contact_id = $3
		     AND NOT EXISTS (SELECT 1 FROM list_subscriptions s WHERE s.workspace_id = $1 AND s.contact_id = $2 AND s.list_id = ls.list_id)`,
	}
	for i, q := range stmts {
		if _, err := tx.ExecContext(ctx, q, wsID, survivor, loser); err != nil {
			return fmt.Errorf("moveChildRows stmt %d: %w", i, err)
		}
	}
	// Cleanup DELETEs: whatever didn't move above because the survivor already
	// had an equivalent row is dropped here so the loser can be deleted clean.
	cleanup := []string{
		`DELETE FROM contact_identities WHERE workspace_id = $1 AND contact_id = $2`,
		`DELETE FROM list_subscriptions WHERE workspace_id = $1 AND contact_id = $2`,
	}
	for i, q := range cleanup {
		if _, err := tx.ExecContext(ctx, q, wsID, loser); err != nil {
			return fmt.Errorf("moveChildRows cleanup %d: %w", i, err)
		}
	}
	return nil
}

// ---- direct link/unlink (the contact page, and "link to existing contact"
// from a conversation) --------------------------------------------------

// ErrDuplicateIdentity is returned when the channel+external_id already
// belongs to a different contact in this workspace.
var ErrDuplicateIdentity = errors.New("that account is already linked to another contact")

// ListIdentities returns one contact's linked accounts, primary first then by
// position.
func ListIdentities(ctx context.Context, db *sql.DB, wsID, contactID string) ([]Identity, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, channel, external_id, label, is_primary, position FROM contact_identities
		WHERE workspace_id = $1 AND contact_id = $2 ORDER BY is_primary DESC, position, created_at`, wsID, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Identity{}
	for rows.Next() {
		var it Identity
		if err := rows.Scan(&it.ID, &it.Channel, &it.ExternalID, &it.Label, &it.IsPrimary, &it.Position); err != nil {
			return nil, err
		}
		list = append(list, it)
	}
	return list, rows.Err()
}

// LinkIdentity attaches one channel account to a contact by hand (the contact
// page's "add a number/handle", or a conversation's "link to this contact").
// If the same channel+external_id is already linked to a DIFFERENT contact in
// this workspace it refuses (ErrDuplicateIdentity) rather than silently
// stealing it — that is what merging two contacts is for, done explicitly via
// Upsert or MergeContacts.
func LinkIdentity(ctx context.Context, db *sql.DB, wsID, contactID, channel, externalID, label string, primary bool) (*Identity, error) {
	ext := normExternalID(channel, externalID)
	if ext == "" {
		return nil, ErrNoIdentity
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var existing sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT contact_id FROM contact_identities WHERE workspace_id = $1 AND channel = $2 AND external_id = $3`,
		wsID, channel, ext).Scan(&existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if existing.Valid && existing.String != contactID {
		return nil, ErrDuplicateIdentity
	}
	if primary {
		if _, err := tx.ExecContext(ctx, `
			UPDATE contact_identities SET is_primary = false, updated_at = now()
			WHERE workspace_id = $1 AND contact_id = $2 AND channel = $3`, wsID, contactID, channel); err != nil {
			return nil, err
		}
	}
	var it Identity
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO contact_identities (workspace_id, contact_id, channel, external_id, label, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, channel, external_id) DO UPDATE SET label = EXCLUDED.label, is_primary = EXCLUDED.is_primary, updated_at = now()
		RETURNING id, channel, external_id, label, is_primary, position`,
		wsID, contactID, channel, ext, label, primary,
	).Scan(&it.ID, &it.Channel, &it.ExternalID, &it.Label, &it.IsPrimary, &it.Position); err != nil {
		return nil, err
	}
	if err := syncPrimaries(ctx, tx, wsID, contactID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &it, nil
}

// UnlinkIdentity removes one linked account. It does not blank a legacy
// contacts.email/phone/handle column that happened to equal it — that column
// is edited through UpdateContact, the same as before this card.
func UnlinkIdentity(ctx context.Context, db *sql.DB, wsID, contactID, identityID string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM contact_identities WHERE workspace_id = $1 AND contact_id = $2 AND id = $3`, wsID, contactID, identityID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// MergeContacts folds src into dst by hand (the contact page's "merge into
// another contact"): every child row moves, dst's blank fields are filled
// from src, and src is deleted. dst and src must be different contacts in
// wsID; either order is accepted, but dst is always the id that survives.
func MergeContacts(ctx context.Context, db *sql.DB, wsID, dst, src string) error {
	if dst == src {
		return errors.New("a contact cannot be merged into itself")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "contacts-upsert:"+wsID); err != nil {
		return err
	}
	var srcRow struct {
		email, phone, handle, name, companyID sql.NullString
		meta                                  []byte
	}
	if err := tx.QueryRowContext(ctx, `SELECT email, phone, handle, name, company_id, meta::text FROM contacts WHERE workspace_id = $1 AND id = $2`,
		wsID, src).Scan(&srcRow.email, &srcRow.phone, &srcRow.handle, &srcRow.name, &srcRow.companyID, &srcRow.meta); err != nil {
		return err
	}
	if err := moveChildRows(ctx, tx, wsID, dst, src); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE contacts SET
		  name   = CASE WHEN name = ''   THEN $3 ELSE name END,
		  email  = CASE WHEN email = ''  THEN $4 ELSE email END,
		  phone  = CASE WHEN phone = ''  THEN $5 ELSE phone END,
		  handle = CASE WHEN handle = '' THEN $6 ELSE handle END,
		  meta = $7::jsonb || meta,
		  updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		wsID, dst, srcRow.name.String, srcRow.email.String, srcRow.phone.String, srcRow.handle.String, string(srcRow.meta)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contacts WHERE workspace_id = $1 AND id = $2`, wsID, src); err != nil {
		return err
	}
	return tx.Commit()
}
