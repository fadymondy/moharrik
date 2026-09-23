package trends

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

/*
Normalisation: the deterministic half of "is this the same story" (MH-385).

Ported from fadymondy.com internal/trends/normalise.go. Two headlines about one
story rarely share their wording, but they share the RARE words — the product,
the company, the person, the place. So a title becomes a set of normalised
tokens here, and cluster.go weighs each token by how rare it is across the
window. Nothing here knows about any story.

What Moharrik adds is Arabic that actually groups. The reference only stripped
the article "ال", so "إيرانيين" / "إيران", "تركية" / "تركيا" and "بإبادتها" /
"إبادة" stayed different words and two outlets' Arabic headlines about one
event never met. stemArabic below is a light stemmer in the Light10 family:
crude, and deliberately symmetric — both sides of a comparison go through it,
so it only has to map variants to ONE form, not to the right one.
*/

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		a an the and or but nor so yet of in on at to for from by with without into onto over under
		about after before between through during than then as is are was were be been being am
		it its it's this that these those there here what which who whom whose why how when where
		i you he she we they me him her us them my your his our their mine yours
		do does did done doing have has had having can could will would shall should may might must
		not no yes all any some more most less least many much few very just also only even still
		new now up down out off again once get gets got getting make makes made say says said
		your you're we're they're i'm vs via per if into amid than too own same other such
		how why what's here's thing things way ways one two first last next week year day today
		show ask tell hn launch launches watch live video
	`) {
		stopwords[w] = true
	}
	// Arabic function words: only words that carry no topic. Both the spelling
	// and its stem are listed, because the check runs on each.
	for _, w := range strings.Fields(`
		في من على الى إلى عن مع هذا هذه ذلك تلك التي الذي الذين ما ماذا لماذا كيف متى أين هل
		او أو و ثم لكن بل قد لقد كان كانت يكون تكون بعد قبل حول عبر بين عند حتى كل بعض غير
		جديد جديدة اول أول ان أن إن لا لم لن هو هي هم نحن انت خلال امام أمام ضد لدى منذ دون
		اي أي نحو وسط اثر إثر تحت فوق عاجل شاهد فيديو بالفيديو صور قال قالت يقول تقول وفق وفقا
		بشأن بشان إما اما ليس ليست مثل عام اليوم امس أمس غدا الآن هناك هنا كما أيضا ايضا فقط لها له لهم به بها فيه فيها
	`) {
		n := normaliseArabic(w)
		stopwords[n] = true
		stopwords[stemArabic(n)] = true
	}
}

var arabicDiacritics = regexp.MustCompile(`[\x{064B}-\x{065F}\x{0670}\x{0640}]`)

// normaliseArabic folds the spelling variants headlines use interchangeably:
// diacritics and tatweel go, every hamza-carrying alef is a bare alef, and
// ta marbuta / alef maqsura become ha / ya.
func normaliseArabic(s string) string {
	s = arabicDiacritics.ReplaceAllString(s, "")
	return strings.NewReplacer("أ", "ا", "إ", "ا", "آ", "ا", "ة", "ه", "ى", "ي", "ؤ", "و", "ئ", "ي").Replace(s)
}

func isArabic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Arabic, r) {
			return true
		}
	}
	return false
}

// The article and its fused prepositions/conjunctions, longest first.
var arabicPrefixes = []string{"وبال", "وال", "بال", "كال", "فال", "لل", "ال"}

// Suffixes, in the order they are tried. The first group are attached
// pronouns: "إبادتها" is "إبادة" + "ها", and once the pronoun is gone the ta
// that was a ta marbuta is dropped too, so it meets "إبادة" -> "اباد".
var (
	arabicPronouns = []string{"هما", "كما", "ها", "هم", "هن", "كم"}
	arabicSuffixes = []string{"ات", "ون", "ين", "ان", "يه", "يا", "ه", "ي", "ا"}
)

// minStem is the shortest an Arabic stem is allowed to become: below three
// letters almost everything collides with something.
const minStem = 3

/*
stemArabic reduces an already-normalised Arabic word. It strips one leading
conjunction "و" (only when a real word is left), the article with its fused
prepositions, attached pronouns, and then plural/dual/nisba/feminine endings
until nothing more comes off:

	إيرانيين -> ايرانيين -> ايراني -> ايران -> اير
	إيران    -> ايران -> اير
	تركية / تركيا -> ترك
	أمريكي / أمريكا / الأمريكيين -> امريك
*/
func stemArabic(w string) string {
	rs := []rune(w)
	if len(rs) > 4 && rs[0] == 'و' && !strings.HasPrefix(w, "وال") && !strings.HasPrefix(w, "وبال") {
		w = string(rs[1:])
		rs = rs[1:]
	}
	// "بإيران", "بإبادتها": the preposition fused to a word that began with a
	// hamza (a bare alef once normalised). "بال" is handled with the article.
	if len(rs) > 4 && rs[0] == 'ب' && rs[1] == 'ا' && rs[2] != 'ل' {
		w = string(rs[1:])
	}
	for _, p := range arabicPrefixes {
		if strings.HasPrefix(w, p) && len([]rune(w))-len([]rune(p)) >= minStem {
			w = strings.TrimPrefix(w, p)
			break
		}
	}
	for _, p := range arabicPronouns {
		if strings.HasSuffix(w, p) && len([]rune(w))-len([]rune(p)) >= minStem {
			w = strings.TrimSuffix(w, p)
			if strings.HasSuffix(w, "ت") && len([]rune(w)) > minStem {
				w = strings.TrimSuffix(w, "ت")
			}
			break
		}
	}
	for changed := true; changed; {
		changed = false
		for _, s := range arabicSuffixes {
			if strings.HasSuffix(w, s) && len([]rune(w))-len([]rune(s)) >= minStem {
				w = strings.TrimSuffix(w, s)
				changed = true
				break
			}
		}
	}
	return w
}

// stem is deliberately crude and symmetric: both sides of a comparison go
// through it, so it only has to map "agents"/"agent" to one form, not be right.
func stem(w string) string {
	n := len([]rune(w))
	switch {
	case isArabic(w):
		return stemArabic(w)
	case n > 4 && strings.HasSuffix(w, "ies"):
		return strings.TrimSuffix(w, "ies") + "y"
	case n > 5 && strings.HasSuffix(w, "ing"):
		return strings.TrimSuffix(w, "ing")
	case n > 4 && strings.HasSuffix(w, "ed") && !strings.HasSuffix(w, "eed"):
		return strings.TrimSuffix(w, "ed")
	case n > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us"):
		return strings.TrimSuffix(w, "s")
	}
	return w
}

// Tokens is a title's normalised, de-duplicated tokens, in first-seen order.
func Tokens(title string) []string {
	tokens, _ := Analyse(title)
	return tokens
}

func isSplit(r rune) bool {
	return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '#')
}

func hasLetter(w string) bool {
	for _, r := range w {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// nameLike reports whether a word, as written, looks like a proper noun.
func nameLike(word string, first, titleCase, arabicTitle, afterName bool) bool {
	runes := []rune(word)
	upper, lower, digit := 0, 0, false
	innerCap := false
	for i, r := range runes {
		switch {
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsUpper(r):
			upper++
			if i > 0 && lower > 0 {
				innerCap = true // iPhone, GitHub, PlayStation, SpaceX
			}
		case unicode.IsLower(r):
			lower++
		}
	}
	switch {
	case digit && hasLetter(word):
		return true // PS5, R8, S26
	case digit && len(runes) == 4 && (strings.HasPrefix(word, "19") || strings.HasPrefix(word, "20")):
		return false // a year says nothing about which story
	case digit && afterName:
		return true // Palma 3, iOS 27, Windows 11
	case digit:
		return false
	case innerCap:
		return true
	case upper >= 2 && lower == 0: // EU, PS, MCP
		return true
	case arabicTitle && !isArabic(word) && (upper > 0 || lower > 0):
		return true // a Latin word in an Arabic headline is a name
	case !titleCase && !first && upper > 0 && unicode.IsUpper(runes[0]):
		return true
	}
	return false
}

/*
Analyse returns a title's tokens and which of them are NAMES — words written
like proper nouns: capitalised mid-sentence in a sentence-case headline, with a
digit (PS5, iOS 27), with an inner capital (iPhone), all capitals (EU), or a
Latin word inside an Arabic headline. In a Title Case headline every word is
capitalised, so capitals alone say nothing there. Arabic has no capitals at
all, which is why cluster.go gives Arabic pairs their own rule.
*/
func Analyse(title string) ([]string, map[string]bool) {
	arabicTitle := isArabic(title)
	words := strings.Fields(strings.NewReplacer("’s ", " ", "'s ", " ", "’", "", "'", "", "&amp;", " ").Replace(title + " "))
	capital, lettered := 0, 0
	for _, w := range words {
		rs := []rune(strings.TrimLeftFunc(w, func(r rune) bool { return !unicode.IsLetter(r) }))
		if len(rs) == 0 || stopwords[strings.ToLower(string(rs))] {
			continue
		}
		lettered++
		if unicode.IsUpper(rs[0]) {
			capital++
		}
	}
	titleCase := lettered > 0 && float64(capital)/float64(lettered) >= 0.6

	var out []string
	seen := map[string]bool{}
	names := map[string]bool{}
	prevName := false
	for wi, w := range words {
		wordName := false
		for _, part := range strings.FieldsFunc(w, isSplit) {
			part = strings.Trim(part, "+#")
			if part == "" {
				continue
			}
			acronym := len([]rune(part)) >= 2 && strings.ToUpper(part) == part && hasLetter(part) && !isArabic(part)
			f := normaliseArabic(strings.ToLower(part))
			// "US" is a country; "us" is a pronoun.
			if stopwords[f] && !acronym {
				continue
			}
			if len([]rune(f)) == 1 && !unicode.IsDigit([]rune(f)[0]) {
				continue
			}
			if !acronym {
				f = stem(f)
			}
			if f == "" || (stopwords[f] && !acronym) {
				continue
			}
			if nameLike(part, wi == 0, titleCase, arabicTitle, prevName) {
				names[f] = true
				wordName = true
			}
			if seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
		prevName = wordName
	}
	return out, names
}

// trackingParams never identify an article. at_* is the BBC's: its feeds tag
// every link with ?at_medium=RSS&at_campaign=rss.
var trackingParams = map[string]bool{
	"source": true, "ref": true, "ref_src": true, "fbclid": true, "gclid": true, "guccounter": true,
	"guce_referrer": true, "guce_referrer_sig": true, "hl": true, "gl": true, "ceid": true, "cmpid": true,
	"sr_share": true, "smid": true, "mc_cid": true, "mc_eid": true, "taid": true, "maca": true,
	"rss": true, "feed": true, "feedtype": true, "cmp": true, "rsscampaign": true,
}

// hostAliases are one publisher answering on two hosts with the same paths.
var hostAliases = map[string]string{"bbc.co.uk": "bbc.com"}

/*
CanonicalURL is the comparable form of an article link: no scheme, no www, no
tracking parameters, no fragment, no trailing slash. "" when the link is not
an article (a Google News redirect, or no link at all).

It is what dedupes across sources (MH-385): the BBC's business feed lists some
stories twice under different guids ("…#0" and "…#1"), and its world feed
carries the same article again — all three are one row.
*/
func CanonicalURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	if alias, ok := hostAliases[host]; ok {
		host = alias
	}
	if host == "news.google.com" || host == "trends.google.com" {
		return ""
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		lk := strings.ToLower(k)
		if trackingParams[lk] || strings.HasPrefix(lk, "utm_") || strings.HasPrefix(lk, "at_") {
			continue
		}
		keys = append(keys, k)
	}
	// Medium's query string is only ever tracking.
	if host == "medium.com" || strings.HasSuffix(host, ".medium.com") {
		keys = nil
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(host)
	b.WriteString(path)
	for i, k := range keys {
		if i == 0 {
			b.WriteByte('?')
		} else {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(k) + "=" + url.QueryEscape(q.Get(k)))
	}
	return b.String()
}
