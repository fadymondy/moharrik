package prospects

import (
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/research"
)

func ev(quote, signal string, conf float64, daysAgo int) research.Evidence {
	at := time.Now().AddDate(0, 0, -daysAgo)
	return research.Evidence{Quote: quote, SignalType: signal, Confidence: conf, SourceURL: "https://news.test/" + quote[:8], CapturedAt: at}
}

func TestScoreExplainsEveryPoint(t *testing.T) {
	p := &ProspectProfile{Industries: []string{"fintech"}, Keywords: []string{"payments"}, Regions: []string{"Egypt"}, SignalWeights: map[string]float64{}}
	score, reasons, f := Score(Input{Name: "Nile Pay", Profile: p, Evidence: []research.Evidence{
		ev("Nile Pay raised $12 million to grow fintech payments in Egypt.", "funding", 0.9, 3),
		ev("Nile Pay is hiring 40 engineers in Cairo.", "hiring", 0.8, 10),
	}})
	sum := 0.0
	dims := map[string]bool{}
	for _, r := range reasons {
		sum += r.Points
		dims[r.Dimension] = true
		if r.Text == "" {
			t.Errorf("a reason with no text: %+v", r)
		}
	}
	for _, d := range []string{"signals", "fit", "region", "evidence", "recency"} {
		if !dims[d] {
			t.Errorf("missing dimension %s: %+v", d, reasons)
		}
	}
	if score < 80 || score > 100 {
		t.Errorf("a strong fit scored %v: %+v", score, reasons)
	}
	if int(sum+0.5) != int(score) && score != 100 {
		t.Errorf("the reasons add up to %v, the score is %v", sum, score)
	}
	if strings.Join(f.Signals, ",") != "hiring,funding" || len(f.Terms) != 2 || len(f.Regions) != 1 {
		t.Errorf("features: %+v", f)
	}
}

func TestExcludedAndWeightsChangeTheScore(t *testing.T) {
	e := []research.Evidence{ev("Acme Crypto raised money for its crypto exchange in Egypt.", "funding", 0.8, 5)}
	base := &ProspectProfile{Industries: []string{"exchange"}, SignalWeights: map[string]float64{}}
	s1, _, _ := Score(Input{Name: "Acme", Profile: base, Evidence: e})
	ex := &ProspectProfile{Industries: []string{"exchange"}, ExcludeKeywords: []string{"crypto"}, SignalWeights: map[string]float64{}}
	s2, r2, _ := Score(Input{Name: "Acme", Profile: ex, Evidence: e})
	if s2 >= s1 || r2[len(r2)-1].Dimension != "excluded" {
		t.Errorf("an excluded word did not lower the score: %v -> %v %+v", s1, s2, r2)
	}
	zero := &ProspectProfile{Industries: []string{"exchange"}, SignalWeights: map[string]float64{"funding": 0}}
	s3, _, _ := Score(Input{Name: "Acme", Profile: zero, Evidence: e})
	if s3 >= s1 {
		t.Errorf("weighting funding at 0 did not lower the score: %v -> %v", s1, s3)
	}
}

// The card's bar: a reject changes later scoring. Rejecting a funding
// prospect lowers another funding prospect, says why, and leaves an
// unrelated one alone.
func TestRejectingLowersSimilarProspectsOnly(t *testing.T) {
	p := &ProspectProfile{Industries: []string{"fintech"}, SignalWeights: map[string]float64{}}
	funding := []research.Evidence{ev("Zeal, a fintech startup, raised $10 million.", "funding", 0.9, 2)}
	hiring := []research.Evidence{ev("Mylo, a fintech app, is hiring engineers.", "hiring", 0.9, 2)}
	before, _, _ := Score(Input{Name: "Zeal", Profile: p, Evidence: funding})
	hiringBefore, _, _ := Score(Input{Name: "Mylo", Profile: p, Evidence: hiring})

	hist := []Verdict{{ProspectID: "other", Accepted: false, Features: ProspectFeatures{Signals: []string{"funding"}}}}
	after, reasons, _ := Score(Input{Name: "Zeal", Profile: p, Evidence: funding, History: hist})
	if after >= before {
		t.Fatalf("rejecting a similar prospect did not lower the score: %v -> %v", before, after)
	}
	last := reasons[len(reasons)-1]
	if last.Dimension != "feedback_down" || last.Count != 1 || !strings.Contains(last.Text, "rejected 1 similar") {
		t.Errorf("the reason does not say why: %+v", last)
	}
	hiringAfter, _, _ := Score(Input{Name: "Mylo", Profile: p, Evidence: hiring, History: hist})
	if hiringAfter != hiringBefore {
		t.Errorf("an unrelated prospect moved: %v -> %v", hiringBefore, hiringAfter)
	}
	// A prospect's own verdict is not "similar".
	self, _, _ := Score(Input{Name: "Zeal", Profile: p, Evidence: funding, History: hist, Self: "other"})
	if self != before {
		t.Errorf("a prospect's own verdict moved its score: %v -> %v", before, self)
	}
	// Accepting ranks similar ones higher.
	up, _, _ := Score(Input{Name: "Zeal", Profile: p, Evidence: funding, History: []Verdict{{Accepted: true, Features: ProspectFeatures{Signals: []string{"funding"}}}}})
	if up <= before && before < 100 {
		t.Errorf("accepting a similar prospect did not raise the score: %v -> %v", before, up)
	}
}

func TestFindContact(t *testing.T) {
	pages := []Page{{URL: "https://news.test/a", Content: "Zeal was founded in 2019.\nOmar Ebeid, co-founder and CEO of Zeal, said the round will fund expansion.\nPress: press@zeal.test or info@gmail.com"}}
	c := FindContact(pages, "Zeal", "zeal.test")
	if c.Name != "Omar Ebeid" || !strings.Contains(strings.ToLower(c.Title), "ceo") || c.Email != "press@zeal.test" || c.SourceURL != "https://news.test/a" {
		t.Errorf("contact: %+v", c)
	}
	c2 := FindContact([]Page{{URL: "u", Content: "Zeal's CEO Omar Ebeid spoke at the event."}}, "Zeal", "")
	if c2.Name != "Omar Ebeid" || c2.Email != "" {
		t.Errorf("title-first contact: %+v", c2)
	}
	// Nothing guessed: no named person, no address on the company's domain.
	c3 := FindContact([]Page{{URL: "u", Content: "Zeal raised money. Write to hello@othersite.test"}}, "Zeal", "zeal.test")
	if c3.Name != "" || c3.Email != "" {
		t.Errorf("a contact was invented: %+v", c3)
	}
}

func TestEffectiveQueries(t *testing.T) {
	p := &ProspectProfile{Industries: []string{"fintech", "التكنولوجيا المالية"}, Regions: []string{"Egypt"}, SignalWeights: map[string]float64{}}
	q := p.effectiveQueries()
	if len(q) != 6 || !strings.Contains(q[0], "fintech") || !strings.Contains(q[0], "Egypt") {
		t.Fatalf("queries: %q", q)
	}
	if !strings.HasPrefix(q[3], "شركات") {
		t.Errorf("an Arabic subject is searched in Arabic: %q", q[3])
	}
	if (&ProspectProfile{Queries: []string{"x"}}).effectiveQueries()[0] != "x" {
		t.Error("the profile's own queries are not used")
	}
	if (&ProspectProfile{}).effectiveQueries() != nil {
		t.Error("an empty profile has queries")
	}
}
