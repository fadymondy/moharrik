package googletest

import (
	"net/http"
	"strconv"
	"strings"
)

/*
The fake's People API (MH-585): people.connections.list (paged, syncToken /
nextSyncToken, EXPIRED_SYNC_TOKEN), contactGroups.list and otherContacts.list.

Unlike the other fake data APIs (data.go), a test builds a person as a plain
map[string]any shaped like Google's Person resource (ContactFields) rather
than a typed Go struct — internal/google's own Person type already decodes
that JSON shape, so re-declaring it here would just be a second copy to keep
in sync. SetContact/DeleteContact assign each change a monotonically
increasing version per account; an incremental (syncToken) listing replays
every change since the token's version, and ExpireContactsSyncToken moves the
floor past any token issued before it, so the next incremental call for that
account gets a 410 EXPIRED_SYNC_TOKEN — exactly the case a real syncToken
outliving Google's retention window produces.
*/

// ContactFields is one Person, People-API-shaped: e.g.
//
//	ContactFields{"names": []map[string]any{{"givenName": "Sam"}}}
type ContactFields = map[string]any

type fakePerson struct {
	resourceName string
	fields       ContactFields
	version      int
	deleted      bool
}

type contactsState struct {
	order   []string // resourceName, first-seen order (stable listing order)
	people  map[string]*fakePerson
	other   []ContactFields
	groups  []ContactFields
	version int // bumped on every SetContact/DeleteContact
	floor   int // a syncToken for a version below this is EXPIRED_SYNC_TOKEN
	pageCap int // items per page; 0 = one page holds everything
}

func (s *Server) contactsOf(sub string) *contactsState {
	c, ok := s.contacts[sub]
	if !ok {
		c = &contactsState{people: map[string]*fakePerson{}}
		s.contacts[sub] = c
	}
	return c
}

// SetContact upserts one Google Contact for sub (a bare "sub-…" id, or the
// account's own Sub) and bumps its account's contacts version, so it shows
// up on the next full listing and on any incremental listing whose token
// predates this call.
func (s *Server) SetContact(sub, resourceName string, fields ContactFields) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contactsOf(sub)
	c.version++
	p, ok := c.people[resourceName]
	if !ok {
		p = &fakePerson{resourceName: resourceName}
		c.people[resourceName] = p
		c.order = append(c.order, resourceName)
	}
	p.fields, p.version, p.deleted = fields, c.version, false
}

// DeleteContact marks a contact deleted at Google: an incremental listing
// past this point reports it with metadata.deleted=true (never removes it
// from the fake's own bookkeeping, same as Google keeps a deleted person
// resolvable for a while).
func (s *Server) DeleteContact(sub, resourceName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contactsOf(sub)
	c.version++
	p, ok := c.people[resourceName]
	if !ok {
		p = &fakePerson{resourceName: resourceName}
		c.people[resourceName] = p
		c.order = append(c.order, resourceName)
	}
	p.deleted, p.version = true, c.version
}

// SetOtherContacts replaces sub's "Other contacts" (otherContacts.list has
// no incremental sync at Google, so there is no version/floor for these).
func (s *Server) SetOtherContacts(sub string, people []ContactFields) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contactsOf(sub).other = people
}

// SetContactGroups replaces sub's contact groups (each map needs at least
// resourceName, name and groupType).
func (s *Server) SetContactGroups(sub string, groups []ContactFields) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contactsOf(sub).groups = groups
}

// ExpireContactsSyncToken makes every syncToken issued so far for sub answer
// 410 EXPIRED_SYNC_TOKEN on its next use, forcing the caller into a full
// resync — the case Google's own retention window (roughly 7 days, or a
// large number of changes) eventually produces.
func (s *Server) ExpireContactsSyncToken(sub string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.contactsOf(sub)
	c.floor = c.version + 1
}

// SetContactsPageSize caps how many people/groups one page of a listing for
// sub holds, to exercise pageToken paging deterministically. 0 (default)
// means one page holds everything.
func (s *Server) SetContactsPageSize(sub string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contactsOf(sub).pageCap = n
}

func personJSON(p *fakePerson) map[string]any {
	out := map[string]any{}
	for k, v := range p.fields {
		out[k] = v
	}
	out["resourceName"] = p.resourceName
	out["etag"] = "etag-" + strconv.Itoa(p.version)
	if p.deleted {
		// Google's contract: a deleted entry on an incremental page carries
		// only resourceName/etag/metadata — no other fields.
		return map[string]any{"resourceName": p.resourceName, "etag": out["etag"], "metadata": map[string]any{"deleted": true}}
	}
	return out
}

func paginate[T any](items []T, pageToken string, cap int) (page []T, next string) {
	offset := 0
	if pageToken != "" {
		offset, _ = strconv.Atoi(pageToken)
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := len(items)
	if cap > 0 && offset+cap < end {
		end = offset + cap
	}
	page = items[offset:end]
	if end < len(items) {
		next = strconv.Itoa(end)
	}
	return page, next
}

// people serves people.connections.list, otherContacts.list and
// contactGroups.list. It reports whether it answered p.
func (s *Server) people(w http.ResponseWriter, r *http.Request, p string) bool {
	switch {
	case p == "/people/v1/people/me/connections" && r.Method == http.MethodGet:
		s.listConnections(w, r)
	case p == "/people/v1/otherContacts" && r.Method == http.MethodGet:
		s.listOtherContacts(w, r)
	case p == "/people/v1/contactGroups" && r.Method == http.MethodGet:
		s.listContactGroups(w, r)
	default:
		return false
	}
	return true
}

func (s *Server) listConnections(w http.ResponseWriter, r *http.Request) {
	acc, _, ok := s.begin(w, r, "connections")
	if !ok {
		return
	}
	q := r.URL.Query()
	pageToken, syncToken := q.Get("pageToken"), q.Get("syncToken")

	s.mu.Lock()
	c := s.contactsOf(acc.Sub)
	var eligible []*fakePerson
	if syncToken != "" {
		tokVer, err := strconv.Atoi(strings.TrimPrefix(syncToken, "sync-"))
		if err != nil || tokVer < c.floor {
			s.mu.Unlock()
			writeAPIError(w, "connections", http.StatusGone, "EXPIRED_SYNC_TOKEN",
				"Sync token is expired. Clear local cache and retry call without the sync token.")
			return
		}
		for _, rn := range c.order {
			if p := c.people[rn]; p.version > tokVer {
				eligible = append(eligible, p)
			}
		}
	} else {
		for _, rn := range c.order {
			if p := c.people[rn]; !p.deleted {
				eligible = append(eligible, p)
			}
		}
	}
	total := len(eligible)
	currentVersion, cap := c.version, c.pageCap
	s.mu.Unlock()

	page, next := paginate(eligible, pageToken, cap)
	out := struct {
		Connections   []map[string]any `json:"connections,omitempty"`
		NextPageToken string           `json:"nextPageToken,omitempty"`
		NextSyncToken string           `json:"nextSyncToken,omitempty"`
		TotalItems    int              `json:"totalItems"`
	}{TotalItems: total}
	for _, p := range page {
		out.Connections = append(out.Connections, personJSON(p))
	}
	out.NextPageToken = next
	if next == "" {
		out.NextSyncToken = "sync-" + strconv.Itoa(currentVersion)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listOtherContacts(w http.ResponseWriter, r *http.Request) {
	acc, _, ok := s.begin(w, r, "otherContacts")
	if !ok {
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	c := s.contactsOf(acc.Sub)
	all, cap := append([]ContactFields(nil), c.other...), c.pageCap
	s.mu.Unlock()

	page, next := paginate(all, q.Get("pageToken"), cap)
	out := struct {
		OtherContacts []map[string]any `json:"otherContacts,omitempty"`
		NextPageToken string           `json:"nextPageToken,omitempty"`
	}{NextPageToken: next}
	for _, f := range page {
		cp := map[string]any{}
		for k, v := range f {
			cp[k] = v
		}
		if _, ok := cp["resourceName"]; !ok {
			cp["resourceName"] = "otherContacts/unset"
		}
		out.OtherContacts = append(out.OtherContacts, cp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listContactGroups(w http.ResponseWriter, r *http.Request) {
	acc, _, ok := s.begin(w, r, "contactGroups")
	if !ok {
		return
	}
	q := r.URL.Query()
	s.mu.Lock()
	c := s.contactsOf(acc.Sub)
	all, cap := append([]ContactFields(nil), c.groups...), c.pageCap
	s.mu.Unlock()

	page, next := paginate(all, q.Get("pageToken"), cap)
	out := struct {
		ContactGroups []map[string]any `json:"contactGroups,omitempty"`
		NextPageToken string           `json:"nextPageToken,omitempty"`
	}{NextPageToken: next}
	out.ContactGroups = append(out.ContactGroups, page...)
	writeJSON(w, http.StatusOK, out)
}
