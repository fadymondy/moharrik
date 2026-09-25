package vcard

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/contacts"
)

// MaxFileSize is the size cap on a single .vcf upload/paste, enforced by
// both the REST route and the MCP tools before this package ever sees the
// bytes: about 2000 typical contacts' worth of vCard 3.0 text, and Apple's
// PHOTO thumbnails are small enough that even a photo-heavy export of that
// size stays under it.
const MaxFileSize = 8 << 20 // 8 MiB

// row is one parsed card plus everything the preview/commit need to know
// about it beyond the card itself.
type row struct {
	card    Card
	invalid string   // non-empty => this card will not be imported, and why
	matches []string // contact ids this card matches on (email/phone/handle/UID)
}

// PreviewRow is one card's classification, returned to a caller (the web
// import dialog or the MCP preview tool) before anything is written.
type PreviewRow struct {
	Index      int      `json:"index"`
	Name       string   `json:"name"`
	Phones     []string `json:"phones"`
	Emails     []string `json:"emails"`
	Status     string   `json:"status"` // "new", "merge" or "invalid"
	Invalid    string   `json:"invalid,omitempty"`
	MatchIDs   []string `json:"match_contact_ids,omitempty"`
	LocalPhone bool     `json:"local_phone,omitempty"` // a TEL had no country code and used the workspace default
}

// Preview is the full result of parsing a file without writing anything.
type VcardPreview struct {
	CardCount    int          `json:"card_count"`
	NewCount     int          `json:"new_count"`
	MergeCount   int          `json:"merge_count"`
	InvalidCount int          `json:"invalid_count"`
	Rows         []PreviewRow `json:"rows"`
	FileHash     string       `json:"file_hash"`
}

// CommitResult is what actually happened when a previewed file was written.
type CommitResult struct {
	CardCount    int      `json:"card_count"`
	CreatedCount int      `json:"created_count"`
	MergedCount  int      `json:"merged_count"`
	InvalidCount int      `json:"invalid_count"`
	ContactIDs   []string `json:"contact_ids"`
}

// ErrTooLarge is returned by Preview/Commit when data exceeds MaxFileSize.
var ErrTooLarge = errors.New("vCard file is larger than the 8 MiB import limit")

func fileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// classify turns a parsed Card into a Candidate plus warnings, resolving its
// phones to E.164 with defaultCountry. It never touches the database — used
// by both Preview (read-only matching) and Commit (which then calls
// contacts.Upsert with the same Candidate).
func classify(c Card, defaultCountry string) (contacts.Candidate, []string) {
	var warnings []string
	cand := contacts.Candidate{
		Name:   c.FN,
		Source: "vcard_import",
		Meta:   map[string]string{},
	}

	// The vCard UID becomes a synthetic identity so the existing matching
	// engine (contacts.Upsert / matchingContacts) treats "same UID" as "same
	// person" for free, and re-importing the same file is idempotent: the
	// second run matches on this identity and fills nothing new.
	if c.UID != "" {
		cand.Identities = append(cand.Identities, contacts.Identity{Channel: "other", ExternalID: "vcarduid:" + strings.TrimSpace(c.UID)})
	}

	firstEmail, firstPhone, firstHandle := true, true, true
	for _, e := range c.Emails {
		v := strings.ToLower(strings.TrimSpace(e.Value))
		if v == "" {
			continue
		}
		if firstEmail {
			cand.Email = v
			firstEmail = false
			continue
		}
		cand.Identities = append(cand.Identities, contacts.Identity{Channel: "email", ExternalID: v, Label: e.Label})
	}
	for _, ph := range c.Phones {
		e164, wasLocal, err := ToE164(ph.Value, defaultCountry)
		if err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if e164 == "" {
			continue
		}
		if wasLocal {
			warnings = append(warnings, fmt.Sprintf("%q assumed to be a %s number", ph.Value, defaultCountry))
		}
		if firstPhone {
			cand.Phone = e164
			firstPhone = false
			continue
		}
		cand.Identities = append(cand.Identities, contacts.Identity{Channel: "phone", ExternalID: e164, Label: ph.Label})
	}
	for _, im := range c.IMPPs {
		channel := "other"
		v := im.URI
		if strings.HasPrefix(strings.ToLower(v), "sip:") {
			channel = "sip"
		}
		cand.Identities = append(cand.Identities, contacts.Identity{Channel: channel, ExternalID: v, Label: im.Label})
	}
	_ = firstHandle

	if cand.Email == "" && cand.Phone == "" && cand.Handle == "" && len(cand.Identities) == 0 {
		// Nothing at all to key on — Upsert would reject this with
		// ErrNoIdentity; the caller reports it invalid instead of failing
		// the whole file.
	}

	for k, v := range c.XProps {
		cand.Meta["vcard_"+k] = v
	}
	if c.Note != "" {
		cand.Meta["biography"] = c.Note
	}
	if c.Title != "" {
		cand.Meta["title"] = c.Title
	}

	return cand, warnings
}

// matchIDs duplicates contacts' own (unexported) matching query — it cannot
// be reused directly — for read-only preview purposes: which existing
// contacts would this candidate resolve onto if committed right now.
func matchIDs(ctx context.Context, db *sql.DB, wsID string, cand contacts.Candidate) ([]string, error) {
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
	if cand.Email != "" {
		if err := add(db.QueryContext(ctx, `SELECT id FROM contacts WHERE workspace_id=$1 AND email<>'' AND lower(email)=lower($2)`, wsID, cand.Email)); err != nil {
			return nil, err
		}
	}
	if cand.Phone != "" {
		if err := add(db.QueryContext(ctx, `SELECT id FROM contacts WHERE workspace_id=$1 AND phone<>'' AND regexp_replace(phone,'[^0-9]','','g')=regexp_replace($2,'[^0-9]','','g')`, wsID, cand.Phone)); err != nil {
			return nil, err
		}
	}
	for _, ident := range cand.Identities {
		if ident.Channel == "email" || ident.Channel == "phone" {
			if err := add(db.QueryContext(ctx, `SELECT contact_id FROM contact_identities WHERE workspace_id=$1 AND channel=$2 AND regexp_replace(external_id,'[^0-9]','','g')=regexp_replace($3,'[^0-9]','','g')`, wsID, ident.Channel, ident.ExternalID)); err != nil {
				return nil, err
			}
			continue
		}
		if err := add(db.QueryContext(ctx, `SELECT contact_id FROM contact_identities WHERE workspace_id=$1 AND channel=$2 AND external_id=$3`, wsID, ident.Channel, ident.ExternalID)); err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids, nil
}

func cardLabel(c Card) string {
	if c.FN != "" {
		return c.FN
	}
	for _, e := range c.Emails {
		if e.Value != "" {
			return e.Value
		}
	}
	for _, p := range c.Phones {
		if p.Value != "" {
			return p.Value
		}
	}
	return "(no name)"
}

func phoneStrings(c Card) []string {
	out := make([]string, 0, len(c.Phones))
	for _, p := range c.Phones {
		out = append(out, p.Value)
	}
	return out
}

func emailStrings(c Card) []string {
	out := make([]string, 0, len(c.Emails))
	for _, e := range c.Emails {
		out = append(out, e.Value)
	}
	return out
}

// PreviewImport parses data and classifies every card as new/merge/invalid
// without writing anything.
func PreviewImport(ctx context.Context, db *sql.DB, wsID string, data []byte, defaultCountry string) (*VcardPreview, error) {
	if len(data) > MaxFileSize {
		return nil, ErrTooLarge
	}
	cards, perrs := ParseCards(data)
	out := &VcardPreview{FileHash: fileHash(data)}
	total := len(cards) + len(perrs)
	out.CardCount = total

	for _, pe := range perrs {
		out.Rows = append(out.Rows, PreviewRow{Index: pe.Index, Status: "invalid", Invalid: pe.Reason})
		out.InvalidCount++
	}
	for i, c := range cards {
		cand, warnings := classify(c, defaultCountry)
		pr := PreviewRow{
			Index:  i,
			Name:   cardLabel(c),
			Phones: phoneStrings(c),
			Emails: emailStrings(c),
		}
		for _, w := range warnings {
			if strings.Contains(w, "assumed to be") {
				pr.LocalPhone = true
			}
		}
		if cand.Email == "" && cand.Phone == "" && cand.Handle == "" && len(cand.Identities) == 0 {
			pr.Status = "invalid"
			pr.Invalid = "no email, phone, UID or other identity to import this contact by"
			out.InvalidCount++
			out.Rows = append(out.Rows, pr)
			continue
		}
		ids, err := matchIDs(ctx, db, wsID, cand)
		if err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			pr.Status = "merge"
			pr.MatchIDs = ids
			out.MergeCount++
		} else {
			pr.Status = "new"
			out.NewCount++
		}
		out.Rows = append(out.Rows, pr)
	}
	return out, nil
}

// CommitImport parses data and writes every valid card via contacts.Upsert
// (fill-blank/append-only merge semantics, per-workspace advisory-locked),
// then fills in the fields Upsert's Candidate has no room for. Re-running
// Commit with the exact same file is idempotent: every card either matches
// its own vcarduid identity or its previously-imported email/phone, so
// nothing new is created and no field already filled is overwritten.
func CommitImport(ctx context.Context, db *sql.DB, wsID, actorID string, data []byte, defaultCountry string) (*CommitResult, error) {
	if len(data) > MaxFileSize {
		return nil, ErrTooLarge
	}
	cards, perrs := ParseCards(data)
	res := &CommitResult{CardCount: len(cards) + len(perrs), InvalidCount: len(perrs)}

	for _, c := range cards {
		cand, _ := classify(c, defaultCountry)
		if cand.Email == "" && cand.Phone == "" && cand.Handle == "" && len(cand.Identities) == 0 {
			res.InvalidCount++
			continue
		}
		if c.Org != "" {
			companyID, err := findOrCreateCompany(ctx, db, wsID, c.Org)
			if err != nil {
				return nil, err
			}
			cand.CompanyID = companyID
		}
		r, err := contacts.Upsert(ctx, db, wsID, cand)
		if err != nil {
			if errors.Is(err, contacts.ErrNoIdentity) {
				res.InvalidCount++
				continue
			}
			return nil, err
		}
		if err := applyExtraFields(ctx, db, wsID, r.ContactID, c); err != nil {
			return nil, err
		}
		res.ContactIDs = append(res.ContactIDs, r.ContactID)
		if r.Created {
			res.CreatedCount++
		} else {
			res.MergedCount++
		}
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO vcard_imports (workspace_id, file_hash, card_count, created_count, merged_count, invalid_count, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		wsID, fileHash(data), res.CardCount, res.CreatedCount, res.MergedCount, res.InvalidCount, actorID); err != nil {
		return nil, err
	}
	return res, nil
}

func findOrCreateCompany(ctx context.Context, db *sql.DB, wsID, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	var id string
	err := db.QueryRowContext(ctx, `SELECT id FROM companies WHERE workspace_id=$1 AND lower(name)=lower($2) LIMIT 1`, wsID, name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = db.QueryRowContext(ctx, `INSERT INTO companies (workspace_id, name) VALUES ($1,$2) RETURNING id`, wsID, name).Scan(&id)
	return id, err
}

// applyExtraFields fills the columns contacts.Candidate has no room for
// (fill-blank only, matching Upsert's own semantics), and additively merges
// the array fields (tags, addresses, urls) so a re-import never duplicates
// or drops what is already there.
func applyExtraFields(ctx context.Context, db *sql.DB, wsID, id string, c Card) error {
	_, err := db.ExecContext(ctx, `
		UPDATE contacts SET
		  given_name              = CASE WHEN given_name = '' AND $3 <> '' THEN $3 ELSE given_name END,
		  family_name             = CASE WHEN family_name = '' AND $4 <> '' THEN $4 ELSE family_name END,
		  middle_name             = CASE WHEN middle_name = '' AND $5 <> '' THEN $5 ELSE middle_name END,
		  name_prefix             = CASE WHEN name_prefix = '' AND $6 <> '' THEN $6 ELSE name_prefix END,
		  name_suffix             = CASE WHEN name_suffix = '' AND $7 <> '' THEN $7 ELSE name_suffix END,
		  nickname                = CASE WHEN nickname = '' AND $8 <> '' THEN $8 ELSE nickname END,
		  title                   = CASE WHEN title = '' AND $9 <> '' THEN $9 ELSE title END,
		  job_role                = CASE WHEN job_role = '' AND $10 <> '' THEN $10 ELSE job_role END,
		  biography               = CASE WHEN biography = '' AND $11 <> '' THEN $11 ELSE biography END,
		  birthday                = CASE WHEN birthday IS NULL AND $12 <> '' THEN NULLIF($12,'')::date ELSE birthday END,
		  anniversary             = CASE WHEN anniversary IS NULL AND $13 <> '' THEN NULLIF($13,'')::date ELSE anniversary END,
		  photo_url               = CASE WHEN photo_url = '' AND $14 <> '' THEN $14 ELSE photo_url END,
		  photo_source            = CASE WHEN photo_url = '' AND $14 <> '' THEN 'vcard' ELSE photo_source END,
		  external_ids            = CASE WHEN NOT (external_ids ? 'vcard') AND $15 <> '' THEN jsonb_set(external_ids, ARRAY['vcard'], to_jsonb($15::text), true) ELSE external_ids END,
		  tags                    = CASE WHEN $16::text[] = '{}' THEN tags ELSE (SELECT array_agg(DISTINCT t) FROM unnest(tags || $16::text[]) t) END,
		  addresses               = CASE WHEN jsonb_array_length(addresses) = 0 AND $17::jsonb <> '[]'::jsonb THEN $17::jsonb ELSE addresses END,
		  urls                    = CASE WHEN $18::jsonb = '[]'::jsonb THEN urls ELSE (SELECT jsonb_agg(DISTINCT v) FROM jsonb_array_elements_text(urls || $18::jsonb) v) END,
		  updated_at              = now()
		WHERE workspace_id = $1 AND id = $2`,
		wsID, id,
		c.N.Given, c.N.Family, c.N.Additional, c.N.Prefix, c.N.Suffix,
		c.Nickname, c.Title, c.Role, c.Note,
		safeDate(c.BDay), safeDate(c.Anniv),
		c.PhotoURL,
		firstNonEmpty(c.UID),
		emptyToNilStrings(c.Categories),
		addressesJSON(c.Addresses),
		urlsJSON(c.URLs),
	)
	return err
}

func safeDate(v string) string {
	if len(v) == 10 && v[4] == '-' && v[7] == '-' {
		return v
	}
	return ""
}

func firstNonEmpty(s string) string { return strings.TrimSpace(s) }

func addressesJSON(addrs []Address) string {
	type jaddr struct {
		Type     string `json:"type"`
		Street   string `json:"street"`
		Locality string `json:"locality"`
		Region   string `json:"region"`
		Postal   string `json:"postal_code"`
		Country  string `json:"country"`
		Label    string `json:"label"`
	}
	list := make([]jaddr, 0, len(addrs))
	for _, a := range addrs {
		typ := ""
		if len(a.Types) > 0 {
			typ = strings.ToLower(a.Types[0])
		}
		list = append(list, jaddr{Type: typ, Street: strings.TrimSpace(a.Extended + " " + a.Street), Locality: a.Locality, Region: a.Region, Postal: a.Postal, Country: a.Country, Label: a.Label})
	}
	b, _ := json.Marshal(list)
	return string(b)
}

func urlsJSON(urls []string) string {
	b, _ := json.Marshal(urls)
	return string(b)
}

// emptyToNilStrings never returns a Go nil, so the pgx text[] parameter this
// feeds is always a real (possibly empty) Postgres array, never NULL — a
// NULL would poison the "tags || categories" concatenation in
// applyExtraFields into NULL as well, wiping tags a member already set.
func emptyToNilStrings(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}
