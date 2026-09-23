package icp

import (
	"strings"
	"testing"
)

func profile(t *testing.T, p Profile) Profile {
	t.Helper()
	n, err := p.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hotel() Facts {
	return Facts{
		Industry: "Hospitality", Size: "201-1000", Country: "Egypt", HasCompany: true,
		Text:    map[string]string{"title": "F&B Director", "tags": "decision-maker tender", "company": "Nile Residence Hotel"},
		Signals: map[string]bool{SigOpenDeal: true, SigSubscribed: true},
	}
}

// Worked by hand: industry 3×1, size 2×1, region 2×0, keywords 2×0.5 (one of
// two), open_deal 3×1, subscribed 1×1, conversation 1×0.
// Σ contribution = 3+2+0+1+3+1+0 = 10; Σ positive weights = 3+2+2+2+3+1+1 = 14;
// 10/14 = 0.714… → 71.
func TestComputeWorkedExample(t *testing.T) {
	p := profile(t, Profile{
		Industries: Criterion{Values: []string{"hospitality", "Catering"}, Weight: 3},
		Sizes:      Criterion{Values: []string{"201-1000", "1000+"}, Weight: 2},
		Regions:    Criterion{Values: []string{"Saudi Arabia"}, Weight: 2},
		Keywords:   Criterion{Values: []string{"F&B", "procurement"}, Weight: 2},
		Signals:    map[string]float64{SigOpenDeal: 3, SigSubscribed: 1, SigConversation: 1},
	})
	s := Compute(p, hotel())
	if s.Score == nil || *s.Score != 71 {
		t.Fatalf("score: %v, reasons %+v", s.Score, s.Reasons)
	}
	dims := []string{}
	for _, r := range s.Reasons {
		dims = append(dims, r.Dimension)
	}
	if strings.Join(dims, ",") != "industry,size,region,keywords,open_deal,conversation,subscribed" {
		t.Errorf("reason order: %v", dims)
	}
	kw := s.Reasons[3]
	if kw.Value != 0.5 || kw.Contribution != 1 || len(kw.Matched) != 1 || kw.Matched[0] != "F&B" || !strings.Contains(kw.Text, "F&B (in title)") {
		t.Errorf("keywords reason: %+v", kw)
	}
	if r := s.Reasons[2]; r.Value != 0 || r.Detail != "Egypt" || !strings.Contains(r.Text, "not in the profile") {
		t.Errorf("region reason: %+v", r)
	}
	if r := s.Reasons[0]; r.Matched[0] != "hospitality" || r.Contribution != 3 {
		t.Errorf("industry reason: %+v", r)
	}
}

// Raising one weight changes the ranking; scaling every weight does not.
func TestWeightsReRank(t *testing.T) {
	base := Profile{
		Industries: Criterion{Values: []string{"Hospitality"}, Weight: 1},
		Signals:    map[string]float64{SigOpenDeal: 3},
	}
	a := hotel() // a fit hotel with an open deal
	b := Facts{Industry: "Software", HasCompany: true, Signals: map[string]bool{SigOpenDeal: true}}
	c := Facts{Industry: "Hospitality", HasCompany: true, Signals: map[string]bool{}}

	p1 := profile(t, base)
	sa, sb, sc := *Compute(p1, a).Score, *Compute(p1, b).Score, *Compute(p1, c).Score
	if !(sa > sb && sb > sc) {
		t.Fatalf("deal-heavy profile: a %d b %d c %d", sa, sb, sc)
	}
	// Industry now outweighs the open deal: c (fit, no deal) passes b (deal, no fit).
	base.Industries.Weight = 6
	p2 := profile(t, base)
	sb2, sc2 := *Compute(p2, b).Score, *Compute(p2, c).Score
	if !(sc2 > sb2) {
		t.Fatalf("industry-heavy profile should put c above b: b %d c %d", sb2, sc2)
	}
	// Doubling everything ranks nobody differently.
	base.Industries.Weight, base.Signals[SigOpenDeal] = 2, 6
	p3 := profile(t, base)
	if *Compute(p3, a).Score != sa || *Compute(p3, b).Score != sb || *Compute(p3, c).Score != sc {
		t.Errorf("doubled weights changed scores: %d %d %d", *Compute(p3, a).Score, *Compute(p3, b).Score, *Compute(p3, c).Score)
	}
}

// A negative weight is a penalty, floored at 0; an empty criterion is not
// scored; nothing positive to weigh gives no score at all.
func TestPenaltiesAndEmpty(t *testing.T) {
	p := profile(t, Profile{Signals: map[string]float64{SigOpenDeal: 2, SigLostDeal: -4}})
	s := Compute(p, Facts{Signals: map[string]bool{SigLostDeal: true}})
	if s.Score == nil || *s.Score != 0 || len(s.Reasons) != 2 || s.Reasons[1].Contribution != -4 {
		t.Errorf("penalty: %v %+v", s.Score, s.Reasons)
	}
	s = Compute(p, Facts{Signals: map[string]bool{SigOpenDeal: true, SigLostDeal: true}})
	if *s.Score != 0 {
		t.Errorf("2 - 4 over 2 floors at 0, got %d", *s.Score)
	}
	// No industries listed: industry is not a reason at all.
	for _, r := range Compute(p, hotel()).Reasons {
		if r.Dimension == DimIndustry {
			t.Errorf("an empty criterion was scored: %+v", r)
		}
	}
	// Only a penalty: nothing to divide by, no score.
	neg := profile(t, Profile{Signals: map[string]float64{SigLostDeal: -1}})
	if s := Compute(neg, Facts{}); s.Score != nil {
		t.Errorf("no positive weight should give no score, got %d", *s.Score)
	}
}

// A contact with no company: the company criteria count as no match, and say why.
func TestNoCompany(t *testing.T) {
	p := profile(t, Profile{Industries: Criterion{Values: []string{"Hospitality"}, Weight: 2}, Signals: map[string]float64{SigSubscribed: 2}})
	s := Compute(p, Facts{Signals: map[string]bool{SigSubscribed: true}})
	if *s.Score != 50 || !strings.Contains(s.Reasons[0].Text, "No company") {
		t.Errorf("no company: %d %+v", *s.Score, s.Reasons)
	}
}

func TestNormalize(t *testing.T) {
	n, err := Profile{
		Industries: Criterion{Values: []string{" Hospitality ", "hospitality", "", "Cafés  &  coffee"}, Weight: 2.345},
		Signals:    map[string]float64{SigWonDeal: 4},
	}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(n.Industries.Values, "|") != "Hospitality|Cafés & coffee" || n.Industries.Weight != 2.35 || n.Signals[SigOpenDeal] != 0 || n.Signals[SigWonDeal] != 4 {
		t.Errorf("normalized: %+v", n)
	}
	for _, bad := range []Profile{
		{Sizes: Criterion{Values: []string{"huge"}}},
		{Signals: map[string]float64{"likes_us": 1}},
		{Keywords: Criterion{Weight: 11}},
		{Signals: map[string]float64{SigWonDeal: -12}},
	} {
		if _, err := bad.Normalize(); err == nil {
			t.Errorf("should refuse %+v", bad)
		}
	}
}
