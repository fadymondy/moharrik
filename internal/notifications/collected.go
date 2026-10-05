package notifications

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
Collected sources (MH-384): the notification center's polled inputs, managed
per workspace. The collector itself is collect.go; this is the catalog, the
CRUD and the REST routes, called by internal/mcp too.
*/

// NotificationKind is one kind of source the center knows about, and whether this
// deployment can collect it. fadymondy.com collects nine; Moharrik collects
// the public three today and names what each of the others is waiting for,
// so the page shows a clear "not configured" instead of a toggle that would
// never collect.
type NotificationKind struct {
	Kind      string `json:"kind"`
	Available bool   `json:"available" doc:"true: a source of this kind can be added now."`
	// TargetHint says what to type for Target; empty for unavailable kinds.
	TargetHint string `json:"target_hint,omitempty"`
	// Needs says, for an unavailable kind, what has to exist first.
	Needs string `json:"needs,omitempty" enum:",google_oauth,oauth_app,browser_plugin"`
}

// catalog is fixed: which kinds exist is code, not configuration.
var catalog = []NotificationKind{
	{Kind: "bluesky", Available: true, TargetHint: "handle"},
	{Kind: "youtube", Available: true, TargetHint: "channel"},
	{Kind: "rss", Available: true, TargetHint: "url"},
	// Gmail needs Google's restricted gmail.readonly scope on Moharrik's own
	// Google app, which Google grants only after a security assessment: the
	// Google integration (MH-439) cannot ask for it until that exists. The
	// inbox's own mailbox (IMAP) already files mail as conversations.
	{Kind: "gmail", Needs: "google_oauth"},
	// Each network's API needs its own developer app and review, supplied by
	// the deployment's owner; fadymondy.com reads these through the browser
	// plugins instead, which Moharrik's automation extension does not run yet.
	{Kind: "facebook", Needs: "oauth_app"},
	{Kind: "instagram", Needs: "oauth_app"},
	{Kind: "threads", Needs: "oauth_app"},
	{Kind: "linkedin", Needs: "oauth_app"},
	{Kind: "x", Needs: "oauth_app"},
	{Kind: "tiktok", Needs: "browser_plugin"},
}

var collectable = map[string]bool{"bluesky": true, "youtube": true, "rss": true}

// NotificationSource is a collected source as the sources page sees it.
type NotificationSource struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind" enum:"bluesky,youtube,rss"`
	Name            string     `json:"name"`
	Target          string     `json:"target" doc:"The Bluesky handle, YouTube channel id or feed URL it reads."`
	TargetURL       string     `json:"target_url" doc:"Where a person can look at the same thing."`
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	PushEnabled     bool       `json:"push_enabled"`
	PushKinds       string     `json:"push_kinds" doc:"Comma separated item kinds that alert (e.g. reply); empty = all."`
	LastRunAt       *time.Time `json:"last_run_at"`
	LastCollectedAt *time.Time `json:"last_collected_at"`
	NextRunAt       *time.Time `json:"next_run_at" doc:"null while disabled."`
	LastStatus      string     `json:"last_status" enum:",ok,failed" doc:"Empty until the first run."`
	LastError       string     `json:"last_error"`
	LastItemCount   int        `json:"last_item_count" doc:"New items the last run filed."`
	ItemCount       int64      `json:"item_count"`
	UnreadCount     int64      `json:"unread_count"`
	CreatedAt       time.Time  `json:"created_at"`
}

// PollingBudget is the polling load against the workspace's share.
type PollingBudget struct {
	RunsPerHour float64 `json:"runs_per_hour"`
	Limit       float64 `json:"limit"`
}

// NotificationSourceList is GET /api/w/notification_sources.
type NotificationSourceList struct {
	Sources   []NotificationSource `json:"sources"`
	Catalog   []NotificationKind   `json:"catalog"`
	Budget    PollingBudget        `json:"budget"`
	Intervals []int                `json:"intervals"`
}

const collectedSelect = `
	SELECT s.id, s.kind, s.name, s.target, s.enabled, s.interval_minutes, s.push_enabled, s.push_kinds,
	       s.last_run_at, s.last_collected_at, s.next_run_at, s.last_status, s.last_error, s.last_item_count, s.created_at,
	       (SELECT count(*) FROM notification_items i WHERE i.workspace_id = s.workspace_id AND i.feed_source_id = s.id),
	       (SELECT count(*) FROM notification_items i WHERE i.workspace_id = s.workspace_id AND i.feed_source_id = s.id AND i.read_at IS NULL AND (i.resolved_at IS NULL AND (i.snoozed_until IS NULL OR i.snoozed_until <= now())))
	FROM notification_sources s WHERE s.workspace_id = $1`

func scanCollected(row scanner) (NotificationSource, error) {
	var c NotificationSource
	var lastRun, lastCollected, nextRun sql.NullTime
	if err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.Target, &c.Enabled, &c.IntervalMinutes, &c.PushEnabled, &c.PushKinds,
		&lastRun, &lastCollected, &nextRun, &c.LastStatus, &c.LastError, &c.LastItemCount, &c.CreatedAt,
		&c.ItemCount, &c.UnreadCount); err != nil {
		return c, err
	}
	c.LastRunAt, c.LastCollectedAt = timep(lastRun), timep(lastCollected)
	if c.Enabled {
		c.NextRunAt = timep(nextRun)
	}
	c.TargetURL = targetURL(c.Kind, c.Target)
	return c, nil
}

func targetURL(kind, target string) string {
	switch kind {
	case "bluesky":
		return "https://bsky.app/profile/" + target
	case "youtube":
		return "https://www.youtube.com/channel/" + target
	}
	return target
}

// ListCollected lists the workspace's collected sources, the catalog and the budget.
func ListCollected(ctx context.Context, a *app.App) (*NotificationSourceList, error) {
	s, err := scope(ctx, authz.NotificationsRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, collectedSelect+` ORDER BY lower(s.name), s.id`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the collected sources")
	}
	defer rows.Close()
	out := &NotificationSourceList{Sources: []NotificationSource{}, Catalog: catalog, Intervals: Intervals, Budget: PollingBudget{Limit: RunsPerHourBudget()}}
	for rows.Next() {
		c, err := scanCollected(rows)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the collected sources")
		}
		if c.Enabled {
			out.Budget.RunsPerHour += runsPerHour(c.IntervalMinutes)
		}
		out.Sources = append(out.Sources, c)
	}
	return out, nil
}

func getCollected(ctx context.Context, a *app.App, wsID, id string) (*NotificationSource, error) {
	c, err := scanCollected(a.SQLDB.QueryRowContext(ctx, collectedSelect+` AND s.id = $2`, wsID, id))
	if notFound(err) {
		return nil, huma.Error404NotFound("notification source not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the notification source")
	}
	return &c, nil
}

// checkBudget refuses a configuration whose enabled sources would poll more
// than the workspace's share (fadymondy.com's ErrBudget). except is the source
// being changed, counted with its NEW interval via extra instead.
func checkBudget(ctx context.Context, a *app.App, wsID, except string, extra float64) error {
	var total sql.NullFloat64
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT sum(60.0 / interval_minutes) FROM notification_sources
		WHERE workspace_id = $1 AND enabled AND id <> $2`, wsID, except).Scan(&total); err != nil {
		return huma.Error500InternalServerError("could not check the polling budget")
	}
	sum, limit := total.Float64+extra, RunsPerHourBudget()
	if sum > limit+1e-9 {
		return huma.Error422UnprocessableEntity(
			"these settings would poll " + formatFloat(sum) +
				" times an hour, and this workspace's share is " + formatFloat(limit) + ": lengthen an interval or turn a source off")
	}
	return nil
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
}

// NewNotificationSource is a source to add.
type NewNotificationSource struct {
	Kind            string `json:"kind" enum:"bluesky,youtube,rss"`
	Target          string `json:"target" minLength:"1" maxLength:"2000" doc:"bluesky: a handle (name.bsky.social). youtube: a channel id (UC…), channel URL or @handle. rss: the feed URL."`
	Name            string `json:"name,omitempty" maxLength:"80" doc:"Default: taken from what it reads."`
	IntervalMinutes int    `json:"interval_minutes,omitempty" doc:"One of 15, 20, 30, 60, 120, 180, 240, 360, 720, 1440. Default 60."`
	PushEnabled     bool   `json:"push_enabled,omitempty"`
	PushKinds       string `json:"push_kinds,omitempty" maxLength:"200"`
}

// CreateCollected checks the target is real (the handle exists, the channel
// resolves, the URL is a feed) before saving, so a typo is a 422 now rather
// than a source that fails forever. The first run is queued at once.
func CreateCollected(ctx context.Context, a *app.App, in NewNotificationSource) (*NotificationSource, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(in.Kind))
	if !collectable[kind] {
		for _, c := range catalog {
			if c.Kind == kind {
				return nil, huma.Error422UnprocessableEntity(kind + " is not configured on this server: " + needsText(c.Needs))
			}
		}
		return nil, huma.Error422UnprocessableEntity("the kind must be bluesky, youtube or rss")
	}
	interval := in.IntervalMinutes
	if interval == 0 {
		interval = 60
	}
	if !validInterval(interval) {
		return nil, huma.Error422UnprocessableEntity("the interval must be one of 15, 20, 30, 60, 120, 180, 240, 360, 720 or 1440 minutes")
	}
	if err := checkBudget(ctx, a, s.WorkspaceID, "", runsPerHour(interval)); err != nil {
		return nil, err
	}
	target, label, err := resolveTarget(ctx, kind, in.Target)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = label
	}
	name = cut(name, 80)
	var id string
	err = a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO notification_sources (workspace_id, kind, name, target, interval_minutes, push_enabled, push_kinds, next_run_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), $8) RETURNING id`,
		s.WorkspaceID, kind, name, target, interval, in.PushEnabled, normaliseKinds(in.PushKinds), s.UserID).Scan(&id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not add the notification source")
	}
	return getCollected(ctx, a, s.WorkspaceID, id)
}

func needsText(needs string) string {
	switch needs {
	case "google_oauth":
		return "it needs Google's restricted Gmail scope on Moharrik's Google app"
	case "oauth_app":
		return "it needs the network's developer app and credentials, which the server's owner supplies"
	case "browser_plugin":
		return "it needs the browser automation plugin for that site"
	}
	return "it is not available"
}

// resolveTarget validates and normalises a target, returning a default name.
func resolveTarget(ctx context.Context, kind, raw string) (string, string, error) {
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	switch kind {
	case "bluesky":
		h, ok := normaliseHandle(raw)
		if !ok {
			return "", "", errBad("that is not a Bluesky handle (like name.bsky.social)")
		}
		label, err := bskyProfile(rctx, h)
		if err != nil {
			return "", "", err
		}
		return h, "Bluesky · " + label, nil
	case "youtube":
		id, err := resolveChannel(rctx, raw)
		if err != nil {
			return "", "", err
		}
		_, title, err := collectFeed(rctx, youtubeFeedURL(id), "video")
		if err != nil {
			return "", "", errBad("YouTube: that channel's public feed could not be read (" + err.Error() + ")")
		}
		return id, "YouTube · " + cut(firstOf(title, id), 60), nil
	case "rss":
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
			return "", "", errBad("give the feed's full address, starting https://")
		}
		_, title, err := collectFeed(rctx, u.String(), "post")
		if err != nil {
			return "", "", err
		}
		return u.String(), cut(firstOf(title, u.Host), 60), nil
	}
	return "", "", errBad("unknown kind")
}

type badInput string

func (e badInput) Error() string { return string(e) }
func errBad(s string) error      { return badInput(s) }

// NotificationSourcePatch changes a source. nil = unchanged.
type NotificationSourcePatch struct {
	Name            *string `json:"name,omitempty" maxLength:"80"`
	Enabled         *bool   `json:"enabled,omitempty"`
	IntervalMinutes *int    `json:"interval_minutes,omitempty"`
	PushEnabled     *bool   `json:"push_enabled,omitempty"`
	PushKinds       *string `json:"push_kinds,omitempty" maxLength:"200"`
}

// UpdateCollected applies a patch, refusing an interval not in Intervals or a
// total over the budget. Enabling a source queues its next run now.
func UpdateCollected(ctx context.Context, a *app.App, id string, p NotificationSourcePatch) (*NotificationSource, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, err
	}
	cur, err := getCollected(ctx, a, s.WorkspaceID, id)
	if err != nil {
		return nil, err
	}
	enabled, interval := cur.Enabled, cur.IntervalMinutes
	u := &update{}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		u.set("name", cut(n, 80))
	}
	if p.IntervalMinutes != nil {
		if !validInterval(*p.IntervalMinutes) {
			return nil, huma.Error422UnprocessableEntity("the interval must be one of 15, 20, 30, 60, 120, 180, 240, 360, 720 or 1440 minutes")
		}
		interval = *p.IntervalMinutes
		u.set("interval_minutes", interval)
	}
	if p.Enabled != nil {
		enabled = *p.Enabled
		u.set("enabled", enabled)
		if enabled && !cur.Enabled {
			u.set("next_run_at", time.Now().UTC())
		}
	}
	if p.PushEnabled != nil {
		u.set("push_enabled", *p.PushEnabled)
	}
	if p.PushKinds != nil {
		u.set("push_kinds", normaliseKinds(*p.PushKinds))
	}
	if enabled {
		if err := checkBudget(ctx, a, s.WorkspaceID, id, runsPerHour(interval)); err != nil {
			return nil, err
		}
	}
	if len(u.sets) > 0 {
		if _, err := a.SQLDB.ExecContext(ctx, `UPDATE notification_sources SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...); err != nil {
			return nil, huma.Error500InternalServerError("could not update the notification source")
		}
	}
	return getCollected(ctx, a, s.WorkspaceID, id)
}

// DeleteCollected removes a source; its items stay, unlinked.
func DeleteCollected(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return err
	}
	res, err := a.SQLDB.ExecContext(ctx, `DELETE FROM notification_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
	if err != nil {
		return huma.Error500InternalServerError("could not delete the notification source")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("notification source not found")
	}
	return nil
}

// manualGap is how soon a source may be collected by hand again: a button
// pressed twice is one poll, and a loop over the endpoint cannot turn the
// server into somebody's crawler.
const manualGap = time.Minute

// CollectNow runs one source immediately and returns it afterwards. A manual
// run counts as the source's next scheduled run.
func CollectNow(ctx context.Context, a *app.App, id string) (*NotificationSource, int, error) {
	s, err := scope(ctx, authz.NotificationsManage)
	if err != nil {
		return nil, 0, err
	}
	var src collectorSource
	var lastRun sql.NullTime
	err = a.SQLDB.QueryRowContext(ctx, `SELECT id, workspace_id, kind, name, target, interval_minutes, push_enabled, push_kinds, last_run_at
		FROM notification_sources WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id).
		Scan(&src.ID, &src.WorkspaceID, &src.Kind, &src.Name, &src.Target, &src.Interval, &src.PushEnabled, &src.PushKinds, &lastRun)
	if notFound(err) {
		return nil, 0, huma.Error404NotFound("notification source not found")
	} else if err != nil {
		return nil, 0, huma.Error500InternalServerError("could not read the notification source")
	}
	if lastRun.Valid && time.Since(lastRun.Time) < manualGap {
		return nil, 0, huma.Error429TooManyRequests("this source was collected less than a minute ago")
	}
	src.FirstRun = !lastRun.Valid
	n, _ := collectOne(ctx, a, src) // the outcome is on the row
	c, err := getCollected(ctx, a, s.WorkspaceID, id)
	return c, n, err
}

// ---- routes --------------------------------------------------------------------------

func registerCollected(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-notification-sources", Method: http.MethodGet, Path: "/api/w/notification_sources",
		Summary: "Collected notification sources (Bluesky, YouTube, RSS), the kinds that are not configured, and the polling budget", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[NotificationSourceList], error) {
		res, err := ListCollected(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(*res), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-notification-source", Method: http.MethodPost, Path: "/api/w/notification_sources",
		DefaultStatus: http.StatusCreated, Summary: "Add a collected source; the target is checked before it is saved, and the first run is queued at once", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body NewNotificationSource
	}) (*body[struct {
		Source NotificationSource `json:"source"`
	}], error) {
		c, err := CreateCollected(ctx, a, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Source NotificationSource `json:"source"`
		}{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-notification-source", Method: http.MethodPatch, Path: "/api/w/notification_sources/{id}",
		Summary: "Rename, enable/disable, change the interval or the push settings of a collected source", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body NotificationSourcePatch
	}) (*body[struct {
		Source NotificationSource `json:"source"`
	}], error) {
		c, err := UpdateCollected(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Source NotificationSource `json:"source"`
		}{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-collect-notification-source", Method: http.MethodPost, Path: "/api/w/notification_sources/{id}/collect",
		Summary: "Collect a source now (at most once a minute)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[struct {
		Source NotificationSource `json:"source"`
		New    int                `json:"new" doc:"Items this run filed."`
	}], error) {
		c, n, err := CollectNow(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Source NotificationSource `json:"source"`
			New    int                `json:"new" doc:"Items this run filed."`
		}{*c, n}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-notification-source", Method: http.MethodDelete, Path: "/api/w/notification_sources/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a collected source (its items stay, unlinked)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		if err := DeleteCollected(ctx, a, in.ID); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})
}
