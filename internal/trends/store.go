package trends

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/ai"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Stories: persisting the clusterer's answer (MH-385).

	news_items ──(cluster.go: same story?)──▶ trend_clusters ──(rank.go)──▶ score + reasons
	                                                  │
	                              summary (extractive, or a model when configured)

Everything here runs on the app (RLS) pool inside the workspace, and names
workspace_id in every predicate as well.
*/

// Window is how far back an item may join an existing story.
const Window = 72 * time.Hour

// maxDocs bounds one clustering pass.
const maxDocs = 5000

/*
Recluster places the workspace's unplaced items into stories and rebuilds
every story that changed. Returns how many stories it created.

The window is every item published in the last 72 hours plus every item not
yet placed: placed items define the existing stories, unplaced ones join one
or start their own.
*/
func Recluster(ctx context.Context, a *app.App, wsID string) (int, error) {
	sctx := tenant.WithWorkspace(ctx, wsID)
	db := a.SQLDB
	rows, err := db.QueryContext(sctx, `
		SELECT id, COALESCE(cluster_id, ''), title, url, published_at FROM news_items
		WHERE workspace_id = $1 AND (cluster_id IS NULL OR published_at >= $2)
		ORDER BY published_at, id LIMIT $3`, wsID, time.Now().Add(-Window), maxDocs)
	if err != nil {
		return 0, err
	}
	var docs []*Doc
	titles := map[string]string{}
	at := map[string]time.Time{}
	unplaced := 0
	for rows.Next() {
		var id, cid, title, link string
		var published time.Time
		if err := rows.Scan(&id, &cid, &title, &link, &published); err != nil {
			rows.Close()
			return 0, err
		}
		d := NewDoc(id, cid, title, link, published)
		docs = append(docs, d)
		titles[id], at[id] = title, published
		if cid == "" {
			unplaced++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if unplaced == 0 {
		return 0, nil
	}

	method := "deterministic"
	if l := Layer(a); l != nil {
		texts := make([]string, len(docs))
		for i, d := range docs {
			texts[i] = titles[d.ID]
		}
		if vectors, model, err := l.Embed(ctx, texts); err == nil && len(vectors) == len(docs) {
			for i := range docs {
				docs[i].Vector = vectors[i]
			}
			method = "deterministic+embed:" + model
		} else if err != nil && !errors.Is(err, ai.ErrNoProvider) {
			a.Log.Warn("trends: embedding failed, clustering deterministically", "err", err)
		}
	}

	assigned := Assign(docs)
	created := map[string]string{}
	touched := map[string]bool{}
	keys := make([]string, 0, len(assigned))
	for id := range assigned {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	for _, itemID := range keys {
		key := assigned[itemID]
		clusterID := key
		if strings.HasPrefix(key, "new:") {
			if existing, ok := created[key]; ok {
				clusterID = existing
			} else {
				first := strings.TrimPrefix(key, "new:")
				if err := db.QueryRowContext(sctx, `
					INSERT INTO trend_clusters (workspace_id, day, title, cluster_method, first_seen_at, last_seen_at)
					VALUES ($1, ($2::timestamptz AT TIME ZONE 'UTC')::date, $3, $4, $2, $2) RETURNING id`,
					wsID, at[first], trimTo(titles[first], 500), method).Scan(&clusterID); err != nil {
					return 0, err
				}
				created[key] = clusterID
			}
		}
		if _, err := db.ExecContext(sctx, `UPDATE news_items SET cluster_id = $3 WHERE workspace_id = $1 AND id = $2`,
			wsID, itemID, clusterID); err != nil {
			return 0, err
		}
		touched[clusterID] = true
	}

	kw, err := keywordTopicNames(sctx, db, wsID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(touched))
	for id := range touched {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := Recompute(sctx, a, wsID, id, kw, method); err != nil {
			return 0, err
		}
	}
	return len(created), nil
}

// keywordTopicNames is the workspace's enabled keyword topics, id -> name.
func keywordTopicNames(ctx context.Context, db *sql.DB, wsID string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name FROM topics WHERE workspace_id = $1 AND enabled`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

/*
RescoreAll recomputes every story from the last 30 days: what a change to the
keyword topics needs, since relevance is part of every story's score.
*/
func RescoreAll(ctx context.Context, a *app.App, wsID string) error {
	sctx := tenant.WithWorkspace(ctx, wsID)
	rows, err := a.SQLDB.QueryContext(sctx, `SELECT id, cluster_method FROM trend_clusters
		WHERE workspace_id = $1 AND last_seen_at >= now() - interval '30 days'`, wsID)
	if err != nil {
		return err
	}
	type c struct{ id, method string }
	var list []c
	for rows.Next() {
		var x c
		if rows.Scan(&x.id, &x.method) == nil {
			list = append(list, x)
		}
	}
	rows.Close()
	kw, err := keywordTopicNames(sctx, a.SQLDB, wsID)
	if err != nil {
		return err
	}
	for _, x := range list {
		if err := Recompute(sctx, a, wsID, x.id, kw, x.method); err != nil {
			return err
		}
	}
	return nil
}

type member struct {
	sourceID  string
	outlet    string
	provider  string
	title     string
	summary   string
	rank      int
	points    int
	comments  int
	published time.Time
	topicIDs  []string
	tokens    []string
}

// Recompute rebuilds a story's title, summary, outlets, counts and rank.
// ctx must already carry the workspace (tenant.WithWorkspace).
func Recompute(ctx context.Context, a *app.App, wsID, clusterID string, keywordTopics map[string]string, method string) error {
	db := a.SQLDB
	rows, err := db.QueryContext(ctx, `
		SELECT COALESCE(source_id, ''), CASE WHEN outlet <> '' THEN outlet ELSE source_name END, provider, title, summary,
		       rank, points, comments, published_at, topic_ids
		FROM news_items WHERE workspace_id = $1 AND cluster_id = $2 ORDER BY published_at, id`, wsID, clusterID)
	if err != nil {
		return err
	}
	var members []member
	for rows.Next() {
		var m member
		var topics pgtext.Array
		if err := rows.Scan(&m.sourceID, &m.outlet, &m.provider, &m.title, &m.summary, &m.rank, &m.points, &m.comments, &m.published, &topics); err != nil {
			rows.Close()
			return err
		}
		m.topicIDs = topics
		m.tokens = Tokens(m.title)
		members = append(members, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(members) == 0 {
		_, err := db.ExecContext(ctx, `DELETE FROM trend_clusters WHERE workspace_id = $1 AND id = $2`, wsID, clusterID)
		return err
	}

	signals := make([]Signal, len(members))
	sourceSet := map[string]bool{}
	var sourceIDs []string
	first, last := members[0].published, members[0].published
	for i, m := range members {
		signals[i] = Signal{Outlet: m.outlet, Provider: m.provider, Rank: m.rank, Points: m.points, Comments: m.comments,
			Published: m.published, TopicIDs: m.topicIDs}
		if m.sourceID != "" && !sourceSet[m.sourceID] {
			sourceSet[m.sourceID] = true
			sourceIDs = append(sourceIDs, m.sourceID)
		}
		if m.published.Before(first) {
			first = m.published
		}
		if m.published.After(last) {
			last = m.published
		}
	}
	sort.Strings(sourceIDs)
	score, parts := Rank(signals, keywordTopics)
	reasons, _ := json.Marshal(parts)
	outlets := []string{}
	for _, p := range parts {
		if p.Kind == "sources" {
			outlets = p.Detail
		}
	}

	best := representative(members)
	title := members[best].title
	summary, summarySource := ExtractiveSummary(members, best)

	// A model writes the summary when one is configured, once per shape of
	// the story: a story that gains items is summarised again.
	var currentSource string
	var currentCount int
	_ = db.QueryRowContext(ctx, `SELECT summary_source, item_count FROM trend_clusters WHERE workspace_id = $1 AND id = $2`,
		wsID, clusterID).Scan(&currentSource, &currentCount)
	keepModel := false
	if l := Layer(a); l != nil && len(members) >= 2 {
		if !strings.HasPrefix(currentSource, "extractive") && currentSource != "" && currentCount == len(members) {
			keepModel = true
		} else {
			var lines []string
			for _, m := range members {
				lines = append(lines, "- "+m.outlet+": "+m.title+" "+trimTo(m.summary, 400))
			}
			if text, prov, err := l.Summarise(ctx, strings.Join(lines, "\n")); err == nil && text != "" {
				summary, summarySource = text, prov
			} else if err != nil && !errors.Is(err, ai.ErrNoProvider) {
				a.Log.Warn("trends: summary failed, keeping the extractive one", "cluster", clusterID, "err", err)
			}
		}
	}

	day := last
	if now := time.Now(); day.After(now) {
		day = now
	}
	q := `UPDATE trend_clusters SET title = $3, source_count = $4, item_count = $5, outlets = $6, source_ids = $7,
		score = $8, reasons = $9::jsonb, first_seen_at = $10, last_seen_at = $11, cluster_method = $12,
		day = ($11::timestamptz AT TIME ZONE 'UTC')::date, updated_at = now()`
	args := []any{wsID, clusterID, trimTo(title, 500), len(outlets), len(members), pgtext.Literal(outlets),
		pgtext.Literal(sourceIDs), score, string(reasons), first, day, method}
	if !keepModel {
		q += `, summary = $13, summary_source = $14`
		args = append(args, trimTo(summary, 1200), summarySource)
	}
	_, err = db.ExecContext(ctx, q+` WHERE workspace_id = $1 AND id = $2`, args...)
	return err
}

// representative is the member whose title shares the most tokens with the
// others; the earliest on a tie.
func representative(members []member) int {
	best, bestShared := 0, -1
	for i, m := range members {
		set := map[string]bool{}
		for _, t := range m.tokens {
			set[t] = true
		}
		shared := 0
		for j, o := range members {
			if i == j {
				continue
			}
			for _, t := range o.tokens {
				if set[t] {
					shared++
				}
			}
		}
		if shared > bestShared {
			best, bestShared = i, shared
		}
	}
	return best
}

var sentenceEnd = regexp.MustCompile(`([.!?؟。])\s+`)

// firstSentences keeps whole sentences up to max runes (at least one).
func firstSentences(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	idx := sentenceEnd.FindAllStringIndex(s, -1)
	out := ""
	for _, m := range idx {
		cut := strings.TrimSpace(s[:m[0]+1])
		if len([]rune(cut)) > max {
			break
		}
		out = cut
	}
	if out == "" {
		return trimTo(s, max)
	}
	return out
}

// junkSummary is a feed "description" that says nothing: HN's RSS mirror
// writes only a Comments link, some feeds repeat the title.
func junkSummary(summary, title string) bool {
	s := strings.TrimSpace(summary)
	if len([]rune(s)) < 40 {
		return true
	}
	ls, lt := strings.ToLower(s), strings.ToLower(strings.TrimSpace(title))
	return ls == lt || strings.HasPrefix(ls, "article url:") || strings.HasPrefix(ls, "comments")
}

/*
ExtractiveSummary picks what the story's own sources say about it, with no
model involved:

 1. the excerpt (an item's feed description) that covers the most of the
    story's core words — the tokens two or more of its headlines share, or
    every headline token when there is one headline — cut to whole sentences
    under 320 characters; source "extractive";
 2. failing that, with two or more items, the other outlets' headlines, which
    are the story told a second way; source "extractive:headlines";
 3. failing that, nothing: a lone headline with no excerpt is its own summary.

best is the representative member (its title is the story's title).
*/
func ExtractiveSummary(members []member, best int) (string, string) {
	df := map[string]int{}
	for _, m := range members {
		for _, t := range m.tokens {
			df[t]++
		}
	}
	core := map[string]bool{}
	for t, n := range df {
		if n >= 2 || len(members) == 1 {
			core[t] = true
		}
	}
	if len(core) == 0 {
		for _, t := range members[best].tokens {
			core[t] = true
		}
	}

	bestText, bestScore := "", -1
	for _, m := range members {
		if junkSummary(m.summary, m.title) {
			continue
		}
		covered := 0
		for _, t := range Tokens(m.summary) {
			if core[t] {
				covered++
			}
		}
		if covered > bestScore || (covered == bestScore && len(m.summary) > len(bestText)) {
			bestText, bestScore = m.summary, covered
		}
	}
	if bestText != "" {
		return firstSentences(bestText, 320), "extractive"
	}

	if len(members) >= 2 {
		seen := map[string]bool{strings.ToLower(members[best].title): true}
		var others []string
		for _, m := range members {
			if k := strings.ToLower(m.title); !seen[k] {
				seen[k] = true
				others = append(others, m.title)
			}
			if len(others) == 2 {
				break
			}
		}
		if len(others) > 0 {
			return strings.Join(others, " · "), "extractive:headlines"
		}
	}
	return "", "extractive"
}
