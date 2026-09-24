package prospects

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/research"
)

/*
Scoring (MH-397): 0 to 100, and every point explained.

A score is a sum of reasons, each with its dimension, its points and what it
matched, so the page can say "Funding and Hiring in the evidence (+30)" and a
person can disagree with a specific part of it. No model is involved: the
production deployment has none, and a number a person cannot trace back to
the evidence would not be trusted — nor should it be.

	signals    the kinds of news in the evidence, weighted by the profile     up to 30
	fit        the profile's industries and keywords the evidence mentions     up to 25
	region     the profile's regions the evidence mentions                     15
	evidence   how many sources, and how confident the strongest is            up to 20
	recency    how new the newest evidence is                                  up to 10
	excluded   a word the profile excludes                                     -30
	feedback   verdicts on SIMILAR prospects (sharing a signal or a term)      -24 … +15

The feedback dimension is how rejecting one prospect changes the ranking of
the next: similar prospects are ranked lower, with the count in the reason.
*/

// ProspectReason is one part of a score.
type ProspectReason struct {
	Dimension string   `json:"dimension" enum:"signals,fit,region,evidence,recency,excluded,feedback_down,feedback_up"`
	Points    float64  `json:"points"`
	Items     []string `json:"items" doc:"What it matched: signal types, terms, regions."`
	Count     int      `json:"count" doc:"Sources for evidence, days for recency, prospects for feedback."`
	Text      string   `json:"text" doc:"The reason in English, for tools and logs; the page translates it from the rest."`
}

// ProspectFeatures are what a prospect is compared on for feedback.
type ProspectFeatures struct {
	Signals []string `json:"signals"`
	Terms   []string `json:"terms"`
	Regions []string `json:"regions"`
}

// Verdict is one past decision on a prospect.
type Verdict struct {
	ProspectID string
	Accepted   bool
	Features   ProspectFeatures
}

// Input is what a score is computed from.
type Input struct {
	Name     string
	Evidence []research.Evidence
	Profile  *ProspectProfile
	History  []Verdict
	Self     string // this prospect's id, so its own verdict is not "similar"
	Now      time.Time
}

// Score computes a prospect's score and features.
func Score(in Input) (float64, []ProspectReason, ProspectFeatures) {
	var hay strings.Builder
	hay.WriteString(research.Normalise(in.Name))
	signals := map[string]bool{}
	sources := map[string]bool{}
	maxConf := 0.0
	var newest time.Time
	for _, e := range in.Evidence {
		fmt.Fprintf(&hay, " %s %s %s", research.Normalise(e.Quote), research.Normalise(e.Claim), research.Normalise(e.SourceTitle))
		if e.SignalType != "" {
			signals[e.SignalType] = true
		}
		sources[e.SourceURL] = true
		if e.Confidence > maxConf {
			maxConf = e.Confidence
		}
		at := e.CapturedAt
		if e.PublishedAt != nil {
			at = *e.PublishedAt
		}
		if at.After(newest) {
			newest = at
		}
	}
	text := " " + hay.String() + " "
	has := func(term string) bool {
		t := research.Stem(term)
		return t != "" && strings.Contains(text, t)
	}

	p := in.Profile
	if p == nil {
		p = &ProspectProfile{}
	}
	var f ProspectFeatures
	var reasons []ProspectReason
	add := func(r ProspectReason) {
		if r.Points != 0 {
			r.Points = math.Round(r.Points*10) / 10
			reasons = append(reasons, r)
		}
	}

	// signals
	sumW := 0.0
	for _, s := range research.SignalTypes {
		if signals[s] {
			f.Signals = append(f.Signals, s)
			sumW += p.weight(s)
		}
	}
	if sumW > 0 {
		add(ProspectReason{Dimension: "signals", Points: 30 * math.Min(1, sumW/2), Items: f.Signals,
			Text: strings.Join(f.Signals, ", ") + " in the evidence"})
	}

	// fit
	for _, term := range append(append([]string{}, p.Industries...), p.Keywords...) {
		if has(term) {
			f.Terms = append(f.Terms, term)
		}
	}
	if len(f.Terms) > 0 {
		add(ProspectReason{Dimension: "fit", Points: 25 * math.Min(1, float64(len(f.Terms))/2), Items: f.Terms,
			Text: "mentions " + strings.Join(f.Terms, ", ")})
	}

	// region
	for _, r := range p.Regions {
		if has(r) {
			f.Regions = append(f.Regions, r)
		}
	}
	if len(f.Regions) > 0 {
		add(ProspectReason{Dimension: "region", Points: 15, Items: f.Regions, Text: "in " + strings.Join(f.Regions, ", ")})
	}

	// evidence
	if n := len(sources); n > 0 {
		add(ProspectReason{Dimension: "evidence", Points: 10*math.Min(1, float64(n)/3) + 10*maxConf, Count: n,
			Text: fmt.Sprintf("%d source%s, strongest %.0f%%", n, plural(n), maxConf*100)})
	}

	// recency
	if !newest.IsZero() {
		now := in.Now
		if now.IsZero() {
			now = time.Now()
		}
		days := int(now.Sub(newest).Hours() / 24)
		if days < 0 {
			days = 0
		}
		switch {
		case days <= 30:
			add(ProspectReason{Dimension: "recency", Points: 10, Count: days, Text: fmt.Sprintf("newest evidence %d days old", days)})
		case days <= 90:
			add(ProspectReason{Dimension: "recency", Points: 5, Count: days, Text: fmt.Sprintf("newest evidence %d days old", days)})
		}
	}

	// excluded
	var ex []string
	for _, term := range p.ExcludeKeywords {
		if has(term) {
			ex = append(ex, term)
		}
	}
	if len(ex) > 0 {
		add(ProspectReason{Dimension: "excluded", Points: -30, Items: ex, Text: "mentions excluded " + strings.Join(ex, ", ")})
	}

	// feedback on similar prospects
	var downs, ups int
	downShared, upShared := map[string]bool{}, map[string]bool{}
	for _, v := range in.History {
		if in.Self != "" && v.ProspectID == in.Self {
			continue
		}
		shared := overlap(f, v.Features)
		if len(shared) == 0 {
			continue
		}
		if v.Accepted {
			ups++
			for _, s := range shared {
				upShared[s] = true
			}
		} else {
			downs++
			for _, s := range shared {
				downShared[s] = true
			}
		}
	}
	if downs > 0 {
		items := keys(downShared)
		add(ProspectReason{Dimension: "feedback_down", Points: -math.Min(24, 8*float64(downs)), Count: downs, Items: items,
			Text: fmt.Sprintf("ranked lower: you rejected %d similar prospect%s (%s)", downs, plural(downs), strings.Join(items, ", "))})
	}
	if ups > 0 {
		items := keys(upShared)
		add(ProspectReason{Dimension: "feedback_up", Points: math.Min(15, 5*float64(ups)), Count: ups, Items: items,
			Text: fmt.Sprintf("ranked higher: you accepted %d similar prospect%s (%s)", ups, plural(ups), strings.Join(items, ", "))})
	}

	total := 0.0
	for _, r := range reasons {
		total += r.Points
	}
	total = math.Max(0, math.Min(100, math.Round(total)))
	if reasons == nil {
		reasons = []ProspectReason{}
	}
	if f.Signals == nil {
		f.Signals = []string{}
	}
	if f.Terms == nil {
		f.Terms = []string{}
	}
	if f.Regions == nil {
		f.Regions = []string{}
	}
	return total, reasons, f
}

// overlap is what two prospects share: signal types and profile terms. A
// region alone does not make two prospects similar — nearly every prospect
// of a regional business shares one.
func overlap(a, b ProspectFeatures) []string {
	var out []string
	in := func(list []string, s string) bool {
		for _, x := range list {
			if strings.EqualFold(x, s) {
				return true
			}
		}
		return false
	}
	for _, s := range a.Signals {
		if in(b.Signals, s) {
			out = append(out, s)
		}
	}
	for _, s := range a.Terms {
		if in(b.Terms, s) {
			out = append(out, s)
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ── the database side ─────────────────────────────────────────────────────

func history(ctx context.Context, db *sql.DB, ws string) ([]Verdict, error) {
	rows, err := db.QueryContext(ctx, `SELECT prospect_id, verdict, features::text FROM prospect_feedback WHERE workspace_id = $1
		ORDER BY created_at DESC LIMIT 500`, ws)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Verdict
	for rows.Next() {
		var v Verdict
		var verdict, feat string
		if err := rows.Scan(&v.ProspectID, &verdict, &feat); err != nil {
			return nil, err
		}
		v.Accepted = verdict == "accepted"
		_ = json.Unmarshal([]byte(feat), &v.Features)
		out = append(out, v)
	}
	return out, rows.Err()
}

// scoreOne rescores one prospect from its current evidence.
func scoreOne(ctx context.Context, db *sql.DB, ws, id, name, key string, p *ProspectProfile, hist []Verdict) (float64, error) {
	ev, err := research.CompanyEvidence(ctx, db, ws, key, 50)
	if err != nil {
		return 0, err
	}
	score, reasons, _ := Score(Input{Name: name, Evidence: ev, Profile: p, History: hist, Self: id})
	rj, _ := json.Marshal(reasons)
	_, err = db.ExecContext(ctx, `UPDATE prospects SET score = $3, reasons = $4::jsonb, scored_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, ws, id, score, string(rj))
	return score, err
}

// RescoreOpen rescores every prospect still waiting for review, and returns
// how many it rescored.
func RescoreOpen(ctx context.Context, db *sql.DB, ws string) (int, error) {
	p, err := GetProfile(ctx, db, ws)
	if err != nil {
		return 0, err
	}
	hist, err := history(ctx, db, ws)
	if err != nil {
		return 0, err
	}
	rows, err := db.QueryContext(ctx, `SELECT p.id, s.name, s.company_key FROM prospects p
		JOIN research_companies s ON s.workspace_id = p.workspace_id AND s.id = p.suggestion_id
		WHERE p.workspace_id = $1 AND p.status = 'new'`, ws)
	if err != nil {
		return 0, err
	}
	type row struct{ id, name, key string }
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.name, &r.key); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, r)
	}
	rows.Close()
	for _, r := range list {
		if _, err := scoreOne(ctx, db, ws, r.id, r.name, r.key, p, hist); err != nil {
			return 0, err
		}
	}
	return len(list), nil
}
