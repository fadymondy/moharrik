package research

import (
	"regexp"
	"strings"
	"unicode"
)

/*
From a question to what is actually searched (MH-466).

The question used to go to SearXNG verbatim. "What are green coffee import rules
and tariffs for Egypt in 2026?" came back as eight pages about the colour green:
the question words and "and/for/in" diluted it, "green coffee" was two
unrelated words, no language was set so the instance's default answered, and
one of the instance's engines (Bing, at the time of writing) matched only the
first word of whatever it was given.

A Plan fixes each of those in turn:

  - quoted phrases are kept as phrases;
  - an obvious multi-word term is quoted too: a word that changes meaning next
    to a noun ("green coffee", "crude oil", "البن الأخضر"), and a run of
    Capitalised Words ("Red Sea", "New Cairo");
  - question words and stop words (English and Arabic) are dropped;
  - two or three variants are searched and merged by rank, so one engine's
    reading of one phrasing does not decide the run;
  - the SearXNG language follows the question's script (Arabic → ar);
  - and the merged results must actually mention the subject (Relevant), which
    is the only defence against an engine that ignores most of the query.
*/

// Plan is what a research run searches for one question.
type Plan struct {
	// Language is SearXNG's language parameter: "ar", "en", or "" for the
	// instance default when the question is neither.
	Language string
	// Phrases are searched quoted.
	Phrases []string
	// Keywords are the remaining content words, in the question's order.
	Keywords []string
	// Queries are the variants searched, most specific first. Never empty
	// for a question with any content.
	Queries []string
}

// Summary is how the run records what it searched.
func (p Plan) Summary() string { return strings.Join(p.Queries, " | ") }

var (
	quotedRe = regexp.MustCompile(`"([^"]+)"|“([^”]+)”|«([^»]+)»`)
	yearRe   = regexp.MustCompile(`^(19|20)\d\d$`)
)

// English question words and stop words: the words that carry no subject.
var enStop = set(`a an the and or but nor so yet of to in on at by for from with without within into onto about over under
between among through during before after above below up down out off again further than then once
what which who whom whose when where why how is are was were be been being am do does did doing done have has had having
can could should would will shall may might must need needs please tell me us you your i we our my it its this that these those
there here any some all each every both few more most other such only own same very just also too not no
latest current currently new recent recently now today know explain find give list show want looking look
kind kinds type types way ways thing things one ones`)

// Words that say what KIND of answer is wanted rather than what it is about.
// They are kept in the fullest variant and dropped from the core one.
var enGeneric = set(`rules rule requirements requirement regulations regulation guide guidelines overview information info details
process procedure procedures steps step options best top tips list`)

// Arabic question words and stop words. Matched after normalise(), so the
// forms here are the normalised ones (no hamza on alef, ى as ي, ة as ه).
var arStop = set(`ما ماذا ماهي ماهو هي هو هم هن هل كيف لماذا متي اين كم اي اى اية من في علي الي عن مع و او ام ثم بل لكن
هذا هذه ذلك تلك هناك هنا التي الذي الذين اللتي اللذين ان انه انها كان كانت يكون تكون قد لقد لم لن لا ليس بين حول عند
كل بعض غير سوي منذ حتي خلال بعد قبل فوق تحت عام لعام سنه لسنه الان حاليا اخر احدث جديد ارجو اريد ممكن يمكن اعرف معرفه`)

// English modifiers that make a different thing of the noun after them:
// "green coffee" is not about green, "crude oil" is not about crude.
var enModifiers = set(`green black white red blue yellow brown grey gray golden silver pink orange purple
raw crude roasted unroasted frozen chilled organic instant specialty fresh dried refined used electric solar
smart digital social cloud real free public private open`)

// Arabic colour and state adjectives, which FOLLOW their noun: "البن الأخضر".
var arModifiers = set(`الاخضر الخضراء الاسود السوداء الابيض البيضاء الاحمر الحمراء الازرق الزرقاء الاصفر الصفراء
البني المحمص الخام العضوي المجمد الطازج سريع الذوبان الحر الحره`)

func set(words string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(words) {
		m[w] = true
	}
	return m
}

// PlanQuery turns a question into what is searched.
func PlanQuery(question string) Plan {
	q := strings.TrimSpace(question)
	p := Plan{Language: detectLanguage(q)}

	// 1. Quoted phrases, kept whole and taken out of the rest.
	rest := quotedRe.ReplaceAllStringFunc(q, func(m string) string {
		sub := quotedRe.FindStringSubmatch(m)
		for _, g := range sub[1:] {
			if g = strings.TrimSpace(g); g != "" {
				p.Phrases = appendUnique(p.Phrases, g)
			}
		}
		return " , " // a boundary: words either side of a quote are not one term
	})

	// 2. Words, with clause boundaries kept, so a term never spans a comma or
	// a stop word ("tariffs for Egypt" is not a term).
	type word struct {
		text    string
		content bool
	}
	var words []word
	for i, tok := range tokenize(rest) {
		if tok == "," {
			words = append(words, word{})
			continue
		}
		key := normalise(tok)
		content := !(enStop[key] || arStop[key]) && len([]rune(key)) > 1 || yearRe.MatchString(key) ||
			// A capitalised stop word mid-sentence is part of a name: "New Cairo", "The Hague".
			i > 0 && capitalised(tok) && len(tok) > 1
		words = append(words, word{text: tok, content: content})
	}

	// 3. Multi-word terms inside each run of content words.
	used := make([]bool, len(words))
	for i := 0; i+1 < len(words); i++ {
		a, b := words[i], words[i+1]
		if !a.content || !b.content || used[i] || used[i+1] {
			continue
		}
		na, nb := normalise(a.text), normalise(b.text)
		switch {
		case enModifiers[na] && !yearRe.MatchString(nb) && isLatin(nb): // green coffee
		case arModifiers[nb] && !isLatin(na): // البن الأخضر
		case capitalised(a.text) && capitalised(b.text) && i > 0: // Red Sea (not the question's first word)
		default:
			continue
		}
		term := a.text + " " + b.text
		// A capitalised run can be longer: "United Arab Emirates".
		j := i + 2
		for capitalised(a.text) && j < len(words) && words[j].content && capitalised(words[j].text) && !used[j] {
			term += " " + words[j].text
			used[j] = true
			j++
		}
		used[i], used[i+1] = true, true
		p.Phrases = appendUnique(p.Phrases, term)
	}
	for i, w := range words {
		if w.content && !used[i] {
			p.Keywords = appendUnique(p.Keywords, w.text)
		}
	}

	p.Queries = p.variants(q)
	return p
}

// variants are the queries searched, most specific first, without repeats.
func (p Plan) variants(original string) []string {
	quoted := make([]string, 0, len(p.Phrases))
	for _, ph := range p.Phrases {
		quoted = append(quoted, `"`+ph+`"`)
	}
	var core, entities []string
	for _, k := range p.Keywords {
		n := normalise(k)
		if yearRe.MatchString(n) || enGeneric[n] {
			continue
		}
		core = append(core, k)
		if capitalised(k) {
			entities = append(entities, k)
		}
	}

	var out []string
	add := func(parts ...[]string) {
		var all []string
		for _, p := range parts {
			all = append(all, p...)
		}
		s := strings.Join(all, " ")
		if strings.TrimSpace(s) == "" {
			return
		}
		for _, o := range out {
			if o == s {
				return
			}
		}
		out = append(out, s)
	}
	add(quoted, p.Keywords) // everything the question is about
	add(quoted, core)       // without the year and the "rules/guide" words
	if len(quoted) > 0 && len(entities) > 0 {
		// The subject and the place alone: the broadest phrasing that still
		// names both, for pages that use other words for "tariffs".
		add(quoted, entities)
	}
	if len(out) == 0 {
		// Nothing but stop words: search what was asked rather than nothing.
		out = append(out, strings.TrimSpace(original))
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

// Relevant reports whether a result is about the question at all: it must name
// a phrase, or enough of the keywords. It is what keeps an engine that matched
// one word of the query ("Green – Wikipedia") out of a run.
func (p Plan) Relevant(title, snippet string) bool {
	text := " " + normalise(title+" "+snippet) + " "
	phrases := 0
	for _, ph := range p.Phrases {
		if strings.Contains(text, normalise(ph)) {
			phrases++
		}
	}
	keywords, counted := 0, 0
	for _, k := range p.Keywords {
		n := normalise(k)
		if yearRe.MatchString(n) {
			continue // "2026" is on half the internet
		}
		counted++
		if strings.Contains(text, stem(n)) {
			keywords++
		}
	}
	switch {
	case phrases > 0:
		return true
	case len(p.Phrases) > 0:
		// The subject is named by a phrase and this result does not use it:
		// only a result that matches most of the rest is let through.
		return counted > 0 && keywords >= 3 || counted > 0 && keywords == counted
	case counted == 0:
		return true
	case counted == 1:
		return keywords == 1
	default:
		return keywords >= 2
	}
}

// detectLanguage picks SearXNG's language from the question's script.
func detectLanguage(s string) string {
	var arabic, latin int
	for _, r := range s {
		switch {
		case unicode.In(r, unicode.Arabic):
			arabic++
		case r < 128 && unicode.IsLetter(r):
			latin++
		}
	}
	switch {
	case arabic > 0 && arabic >= latin:
		return "ar"
	case latin > 0 && looksEnglish(s):
		return "en"
	default:
		return ""
	}
}

func looksEnglish(s string) bool {
	hits, total := 0, 0
	for _, w := range tokenize(s) {
		total++
		if enStop[strings.ToLower(w)] {
			hits++
		}
	}
	// Any English function word in a short question, or one in five in a long one.
	return hits > 0 && (total < 6 || hits*5 >= total)
}

// tokenize splits on spaces and punctuation, keeping commas and sentence
// punctuation as a "," boundary token.
func tokenize(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || r == '\'' || r == '-' || r == '&' || r == '.' && b.Len() > 0:
			b.WriteRune(r)
		case r == ',' || r == '?' || r == '!' || r == ';' || r == ':' || r == '،' || r == '؟' || r == '(' || r == ')':
			flush()
			out = append(out, ",")
		default:
			flush()
		}
	}
	flush()
	for i, t := range out {
		out[i] = strings.Trim(t, ".'-")
	}
	return out
}

// normalise lower-cases, and folds the Arabic spellings people use
// interchangeably, so "إستيراد" and "استيراد" are one word.
func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'أ' || r == 'إ' || r == 'آ' || r == 'ٱ':
			b.WriteRune('ا')
		case r == 'ى':
			b.WriteRune('ي')
		case r == 'ة':
			b.WriteRune('ه')
		case r == 'ـ' || unicode.Is(unicode.Mn, r): // tatweel and diacritics
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// stem is the matching form of a keyword: English plurals trimmed, and the
// Arabic article dropped when enough word is left to be specific.
func stem(n string) string {
	if isLatin(n) {
		switch {
		case strings.HasSuffix(n, "ies") && len(n) > 4:
			return n[:len(n)-3] + "y"
		case strings.HasSuffix(n, "ses") || strings.HasSuffix(n, "xes") || strings.HasSuffix(n, "ches") || strings.HasSuffix(n, "shes"):
			return strings.TrimSuffix(n, "es")
		case strings.HasSuffix(n, "s") && !strings.HasSuffix(n, "ss") && len(n) > 3:
			return n[:len(n)-1]
		}
		return n
	}
	if r := []rune(n); len(r) > 5 && strings.HasPrefix(n, "ال") {
		return string(r[2:])
	}
	return n
}

func isLatin(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && r >= 128 {
			return false
		}
	}
	return true
}

func capitalised(w string) bool {
	for _, r := range w {
		return unicode.IsUpper(r)
	}
	return false
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return list
		}
	}
	return append(list, s)
}
