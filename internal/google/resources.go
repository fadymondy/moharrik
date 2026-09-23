package google

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Resources (MH-440): the GA4 properties, Search Console sites and YouTube
channels a workspace's connected accounts can see, and which of them it reads.

Discovery asks Google, per connection and per product the connection was
granted:

	analytics        GET analyticsadmin/v1beta/accountSummaries   (paged)
	search_console   GET webmasters/v3/sites                      (sites the account can read)
	youtube          GET youtube/v3/channels?mine=true            (1 quota unit)

It runs once right after a connect, whenever the integrations page is opened
and a connection has never been discovered, on "Refresh" and daily from the
collector. A resource that stops being returned is kept, marked unavailable,
never deleted: its data stays, and switching it back on works the moment the
account regains access.

Auto-enable: when discovery leaves a workspace with exactly one resource of a
kind (one distinct external id, whichever accounts can see it), nothing of that
kind is enabled, and nobody has ever chosen for that kind, it is switched on —
so a freshly connected account shows data with no extra step. With several,
nothing is guessed: the integrations page asks.
*/

// Kind is what a resource is.
type Kind string

const (
	KindGA4 Kind = "ga4_property"
	KindGSC Kind = "gsc_site"
	KindYT  Kind = "yt_channel"
)

// Kinds lists every kind, in the order the page shows them.
var Kinds = []Kind{KindGA4, KindGSC, KindYT}

// ParseKind validates a kind name.
func ParseKind(s string) (Kind, bool) {
	for _, k := range Kinds {
		if string(k) == s {
			return k, true
		}
	}
	return "", false
}

// Product is the connect product that grants a kind.
func (k Kind) Product() Product {
	switch k {
	case KindGA4:
		return Analytics
	case KindGSC:
		return SearchConsole
	}
	return YouTube
}

func kindOf(p Product) Kind {
	switch p {
	case Analytics:
		return KindGA4
	case SearchConsole:
		return KindGSC
	}
	return KindYT
}

// Resource is one property, site or channel.
type Resource struct {
	ID           string `json:"id"`
	ConnectionID string `json:"connection_id"`
	// The Google account it is read through. Only for integrations:manage;
	// empty otherwise.
	ConnectionEmail string          `json:"connection_email,omitempty"`
	Kind            Kind            `json:"kind" enum:"ga4_property,gsc_site,yt_channel"`
	ExternalID      string          `json:"external_id"`
	DisplayName     string          `json:"display_name"`
	ParentName      string          `json:"parent_name"`
	Details         json.RawMessage `json:"details"`
	Available       bool            `json:"available"`
	Enabled         bool            `json:"enabled"`
	AutoEnabled     bool            `json:"auto_enabled"`
	LastSyncedAt    *time.Time      `json:"last_synced_at"`
	NextSyncAt      time.Time       `json:"next_sync_at"`
	Syncing         bool            `json:"syncing"`
	BackoffUntil    *time.Time      `json:"backoff_until"`
	LastError       string          `json:"last_error"`
	// The connection needs reconnecting: nothing is read until it is.
	Reauth bool `json:"reauth"`
}

// Discovery is the last discovery of one product on one connection.
type Discovery struct {
	ConnectionID string    `json:"connection_id"`
	Product      Product   `json:"product" enum:"analytics,search_console,youtube"`
	DiscoveredAt time.Time `json:"discovered_at"`
	Found        int       `json:"found"`
	Error        string    `json:"error"`
}

const resourceColumns = `r.id, r.connection_id, c.email, r.kind, r.external_id, r.display_name, r.parent_name,
	r.details, r.available, r.enabled, r.auto_enabled, r.last_synced_at, r.next_sync_at,
	r.sync_started_at IS NOT NULL AND r.sync_started_at > now() - interval '30 minutes',
	CASE WHEN r.backoff_until > now() THEN r.backoff_until END, r.last_error, c.status = 'reauth'`

type rowScanner interface{ Scan(...any) error }

func scanResource(r rowScanner) (Resource, error) {
	var x Resource
	var details []byte
	err := r.Scan(&x.ID, &x.ConnectionID, &x.ConnectionEmail, &x.Kind, &x.ExternalID, &x.DisplayName, &x.ParentName,
		&details, &x.Available, &x.Enabled, &x.AutoEnabled, &x.LastSyncedAt, &x.NextSyncAt, &x.Syncing,
		&x.BackoffUntil, &x.LastError, &x.Reauth)
	if len(details) == 0 {
		details = []byte("{}")
	}
	x.Details = details
	return x, err
}

// Filter narrows ListResources.
type Filter struct {
	Kind        Kind
	EnabledOnly bool
}

// ListResources lists the workspace's resources: by kind, then name.
func ListResources(ctx context.Context, a *app.App, workspaceID string, f Filter) ([]Resource, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	q := `SELECT ` + resourceColumns + `
		FROM google_resources r
		JOIN google_connections c ON c.workspace_id = r.workspace_id AND c.id = r.connection_id
		WHERE r.workspace_id = $1 AND ($2 = '' OR r.kind = $2) AND (NOT $3 OR r.enabled)
		ORDER BY array_position(ARRAY['ga4_property','gsc_site','yt_channel'], r.kind),
		         lower(r.display_name), r.external_id, c.created_at`
	rows, err := a.SQLDB.QueryContext(tenant.WithWorkspace(ctx, workspaceID), q, workspaceID, string(f.Kind), f.EnabledOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Resource{}
	for rows.Next() {
		x, err := scanResource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ErrResourceNotFound: no such resource in this workspace.
var ErrResourceNotFound = errors.New("that Google property, site or channel does not exist in this workspace")

// GetResource reads one resource.
func GetResource(ctx context.Context, a *app.App, workspaceID, id string) (*Resource, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	x, err := scanResource(a.SQLDB.QueryRowContext(tenant.WithWorkspace(ctx, workspaceID), `SELECT `+resourceColumns+`
		FROM google_resources r
		JOIN google_connections c ON c.workspace_id = r.workspace_id AND c.id = r.connection_id
		WHERE r.workspace_id = $1 AND r.id = $2`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	return &x, nil
}

// ResolveEnabled picks the enabled resource of a kind a page reads: id when
// given (it must be enabled and of that kind), else the first enabled one.
// nil, nil when the workspace has none enabled.
func ResolveEnabled(ctx context.Context, a *app.App, workspaceID string, kind Kind, id string) (*Resource, error) {
	if id != "" {
		r, err := GetResource(ctx, a, workspaceID, id)
		if err != nil {
			return nil, err
		}
		if r.Kind != kind || !r.Enabled {
			return nil, ErrResourceNotFound
		}
		return r, nil
	}
	list, err := ListResources(ctx, a, workspaceID, Filter{Kind: kind, EnabledOnly: true})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}

// SetEnabled switches a resource on or off as a person's choice. Switching on
// makes it due for a sync now (and starts one).
func SetEnabled(ctx context.Context, a *app.App, workspaceID, id string, enabled bool) (*Resource, error) {
	if err := ready(a); err != nil {
		return nil, err
	}
	res, err := a.SQLDB.ExecContext(tenant.WithWorkspace(ctx, workspaceID), `
		UPDATE google_resources SET enabled = $3, auto_enabled = false, chosen_at = now(),
			next_sync_at = CASE WHEN $3 AND NOT enabled THEN now() ELSE next_sync_at END,
			failures = CASE WHEN $3 AND NOT enabled THEN 0 ELSE failures END,
			updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, enabled)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrResourceNotFound
	}
	r, err := GetResource(ctx, a, workspaceID, id)
	if err == nil && enabled {
		SyncSoon(a, workspaceID, id)
	}
	return r, err
}

// ListDiscoveries is the workspace's discovery log.
func ListDiscoveries(ctx context.Context, a *app.App, workspaceID string) ([]Discovery, error) {
	rows, err := a.SQLDB.QueryContext(tenant.WithWorkspace(ctx, workspaceID), `
		SELECT connection_id, product, discovered_at, found, error FROM google_discoveries
		WHERE workspace_id = $1 ORDER BY connection_id, product`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Discovery{}
	for rows.Next() {
		var d Discovery
		if err := rows.Scan(&d.ConnectionID, &d.Product, &d.DiscoveredAt, &d.Found, &d.Error); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- discovery -------------------------------------------------------------------

type found struct {
	externalID, name, parent string
	details                  map[string]any
}

// DiscoverWorkspace runs discovery for every usable connection. With onlyNew,
// only connections whose products were never discovered (or were reconnected
// since) are asked — what opening a page does, so it costs nothing twice.
func DiscoverWorkspace(ctx context.Context, a *app.App, workspaceID string, onlyNew bool) error {
	if err := ready(a); err != nil {
		return err
	}
	wctx := tenant.WithWorkspace(ctx, workspaceID)
	rows, err := a.SQLDB.QueryContext(wctx, `
		SELECT c.id FROM google_connections c
		WHERE c.workspace_id = $1 AND c.status = ''
		  AND (NOT $2 OR NOT EXISTS (
		        SELECT 1 FROM google_discoveries d
		        WHERE d.workspace_id = c.workspace_id AND d.connection_id = c.id AND d.discovered_at >= c.updated_at))
		ORDER BY c.created_at`, workspaceID, onlyNew)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		if err := Discover(ctx, a, workspaceID, id); err != nil && !errors.Is(err, ErrReauth) {
			return err
		}
	}
	return nil
}

// Discover asks Google what one connection can see, for every product it was
// granted, and records the result per product. A product that fails keeps its
// resources as they were and records why.
func Discover(ctx context.Context, a *app.App, workspaceID, connectionID string) error {
	src, err := TokenSource(ctx, a, workspaceID, connectionID)
	if err != nil {
		return err
	}
	wctx := tenant.WithWorkspace(ctx, workspaceID)
	var granted pgtext.Array
	if err := a.SQLDB.QueryRowContext(wctx, `SELECT granted_scopes FROM google_connections WHERE workspace_id = $1 AND id = $2`,
		workspaceID, connectionID).Scan(&granted); err != nil {
		return err
	}
	var reauth error
	for _, p := range ProductsOf(granted) {
		list, derr := discoverProduct(ctx, src, p)
		msg := ""
		if derr != nil {
			msg = Describe(derr)
			if errors.Is(derr, ErrReauth) {
				reauth = derr
			}
			logWarn(a, "google discovery failed: "+string(p), derr)
		} else if err := saveFound(wctx, a, workspaceID, connectionID, kindOf(p), list); err != nil {
			return err
		}
		if _, err := a.SQLDB.ExecContext(wctx, `
			INSERT INTO google_discoveries (workspace_id, connection_id, product, discovered_at, found, error)
			VALUES ($1, $2, $3, now(), $4, $5)
			ON CONFLICT (workspace_id, connection_id, product) DO UPDATE SET
				discovered_at = now(), found = EXCLUDED.found, error = EXCLUDED.error`,
			workspaceID, connectionID, string(p), len(list), msg); err != nil {
			return err
		}
		if reauth != nil {
			break
		}
	}
	if err := autoEnable(wctx, a, workspaceID); err != nil {
		return err
	}
	return reauth
}

func discoverProduct(ctx context.Context, src *Source, p Product) ([]found, error) {
	switch p {
	case Analytics:
		return discoverGA4(ctx, src)
	case SearchConsole:
		return discoverGSC(ctx, src)
	case YouTube:
		return discoverYouTube(ctx, src)
	}
	return nil, nil
}

func discoverGA4(ctx context.Context, src *Source) ([]found, error) {
	c := src.Client(ScopeAnalytics)
	out := []found{}
	page := ""
	for i := 0; i < 20; i++ {
		q := url.Values{"pageSize": {"200"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var res struct {
			AccountSummaries []struct {
				Account           string `json:"account"`
				DisplayName       string `json:"displayName"`
				PropertySummaries []struct {
					Property     string `json:"property"`
					DisplayName  string `json:"displayName"`
					PropertyType string `json:"propertyType"`
					Parent       string `json:"parent"`
				} `json:"propertySummaries"`
			} `json:"accountSummaries"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := c.Get(ctx, API().AnalyticsAdmin+"/accountSummaries?"+q.Encode(), &res); err != nil {
			return nil, err
		}
		for _, acc := range res.AccountSummaries {
			for _, p := range acc.PropertySummaries {
				if !strings.HasPrefix(p.Property, "properties/") {
					continue
				}
				name := p.DisplayName
				if name == "" {
					name = p.Property
				}
				out = append(out, found{externalID: p.Property, name: name, parent: acc.DisplayName,
					details: map[string]any{"account": acc.Account, "property_type": p.PropertyType}})
			}
		}
		if res.NextPageToken == "" {
			break
		}
		page = res.NextPageToken
	}
	return out, nil
}

func discoverGSC(ctx context.Context, src *Source) ([]found, error) {
	var res struct {
		SiteEntry []struct {
			SiteURL         string `json:"siteUrl"`
			PermissionLevel string `json:"permissionLevel"`
		} `json:"siteEntry"`
	}
	if err := src.Client().Get(ctx, API().Webmasters+"/sites", &res); err != nil {
		return nil, err
	}
	out := []found{}
	for _, s := range res.SiteEntry {
		// An unverified site has no data this account can read.
		if s.SiteURL == "" || s.PermissionLevel == "siteUnverifiedUser" {
			continue
		}
		name := strings.TrimPrefix(s.SiteURL, "sc-domain:")
		out = append(out, found{externalID: s.SiteURL, name: name, parent: s.PermissionLevel,
			details: map[string]any{"permission_level": s.PermissionLevel, "domain_property": strings.HasPrefix(s.SiteURL, "sc-domain:")}})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].externalID < out[j].externalID })
	return out, nil
}

func discoverYouTube(ctx context.Context, src *Source) ([]found, error) {
	var res struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title      string `json:"title"`
				CustomURL  string `json:"customUrl"`
				Thumbnails map[string]struct {
					URL string `json:"url"`
				} `json:"thumbnails"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := ChargeYouTube(ctx, src, 1); err != nil {
		return nil, err
	}
	if err := src.Client(ScopeYouTube).Get(ctx, API().YouTube+"/channels?"+url.Values{
		"part": {"snippet"}, "mine": {"true"}, "maxResults": {"50"},
	}.Encode(), &res); err != nil {
		return nil, err
	}
	out := []found{}
	for _, it := range res.Items {
		if it.ID == "" {
			continue
		}
		avatar := ""
		for _, k := range []string{"medium", "default", "high"} {
			if t, ok := it.Snippet.Thumbnails[k]; ok && t.URL != "" {
				avatar = t.URL
				break
			}
		}
		name := it.Snippet.Title
		if name == "" {
			name = it.ID
		}
		out = append(out, found{externalID: it.ID, name: name, parent: it.Snippet.CustomURL,
			details: map[string]any{"handle": it.Snippet.CustomURL, "avatar_url": avatar}})
	}
	return out, nil
}

// ChargeYouTube spends YouTube Data API units for the workspace the source
// belongs to. It is set by internal/youtube (its two-level quota ledger); nil
// charges nothing.
var chargeYouTube func(ctx context.Context, a *app.App, workspaceID string, units int64) error

// SetYouTubeCharger installs the ledger (internal/youtube does, at boot).
func SetYouTubeCharger(f func(ctx context.Context, a *app.App, workspaceID string, units int64) error) {
	chargeYouTube = f
}

// ChargeYouTube reserves units against the ledger before a Data API call.
func ChargeYouTube(ctx context.Context, src *Source, units int64) error {
	if chargeYouTube == nil {
		return nil
	}
	return chargeYouTube(ctx, src.a, src.workspaceID, units)
}

func saveFound(ctx context.Context, a *app.App, workspaceID, connectionID string, kind Kind, list []found) error {
	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	ids := make([]string, 0, len(list))
	for _, f := range list {
		details, _ := json.Marshal(f.details)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO google_resources (workspace_id, connection_id, kind, external_id, display_name, parent_name, details, available)
			VALUES ($1, $2, $3, $4, $5, $6, $7, true)
			ON CONFLICT (workspace_id, connection_id, kind, external_id) DO UPDATE SET
				display_name = EXCLUDED.display_name, parent_name = EXCLUDED.parent_name,
				details = google_resources.details || EXCLUDED.details, available = true, updated_at = now()`,
			workspaceID, connectionID, string(kind), f.externalID, clip(f.name, 300), clip(f.parent, 300), string(details)); err != nil {
			return err
		}
		ids = append(ids, f.externalID)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE google_resources SET available = false, updated_at = now()
		WHERE workspace_id = $1 AND connection_id = $2 AND kind = $3 AND available
		  AND NOT (external_id = ANY ($4::text[]))`,
		workspaceID, connectionID, string(kind), pgtext.Literal(ids)); err != nil {
		return err
	}
	return tx.Commit()
}

// autoEnable applies the "only one of its kind" rule (see the package note).
func autoEnable(ctx context.Context, a *app.App, workspaceID string) error {
	rows, err := a.SQLDB.QueryContext(ctx, `
		UPDATE google_resources r SET enabled = true, auto_enabled = true, next_sync_at = now(), updated_at = now()
		FROM (
			SELECT DISTINCT ON (x.kind) x.id
			FROM google_resources x
			JOIN google_connections c ON c.workspace_id = x.workspace_id AND c.id = x.connection_id
			WHERE x.workspace_id = $1 AND x.available
			  AND NOT EXISTS (SELECT 1 FROM google_resources y WHERE y.workspace_id = x.workspace_id AND y.kind = x.kind
			                  AND (y.enabled OR y.chosen_at IS NOT NULL))
			  AND (SELECT count(DISTINCT z.external_id) FROM google_resources z
			       WHERE z.workspace_id = x.workspace_id AND z.kind = x.kind AND z.available) = 1
			ORDER BY x.kind, c.created_at, x.created_at
		) pick
		WHERE r.workspace_id = $1 AND r.id = pick.id
		RETURNING r.id`, workspaceID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		SyncSoon(a, workspaceID, id)
	}
	return rows.Err()
}

// NeedsChoice lists the kinds where the workspace can see several resources
// and has none enabled: the page asks rather than guessing.
func NeedsChoice(list []Resource) []Kind {
	distinct := map[Kind]map[string]bool{}
	enabled := map[Kind]bool{}
	for _, r := range list {
		if r.Enabled {
			enabled[r.Kind] = true
		}
		if !r.Available {
			continue
		}
		if distinct[r.Kind] == nil {
			distinct[r.Kind] = map[string]bool{}
		}
		distinct[r.Kind][r.ExternalID] = true
	}
	out := []Kind{}
	for _, k := range Kinds {
		if len(distinct[k]) > 1 && !enabled[k] {
			out = append(out, k)
		}
	}
	return out
}
