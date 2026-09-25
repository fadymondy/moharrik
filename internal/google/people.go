package google

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

/*
Package-internal People API client (MH-585): reads a connected account's
Google Contacts and "Other contacts" through the same Client (api.go) every
other product uses — same token handling, same retry/backoff, same
GOOGLE_API_BASE override for tests.

people.connections.list is Google's own incremental-sync design: pass
syncToken (from a previous page's nextSyncToken) and only the people that
changed since come back; a token too old to honour comes back as a 410 with
reason EXPIRED_SYNC_TOKEN (ErrSyncTokenExpired here), and the caller must drop
it and list again with no token at all (a full resync).

personFields lists every field this app stores parity for (166_contact_
identities.sql / crm.go's ContactDetail): names (incl. phonetic), nicknames,
birthdays, biographies, phone numbers, emails, addresses, organizations,
urls, relations, events, userDefined (custom fields) and memberships (which
contact groups a person is in, mapped to tags by internal/google/contacts.go).
*/

// PersonFields is what every people.connections.list / people.get call asks
// for. Exported so a caller building its own request (none does today) stays
// in sync with what Person below can decode.
const PersonFields = "names,nicknames,birthdays,biographies,phoneNumbers,emailAddresses,addresses,organizations,urls,relations,events,userDefined,memberships,metadata"

// PersonName is one entry in Person.Names (People API returns an array —
// several sources can each contribute a name; the first, or the one flagged
// primary, is what this app imports as the contact's own name).
type PersonName struct {
	Metadata           FieldMetadata `json:"metadata"`
	DisplayName        string        `json:"displayName"`
	FamilyName         string        `json:"familyName"`
	GivenName          string        `json:"givenName"`
	MiddleName         string        `json:"middleName"`
	HonorificPrefix    string        `json:"honorificPrefix"`
	HonorificSuffix    string        `json:"honorificSuffix"`
	PhoneticFamilyName string        `json:"phoneticFamilyName"`
	PhoneticGivenName  string        `json:"phoneticGivenName"`
}

// FieldMetadata is the small metadata block every repeatable People field
// carries; Primary is the only part this app reads (which of several values
// to treat as "the" one when filling contacts' scalar columns).
type FieldMetadata struct {
	Primary bool `json:"primary"`
}

type PersonNickname struct {
	Value string `json:"value"`
}

type PersonDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// ISO renders YYYY-MM-DD, or "" when there is not enough of a date to store
// (Google allows a bare month/day with no year — this app's birthday column
// is a real date, so that case is dropped rather than guessed at).
func (d PersonDate) ISO() string {
	if d.Year == 0 || d.Month == 0 || d.Day == 0 {
		return ""
	}
	return pad4(d.Year) + "-" + pad2(d.Month) + "-" + pad2(d.Day)
}

func pad2(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 2 {
		return "0" + s
	}
	return s
}
func pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

type PersonBirthday struct {
	Date PersonDate `json:"date"`
}

type PersonBiography struct {
	Value string `json:"value"`
}

type PersonPhone struct {
	Value         string `json:"value"`
	Type          string `json:"type"`
	FormattedType string `json:"formattedType"`
}

type PersonEmail struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type PersonAddress struct {
	FormattedValue string `json:"formattedValue"`
	Type           string `json:"type"`
	StreetAddress  string `json:"streetAddress"`
	City           string `json:"city"`
	Region         string `json:"region"`
	PostalCode     string `json:"postalCode"`
	Country        string `json:"country"`
}

type PersonOrganization struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Department string `json:"department"`
}

type PersonURL struct {
	Value string `json:"value"`
	Type  string `json:"type"`
}

type PersonRelation struct {
	Person string `json:"person"`
	Type   string `json:"type"`
}

type PersonEvent struct {
	Date PersonDate `json:"date"`
	Type string     `json:"type"`
}

type PersonUserDefined struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ContactGroupMembership struct {
	ContactGroupResourceName string `json:"contactGroupResourceName"`
}

type PersonMembership struct {
	ContactGroupMembership *ContactGroupMembership `json:"contactGroupMembership"`
}

type PersonMetadata struct {
	// Deleted (MH-585): true on an incremental (syncToken) page means the
	// person was removed at Google since the last sync. This app never
	// hard-deletes for that — see FlagDeleted in contacts.go.
	Deleted bool `json:"deleted"`
}

// Person is the subset of Google's Person resource this app understands,
// decoded straight off the wire (json tags match the API's own field names).
type Person struct {
	ResourceName   string               `json:"resourceName"`
	ETag           string               `json:"etag"`
	Metadata       PersonMetadata       `json:"metadata"`
	Names          []PersonName         `json:"names"`
	Nicknames      []PersonNickname     `json:"nicknames"`
	Birthdays      []PersonBirthday     `json:"birthdays"`
	Biographies    []PersonBiography    `json:"biographies"`
	PhoneNumbers   []PersonPhone        `json:"phoneNumbers"`
	EmailAddresses []PersonEmail        `json:"emailAddresses"`
	Addresses      []PersonAddress      `json:"addresses"`
	Organizations  []PersonOrganization `json:"organizations"`
	URLs           []PersonURL          `json:"urls"`
	Relations      []PersonRelation     `json:"relations"`
	Events         []PersonEvent        `json:"events"`
	UserDefined    []PersonUserDefined  `json:"userDefined"`
	Memberships    []PersonMembership   `json:"memberships"`
}

// PrimaryName picks the name to import: the one flagged primary, else the
// first, else a zero value.
func (p Person) PrimaryName() PersonName {
	for _, n := range p.Names {
		if n.Metadata.Primary {
			return n
		}
	}
	if len(p.Names) > 0 {
		return p.Names[0]
	}
	return PersonName{}
}

// ConnectionsPage is one page of people.connections.list.
type ConnectionsPage struct {
	Connections   []Person `json:"connections"`
	NextPageToken string   `json:"nextPageToken"`
	// NextSyncToken is only ever present on the LAST page of a listing
	// (Google's contract), full or incremental; the caller stores it once
	// that page is seen.
	NextSyncToken string `json:"nextSyncToken"`
	TotalItems    int    `json:"totalItems"`
}

// ErrSyncTokenExpired is EXPIRED_SYNC_TOKEN (Google answers this as an
// HTTP 410 to people.connections.list when the stored syncToken is too old
// to honour, e.g. after 7 days or a very large number of changes). The
// caller must drop the stored token and list again from scratch.
type errSyncTokenExpired struct{ *APIError }

func (e *errSyncTokenExpired) Unwrap() error { return e.APIError }

// IsSyncTokenExpired reports whether err is Google's 410/EXPIRED_SYNC_TOKEN.
func IsSyncTokenExpired(err error) bool {
	var ae *APIError
	if !asAPIError(err, &ae) {
		return false
	}
	return ae.Status == 410 && strings.EqualFold(ae.Reason, "EXPIRED_SYNC_TOKEN")
}

func asAPIError(err error, out **APIError) bool {
	for err != nil {
		if ae, ok := err.(*APIError); ok {
			*out = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// ListConnectionsPage fetches one page of "people/me/connections" (the
// signed-in account's own saved contacts). pageToken/syncToken are both
// optional; passing syncToken makes this an incremental page (see
// IsSyncTokenExpired for the one error the caller must special-case).
func ListConnectionsPage(ctx context.Context, src *Source, pageToken, syncToken string, other bool) (*ConnectionsPage, error) {
	need := ScopeContacts
	base := API().People + "/people/me/connections"
	if other {
		need = ScopeContactsOther
		base = API().People + "/otherContacts"
	}
	q := url.Values{
		"personFields": {PersonFields},
		"pageSize":     {"200"},
	}
	if other {
		// otherContacts.list has its own, smaller field mask (no
		// memberships/organizations/relations/events/userDefined — Google
		// never associates those with an inferred, unsaved contact).
		q.Set("readMask", "names,emailAddresses,phoneNumbers,metadata")
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	if syncToken != "" {
		q.Set("syncToken", syncToken)
		q.Set("requestSyncToken", "true")
	} else if !other {
		q.Set("requestSyncToken", "true")
	}
	var page ConnectionsPage
	if other {
		var raw struct {
			OtherContacts []Person `json:"otherContacts"`
			NextPageToken string   `json:"nextPageToken"`
		}
		if err := src.Client(need).Get(ctx, base+"?"+q.Encode(), &raw); err != nil {
			return nil, err
		}
		page.Connections = raw.OtherContacts
		page.NextPageToken = raw.NextPageToken
		return &page, nil
	}
	if err := src.Client(need).Get(ctx, base+"?"+q.Encode(), &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// ContactGroup is one entry from contactGroups.list.
type ContactGroup struct {
	ResourceName string `json:"resourceName"`
	Name         string `json:"name"`
	GroupType    string `json:"groupType"`
}

// ListContactGroups fetches every user-created and system contact group
// (paged), for mapping memberships to tags.
func ListContactGroups(ctx context.Context, src *Source) (map[string]ContactGroup, error) {
	out := map[string]ContactGroup{}
	pageToken := ""
	for {
		var res struct {
			ContactGroups []ContactGroup `json:"contactGroups"`
			NextPageToken string         `json:"nextPageToken"`
		}
		q := url.Values{"pageSize": {"200"}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		if err := src.Client(ScopeContacts).Get(ctx, API().People+"/contactGroups?"+q.Encode(), &res); err != nil {
			return nil, err
		}
		for _, g := range res.ContactGroups {
			out[g.ResourceName] = g
		}
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}
	return out, nil
}

// ListAllConnections drains every page of a full (no syncToken) or
// incremental (syncToken set) listing, calling fn for each Person as it
// arrives so a caller can commit incrementally instead of holding the whole
// account in memory. It returns the syncToken to store for next time.
//
// other selects "Other contacts" instead of saved contacts — callers combine
// both when the connection was granted ScopeContactsOther (otherContacts.list
// has no syncToken support at all: Google never offers incremental sync for
// it, so it is always a full pass).
func ListAllConnections(ctx context.Context, src *Source, syncToken string, other bool, fn func(Person) error) (nextSyncToken string, err error) {
	pageToken := ""
	for {
		page, err := ListConnectionsPage(ctx, src, pageToken, syncToken, other)
		if err != nil {
			return "", err
		}
		for _, p := range page.Connections {
			if err := fn(p); err != nil {
				return "", err
			}
		}
		if page.NextSyncToken != "" {
			nextSyncToken = page.NextSyncToken
		}
		if page.NextPageToken == "" {
			break
		}
		pageToken = page.NextPageToken
	}
	return nextSyncToken, nil
}
