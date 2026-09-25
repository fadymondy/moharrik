// Package googlecontacts converts Google People API results into
// vcard.Card and runs the Google Contacts sync (MH-585). It lives outside
// internal/google because internal/vcard transitively imports
// internal/google (via internal/conversions), so this package — which
// needs both google's People/OAuth client and vcard's Card/CommitCards —
// cannot live inside either of them without an import cycle.
package googlecontacts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/photocache"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/vcard"
)

/*
Google Contacts import/sync (MH-585): converts People API Persons into
vcard.Card and feeds them through vcard's own PreviewCards/CommitCards — the
exact preview -> commit flow MH-584 built for vCard files — so an agent or a
member sees and imports a Google contact exactly like a vCard one.

# Matching

A Person becomes a Card with UID = "google:<resourceName>" and
ImportSource = "google", so vcard's classify() links it to a synthetic
"other" identity (external_id "google:<resourceName>") the same way a vCard
UID already does. Upsert's own matching (internal/contacts.matchingContacts)
is a union, not a staged fallback: it matches on that identity OR any
phone/email identity in the same pass, merging together whatever it finds —
which already satisfies "resourceName first, then phone/email" for every
case that matters (they agree), and is strictly safer than a two-stage
fallback when they disagree (two existing contacts for the same Google
person get merged into one, instead of one silently winning).

# Deleted-at-Google

An incremental sync page can say metadata.deleted=true for a person: MH-585
requires flagging, never a hard delete. FlagDeleted sets google_deleted_at on
the contact linked to that resourceName (post/173_google_contacts.sql). A
deleted-flagged contact is otherwise a normal contact — nothing here hides it
from the UI; that is a separate card if Fady wants it filtered.

# Sync tokens

One syncToken per (workspace, google_connections row), in google_contacts_sync
(post/173). A 410 EXPIRED_SYNC_TOKEN drops the stored token and retries once
as a full resync — see Sync below.
*/

// SyncResult is what a sync (manual or, one day, scheduled) did.
type SyncResult struct {
	Added          int  `json:"added"`
	Updated        int  `json:"updated"`
	FlaggedDeleted int  `json:"flagged_deleted"`
	Invalid        int  `json:"invalid"`
	FullResync     bool `json:"full_resync"`
}

// personToCard converts one Person into a vcard.Card. groups maps a contact
// group resourceName to its ContactGroup (for membership -> tag); nil is
// fine (no memberships get tagged, e.g. when the group listing scope/call
// failed and the caller decided contacts still matter more than tags).
func personToCard(p google.Person, groups map[string]google.ContactGroup) vcard.Card {
	name := p.PrimaryName()
	c := vcard.Card{
		Version:      "4.0",
		ImportSource: "google",
		UID:          "google:" + p.ResourceName,
		XProps:       map[string]string{},
		Meta:         map[string]string{},
		N: vcard.Name{
			Family:     name.FamilyName,
			Given:      name.GivenName,
			Additional: name.MiddleName,
			Prefix:     name.HonorificPrefix,
			Suffix:     name.HonorificSuffix,
		},
		FN:             name.DisplayName,
		PhoneticGiven:  name.PhoneticGivenName,
		PhoneticFamily: name.PhoneticFamilyName,
	}
	if c.FN == "" {
		c.FN = strings.TrimSpace(name.GivenName + " " + name.FamilyName)
	}
	if len(p.Nicknames) > 0 {
		c.Nickname = p.Nicknames[0].Value
	}
	if len(p.Biographies) > 0 {
		c.Note = p.Biographies[0].Value
	}
	for _, b := range p.Birthdays {
		if iso := b.Date.ISO(); iso != "" {
			c.BDay = iso
			break
		}
	}
	for _, ph := range p.PhoneNumbers {
		if ph.Value == "" {
			continue
		}
		typ := ph.FormattedType
		if typ == "" {
			typ = ph.Type
		}
		c.Phones = append(c.Phones, vcard.Phone{Value: ph.Value, Label: typ})
	}
	for _, e := range p.EmailAddresses {
		if e.Value == "" {
			continue
		}
		c.Emails = append(c.Emails, vcard.Email{Value: e.Value, Label: e.Type})
	}
	for _, a := range p.Addresses {
		c.Addresses = append(c.Addresses, vcard.Address{
			Types: nonEmpty(a.Type), Street: a.StreetAddress, Locality: a.City,
			Region: a.Region, Postal: a.PostalCode, Country: a.Country, Label: a.FormattedValue,
		})
	}
	for _, u := range p.URLs {
		if u.Value != "" {
			c.URLs = append(c.URLs, u.Value)
		}
	}
	// Organizations: the first becomes Org/Title/Role (Card's own scalar
	// fields, same as vCard's ORG/TITLE/ROLE), same as every vCard import;
	// any further ones (Google allows several — a person with two jobs) have
	// nowhere to go as columns, so they are preserved in meta rather than
	// silently dropped.
	if len(p.Organizations) > 0 {
		c.Org = p.Organizations[0].Name
		c.Title = p.Organizations[0].Title
		c.Role = p.Organizations[0].Department
	}
	if len(p.Organizations) > 1 {
		putJSON(c.Meta, "google_organizations", p.Organizations[1:])
	}
	if len(p.Relations) > 0 {
		putJSON(c.Meta, "google_relations", p.Relations)
	}
	if len(p.Events) > 0 {
		putJSON(c.Meta, "google_events", p.Events)
	}
	// userDefined (Google's own name for "custom fields"): each becomes its
	// own meta key, exactly what Fady asked contacts.meta for in the first
	// place ("add a meta value that i can add key and value of each
	// contact"). A key colliding with one of the structured ones above (rare
	// — "google_relations" as someone's own custom field label) is prefixed.
	for _, ud := range p.UserDefined {
		key := strings.TrimSpace(ud.Key)
		if key == "" {
			continue
		}
		if strings.HasPrefix(key, "google_") {
			key = "custom_" + key
		}
		c.Meta[key] = ud.Value
	}
	for _, m := range p.Memberships {
		if m.ContactGroupMembership == nil {
			continue
		}
		rn := m.ContactGroupMembership.ContactGroupResourceName
		g, ok := groups[rn]
		if !ok || g.GroupType != "USER_CONTACT_GROUP" || g.Name == "" {
			continue // system groups (myContacts, starred, ...) are not tags
		}
		c.Categories = append(c.Categories, g.Name)
	}
	return c
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func putJSON(m map[string]string, key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	m[key] = string(b)
}

// otherContactCard is like personToCard but for otherContacts.list results:
// Google returns a much smaller field set for those (never organizations,
// relations, events, userDefined or memberships — see internal/google's
// people.go), so this stays a thin wrapper rather than special-casing every
// field above.
func otherContactCard(p google.Person) vcard.Card {
	return personToCard(p, nil)
}

// ---- sync token storage -------------------------------------------------

func loadSyncToken(ctx context.Context, a *app.App, wsID, connID string) (string, error) {
	wctx := tenant.WithWorkspace(ctx, wsID)
	var tok string
	err := a.SQLDB.QueryRowContext(wctx, `
		SELECT sync_token FROM google_contacts_sync WHERE workspace_id = $1 AND connection_id = $2`,
		wsID, connID).Scan(&tok)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return tok, err
}

func storeSyncToken(ctx context.Context, a *app.App, wsID, connID, token string, lastErr string) error {
	wctx := tenant.WithWorkspace(ctx, wsID)
	_, err := a.SQLDB.ExecContext(wctx, `
		INSERT INTO google_contacts_sync (workspace_id, connection_id, sync_token, last_synced_at, last_error)
		VALUES ($1, $2, $3, now(), $4)
		ON CONFLICT (workspace_id, connection_id) DO UPDATE SET
		  sync_token = EXCLUDED.sync_token, last_synced_at = now(), last_error = EXCLUDED.last_error`,
		wsID, connID, token, lastErr)
	return err
}

// SyncStatus is what the UI's "Import from Google" panel shows per connection.
type SyncStatus struct {
	ConnectionID string  `json:"connection_id"`
	LastSyncedAt *string `json:"last_synced_at"`
	LastError    string  `json:"last_error"`
	HasToken     bool    `json:"has_token"`
}

// Status reads the sync bookkeeping row for a connection, or a zero value
// when it has never synced.
func Status(ctx context.Context, a *app.App, wsID, connID string) (*SyncStatus, error) {
	wctx := tenant.WithWorkspace(ctx, wsID)
	// A connection that never synced has no google_contacts_sync row, so the
	// query below alone can't tell "never synced" from "not this workspace's
	// connection" — both come back sql.ErrNoRows. Without this check, a
	// cross-workspace id request "not synced yet" for a connection that
	// actually belongs to another workspace, confirming that id exists
	// (TestCrossWorkspaceIsolation caught exactly this).
	if _, err := grantedScopes(ctx, a, wsID, connID); err != nil {
		return nil, err
	}
	out := &SyncStatus{ConnectionID: connID}
	var syncedAt sql.NullString
	var tok string
	err := a.SQLDB.QueryRowContext(wctx, `
		SELECT to_char(last_synced_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), last_error, sync_token
		FROM google_contacts_sync WHERE workspace_id = $1 AND connection_id = $2`,
		wsID, connID).Scan(&syncedAt, &out.LastError, &tok)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	if syncedAt.Valid {
		out.LastSyncedAt = &syncedAt.String
	}
	out.HasToken = tok != ""
	return out, nil
}

// FlagDeleted marks the contact linked to resourceName as deleted at Google
// (google_deleted_at), never a hard DELETE (MH-585 spec). No matching
// contact (never imported, or already unlinked) is not an error — nothing to
// flag.
func FlagDeleted(ctx context.Context, a *app.App, wsID, resourceName string) error {
	wctx := tenant.WithWorkspace(ctx, wsID)
	_, err := a.SQLDB.ExecContext(wctx, `
		UPDATE contacts SET google_deleted_at = now()
		WHERE workspace_id = $1 AND id = (
		  SELECT contact_id FROM contact_identities
		  WHERE workspace_id = $1 AND channel = 'other' AND external_id = $2
		) AND google_deleted_at IS NULL`,
		wsID, "google:"+resourceName)
	return err
}

func defaultCountry(ctx context.Context, a *app.App, wsID string) string {
	var c string
	_ = a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, wsID),
		`SELECT default_country FROM workspaces WHERE id = $1`, wsID).Scan(&c)
	return c
}

// Preview fetches every People connection (+ Other contacts, when granted)
// for connID and classifies each one via vcard.PreviewCards — new / merge /
// invalid — without writing anything and without touching the stored sync
// token. Used by import_contacts_preview{source:"google"} and its REST
// equivalent so an agent or member can see what a sync would do before
// running it.
func Preview(ctx context.Context, a *app.App, wsID, connID string) (*vcard.VcardPreview, error) {
	src, err := google.TokenSource(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	granted, err := grantedScopes(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	if !hasScope(granted, google.ScopeContacts) {
		return nil, google.ErrScope
	}
	other := hasScope(granted, google.ScopeContactsOther)

	groups, gerr := google.ListContactGroups(ctx, src)
	if gerr != nil {
		groups = nil
	}

	// Preview always looks at the full connections list (never the stored
	// sync token): a partial, token-scoped page would under-report what an
	// import will do the first time it runs for real.
	cards, _, err := collect(ctx, src, "", groups)
	if err != nil {
		return nil, err
	}
	if other {
		opage, _, operr := collectOther(ctx, src)
		if operr == nil {
			cards.cards = append(cards.cards, opage...)
		}
	}
	runID := fmt.Sprintf("google-preview:%s:%d", connID, len(cards.cards))
	return vcard.PreviewCards(ctx, a.SQLDB, wsID, cards.cards, nil, runID, defaultCountry(ctx, a, wsID))
}

// Sync imports every connections + (when granted) "Other contacts" person
// for connID into wsID's contacts, through vcard's PreviewCards/CommitCards
// engine. It is incremental when a sync token is stored and full otherwise
// (first sync, or after EXPIRED_SYNC_TOKEN below).
func Sync(ctx context.Context, a *app.App, wsID, connID, actorID string) (*SyncResult, error) {
	src, err := google.TokenSource(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	granted, err := grantedScopes(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	if !hasScope(granted, google.ScopeContacts) {
		return nil, google.ErrScope
	}
	other := hasScope(granted, google.ScopeContactsOther)

	groups, gerr := google.ListContactGroups(ctx, src)
	if gerr != nil {
		groups = nil // memberships just won't become tags this run
	}

	token, err := loadSyncToken(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	fullResync := token == ""

	cards, deletedResourceNames, err := collect(ctx, src, token, groups)
	if google.IsSyncTokenExpired(err) {
		// Drop the stale token and retry once as a full resync (MH-585 spec).
		fullResync = true
		cards, deletedResourceNames, err = collect(ctx, src, "", groups)
	}
	if err != nil {
		_ = storeSyncToken(ctx, a, wsID, connID, "", err.Error())
		return nil, err
	}
	nextToken := cards.nextSyncToken

	if other {
		opage, ophotos, operr := collectOther(ctx, src)
		if operr == nil {
			cards.cards = append(cards.cards, opage...)
			for uid, url := range ophotos {
				cards.photos[uid] = url
			}
		}
		// Other-contacts failures never fail the whole sync: it is the
		// optional half of the grant.
	}

	res := &SyncResult{FullResync: fullResync}
	if len(cards.cards) > 0 {
		country := defaultCountry(ctx, a, wsID)
		runID := fmt.Sprintf("google-sync:%s:%d", connID, len(cards.cards))
		cr, err := vcard.CommitCards(ctx, a.SQLDB, wsID, actorID, cards.cards, nil, runID, country)
		if err != nil {
			return nil, err
		}
		res.Added = cr.CreatedCount
		res.Updated = cr.MergedCount
		res.Invalid = cr.InvalidCount
		applyGooglePhotos(ctx, a, wsID, cards.photos)
	}
	for _, rn := range deletedResourceNames {
		if err := FlagDeleted(ctx, a, wsID, rn); err != nil {
			return nil, err
		}
		res.FlaggedDeleted++
	}

	if err := storeSyncToken(ctx, a, wsID, connID, nextToken, ""); err != nil {
		return nil, err
	}
	return res, nil
}

type collected struct {
	cards         []vcard.Card
	nextSyncToken string
	// photos maps a card's UID ("google:<resourceName>") to a non-default
	// profile photo URL Google returned for it — kept OUT of vcard.Card
	// itself, since vcard's own generic photo_url fill-blank path
	// (applyExtraFields) is for vCard-sourced photos, tags photo_source
	// 'vcard' and never resizes/caches. Sync applies this map separately,
	// through internal/photocache, after CommitCards has run (see applyPhotos).
	photos map[string]string
}

func collect(ctx context.Context, src *google.Source, token string, groups map[string]google.ContactGroup) (collected, []string, error) {
	out := collected{photos: map[string]string{}}
	var deleted []string
	next, err := google.ListAllConnections(ctx, src, token, false, func(p google.Person) error {
		if p.Metadata.Deleted {
			deleted = append(deleted, p.ResourceName)
			return nil
		}
		card := personToCard(p, groups)
		out.cards = append(out.cards, card)
		if url := p.PrimaryPhoto(); url != "" {
			out.photos[card.UID] = url
		}
		return nil
	})
	out.nextSyncToken = next
	return out, deleted, err
}

func collectOther(ctx context.Context, src *google.Source) ([]vcard.Card, map[string]string, error) {
	var out []vcard.Card
	photos := map[string]string{}
	_, err := google.ListAllConnections(ctx, src, "", true, func(p google.Person) error {
		if p.Metadata.Deleted {
			return nil
		}
		card := otherContactCard(p)
		out = append(out, card)
		if url := p.PrimaryPhoto(); url != "" {
			photos[card.UID] = url
		}
		return nil
	})
	return out, photos, err
}

// applyGooglePhotos caches each just-committed contact's Google profile photo
// through internal/photocache, the same download-resize-data:URI pipeline
// MH-588's Telegram/Slack/Discord fetchers use (internal/channels/avatar.go),
// tagged photo_source='google'. It runs AFTER CommitCards, outside vcard's own
// generic photo_url fill-blank path (vcard's applyExtraFields, which is for
// vCard PHOTO properties and would tag the wrong source) — each entry is
// re-resolved by external_ids->>'google', since CommitResult.ContactIDs is
// not reliably aligned with the input cards once invalid/skipped ones are in
// the mix. An uploaded avatar (photo_attachment_id set) always wins per
// internal/photourl's priority order, so those contacts are skipped rather
// than overwritten. Best-effort throughout: any one contact's fetch failing
// (a dead URL, a network error) is cached as a failed status and never fails
// the sync that triggered it.
func applyGooglePhotos(ctx context.Context, a *app.App, wsID string, photos map[string]string) {
	if len(photos) == 0 {
		return
	}
	wctx := tenant.WithWorkspace(ctx, wsID)
	for uid, photoURL := range photos {
		if photoURL == "" {
			continue
		}
		var contactID string
		var avatarID sql.NullString
		err := a.SQLDB.QueryRowContext(wctx, `
			SELECT id, photo_attachment_id FROM contacts
			WHERE workspace_id = $1 AND external_ids->>'google' = $2`, wsID, uid).Scan(&contactID, &avatarID)
		if err != nil || contactID == "" {
			continue // not committed this run (invalid card), or already gone
		}
		if avatarID.Valid && avatarID.String != "" {
			continue // an uploaded photo always wins — never overwrite it
		}
		var fetchedAt sql.NullTime
		if err := a.SQLDB.QueryRowContext(wctx, `SELECT photo_fetched_at FROM contacts WHERE workspace_id = $1 AND id = $2`,
			wsID, contactID).Scan(&fetchedAt); err != nil {
			continue
		}
		if !photocache.IsStale(fetchedAt, photocache.RefreshWindow) {
			continue
		}
		raw, ct, derr := photocache.Download(ctx, photoURL, nil)
		var dataURI string
		if derr == nil {
			dataURI, derr = photocache.EncodeDataURI(raw, ct)
		}
		_ = photocache.UpdatePhoto(ctx, a.SQLDB, "contacts", wsID, contactID, "google", dataURI, derr)
	}
}

func grantedScopes(ctx context.Context, a *app.App, wsID, connID string) ([]string, error) {
	wctx := tenant.WithWorkspace(ctx, wsID)
	var granted pgtext.Array
	err := a.SQLDB.QueryRowContext(wctx, `
		SELECT granted_scopes FROM google_connections WHERE workspace_id = $1 AND id = $2`,
		wsID, connID).Scan(&granted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, google.ErrNotConnected
	}
	return []string(granted), err
}

// hasScope reports whether want is present in have. internal/google's own
// equivalent (hasAll) is unexported, so this package — which cannot import
// it — keeps a small local copy rather than exporting internal/google's
// helper just for this one caller.
func hasScope(have []string, want string) bool {
	for _, h := range have {
		if h == want {
			return true
		}
	}
	return false
}
