package notifications

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
)

// Facet is one value of a filter and its counts (fadymondy.com store.go).
type Facet struct {
	Value  string `json:"value"`
	Total  int64  `json:"total"`
	Unread int64  `json:"unread"`
}

// SourceFacet is one source in the source filter: its key (the value the
// list's source_id takes), its label and where it comes from.
type SourceFacet struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Origin string `json:"origin" enum:"webhook,collected,internal"`
	Total  int64  `json:"total"`
	Unread int64  `json:"unread"`
}

// sourcePredicate narrows a notification_items query to one source key, with
// its argument numbered n. The shapes cannot collide: ids are UUIDs and an
// internal key carries a prefix.
func sourcePredicate(key string, n int) (string, any) {
	arg := "$" + strconv.Itoa(n)
	if strings.HasPrefix(key, internalKeyPrefix) {
		return ` AND source_id IS NULL AND feed_source_id IS NULL AND source_name = ` + arg, strings.TrimPrefix(key, internalKeyPrefix)
	}
	return ` AND (source_id = ` + arg + ` OR feed_source_id = ` + arg + `)`, key
}

// likeEscape makes user text literal inside ILIKE (fadymondy.com store.go).
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// facets counts the workspace's items per source and per kind. A source's
// label is its live name while it exists, else the snapshot on the item.
func facets(ctx context.Context, a *app.App, wsID string) ([]SourceFacet, []Facet, error) {
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT i.source_id, i.feed_source_id, i.source_name, max(w.name), max(f.name),
		       count(*), count(*) FILTER (WHERE i.read_at IS NULL AND ` + ActionableSQL("i") + `)
		FROM notification_items i
		LEFT JOIN webhook_sources w ON w.workspace_id = i.workspace_id AND w.id = i.source_id
		LEFT JOIN notification_sources f ON f.workspace_id = i.workspace_id AND f.id = i.feed_source_id
		WHERE i.workspace_id = $1
		GROUP BY i.source_id, i.feed_source_id, i.source_name`, wsID)
	if err != nil {
		return nil, nil, err
	}
	merged := map[string]*SourceFacet{}
	var order []string
	for rows.Next() {
		var sid, fid, wname, fname sql.NullString
		var snap string
		var total, unread int64
		if err := rows.Scan(&sid, &fid, &snap, &wname, &fname, &total, &unread); err != nil {
			rows.Close()
			return nil, nil, err
		}
		origin, key := originOf(sid, fid, snap)
		name := snap
		if wname.Valid {
			name = wname.String
		} else if fname.Valid {
			name = fname.String
		}
		// A webhook source renamed mid-history has two snapshots: one facet.
		if f, ok := merged[key]; ok {
			f.Total += total
			f.Unread += unread
			continue
		}
		merged[key] = &SourceFacet{Key: key, Name: name, Origin: origin, Total: total, Unread: unread}
		order = append(order, key)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, err
	}
	rows.Close()
	sources := make([]SourceFacet, 0, len(order))
	for _, k := range order {
		sources = append(sources, *merged[k])
	}
	// By label, so the menu reads alphabetically whatever the map did.
	sortFacets(sources)

	krows, err := a.SQLDB.QueryContext(ctx, `SELECT kind, count(*), count(*) FILTER (WHERE read_at IS NULL AND `+ActionableSQL("")+`)
		FROM notification_items WHERE workspace_id = $1 AND kind <> '' GROUP BY kind ORDER BY kind`, wsID)
	if err != nil {
		return nil, nil, err
	}
	defer krows.Close()
	kinds := []Facet{}
	for krows.Next() {
		var f Facet
		if err := krows.Scan(&f.Value, &f.Total, &f.Unread); err != nil {
			return nil, nil, err
		}
		kinds = append(kinds, f)
	}
	return sources, kinds, krows.Err()
}

func sortFacets(list []SourceFacet) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && strings.ToLower(list[j].Name) < strings.ToLower(list[j-1].Name); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
