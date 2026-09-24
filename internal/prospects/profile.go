// Package prospects is prospect discovery (MH-397): research, pointed at the
// workspace's ideal customer, turned into scored companies with the evidence
// behind them, reviewed by a person, and drafted outreach that is never sent.
//
// It is built on the research evidence store (MH-388): a prospect is a
// research company suggestion, scored. Discovery is a research run with
// origin "discovery"; scheduled discovery spends the same daily budget as
// scheduled research.
package prospects

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/research"
)

// ProspectProfile is the workspace's ideal customer, as discovery searches for it.
type ProspectProfile struct {
	Offer           string             `json:"offer" doc:"What the workspace sells, in a sentence or two. The outreach draft's pitch."`
	Industries      []string           `json:"industries"`
	Keywords        []string           `json:"keywords"`
	Regions         []string           `json:"regions"`
	ExcludeKeywords []string           `json:"exclude_keywords"`
	SignalWeights   map[string]float64 `json:"signal_weights" doc:"Signal type -> weight 0..3; 0 ignores it."`
	Queries         []string           `json:"queries" doc:"What discovery searches, one per pass, in turn. Empty: built from the profile."`
	Enabled         bool               `json:"enabled" doc:"Scheduled discovery on or off."`
	Cadence         string             `json:"cadence" enum:"daily,weekly"`
	NextRunAt       time.Time          `json:"next_run_at"`
	LastRunID       string             `json:"last_run_id"`
	LastRunAt       *time.Time         `json:"last_run_at"`
	LastStatus      string             `json:"last_status" enum:",started,over_budget,busy,not_configured,failed,no_queries"`
	LastNote        string             `json:"last_note"`
	// Configured is false until the profile names something to look for.
	Configured bool `json:"configured"`
	// EffectiveQueries is what discovery will actually search.
	EffectiveQueries []string `json:"effective_queries"`
}

// ProspectProfileInput changes the profile. nil = unchanged.
type ProspectProfileInput struct {
	Offer           *string             `json:"offer,omitempty" maxLength:"600"`
	Industries      *[]string           `json:"industries,omitempty" maxItems:"20"`
	Keywords        *[]string           `json:"keywords,omitempty" maxItems:"30"`
	Regions         *[]string           `json:"regions,omitempty" maxItems:"20"`
	ExcludeKeywords *[]string           `json:"exclude_keywords,omitempty" maxItems:"30"`
	SignalWeights   *map[string]float64 `json:"signal_weights,omitempty"`
	Queries         *[]string           `json:"queries,omitempty" maxItems:"20"`
	Enabled         *bool               `json:"enabled,omitempty"`
	Cadence         *string             `json:"cadence,omitempty" enum:"daily,weekly"`
}

// DefaultWeights is how much each kind of news says "buying now", before a
// workspace says otherwise.
var DefaultWeights = map[string]float64{
	"funding": 1, "hiring": 1, "expansion": 0.8, "launch": 0.6, "technology": 0.6,
	"partnership": 0.5, "leadership": 0.4, "regulatory": 0.3,
}

func (p *ProspectProfile) weight(signal string) float64 {
	if w, ok := p.SignalWeights[signal]; ok {
		return w
	}
	return DefaultWeights[signal]
}

// configured is true once the profile names what to look for.
func (p *ProspectProfile) configured() bool {
	return len(p.Industries)+len(p.Keywords)+len(p.Queries) > 0
}

// signalWords are how a discovery query asks for a signal.
var signalWords = map[string][2]string{
	"funding": {"raises funding", "تمويل"}, "hiring": {"hiring", "توظيف"}, "expansion": {"expansion", "توسع"},
	"launch": {"launches", "إطلاق"}, "technology": {"digital transformation", "التحول الرقمي"},
	"partnership": {"partnership", "شراكة"}, "leadership": {"appoints CEO", "تعيين"}, "regulatory": {"regulation", "قانون"},
}

// EffectiveQueries is what discovery searches: the profile's own queries, or
// ones built from it — a subject (an industry or keyword), the strongest
// signals, and a region — in the script the subject is written in.
func (p *ProspectProfile) effectiveQueries() []string {
	if len(p.Queries) > 0 {
		return p.Queries
	}
	subjects := append(append([]string{}, p.Industries...), p.Keywords...)
	if len(subjects) == 0 {
		return nil
	}
	type sw struct {
		s string
		w float64
	}
	var sigs []sw
	// Ties go to the signals whose news names companies: a funding round or an
	// expansion is reported as "Acme raises…", while "fintech companies
	// hiring" mostly finds job boards.
	for _, s := range []string{"funding", "expansion", "launch", "partnership", "hiring", "leadership", "technology", "regulatory"} {
		if w := p.weight(s); w > 0 {
			sigs = append(sigs, sw{s, w})
		}
	}
	for i := 1; i < len(sigs); i++ {
		for j := i; j > 0 && sigs[j].w > sigs[j-1].w; j-- {
			sigs[j], sigs[j-1] = sigs[j-1], sigs[j]
		}
	}
	if len(sigs) > 3 {
		sigs = sigs[:3]
	}
	region := ""
	if len(p.Regions) > 0 {
		region = p.Regions[0]
	}
	var out []string
	for _, subj := range subjects {
		ar := isArabic(subj)
		for _, s := range sigs {
			word := signalWords[s.s][0]
			if ar {
				word = signalWords[s.s][1]
			}
			q := strings.TrimSpace(fmt.Sprintf("%s companies %s %s", subj, word, region))
			if ar {
				q = strings.TrimSpace(fmt.Sprintf("شركات %s %s %s", subj, word, region))
			}
			out = append(out, q)
			if len(out) >= 12 {
				return out
			}
		}
	}
	return out
}

func isArabic(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}

func clean(list []string, max int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range list {
		s = strings.TrimSpace(s)
		k := strings.ToLower(s)
		if s == "" || seen[k] {
			continue
		}
		if r := []rune(s); len(r) > 120 {
			s = string(r[:120])
		}
		seen[k] = true
		out = append(out, s)
		if len(out) >= max {
			break
		}
	}
	return out
}

func ensureProfile(ctx context.Context, db *sql.DB, ws string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO prospect_profiles (workspace_id) VALUES ($1) ON CONFLICT (workspace_id) DO NOTHING`, ws)
	return err
}

// GetProfile reads the workspace's profile (an empty one is created on first read).
func GetProfile(ctx context.Context, db *sql.DB, ws string) (*ProspectProfile, error) {
	if err := ensureProfile(ctx, db, ws); err != nil {
		return nil, err
	}
	var p ProspectProfile
	var ind, kw, reg, ex, q, weights string
	var last sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT offer, industries::text, keywords::text, regions::text, exclude_keywords::text, signal_weights::text, queries::text,
		       enabled, cadence, next_run_at, last_run_id, last_run_at, last_status, last_note
		FROM prospect_profiles WHERE workspace_id = $1`, ws).Scan(&p.Offer, &ind, &kw, &reg, &ex, &weights, &q,
		&p.Enabled, &p.Cadence, &p.NextRunAt, &p.LastRunID, &last, &p.LastStatus, &p.LastNote); err != nil {
		return nil, err
	}
	p.Industries, p.Keywords, p.Regions, p.ExcludeKeywords, p.Queries = pgList(ind), pgList(kw), pgList(reg), pgList(ex), pgList(q)
	p.SignalWeights = map[string]float64{}
	_ = json.Unmarshal([]byte(weights), &p.SignalWeights)
	// Every signal type is present in the answer, with the weight in effect,
	// so a client sees the defaults rather than an empty map.
	for _, s := range research.SignalTypes {
		p.SignalWeights[s] = p.weight(s)
	}
	if last.Valid {
		p.LastRunAt = &last.Time
	}
	p.Configured = p.configured()
	p.EffectiveQueries = p.effectiveQueries()
	if p.EffectiveQueries == nil {
		p.EffectiveQueries = []string{}
	}
	return &p, nil
}

// SaveProfile changes the profile, and rescores the prospects waiting for
// review: a changed profile re-ranks them.
func SaveProfile(ctx context.Context, db *sql.DB, ws, userID string, in ProspectProfileInput) (*ProspectProfile, error) {
	cur, err := GetProfile(ctx, db, ws)
	if err != nil {
		return nil, err
	}
	if in.Offer != nil {
		cur.Offer = strings.TrimSpace(*in.Offer)
	}
	if in.Industries != nil {
		cur.Industries = clean(*in.Industries, 20)
	}
	if in.Keywords != nil {
		cur.Keywords = clean(*in.Keywords, 30)
	}
	if in.Regions != nil {
		cur.Regions = clean(*in.Regions, 20)
	}
	if in.ExcludeKeywords != nil {
		cur.ExcludeKeywords = clean(*in.ExcludeKeywords, 30)
	}
	if in.Queries != nil {
		cur.Queries = clean(*in.Queries, 20)
	}
	if in.SignalWeights != nil {
		for k, v := range *in.SignalWeights {
			if !validSignal(k) {
				return nil, fmt.Errorf("unknown signal type %q", k)
			}
			if v < 0 || v > 3 {
				return nil, errors.New("signal weights are 0 to 3")
			}
			cur.SignalWeights[k] = v
		}
	}
	if in.Cadence != nil {
		if *in.Cadence != "daily" && *in.Cadence != "weekly" {
			return nil, errors.New("cadence is daily or weekly")
		}
		cur.Cadence = *in.Cadence
	}
	wasOn := cur.Enabled
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	if cur.Enabled && !cur.configured() {
		return nil, errors.New("name at least one industry, keyword or query before turning discovery on")
	}
	weights, _ := json.Marshal(cur.SignalWeights)
	if _, err := db.ExecContext(ctx, `
		UPDATE prospect_profiles SET offer = $2, industries = $3, keywords = $4, regions = $5, exclude_keywords = $6,
			signal_weights = $7::jsonb, queries = $8, enabled = $9, cadence = $10,
			next_run_at = CASE WHEN $9 AND NOT $11 THEN now() ELSE next_run_at END,
			updated_by = $12, updated_at = now()
		WHERE workspace_id = $1`,
		ws, cur.Offer, pgArr(cur.Industries), pgArr(cur.Keywords), pgArr(cur.Regions), pgArr(cur.ExcludeKeywords),
		string(weights), pgArr(cur.Queries), cur.Enabled, cur.Cadence, wasOn, userID); err != nil {
		return nil, err
	}
	if _, err := RescoreOpen(ctx, db, ws); err != nil {
		return nil, err
	}
	return GetProfile(ctx, db, ws)
}

func validSignal(s string) bool {
	for _, t := range research.SignalTypes {
		if t == s {
			return true
		}
	}
	return false
}

// pgArr / pgList: text[] in and out, for the plain words this package stores.
func pgArr(items []string) string {
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func pgList(s string) []string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s == "{}" {
		return []string{}
	}
	s = s[1 : len(s)-1]
	var out []string
	var b strings.Builder
	inQ, esc := false, false
	for _, r := range s {
		switch {
		case esc:
			b.WriteRune(r)
			esc = false
		case r == '\\':
			esc = true
		case r == '"':
			inQ = !inQ
		case r == ',' && !inQ:
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(r)
		}
	}
	return append(out, b.String())
}
