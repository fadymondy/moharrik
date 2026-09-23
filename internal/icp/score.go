// Package icp is the workspace's ideal-customer profile and the lead score it
// gives every contact and company (MH-395).
//
// The model is fadymondy.com's opportunity scoring (internal/radar/scoring.go,
// FM-104), generalised from one person's radar to any workspace's CRM:
//
//   - Weights are data, not constants. They live in the workspace's profile
//     (icp_profiles.criteria), and changing how the CRM ranks is a save on the
//     settings page, never a deploy.
//   - Every dimension of a score is a Reason: its weight, how much of it is
//     present (0..1), the contribution (weight × value) and what it was made of.
//     A number nobody can explain is not shown.
//   - The total is normalised by the POSITIVE weights that were considered:
//     score = clamp(Σ contribution / Σ positive weights, 0, 1) × 100. Doubling
//     every weight ranks nobody differently; raising one weight does. A negative
//     weight is a penalty and can pull a score down but never below 0.
//   - A criterion the profile leaves empty (no industries listed) is not
//     scored at all — it is neither a match nor a miss, and it does not count
//     in the denominator. A criterion the profile sets but the record cannot
//     answer (no company linked, no industry recorded) counts as no match and
//     says so: a fit has to be shown by the record, not assumed. A workspace
//     whose profile weighs nothing positive gives no score (nil), not 0.
//
// fadymondy.com's employment-eligibility gates and its personal capability
// profile are specific to one freelancer and are deliberately not here (the
// card puts them out of scope).
package icp

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Dimensions, in the order a reader sees them.
const (
	DimIndustry = "industry"
	DimSize     = "size"
	DimRegion   = "region"
	DimKeywords = "keywords"
)

// Signals: what the workspace's own records say a contact has done. Each has
// its own weight in the profile; 0 turns it off.
const (
	SigOpenDeal       = "open_deal"       // an open deal with them or their company
	SigWonDeal        = "won_deal"        // a won deal with them or their company
	SigLostDeal       = "lost_deal"       // a lost deal (a penalty, if weighted negative)
	SigInboundLead    = "inbound_lead"    // came in through a website form (a converted lead)
	SigConversation   = "conversation"    // wrote to the inbox (a linked conversation)
	SigSubscribed     = "subscribed"      // confirmed on a mailing list
	SigWhatsAppOptIn  = "whatsapp_opt_in" // opted in to WhatsApp and has not said STOP
	SigRecentActivity = "recent_activity" // a note, call, meeting or task in the last 30 days
)

// Signals is every signal in reading order.
var Signals = []string{SigOpenDeal, SigWonDeal, SigLostDeal, SigInboundLead, SigConversation, SigSubscribed, SigWhatsAppOptIn, SigRecentActivity}

// RecentDays is how far back an activity still counts as recent.
const RecentDays = 30

// KeywordSaturation is how many matched keywords make the keywords criterion
// fully present: one match is a hint, two are a fit.
const KeywordSaturation = 2

// MaxWeight bounds every weight, both ways.
const MaxWeight = 10

// Criterion is a list of values and the weight of matching one.
type Criterion struct {
	Values []string `json:"values" maxItems:"50"`
	Weight float64  `json:"weight" minimum:"-10" maximum:"10"`
}

// Profile is a workspace's ideal customer.
type Profile struct {
	Industries Criterion `json:"industries" doc:"Company industries that fit (matched case-insensitively, either containing the other)."`
	Sizes      Criterion `json:"sizes" doc:"Company sizes that fit: 1-10, 11-50, 51-200, 201-1000, 1000+."`
	Regions    Criterion `json:"regions" doc:"Company countries or regions that fit (the company's country field)."`
	Keywords   Criterion `json:"keywords" doc:"Words that mark a fit in a contact's title and tags, or a company's name, industry and notes."`
	// Signals maps each signal to its weight; a signal missing here weighs 0.
	Signals map[string]float64 `json:"signals" doc:"Weight per signal: open_deal, won_deal, lost_deal, inbound_lead, conversation, subscribed, whatsapp_opt_in, recent_activity."`
}

// Default is the profile a workspace starts with: no firmographic criteria yet
// (the workspace has to say who it sells to), and the behavioural signals on,
// so contacts are ranked by what they have done from the first day.
func Default() Profile {
	return Profile{
		Industries: Criterion{Values: []string{}, Weight: 3},
		Sizes:      Criterion{Values: []string{}, Weight: 2},
		Regions:    Criterion{Values: []string{}, Weight: 2},
		Keywords:   Criterion{Values: []string{}, Weight: 2},
		Signals: map[string]float64{
			SigOpenDeal: 3, SigWonDeal: 3, SigLostDeal: 0, SigInboundLead: 2,
			SigConversation: 1, SigSubscribed: 1, SigWhatsAppOptIn: 1, SigRecentActivity: 2,
		},
	}
}

var validSizes = map[string]bool{"1-10": true, "11-50": true, "51-200": true, "201-1000": true, "1000+": true}

// Normalize trims, dedupes (case-insensitively) and bounds a profile, and
// refuses what cannot be scored: an unknown size or signal, a weight out of
// range. The error names the field.
func (p Profile) Normalize() (Profile, error) {
	clean := func(name string, c Criterion, maxLen int) (Criterion, error) {
		if math.IsNaN(c.Weight) || c.Weight < -MaxWeight || c.Weight > MaxWeight {
			return c, fmt.Errorf("%s: the weight is -%d to %d", name, MaxWeight, MaxWeight)
		}
		out := Criterion{Values: []string{}, Weight: round2(c.Weight)}
		seen := map[string]bool{}
		for _, v := range c.Values {
			v = strings.Join(strings.Fields(v), " ")
			k := strings.ToLower(v)
			if v == "" || seen[k] {
				continue
			}
			if len([]rune(v)) > maxLen {
				return c, fmt.Errorf("%s: %q is longer than %d characters", name, v, maxLen)
			}
			seen[k] = true
			out.Values = append(out.Values, v)
		}
		if len(out.Values) > 50 {
			return c, fmt.Errorf("%s: at most 50 values", name)
		}
		return out, nil
	}
	var err error
	out := Profile{Signals: map[string]float64{}}
	if out.Industries, err = clean("industries", p.Industries, 80); err != nil {
		return out, err
	}
	if out.Sizes, err = clean("sizes", p.Sizes, 10); err != nil {
		return out, err
	}
	for _, s := range out.Sizes.Values {
		if !validSizes[s] {
			return out, fmt.Errorf("sizes: %q is not one of 1-10, 11-50, 51-200, 201-1000, 1000+", s)
		}
	}
	if out.Regions, err = clean("regions", p.Regions, 80); err != nil {
		return out, err
	}
	if out.Keywords, err = clean("keywords", p.Keywords, 60); err != nil {
		return out, err
	}
	known := map[string]bool{}
	for _, s := range Signals {
		known[s] = true
		out.Signals[s] = 0
	}
	for k, w := range p.Signals {
		if !known[k] {
			return out, fmt.Errorf("signals: %q is not a signal (%s)", k, strings.Join(Signals, ", "))
		}
		if math.IsNaN(w) || w < -MaxWeight || w > MaxWeight {
			return out, fmt.Errorf("signals: %s's weight is -%d to %d", k, MaxWeight, MaxWeight)
		}
		out.Signals[k] = round2(w)
	}
	return out, nil
}

// Facts is what the workspace knows about one contact or one company — the
// only things a score may rest on.
type Facts struct {
	// The company's (for a contact: its company's; empty with none).
	Industry, Size, Country string
	// Text the keywords are looked for in, each labelled for the reason:
	// "title", "tags", "company", "industry", "notes".
	Text map[string]string
	// Signals present (true) — every signal is decided, never unknown.
	Signals map[string]bool
	// HasCompany is false for a contact with no company: the company criteria
	// then count as no match, and the reason says why.
	HasCompany bool
}

// Reason is one scored dimension.
type Reason struct {
	Dimension    string   `json:"dimension" doc:"industry, size, region, keywords, or a signal name."`
	Weight       float64  `json:"weight"`
	Value        float64  `json:"value" doc:"How much of it is present, 0..1."`
	Contribution float64  `json:"contribution" doc:"weight × value."`
	Matched      []string `json:"matched" doc:"The profile values that matched (keywords: the words found)."`
	Detail       string   `json:"detail" doc:"The fact it was judged on: the company's industry, size or country; empty for a signal."`
	Text         string   `json:"text" doc:"The reason in one English sentence."`
}

// Score is a score and its explanation. Score is nil when no criterion could
// be judged.
type Score struct {
	Score   *int     `json:"score"`
	Reasons []Reason `json:"reasons"`
}

func round2(f float64) float64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return math.Round(f*100) / 100
}

func clamp01(f float64) float64 {
	switch {
	case math.IsNaN(f) || f < 0:
		return 0
	case f > 1:
		return 1
	}
	return f
}

// fits reports whether a recorded value matches a profile value: equal, or one
// containing the other ("Hospitality" fits "Hospitality & hotels").
func fits(recorded, want string) bool {
	r, w := strings.ToLower(strings.TrimSpace(recorded)), strings.ToLower(strings.TrimSpace(want))
	if r == "" || w == "" {
		return false
	}
	return r == w || strings.Contains(r, w) || strings.Contains(w, r)
}

// Compute scores one contact or company against a (normalized) profile. Pure:
// the same facts and profile give the same score, reasons in the same order.
func Compute(p Profile, f Facts) Score {
	reasons := []Reason{}
	firmo := func(dim string, c Criterion, recorded, what string, exact bool) {
		if len(c.Values) == 0 || c.Weight == 0 {
			return
		}
		r := Reason{Dimension: dim, Weight: c.Weight, Matched: []string{}, Detail: recorded}
		switch {
		case !f.HasCompany:
			r.Text = fmt.Sprintf("No company is linked, so its %s could not be checked.", what)
		case strings.TrimSpace(recorded) == "":
			r.Text = fmt.Sprintf("The company's %s is not recorded.", what)
		default:
			for _, v := range c.Values {
				if (exact && strings.EqualFold(recorded, v)) || (!exact && fits(recorded, v)) {
					r.Matched = append(r.Matched, v)
				}
			}
			if len(r.Matched) > 0 {
				r.Value = 1
				r.Text = fmt.Sprintf("The company's %s, %s, is in the profile.", what, recorded)
			} else {
				r.Text = fmt.Sprintf("The company's %s, %s, is not in the profile.", what, recorded)
			}
		}
		reasons = append(reasons, r)
	}
	firmo(DimIndustry, p.Industries, f.Industry, "industry", false)
	firmo(DimSize, p.Sizes, f.Size, "size", true)
	firmo(DimRegion, p.Regions, f.Country, "country", false)

	if len(p.Keywords.Values) > 0 && p.Keywords.Weight != 0 {
		r := Reason{Dimension: DimKeywords, Weight: p.Keywords.Weight, Matched: []string{}}
		where := []string{}
		fields := make([]string, 0, len(f.Text))
		for k := range f.Text {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		for _, kw := range p.Keywords.Values {
			lk := strings.ToLower(kw)
			for _, field := range fields {
				if strings.Contains(strings.ToLower(f.Text[field]), lk) {
					r.Matched = append(r.Matched, kw)
					where = append(where, kw+" (in "+field+")")
					break
				}
			}
		}
		r.Value = clamp01(float64(len(r.Matched)) / KeywordSaturation)
		if len(r.Matched) > 0 {
			r.Text = "Profile keywords found: " + strings.Join(where, ", ") + "."
		} else {
			r.Text = "None of the profile's keywords appear in the title, tags or company."
		}
		reasons = append(reasons, r)
	}

	for _, sig := range Signals {
		w := p.Signals[sig]
		if w == 0 {
			continue
		}
		r := Reason{Dimension: sig, Weight: w, Matched: []string{}}
		if f.Signals[sig] {
			r.Value = 1
			r.Text = signalText[sig] + "."
		} else {
			r.Text = signalAbsent[sig] + "."
		}
		reasons = append(reasons, r)
	}

	total, positive, judged := 0.0, 0.0, false
	for i := range reasons {
		reasons[i].Value = round2(clamp01(reasons[i].Value))
		reasons[i].Contribution = round2(reasons[i].Weight * reasons[i].Value)
		total += reasons[i].Contribution
		if reasons[i].Weight > 0 {
			positive += reasons[i].Weight
		}
		judged = true
	}
	out := Score{Reasons: reasons}
	if !judged || positive <= 0 {
		return out
	}
	s := int(math.Round(clamp01(total/positive) * 100))
	out.Score = &s
	return out
}

var signalText = map[string]string{
	SigOpenDeal:       "Has an open deal (theirs or their company's)",
	SigWonDeal:        "Has a won deal (theirs or their company's)",
	SigLostDeal:       "Has a lost deal (theirs or their company's)",
	SigInboundLead:    "Came in through a website form",
	SigConversation:   "Has written to the inbox",
	SigSubscribed:     "Is subscribed to a mailing list",
	SigWhatsAppOptIn:  "Opted in to WhatsApp",
	SigRecentActivity: "Had a note, call, meeting or task in the last 30 days",
}

var signalAbsent = map[string]string{
	SigOpenDeal:       "No open deal (theirs or their company's)",
	SigWonDeal:        "No won deal yet (theirs or their company's)",
	SigLostDeal:       "No lost deal (theirs or their company's)",
	SigInboundLead:    "Did not come in through a website form",
	SigConversation:   "Has not written to the inbox",
	SigSubscribed:     "Is not subscribed to a mailing list",
	SigWhatsAppOptIn:  "Has not opted in to WhatsApp",
	SigRecentActivity: "Nothing logged in the last 30 days",
}
