// Package model holds hand-written GraphQL model types that must NOT autobind
// to internal/db/gen's sqlc structs (MH-591). sqlc already exports a stale
// Contact and ContactIdentity (missing the MH-583 parity fields, meta and
// most of the columns the CRM actually reads/writes), and gqlgen's autobind
// matches by name — so left alone it would silently wire the GraphQL schema
// to the wrong struct. gqlgen.yml's `models:` section points Contact and
// ContactIdentity at these instead; everything else in contact.graphqls is
// left to gqlgen's normal generated models (no name collision exists there).
package model

import "time"

// Contact mirrors internal/crm.ContactDetail's exported shape closely enough
// that resolvers can convert one to the other field-by-field, but is its own
// type: ContactDetail carries JSON tags and REST-only fields (subscriptions,
// lifecycle history, score) the schema does not expose.
type Contact struct {
	ID          string
	Name        string
	Email       string
	Phone       string
	Handle      string
	Title       string
	CompanyID   *string
	OwnerUserID *string
	Tags        []string
	Lifecycle   string
	Locale      string

	GivenName          string
	FamilyName         string
	MiddleName         string
	NamePrefix         string
	NameSuffix         string
	PhoneticGivenName  string
	PhoneticFamilyName string
	Nickname           string
	Birthday           *string
	Biography          string

	AvatarURL  string
	Meta       []*ContactMetaEntry
	Identities []*ContactIdentity

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ContactMetaEntry is one key/value pair. GraphQL has no map scalar, so
// Contact.Meta (a map[string]string on the crm side) travels as a sorted list.
type ContactMetaEntry struct {
	Key   string
	Value string
}

// ContactIdentity is one linked account (crm.ContactIdentity on the wire).
type ContactIdentity struct {
	ID         string
	Channel    string
	ExternalID string
	Label      string
	IsPrimary  bool
}
