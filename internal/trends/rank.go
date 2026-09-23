package trends

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

/*
Ranking: why a topic is where it is (MH-385).

A topic's score is the sum of three parts, and the parts ARE the explanation —
the page renders them as chips, it does not re-derive them:

	sources    how many distinct outlets carry it:   5 + 20 per extra outlet, max 65
	velocity   the strongest rising signal, max 30:
	             several reports inside six hours (a burst),
	             Hacker News position and points (HN via the API),
	             dev.to reactions
	relevance  the workspace's keyword topics it matches: 15 each, max 30

The keyword topics used to be the whole feature (a list filtered by keyword);
here they are one input to the rank, not a replacement for it. Adapted from
fadymondy.com internal/trends/rank.go, whose relevance input was a capability
profile and whose velocity also read Google Trends and X — browser-tier
sources Moharrik does not collect yet.

Parts are ordered largest first; a part worth nothing is left out, except
sources, which every topic has.
*/

// TrendReason is one component of a topic's rank.
type TrendReason struct {
	Kind   string   `json:"kind" enum:"sources,velocity,relevance"`
	Points float64  `json:"points"`
	Label  string   `json:"label" doc:"An English sentence, for API and MCP clients; the page renders kind + detail in the reader's language."`
	Detail []string `json:"detail"`
	// The structured half, so the page can say it in the reader's language
	// without parsing Label: how many (outlets, reports in a burst, topics);
	// which velocity signal won (burst | hn | devto); its list position and its
	// points or reactions.
	Count    int    `json:"count,omitempty"`
	Signal   string `json:"signal,omitempty" enum:"burst,hn,devto"`
	Position int    `json:"position,omitempty"`
	Metric   int    `json:"metric,omitempty"`
}

// Signal is one item as the ranker sees it.
type Signal struct {
	Outlet    string
	Provider  string // feed | hn | devto
	Rank      int
	Points    int // HN points, dev.to reactions
	Comments  int
	Published time.Time
	TopicIDs  []string
}

// BurstWindow is how close together reports must be to count as a burst.
const BurstWindow = 6 * time.Hour

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// velocity is one item's own rising signal: points and a label.
func velocity(s Signal) (float64, string) {
	switch s.Provider {
	case ProviderHN:
		if s.Rank <= 0 {
			return 0, ""
		}
		pts := math.Max(0, float64(31-s.Rank)/30*15)
		pts += math.Min(15, float64(s.Points)/100)
		label := fmt.Sprintf("Hacker News #%d", s.Rank)
		if s.Points > 0 {
			label += fmt.Sprintf(", %d points", s.Points)
		}
		return math.Min(30, pts), label
	case ProviderDevto:
		if s.Points > 0 {
			return math.Min(10, float64(s.Points)/20), fmt.Sprintf("%d reactions on DEV", s.Points)
		}
	}
	return 0, ""
}

// burst is the largest number of reports inside any BurstWindow.
func burst(items []Signal) int {
	times := make([]time.Time, 0, len(items))
	for _, it := range items {
		if !it.Published.IsZero() {
			times = append(times, it.Published)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	best, lo := 0, 0
	for hi := range times {
		for times[hi].Sub(times[lo]) > BurstWindow {
			lo++
		}
		if n := hi - lo + 1; n > best {
			best = n
		}
	}
	return best
}

// Rank scores a topic from its items and the workspace's keyword topics
// (id -> name; only the enabled ones).
func Rank(items []Signal, keywordTopics map[string]string) (score float64, parts []TrendReason) {
	outlets := []string{}
	seenOutlet := map[string]bool{}
	for _, it := range items {
		name := strings.TrimSpace(it.Outlet)
		if name == "" {
			continue
		}
		if key := strings.ToLower(name); !seenOutlet[key] {
			seenOutlet[key] = true
			outlets = append(outlets, name)
		}
	}
	sort.Strings(outlets)

	n := len(outlets)
	if n == 0 {
		n = 1
	}
	srcPts := math.Min(65, 5+20*float64(n-1))
	label := "Carried by 1 source"
	if n != 1 {
		label = fmt.Sprintf("Carried by %d sources", n)
	}
	parts = append(parts, TrendReason{Kind: "sources", Points: srcPts, Label: label, Detail: outlets, Count: n})

	// Velocity: the strongest of the burst and each item's own signal.
	bestV, bestLabel, bestCount := 0.0, "", 0
	bestSignal, bestPos, bestMetric := "", 0, 0
	var vDetail []string
	if b := burst(items); b >= 2 {
		pts := math.Min(20, 6*float64(b-1))
		l := fmt.Sprintf("%d reports within %d hours", b, int(BurstWindow.Hours()))
		vDetail = append(vDetail, l)
		bestV, bestLabel, bestCount = pts, l, b
		bestSignal = "burst"
	}
	for _, it := range items {
		if pts, l := velocity(it); pts > 0 {
			vDetail = append(vDetail, l)
			if pts > bestV {
				bestV, bestLabel, bestCount = pts, l, 0
				bestSignal, bestPos, bestMetric = it.Provider, it.Rank, it.Points
			}
		}
	}
	if bestV > 0 {
		parts = append(parts, TrendReason{Kind: "velocity", Points: round1(bestV), Label: "Rising: " + bestLabel, Detail: vDetail, Count: bestCount,
			Signal: bestSignal, Position: bestPos, Metric: bestMetric})
	}

	// Relevance: the keyword topics any item matched, in name order.
	hitSet := map[string]bool{}
	var hits []string
	for _, it := range items {
		for _, id := range it.TopicIDs {
			if name, ok := keywordTopics[id]; ok && !hitSet[id] {
				hitSet[id] = true
				hits = append(hits, name)
			}
		}
	}
	sort.Strings(hits)
	if len(hits) > 0 {
		parts = append(parts, TrendReason{
			Kind: "relevance", Points: math.Min(30, 15*float64(len(hits))),
			Label: "Matches your topics: " + strings.Join(hits, ", "), Detail: hits, Count: len(hits),
		})
	}

	sort.SliceStable(parts, func(i, j int) bool { return parts[i].Points > parts[j].Points })
	for _, p := range parts {
		score += p.Points
	}
	return round1(score), parts
}
