package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pglock"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/trends"
)

/*
Collection (MH-384): the notification center polls, as well as receiving
webhooks. Ported from fadymondy.com's internal/center (collect.go, public.go,
push.go), made per workspace.

fadymondy.com reads its logged-in feeds (Gmail, Facebook, LinkedIn, X, …)
through browser plugins run on the automation schedules, and only Bluesky
through a public API. Moharrik builds the public half first, as the card asks,
so the center is useful before the plugins land:

	bluesky  replies by other accounts to a handle's recent posts, from the
	         public AppView (app.bsky.feed.getAuthorFeed + getPostThread) —
	         fadymondy.com's collector.API.bluesky "replies" mode
	youtube  a channel's new uploads, from its public feed
	         (youtube.com/feeds/videos.xml?channel_id=…); a connected channel's
	         COMMENTS already arrive through the YouTube integration (MH-441)
	rss      any RSS or Atom feed (a status page, a changelog, a forum)

Everything else in the catalog is "not configured" and says what it needs.

# Scheduling

One in-process ticker (NOTIFY_COLLECTOR=off turns it off, for the tests and
the OpenAPI export). The queue is read on the owner pool, the one cross-tenant
read, exactly as the trends collector reads feed_sources; every write after
that runs on the app pool scoped to the source's workspace. A source's next
run is its own interval; a failure retries on the same interval rather than
hammering a service that is down.

# Budget

fadymondy.com's center takes a share of the browser's hourly run limit
(CENTER_RUNS_PER_HOUR, default 20) and refuses a configuration that would
exceed it. The public APIs here have no such hard limit, but they are other
people's servers, so the same rule applies per workspace:
NOTIFY_RUNS_PER_HOUR (default 20), counted as the sum of 60/interval over the
enabled sources.

# First run

The first collection of a new source files what it finds but alerts nobody
(fadymondy.com's "connecting never floods" rule for YouTube comments): a
Bluesky account with forty replies should not ring forty phones the moment it
is added.
*/

// Intervals a collected source may poll at, in minutes (fadymondy.com's list).
var Intervals = []int{15, 20, 30, 60, 120, 180, 240, 360, 720, 1440}

func validInterval(m int) bool {
	for _, v := range Intervals {
		if v == m {
			return true
		}
	}
	return false
}

// RunsPerHourBudget is how many polls an hour one workspace's collected
// sources may add up to.
func RunsPerHourBudget() float64 {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("NOTIFY_RUNS_PER_HOUR"))); err == nil && v > 0 {
		return float64(v)
	}
	return 20
}

func runsPerHour(interval int) float64 {
	if interval <= 0 {
		return 0
	}
	return 60 / float64(interval)
}

// pushBatchLimit: more new push-worthy items than this from one poll become a
// single summary alert (fadymondy.com center/push.go).
const pushBatchLimit = 3

// ---- where the public services are ----------------------------------------------

// Endpoints are the public services the collectors read. Tests point them at
// httptest servers; production uses the defaults.
type Endpoints struct {
	Bluesky string // https://public.api.bsky.app
	YouTube string // https://www.youtube.com
}

var endpoints = Endpoints{Bluesky: "https://public.api.bsky.app", YouTube: "https://www.youtube.com"}

// SetEndpoints replaces the endpoints and returns a function restoring them.
// Tests only.
func SetEndpoints(e Endpoints) func() {
	prev := endpoints
	endpoints = e
	allowPrivate = true
	return func() {
		endpoints = prev
		allowPrivate = false
	}
}

// allowPrivate lets the collector reach loopback and private addresses. Only
// the tests (httptest listens on loopback) and NOTIFY_ALLOW_PRIVATE=1 turn it
// on: an RSS URL is typed by a workspace member, and without this rule it
// could point the server at its own network (the API, the database, the
// cloud metadata endpoint) and hand the answer back as items.
var allowPrivate = os.Getenv("NOTIFY_ALLOW_PRIVATE") == "1"

const collectUA = "Moharrik/1.0 (+https://moharrik.com) notification-center"

// maxFetch bounds any one response a collector reads.
const maxFetch = 4 << 20

var errPrivate = errors.New("that address is on a private network, which the notification center does not read")

// client refuses to connect to a non-public address, checked at DIAL time on
// the resolved IP (so a public name that resolves to 127.0.0.1, or a
// redirect to one, is refused too, not just a literal private URL).
var client = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				if allowPrivate {
					return nil
				}
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || !publicIP(ip) {
					return errPrivate
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       60 * time.Second,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return errors.New("a redirect left http(s)")
		}
		return nil
	},
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		// 100.64.0.0/10 (carrier-grade NAT, also Tailscale) is not "private" to Go.
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64))
}

func get(ctx context.Context, u, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("that address is not usable: %s", err.Error())
	}
	req.Header.Set("User-Agent", collectUA)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	res, err := client.Do(req)
	if err != nil && !errors.Is(err, errPrivate) && ctx.Err() == nil {
		// One retry for a transient failure (a DNS lookup that timed out, a
		// reset connection): a source should not show "failing" for an hour
		// because one packet was lost. A second failure is reported as it is.
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			res, err = client.Do(req.Clone(ctx))
		}
	}
	if err != nil {
		if errors.Is(err, errPrivate) {
			return nil, errPrivate
		}
		return nil, fmt.Errorf("could not reach it: %s", shortErr(err))
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxFetch))
	if err != nil {
		return nil, fmt.Errorf("the answer was cut off: %s", shortErr(err))
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return body, &httpStatusError{Status: res.StatusCode}
	}
	return body, nil
}

type httpStatusError struct{ Status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("it answered %d", e.Status) }

func shortErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	s := err.Error()
	if errors.Is(err, errPrivate) || strings.Contains(s, errPrivate.Error()) {
		return errPrivate.Error()
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ---- the collected item ------------------------------------------------------

// collected is one item a collector found, before it is stored.
type collected struct {
	ExternalID string
	Kind       string
	Title      string
	Body       string
	Actor      string
	URL        string
	Payload    string
	OccurredAt time.Time
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ---- Bluesky ---------------------------------------------------------------------

var handleRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

// normaliseHandle accepts @name.bsky.social, name.bsky.social or a profile
// URL, and returns the bare handle (or a did:plc: id).
func normaliseHandle(s string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "https://bsky.app/profile/")
	s = strings.TrimPrefix(strings.TrimSuffix(s, "/"), "@")
	if strings.HasPrefix(s, "did:plc:") && len(s) > 10 && len(s) < 60 {
		return s, true
	}
	s = strings.ToLower(s)
	return s, handleRE.MatchString(s) && len(s) <= 253
}

type bskyPost struct {
	URI    string `json:"uri"`
	Author struct {
		DID         string `json:"did"`
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
	} `json:"author"`
	Record struct {
		Text      string `json:"text"`
		CreatedAt string `json:"createdAt"`
	} `json:"record"`
	ReplyCount int    `json:"replyCount"`
	IndexedAt  string `json:"indexedAt"`
}

func bskyWebURL(p bskyPost) string {
	parts := strings.Split(strings.TrimPrefix(p.URI, "at://"), "/")
	if len(parts) != 3 {
		return ""
	}
	who := p.Author.Handle
	if who == "" {
		who = parts[0]
	}
	return "https://bsky.app/profile/" + who + "/post/" + parts[2]
}

func bskyTime(p bskyPost) time.Time {
	for _, v := range []string{p.Record.CreatedAt, p.IndexedAt} {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// bskyProfile checks a handle exists and returns its display name.
func bskyProfile(ctx context.Context, handle string) (string, error) {
	body, err := get(ctx, endpoints.Bluesky+"/xrpc/app.bsky.actor.getProfile?"+url.Values{"actor": {handle}}.Encode(), "application/json")
	var se *httpStatusError
	if errors.As(err, &se) && se.Status == 400 {
		return "", fmt.Errorf("Bluesky has no account %q", handle)
	}
	if err != nil {
		return "", fmt.Errorf("Bluesky: %s", err.Error())
	}
	var p struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
	}
	if json.Unmarshal(body, &p) != nil || p.Handle == "" {
		return "", fmt.Errorf("Bluesky answered something that is not a profile")
	}
	if p.DisplayName != "" {
		return p.DisplayName, nil
	}
	return p.Handle, nil
}

// maxRepliesPerPost bounds how many replies one post contributes to one poll.
const maxRepliesPerPost = 40

// collectBluesky reads replies by other accounts to the handle's recent posts:
// the public half of a Bluesky notification feed. It opens at most five
// recent posts that have replies (fadymondy.com's "posts" default).
func collectBluesky(ctx context.Context, handle string) ([]collected, error) {
	feedURL := endpoints.Bluesky + "/xrpc/app.bsky.feed.getAuthorFeed?" + url.Values{
		"actor": {handle}, "limit": {"20"}, "filter": {"posts_no_replies"},
	}.Encode()
	body, err := get(ctx, feedURL, "application/json")
	if err != nil {
		return nil, fmt.Errorf("Bluesky: %s", err.Error())
	}
	var doc struct {
		Feed []struct {
			Post bskyPost `json:"post"`
		} `json:"feed"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("Bluesky answered something that is not a feed")
	}
	var out []collected
	open := 5
	for _, f := range doc.Feed {
		p := f.Post
		if open <= 0 {
			break
		}
		if p.ReplyCount == 0 || p.URI == "" {
			continue
		}
		open--
		threadURL := endpoints.Bluesky + "/xrpc/app.bsky.feed.getPostThread?" + url.Values{"uri": {p.URI}, "depth": {"1"}, "parentHeight": {"0"}}.Encode()
		tb, err := get(ctx, threadURL, "application/json")
		if err != nil {
			return out, fmt.Errorf("Bluesky: %s", err.Error())
		}
		var thread struct {
			Thread struct {
				Replies []struct {
					Post bskyPost `json:"post"`
				} `json:"replies"`
			} `json:"thread"`
		}
		if err := json.Unmarshal(tb, &thread); err != nil {
			continue
		}
		// A popular post can have hundreds of direct replies; the newest
		// maxRepliesPerPost are the notifications, and a reply older than that
		// window was either seen on an earlier poll or is not news any more.
		sort.SliceStable(thread.Thread.Replies, func(i, j int) bool {
			return bskyTime(thread.Thread.Replies[i].Post).After(bskyTime(thread.Thread.Replies[j].Post))
		})
		if len(thread.Thread.Replies) > maxRepliesPerPost {
			thread.Thread.Replies = thread.Thread.Replies[:maxRepliesPerPost]
		}
		for _, r := range thread.Thread.Replies {
			rp := r.Post
			if rp.URI == "" || strings.EqualFold(rp.Author.Handle, handle) || rp.Author.DID == p.Author.DID {
				continue // the account answering its own thread is not a notification
			}
			who := rp.Author.DisplayName
			if who == "" {
				who = rp.Author.Handle
			}
			raw, _ := json.Marshal(map[string]any{"reply": rp, "parent_uri": p.URI, "parent_text": cut(p.Record.Text, 300)})
			out = append(out, collected{
				ExternalID: rp.URI, Kind: "reply",
				Title: cut(who+" replied: "+strings.Join(strings.Fields(rp.Record.Text), " "), 300),
				Body:  cut(rp.Record.Text+"\n\n↳ "+cut(p.Record.Text, 200), 2000),
				Actor: who, URL: bskyWebURL(rp), Payload: string(raw), OccurredAt: bskyTime(rp),
			})
		}
	}
	return out, nil
}

// ---- YouTube -------------------------------------------------------------------------

var channelIDRE = regexp.MustCompile(`^UC[0-9A-Za-z_-]{22}$`)
var externalIDRE = regexp.MustCompile(`"(?:externalId|channelId)":"(UC[0-9A-Za-z_-]{22})"`)

// resolveChannel turns a channel id, a channel URL or an @handle into the
// channel id its public feed is keyed by.
func resolveChannel(ctx context.Context, s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, p := range []string{"https://www.youtube.com/channel/", "https://youtube.com/channel/", "http://www.youtube.com/channel/"} {
		s = strings.TrimPrefix(s, p)
	}
	s = strings.TrimSuffix(s, "/")
	if channelIDRE.MatchString(s) {
		return s, nil
	}
	handle := s
	for _, p := range []string{"https://www.youtube.com/", "https://youtube.com/", "https://m.youtube.com/"} {
		handle = strings.TrimPrefix(handle, p)
	}
	if !strings.HasPrefix(handle, "@") {
		handle = "@" + handle
	}
	if !regexp.MustCompile(`^@[A-Za-z0-9._-]{3,100}$`).MatchString(handle) {
		return "", errors.New("give a YouTube channel id (UC…), a channel URL or an @handle")
	}
	body, err := get(ctx, endpoints.YouTube+"/"+handle, "text/html")
	if err != nil {
		var se *httpStatusError
		if errors.As(err, &se) && se.Status == 404 {
			return "", fmt.Errorf("YouTube has no channel %s", handle)
		}
		return "", fmt.Errorf("YouTube: %s", err.Error())
	}
	m := externalIDRE.FindSubmatch(body)
	if m == nil {
		return "", fmt.Errorf("could not find the channel id for %s; paste the channel id (UC…) instead", handle)
	}
	return string(m[1]), nil
}

func youtubeFeedURL(channelID string) string {
	return endpoints.YouTube + "/feeds/videos.xml?" + url.Values{"channel_id": {channelID}}.Encode()
}

func collectFeed(ctx context.Context, feedURL, kind string) ([]collected, string, error) {
	body, err := get(ctx, feedURL, "application/atom+xml, application/rss+xml, application/xml;q=0.9, */*;q=0.8")
	if err != nil {
		return nil, "", err
	}
	feed, err := trends.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, "", errors.New("that address is not an RSS or Atom feed")
	}
	out := make([]collected, 0, len(feed.Items))
	for i, it := range feed.Items {
		if i >= 50 {
			break
		}
		id := it.ExternalID
		if id == "" {
			sum := sha256.Sum256([]byte(it.URL + "\x1f" + it.Title))
			id = hex.EncodeToString(sum[:])
		}
		raw, _ := json.Marshal(map[string]any{"feed": feed.Title, "entry": it})
		title := it.Title
		if kind == "video" {
			title = cut("New video: "+it.Title, 300)
		}
		out = append(out, collected{
			ExternalID: cut(id, 500), Kind: kind, Title: cut(title, 300), Body: cut(it.Summary, 2000),
			Actor: cut(it.Author, 200), URL: it.URL, Payload: string(raw), OccurredAt: it.PublishedAt,
		})
	}
	return out, feed.Title, nil
}

// ---- running one source ----------------------------------------------------------

// collectorSource is a due source as the collector reads it.
type collectorSource struct {
	ID, WorkspaceID, Kind, Name, Target string
	Interval                            int
	PushEnabled                         bool
	PushKinds                           string
	FirstRun                            bool
}

// fetch runs the source's collector.
func fetch(ctx context.Context, s collectorSource) ([]collected, error) {
	switch s.Kind {
	case "bluesky":
		return collectBluesky(ctx, s.Target)
	case "youtube":
		items, _, err := collectFeed(ctx, youtubeFeedURL(s.Target), "video")
		if err != nil {
			var se *httpStatusError
			if errors.As(err, &se) && se.Status == 404 {
				// YouTube answers 404 for a real channel's feed now and then, for
				// hours at a time; the next run tries again.
				return nil, errors.New("YouTube: the channel's public feed answered 404 (YouTube does this for a while at times; the next check tries again)")
			}
			return nil, fmt.Errorf("YouTube: %s", err.Error())
		}
		return items, nil
	case "rss":
		items, _, err := collectFeed(ctx, s.Target, "post")
		return items, err
	}
	return nil, fmt.Errorf("the %s collector does not exist", s.Kind)
}

// collectOne runs one source, stores what is new, records the outcome on the
// source and tells the workspace. It never returns an error: a failing source
// is a status on its row ("last_error"), not a failure of the collector.
func collectOne(ctx context.Context, a *app.App, s collectorSource) (int, error) {
	sctx := tenant.WithWorkspace(ctx, s.WorkspaceID)
	fctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	items, ferr := fetch(fctx, s)
	cancel()

	var fresh []NotificationItemOut
	inserted := 0
	if ferr == nil {
		now := time.Now().UTC()
		for _, it := range items {
			if it.ExternalID == "" || it.Title == "" {
				continue
			}
			at := it.OccurredAt
			if at.IsZero() || at.After(now.Add(time.Hour)) {
				at = now
			}
			var id string
			err := a.SQLDB.QueryRowContext(sctx, `
				INSERT INTO notification_items (workspace_id, feed_source_id, source_name, external_id, kind, severity, title, body, actor, url, payload, occurred_at)
				VALUES ($1, $2, $3, $4, $5, 'info', $6, $7, $8, $9, $10, $11)
				ON CONFLICT (workspace_id, feed_source_id, external_id) WHERE feed_source_id IS NOT NULL AND external_id <> '' DO NOTHING
				RETURNING id`,
				s.WorkspaceID, s.ID, s.Name, it.ExternalID, it.Kind, it.Title, it.Body, it.Actor, it.URL, it.Payload, at,
			).Scan(&id)
			if notFound(err) {
				continue // seen on an earlier poll
			} else if err != nil {
				ferr = errors.New("could not store what it found")
				break
			}
			inserted++
			fresh = append(fresh, NotificationItemOut{ID: id, Kind: it.Kind, Title: it.Title, Body: it.Body, SourceName: s.Name})
		}
	}

	status, msg := "ok", ""
	if ferr != nil {
		status, msg = "failed", cut(ferr.Error(), 500)
	}
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE notification_sources SET
		last_run_at = now(), next_run_at = now() + make_interval(mins => interval_minutes),
		last_status = $3, last_error = $4, last_item_count = $5,
		last_collected_at = CASE WHEN $3 = 'ok' THEN now() ELSE last_collected_at END,
		updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, s.ID, status, msg, inserted)

	announceCollected(ctx, a, s, fresh)
	return inserted, ferr
}

// announceCollected tells the members' tabs, and alerts per the source's push
// settings: one alert per item, or one summary above pushBatchLimit
// (fadymondy.com's pushItems). The first run of a source alerts nobody.
func announceCollected(ctx context.Context, a *app.App, s collectorSource, fresh []NotificationItemOut) {
	if len(fresh) == 0 {
		// The source's status moved; the sources page listens for this too.
		alerts.Emit(ctx, a, s.WorkspaceID, realtime.Event{Kind: "notifications", Permission: authz.NotificationsRead})
		return
	}
	var push []NotificationItemOut
	if s.PushEnabled && !s.FirstRun {
		for _, it := range fresh {
			if pushKind(s.PushKinds, it.Kind) {
				push = append(push, it)
			}
		}
	}
	if len(push) == 0 {
		alerts.Emit(ctx, a, s.WorkspaceID, realtime.Event{Kind: "notifications", Permission: authz.NotificationsRead})
		return
	}
	if len(push) > pushBatchLimit {
		alerts.Emit(ctx, a, s.WorkspaceID, realtime.Event{
			Kind: "notification", ItemID: push[0].ID, Title: fmt.Sprintf("%s: %d new", s.Name, len(push)),
			Severity: "info", Source: s.Name, Permission: authz.NotificationsRead, Alerting: true,
		})
		return
	}
	for _, it := range push {
		alerts.Emit(ctx, a, s.WorkspaceID, realtime.Event{
			Kind: "notification", ItemID: it.ID, Title: it.Title, Severity: "info", Source: s.Name,
			Permission: authz.NotificationsRead, Alerting: true,
		})
	}
}

// pushKind reports whether an item of this kind should alert. kinds is a
// comma-separated list; empty means every kind.
func pushKind(kinds, kind string) bool {
	kinds = strings.TrimSpace(kinds)
	if kinds == "" {
		return true
	}
	for _, k := range strings.Split(kinds, ",") {
		if strings.EqualFold(strings.TrimSpace(k), kind) {
			return true
		}
	}
	return false
}

func normaliseKinds(s string) string {
	seen := map[string]bool{}
	var out []string
	for _, k := range strings.Split(s, ",") {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" || seen[k] || len(k) > 40 {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return strings.Join(out, ",")
}

// ---- the queue --------------------------------------------------------------------

// CollectDue runs up to limit sources whose time has come, oldest first.
func CollectDue(ctx context.Context, a *app.App, limit int) (sources, items int) {
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT id, workspace_id, kind, name, target, interval_minutes, push_enabled, push_kinds, last_run_at IS NULL
		FROM notification_sources
		WHERE enabled AND (next_run_at IS NULL OR next_run_at <= now())
		ORDER BY next_run_at NULLS FIRST, id LIMIT $1`, limit)
	if err != nil {
		a.Log.Warn("notifications: could not read the collection queue", "err", err)
		return 0, 0
	}
	var due []collectorSource
	for rows.Next() {
		var s collectorSource
		if rows.Scan(&s.ID, &s.WorkspaceID, &s.Kind, &s.Name, &s.Target, &s.Interval, &s.PushEnabled, &s.PushKinds, &s.FirstRun) == nil {
			due = append(due, s)
		}
	}
	rows.Close()
	for _, s := range due {
		n, err := collectOne(ctx, a, s)
		if err != nil {
			a.Log.Info("notifications: source failed", "workspace", s.WorkspaceID, "source", s.ID, "kind", s.Kind, "err", err)
		}
		items += n
	}
	return len(due), items
}

// StartCollector runs CollectDue on a ticker for the life of the process. The
// tick is how often the queue is looked at; each source's interval says when
// it is due.
//
// The due-queue SELECT has no atomic claim, so two replicas ticking together
// could both pick and poll the same source — a cluster-wide advisory lock
// (internal/pglock) keeps the tick to one replica at a time.
func StartCollector(a *app.App, tick time.Duration, perTick int) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		timer := time.NewTimer(20 * time.Second) // a restart does not hammer every source at boot
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			_, err := pglock.WithLock(ctx, a.SysDB, "moharrik:notifications:collector", func() {
				if n, items := CollectDue(ctx, a, perTick); n > 0 {
					a.Log.Info("notifications: collected", "sources", n, "new_items", items)
				}
			})
			if err != nil {
				a.Log.Error("notifications collector: could not acquire the cluster lock", "err", err)
			}
			timer.Reset(tick)
		}
	}()
	return cancel
}
