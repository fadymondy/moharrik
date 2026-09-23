package googletest

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

/*
The fake's data APIs (MH-440..447). Every call needs a live bearer token the
fake issued, for an account whose grant holds the scope the API needs, exactly
as Google checks: a stale or revoked token is 401 UNAUTHENTICATED, a missing
scope 403 ACCESS_TOKEN_SCOPE_INSUFFICIENT.

Responses are shaped like Google's, including the awkward parts code must
survive: the Data API's counts are strings, an empty list is an absent field
({} rather than {"siteEntry": []}), a report with no data has no "rows",
pagination tokens are opaque, and errors come in both envelopes (the gRPC-style
one of the Analytics APIs and the JSON-API one of webmasters and YouTube).

What each account can see is its AccountData. Accounts with none set get
Demo(account): one GA4 property, one Search Console site and one channel.
*/

// GA4Property is a fake Analytics property.
type GA4Property struct {
	ID          string // "properties/123"
	Name        string
	Account     string // "accounts/1"
	AccountName string
	// Empty: a property created today, with no data yet.
	Empty bool
}

// GSCSite is a fake Search Console property.
type GSCSite struct {
	URL        string // "sc-domain:example.com" or "https://example.com/"
	Permission string // siteOwner | siteFullUser | siteRestrictedUser | siteUnverifiedUser
	Empty      bool
}

// YTVideo is a fake upload.
type YTVideo struct {
	ID               string
	Title            string
	Description      string
	Tags             []string
	Privacy          string // public | unlisted | private
	PublishedAt      time.Time
	Duration         string
	Views            int64
	Likes            int64
	Comments         int64
	CommentsDisabled bool
}

// YTComment is a fake top-level comment thread.
type YTComment struct {
	ID              string
	VideoID         string
	Author          string
	AuthorChannelID string
	Text            string
	Likes           int64
	Replies         int64
	PublishedAt     time.Time
	// The channel owner replied in the thread.
	OwnerReplied bool
}

// YTChannel is a fake channel.
type YTChannel struct {
	ID           string
	Title        string
	Handle       string
	Subscribers  int64
	HiddenSubs   bool
	Views        int64
	Videos       []YTVideo // newest first, as the uploads playlist lists them
	CommentsList []YTComment
	// NoAnalytics: YouTube Analytics has nothing for this channel yet.
	NoAnalytics bool
}

// AccountData is what one Google account can see.
type AccountData struct {
	GA4     []GA4Property
	Sites   []GSCSite
	Channel *YTChannel
	// Sitemaps per site URL (MH-446).
	Sitemaps map[string][]Sitemap
}

// Sitemap is a submitted sitemap.
type Sitemap struct {
	Path           string
	LastSubmitted  time.Time
	LastDownloaded time.Time
	IsPending      bool
	Warnings       int64
	Errors         int64
	Submitted      int64
	Indexed        int64
}

// Demo is the default data of an account: one of each.
func Demo(a Account) *AccountData {
	now := time.Now().UTC()
	h := hashN(a.Sub, 900000) + 100000
	ch := &YTChannel{
		ID: "UC" + pad(a.Sub, 22), Title: "Demo Channel", Handle: "@demochannel",
		Subscribers: 12840, Views: 1_204_331,
	}
	titles := []string{"How we plan a launch", "Five pricing mistakes", "Behind the scenes: our studio", "Customer story: Nile Foods", "Q&A: your questions answered", "Getting started in ten minutes"}
	for i, t := range titles {
		id := fmt.Sprintf("vid%s%02d", pad(a.Sub, 6), i)
		ch.Videos = append(ch.Videos, YTVideo{
			ID: id, Title: t, Description: "A video about " + strings.ToLower(t) + ".", Tags: []string{"demo", "business"},
			Privacy: "public", PublishedAt: now.AddDate(0, 0, -(i*9 + 2)), Duration: fmt.Sprintf("PT%dM%dS", 4+i*2, 10+i*7),
			Views: int64(9000 - i*1200), Likes: int64(420 - i*50), Comments: int64(3 + i),
		})
	}
	ch.CommentsList = []YTComment{
		{ID: "Ugw" + pad(a.Sub, 8) + "c1", VideoID: ch.Videos[0].ID, Author: "Mona Adel", AuthorChannelID: "UCviewer000000000000001", Text: "This helped a lot, thank you!", Likes: 14, PublishedAt: now.Add(-3 * time.Hour)},
		{ID: "Ugw" + pad(a.Sub, 8) + "c2", VideoID: ch.Videos[1].ID, Author: "Karim S.", AuthorChannelID: "UCviewer000000000000002", Text: "Can you do one about annual plans?", Likes: 6, Replies: 1, PublishedAt: now.Add(-26 * time.Hour), OwnerReplied: true},
		{ID: "Ugw" + pad(a.Sub, 8) + "c3", VideoID: ch.Videos[0].ID, Author: "Sara", AuthorChannelID: "UCviewer000000000000003", Text: "Great studio setup.", Likes: 2, PublishedAt: now.Add(-50 * time.Hour)},
	}
	return &AccountData{
		GA4:     []GA4Property{{ID: fmt.Sprintf("properties/%d", h), Name: "demo.example – GA4", Account: fmt.Sprintf("accounts/%d", h/10), AccountName: "Demo Co"}},
		Sites:   []GSCSite{{URL: "sc-domain:demo.example", Permission: "siteOwner"}},
		Channel: ch,
		Sitemaps: map[string][]Sitemap{"sc-domain:demo.example": {{Path: "https://demo.example/sitemap.xml",
			LastSubmitted: now.AddDate(0, 0, -20), LastDownloaded: now.AddDate(0, 0, -1), Submitted: 42, Indexed: 37}}},
	}
}

func pad(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	for len(out) < n {
		out += "0"
	}
	return out[len(out)-n:]
}

func hashN(s string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return int(h.Sum32() % uint32(n))
}

// SetData replaces what an account can see.
func (s *Server) SetData(sub string, d *AccountData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[sub] = d
}

// Data is what an account can see (its Demo until set). The returned value is
// the fake's own: change it under Set.
func (s *Server) Data(sub string) *AccountData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dataOf(Account{Sub: sub})
}

// Empty is an account whose property and site have no data yet and whose
// channel has no uploads.
func Empty(a Account) *AccountData {
	d := Demo(a)
	d.GA4[0].Name, d.GA4[0].Empty = "Brand new site – GA4", true
	d.Sites[0].Empty = true
	d.Channel.Title, d.Channel.Handle, d.Channel.Subscribers, d.Channel.Views = "New Channel", "@newchannel", 0, 0
	d.Channel.Videos, d.Channel.CommentsList, d.Channel.NoAnalytics = nil, nil, true
	d.Sitemaps = nil
	return d
}

// Many is an account that sees several of each kind: the picker case.
func Many(a Account) *AccountData {
	d := Demo(a)
	d.GA4 = []GA4Property{
		{ID: "properties/410000001", Name: "Shop – GA4", Account: "accounts/41000", AccountName: "Acme Retail"},
		{ID: "properties/410000002", Name: "Blog – GA4", Account: "accounts/41000", AccountName: "Acme Retail"},
		{ID: "properties/420000001", Name: "New app (no data yet)", Account: "accounts/42000", AccountName: "Acme Labs", Empty: true},
		{ID: "properties/430000001", Name: "Docs – GA4", Account: "accounts/43000", AccountName: "Acme Docs"},
		{ID: "properties/430000002", Name: "Careers – GA4", Account: "accounts/43000", AccountName: "Acme Docs"},
	}
	d.Sites = []GSCSite{
		{URL: "sc-domain:acme.test", Permission: "siteOwner"},
		{URL: "https://blog.acme.test/", Permission: "siteFullUser"},
		{URL: "https://new.acme.test/", Permission: "siteOwner", Empty: true},
	}
	d.Channel.Title, d.Channel.Handle = "Acme TV", "@acmetv"
	return d
}

func (s *Server) dataOf(a Account) *AccountData {
	d, ok := s.data[a.Sub]
	if !ok {
		d = Demo(a)
		if s.DataFor != nil {
			if x := s.DataFor(a); x != nil {
				d = x
			}
		}
		s.data[a.Sub] = d
	}
	return d
}

// ---- failure injection ----------------------------------------------------------

type failSpec struct {
	status        int
	reason, msg   string
	times         int // -1 = until cleared
	retryAfterSec int
}

// Fail makes the next `times` calls to an API (see apiName) answer status
// with reason; times -1 keeps failing until ClearFailures.
func (s *Server) Fail(api string, status int, reason string, times int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[api] = &failSpec{status: status, reason: reason, times: times, msg: reasonMessage(status, reason)}
}

// ClearFailures stops every injected failure.
func (s *Server) ClearFailures() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = map[string]*failSpec{}
}

func reasonMessage(status int, reason string) string {
	switch reason {
	case "SERVICE_DISABLED", "accessNotConfigured":
		return "Google Analytics Admin API has not been used in project 123456789 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/analyticsadmin.googleapis.com/overview?project=123456789 then retry."
	case "quotaExceeded":
		return "The request cannot be completed because you have exceeded your quota."
	case "rateLimitExceeded", "RESOURCE_EXHAUSTED":
		return "Exhausted property tokens per hour for a project per property."
	}
	return http.StatusText(status)
}

// takeFailure consumes one injected failure for api, if any.
func (s *Server) takeFailure(api string) *failSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.failures[api]
	if f == nil || f.times == 0 {
		return nil
	}
	if f.times > 0 {
		f.times--
	}
	cp := *f
	return &cp
}

// grpcStyle: the Analytics APIs and URL inspection answer gRPC-style errors.
func grpcStyle(api string) bool {
	switch api {
	case "admin", "runReport", "inspect":
		return true
	}
	return false
}

func writeAPIError(w http.ResponseWriter, api string, status int, reason, msg string) {
	if grpcStyle(api) {
		st := map[int]string{400: "INVALID_ARGUMENT", 401: "UNAUTHENTICATED", 403: "PERMISSION_DENIED", 404: "NOT_FOUND", 429: "RESOURCE_EXHAUSTED", 500: "INTERNAL", 503: "UNAVAILABLE"}[status]
		e := map[string]any{"code": status, "message": msg, "status": st}
		if reason != "" && reason != st {
			e["details"] = []map[string]any{{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason, "domain": "googleapis.com"}}
		}
		writeJSON(w, status, map[string]any{"error": e})
		return
	}
	errs := []map[string]string{}
	if reason != "" {
		errs = append(errs, map[string]string{"message": msg, "domain": "global", "reason": reason})
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": msg, "errors": errs}})
}

// ---- auth -------------------------------------------------------------------------

// needScope is the scope each API requires.
func needScope(api string) []string {
	switch api {
	case "admin", "runReport":
		return []string{"https://www.googleapis.com/auth/analytics.readonly"}
	case "sites", "searchAnalytics", "sitemaps", "inspect":
		return []string{"https://www.googleapis.com/auth/webmasters", "https://www.googleapis.com/auth/webmasters.readonly"}
	case "sitemapSubmit":
		return []string{"https://www.googleapis.com/auth/webmasters"}
	case "ytAnalytics":
		return []string{"https://www.googleapis.com/auth/yt-analytics.readonly"}
	case "ytReporting":
		return []string{"https://www.googleapis.com/auth/yt-analytics.readonly", "https://www.googleapis.com/auth/yt-analytics-monetary.readonly"}
	}
	return []string{"https://www.googleapis.com/auth/youtube.readonly"}
}

// begin authenticates a data-API call and applies any injected failure. It
// returns the caller's account and data, or false when it has answered.
func (s *Server) begin(w http.ResponseWriter, r *http.Request, api string) (Account, *AccountData, bool) {
	s.count("api:" + api)
	at := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.URL.Query().Get("access_token") != "" {
		http.Error(w, "token in query", http.StatusBadRequest)
		return Account{}, nil, false
	}
	s.mu.Lock()
	g, ok := s.access[at]
	var scopes map[string]bool
	if ok {
		scopes = s.granted[g.account.Sub]
	}
	s.mu.Unlock()
	if !ok || time.Now().After(g.expires) {
		writeAPIError(w, "admin", http.StatusUnauthorized, "", "Request had invalid authentication credentials. Expected OAuth 2 access token, login cookie or other valid authentication credential.")
		return Account{}, nil, false
	}
	allowed := false
	for _, sc := range needScope(api) {
		if scopes[sc] {
			allowed = true
		}
	}
	if !allowed {
		writeAPIError(w, api, http.StatusForbidden, "ACCESS_TOKEN_SCOPE_INSUFFICIENT", "Request had insufficient authentication scopes.")
		return Account{}, nil, false
	}
	if f := s.takeFailure(api); f != nil {
		if f.retryAfterSec > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(f.retryAfterSec))
		}
		writeAPIError(w, api, f.status, f.reason, f.msg)
		return Account{}, nil, false
	}
	s.mu.Lock()
	d := s.dataOf(g.account)
	s.mu.Unlock()
	return g.account, d, true
}

// ---- routing ------------------------------------------------------------------------

// api dispatches every data-API path. Paths are matched on the ESCAPED path:
// a Search Console site URL travels percent-encoded inside it.
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	p := r.URL.EscapedPath()
	switch {
	case p == "/analyticsadmin/v1beta/accountSummaries" && r.Method == http.MethodGet:
		s.accountSummaries(w, r)
	case p == "/webmasters/v3/sites" && r.Method == http.MethodGet:
		s.sites(w, r)
	case p == "/youtube/v3/channels" && r.Method == http.MethodGet:
		s.channels(w, r)
	case p == "/_fake/fail" && r.Method == http.MethodPost:
		// The dev check's remote control: ?api=runReport&status=429&reason=…&times=-1
		// (status=0 clears every failure).
		q := r.URL.Query()
		status, _ := strconv.Atoi(q.Get("status"))
		times, err := strconv.Atoi(q.Get("times"))
		if err != nil {
			times = -1
		}
		if status == 0 {
			s.ClearFailures()
		} else {
			s.Fail(q.Get("api"), status, q.Get("reason"), times)
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(p, "/img/"):
		// Avatars and thumbnails, so a page showing them loads real images.
		s.image(w, p)
	default:
		if s.route(w, r, p) {
			return
		}
		writeAPIError(w, "", http.StatusNotFound, "notFound", "The requested URL "+p+" was not found on this server.")
	}
}

// ---- Analytics Admin: accountSummaries.list ------------------------------------

func (s *Server) accountSummaries(w http.ResponseWriter, r *http.Request) {
	_, d, ok := s.begin(w, r, "admin")
	if !ok {
		return
	}
	type prop struct {
		Property     string `json:"property"`
		DisplayName  string `json:"displayName"`
		PropertyType string `json:"propertyType"`
		Parent       string `json:"parent"`
	}
	type summary struct {
		Name              string `json:"name"`
		Account           string `json:"account"`
		DisplayName       string `json:"displayName"`
		PropertySummaries []prop `json:"propertySummaries,omitempty"`
	}
	s.mu.Lock()
	byAcc := map[string]*summary{}
	order := []string{}
	for _, p := range d.GA4 {
		sm, ok := byAcc[p.Account]
		if !ok {
			sm = &summary{Name: "accountSummaries/" + strings.TrimPrefix(p.Account, "accounts/"), Account: p.Account, DisplayName: p.AccountName}
			byAcc[p.Account] = sm
			order = append(order, p.Account)
		}
		sm.PropertySummaries = append(sm.PropertySummaries, prop{Property: p.ID, DisplayName: p.Name, PropertyType: "PROPERTY_TYPE_ORDINARY", Parent: p.Account})
	}
	pageSize := s.AdminPageSize
	s.mu.Unlock()
	if pageSize <= 0 {
		pageSize = 2
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("pageSize")); err == nil && n > 0 && n < pageSize {
		pageSize = n
	}
	start := 0
	if t := r.URL.Query().Get("pageToken"); t != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(t, "acct-page-"))
		if err != nil {
			writeAPIError(w, "admin", http.StatusBadRequest, "", "Invalid page token.")
			return
		}
		start = n
	}
	out := map[string]any{}
	list := []summary{}
	for i := start; i < len(order) && i < start+pageSize; i++ {
		list = append(list, *byAcc[order[i]])
	}
	if len(list) > 0 {
		out["accountSummaries"] = list
	}
	if start+pageSize < len(order) {
		out["nextPageToken"] = "acct-page-" + strconv.Itoa(start+pageSize)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- Search Console: sites.list ---------------------------------------------------

func (s *Server) sites(w http.ResponseWriter, r *http.Request) {
	_, d, ok := s.begin(w, r, "sites")
	if !ok {
		return
	}
	s.mu.Lock()
	entries := []map[string]string{}
	for _, st := range d.Sites {
		entries = append(entries, map[string]string{"siteUrl": st.URL, "permissionLevel": st.Permission})
	}
	s.mu.Unlock()
	out := map[string]any{}
	if len(entries) > 0 { // Google omits the field when there are none
		out["siteEntry"] = entries
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- YouTube Data: channels.list mine=true -----------------------------------------

func (s *Server) channels(w http.ResponseWriter, r *http.Request) {
	_, d, ok := s.begin(w, r, "channels")
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("mine") != "true" && q.Get("id") == "" {
		writeAPIError(w, "channels", http.StatusBadRequest, "missingRequiredParameter", "No filter selected. Expected one of: mine, id, forHandle, …")
		return
	}
	out := map[string]any{"kind": "youtube#channelListResponse", "etag": "fake-etag",
		"pageInfo": map[string]int{"totalResults": 0, "resultsPerPage": 5}}
	s.mu.Lock()
	ch := d.Channel
	var items []map[string]any
	if ch != nil {
		items = append(items, channelResource(s.URL, ch, strings.Split(q.Get("part"), ",")))
	}
	s.mu.Unlock()
	if len(items) > 0 {
		out["items"] = items
		out["pageInfo"] = map[string]int{"totalResults": len(items), "resultsPerPage": 5}
	}
	writeJSON(w, http.StatusOK, out)
}

func thumbs(base string) map[string]any {
	return map[string]any{
		"default": map[string]any{"url": base + "/default.jpg", "width": 88, "height": 88},
		"medium":  map[string]any{"url": base + "/mqdefault.jpg", "width": 240, "height": 240},
		"high":    map[string]any{"url": base + "/hqdefault.jpg", "width": 800, "height": 800},
	}
}

func channelResource(origin string, ch *YTChannel, parts []string) map[string]any {
	it := map[string]any{"kind": "youtube#channel", "etag": "fake", "id": ch.ID}
	for _, p := range parts {
		switch strings.TrimSpace(p) {
		case "snippet":
			sn := map[string]any{"title": ch.Title, "description": "", "publishedAt": "2019-04-02T10:00:00Z",
				"thumbnails": thumbs(origin + "/img/" + ch.ID)}
			if ch.Handle != "" {
				sn["customUrl"] = ch.Handle
			}
			it["snippet"] = sn
		case "contentDetails":
			it["contentDetails"] = map[string]any{"relatedPlaylists": map[string]string{"likes": "", "uploads": "UU" + strings.TrimPrefix(ch.ID, "UC")}}
		case "statistics":
			st := map[string]any{"viewCount": strconv.FormatInt(ch.Views, 10), "hiddenSubscriberCount": ch.HiddenSubs,
				"videoCount": strconv.Itoa(len(ch.Videos))}
			if !ch.HiddenSubs {
				st["subscriberCount"] = strconv.FormatInt(ch.Subscribers, 10)
			}
			it["statistics"] = st
		}
	}
	return it
}

// ---- helpers for the per-API files ------------------------------------------------------

func readJSONBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func unescapeSegment(s string) string {
	u, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return u
}

func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// image is a tiny SVG standing in for a YouTube avatar or thumbnail, tinted by
// its path so different videos look different.
func (s *Server) image(w http.ResponseWriter, p string) {
	hue := hashN(p, 360)
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 180"><rect width="320" height="180" fill="hsl(%d 45%% 55%%)"/><circle cx="160" cy="90" r="34" fill="hsl(%d 45%% 85%%)"/><path d="M150 72 L176 90 L150 108 Z" fill="hsl(%d 45%% 40%%)"/></svg>`, hue, hue, hue)
}
