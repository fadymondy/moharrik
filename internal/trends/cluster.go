package trends

import (
	"math"
	"sort"
	"time"
)

/*
Clustering: which items are the same story (MH-385).

Ported from fadymondy.com internal/trends/cluster.go, minus the Google Trends
and X row kinds (Moharrik collects headlines only; browser-tier sources wait
for the automation plugins), plus a rule for Arabic.

# The deterministic path (always on)

Production has no generation model, so this path has to be good on its own.
Two items are the same story when any of these holds:

 1. URL: their canonical URLs are equal.
 2. Rare shared NAMES: each token is weighted by how rare it is across the
    window (w = ln(1 + N/df)), and the weighted overlap
    shared / min(weight(a), weight(b)) is compared. A token is distinctive when
    df <= max(8, N/20), and a name when either title writes it like a proper
    noun (normalise.go Analyse). A match is two distinctive shared names at an
    overlap of at least 0.25 (with a third shared word, or at 0.5); or three distinctive shared tokens, one of them
    a name, at 0.34; or four shared tokens of any kind at 0.6.
 3. Arabic: Arabic has no capitals, so an Arabic headline has no "names" by
    the rule above and two outlets' reports of one event could only ever meet
    on four shared words. Between two Arabic headlines, three distinctive
    shared tokens at an overlap of 0.3 is a match, four shared tokens of any
    kind at 0.4, or two distinctive ones at 0.5 — and at least one shared token
    (both, for two) has to be RARE in the window: df <= max(3, N/100). Without
    that, stock phrases ("الأمم المتحدة", "الاتحاد الأوروبي" + "روسيا") glued
    unrelated stories together.

# Anti-chaining

Single-link clustering chains: A~B and B~C puts A with C when A and C have
nothing in common. So a new item has to match at least a third of a topic's
members to join it.

# The AI path (when configured)

When an embedding provider is configured (AI_TASK_EMBED), titles are embedded
and a cosine of at least EmbedThreshold ALSO counts as a match — but only with
at least one shared token, so a model's idea of "similar" (two different phone
launches) cannot merge stories on its own.
*/

// EmbedThreshold is the cosine above which two embedded titles are one story.
const EmbedThreshold = 0.88

// Doc is one item as the clusterer sees it.
type Doc struct {
	ID        string
	ClusterID string // "" until assigned
	Canonical string
	Tokens    []string
	Names     map[string]bool // tokens written like proper nouns (Analyse)
	Arabic    bool
	At        time.Time // published (or collected) time: the placing order
	Vector    []float32
}

// NewDoc analyses a title into a Doc.
func NewDoc(id, clusterID, title, link string, at time.Time) *Doc {
	tokens, names := Analyse(title)
	return &Doc{ID: id, ClusterID: clusterID, Canonical: CanonicalURL(link), Tokens: tokens, Names: names, Arabic: isArabic(title), At: at}
}

type clusterer struct {
	df map[string]int
	n  int
}

func newClusterer(docs []*Doc) *clusterer {
	c := &clusterer{df: map[string]int{}, n: len(docs)}
	for _, d := range docs {
		for _, t := range d.Tokens {
			c.df[t]++
		}
	}
	return c
}

func (c *clusterer) weight(t string) float64 {
	df := c.df[t]
	if df < 1 {
		df = 1
	}
	return math.Log(1 + float64(c.n)/float64(df))
}

func (c *clusterer) distinctive(t string) bool {
	limit := c.n / 20
	if limit < 8 {
		limit = 8
	}
	return c.df[t] <= limit
}

// rare is a token almost nobody else in the window uses: the place, the
// person, the number in one story. Arabic pairs need one (see similar).
func (c *clusterer) rare(t string) bool {
	limit := c.n / 100
	if limit < 3 {
		limit = 3
	}
	return c.df[t] <= limit
}

func cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// similar says whether two items are the same story, and how strongly.
func (c *clusterer) similar(a, b *Doc) (bool, float64) {
	if a.Canonical != "" && a.Canonical == b.Canonical {
		return true, 1
	}
	inB := make(map[string]bool, len(b.Tokens))
	var wb float64
	for _, t := range b.Tokens {
		inB[t] = true
		wb += c.weight(t)
	}
	var wa, shared float64
	sharedN, distinctN, namesN, rareN := 0, 0, 0, 0
	for _, t := range a.Tokens {
		w := c.weight(t)
		wa += w
		if inB[t] {
			shared += w
			sharedN++
			if c.rare(t) {
				rareN++
			}
			if c.distinctive(t) {
				distinctN++
				if a.Names[t] || b.Names[t] {
					namesN++
				}
			}
		}
	}
	if sharedN == 0 {
		return false, 0
	}
	overlap := shared / math.Min(wa, wb)
	arabic := a.Arabic && b.Arabic
	switch {
	// Two shared names alone ("Trump", "AI") are two stories as often as one:
	// they need a third shared word, or half the title.
	case namesN >= 2 && overlap >= 0.25 && (sharedN >= 3 || overlap >= 0.5):
		return true, overlap
	case distinctN >= 3 && namesN >= 1 && overlap >= 0.34:
		return true, overlap
	case sharedN >= 4 && overlap >= 0.6:
		return true, overlap
	case arabic && distinctN >= 3 && rareN >= 1 && overlap >= 0.3:
		return true, overlap
	case arabic && sharedN >= 4 && rareN >= 1 && overlap >= 0.4:
		return true, overlap
	case arabic && distinctN >= 2 && rareN >= 2 && overlap >= 0.5:
		return true, overlap
	case len(a.Vector) > 0 && len(b.Vector) > 0:
		if cos := cosine(a.Vector, b.Vector); cos >= EmbedThreshold {
			return true, cos
		}
	}
	return false, overlap
}

/*
Assign places every doc with no ClusterID into an existing cluster or a new one.

docs holds the whole window: assigned docs define the existing clusters, and
unassigned ones are placed in time order (then id), so the result is
deterministic. The returned map is doc id -> cluster key; a new cluster's key
is "new:" + the id of its first doc.
*/
func Assign(docs []*Doc) map[string]string {
	c := newClusterer(docs)
	groups := map[string][]*Doc{}
	var order []string
	var pending []*Doc
	for _, d := range docs {
		if d.ClusterID == "" {
			pending = append(pending, d)
			continue
		}
		if _, ok := groups[d.ClusterID]; !ok {
			order = append(order, d.ClusterID)
		}
		groups[d.ClusterID] = append(groups[d.ClusterID], d)
	}
	sort.SliceStable(pending, func(i, j int) bool {
		if !pending[i].At.Equal(pending[j].At) {
			return pending[i].At.Before(pending[j].At)
		}
		return pending[i].ID < pending[j].ID
	})

	out := map[string]string{}
	for _, d := range pending {
		best, bestScore := "", 0.0
		for _, key := range order {
			members := groups[key]
			matches, top := 0, 0.0
			for _, m := range members {
				if ok, s := c.similar(d, m); ok {
					matches++
					if s > top {
						top = s
					}
				}
			}
			need := (len(members) + 2) / 3
			if matches >= need && matches > 0 && top > bestScore {
				best, bestScore = key, top
			}
		}
		if best == "" {
			best = "new:" + d.ID
			order = append(order, best)
		}
		groups[best] = append(groups[best], d)
		out[d.ID] = best
	}
	return out
}
