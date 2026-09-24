package research

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

/*
From a page to evidence (MH-388).

A run used to end at a summary: the pages were kept, but nothing in them could
be searched, compared across runs, or handed to the CRM. This file turns each
page into a few pieces of EVIDENCE — a verbatim sentence, the company it is
about, the kind of signal it reports — and it does so without a model, because
production has none configured today.

The heuristic path is the product, not a placeholder:

  - A quote is ALWAYS a sentence copied from the page. Nothing is paraphrased,
    so a quote can be checked against its source by anyone who opens the link.
  - A sentence is kept because it is about the question (the plan's phrases and
    keywords, the same ones the search used) or because it reports a signal
    (hiring, funding, launch, expansion, partnership, leadership, technology,
    regulatory) — in English or Arabic.
  - The company is read from the sentence ("Acme raised…", "شركة أكمي…",
    "Acme Ltd") or, on a company's own website, from the page itself. A news
    site, a social network or a job board is never mistaken for the company
    the article is about.

A model, when one is configured, adds a one-line claim per quote — and the
claim is only kept when its quote is really on the page (see modelExtract).
*/

// Extracted is one piece of evidence, before it is stored.
type Extracted struct {
	Kind          string     `json:"kind"`
	Quote         string     `json:"quote"`
	Claim         string     `json:"claim"`
	CompanyName   string     `json:"company_name"`
	CompanyDomain string     `json:"company_domain"`
	SignalType    string     `json:"signal_type"`
	Confidence    float64    `json:"confidence"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
	Extractor     string     `json:"extractor"`
}

// Page is what extraction reads.
type Page struct {
	URL     string
	Title   string
	Text    string // the fetched page as markdown; empty when it could not be read
	Snippet string // the search engine's line, used when there is no page text
}

// SignalTypes is the closed vocabulary, in the order the UI lists them.
var SignalTypes = []string{"hiring", "funding", "launch", "expansion", "partnership", "leadership", "technology", "regulatory"}

func validSignal(s string) bool {
	for _, t := range SignalTypes {
		if t == s {
			return true
		}
	}
	return false
}

// perPage bounds the evidence one page contributes: a long article about the
// question would otherwise drown the other sources in near-duplicates.
const perPage = 4

// minScore is the bar a sentence clears to be evidence at all.
const minScore = 0.3

// signalWords are matched against the normalised sentence (normalise():
// lower-case, Arabic alef/ya/ta-marbuta folded). English entries carry their
// own leading space so "hires" does not match "chires"; Arabic ones match
// inside words, because the article and prefixes attach to them.
var signalWords = map[string][]string{
	"hiring": {" we're hiring", " we are hiring", " is hiring", " are hiring", " hiring ", " hires ", " job opening",
		" open position", " open role", " vacanc", " recruit", " join our team", " careers at",
		"توظيف", "وظائف", "وظيفه", "مطلوب", "تبحث عن", "نبحث عن", "انضم الي فريق"},
	"funding": {" raised ", " raises ", " funding", " series a", " series b", " series c", " seed round", " pre-seed",
		" investment round", " valuation", " venture capital", " secured $", " secures $", " million in ",
		"تمويل", "جوله استثماريه", "جمعت", "استثمارات", "مستثمرين", "مليون دولار"},
	"launch": {" launched", " launches", " unveil", " introduces", " introduced", " rolled out", " rolls out",
		" now available", " new product", " new platform", " released", " releases",
		"اطلقت", "اطلاق", "تطلق", "كشفت عن", "منتج جديد", "منصه جديده"},
	"expansion": {" expand", " expansion", " new office", " opens office", " opened an office", " entering the",
		" enters the", " new market", " new branch", " opens in ", " opened in ",
		"توسع", "توسيع", "افتتاح", "فرع جديد", "مقر جديد", "دخول سوق"},
	"partnership": {" partnership", " partners with", " partnered", " teamed up", " collaborat", " signed an agreement",
		" memorandum of understanding", " mou ",
		"شراكه", "اتفاقيه", "مذكره تفاهم", "وقعت"},
	"leadership": {" appointed", " appoints", " names new", " named new", " joins as", " new ceo", " new cto",
		" chief executive", " chief technology", " steps down",
		"تعيين", "عينت", "رئيسا تنفيذيا", "رئيس تنفيذي جديد"},
	"technology": {" migrat", " adopts", " adopted", " implement", " deploys", " deployed", " built on", " powered by",
		" switch to", " moving to", " digital transformation",
		"التحول الرقمي", "تعتمد علي", "ترحيل", "الانتقال الي"},
	"regulatory": {" regulation", " regulator", " compliance", " licens", " central bank", " ministry", " decree",
		" mandate", " new law",
		"قانون", "لائحه", "ترخيص", "البنك المركزي", "وزاره", "قرار وزاري"},
}

// Signal returns the strongest signal a sentence reports, or "".
func Signal(sentence string) string {
	s := " " + normalise(sentence) + " "
	best, bestHits := "", 0
	for _, t := range SignalTypes {
		hits := 0
		for _, w := range signalWords[t] {
			if strings.Contains(s, w) {
				hits++
			}
		}
		if hits > bestHits {
			best, bestHits = t, hits
		}
	}
	return best
}

var (
	mdImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	bareURL    = regexp.MustCompile(`https?://\S+`)
	htmlTag    = regexp.MustCompile(`<[^>]+>`)
	mdNoise    = regexp.MustCompile("[#*_>`|~]+")
	hasDigit   = regexp.MustCompile(`\d`)
	listMarker = regexp.MustCompile(`^(?:[-•·*+]\s+|\d{1,3}[.)]\s+)+`)
	// "mylo hiring Full-Stack Engineer in Cairo | LinkedIn": the job boards'
	// own title format names the employer before "hiring", in any case.
	titleHiring = regexp.MustCompile(`(?i)^\s*(.{2,60}?)\s+(?:is\s+)?hiring\b`)
	spaceRun   = regexp.MustCompile(`\s+`)
	boilerRe   = regexp.MustCompile(`(?i)cookie|subscribe|sign up|sign in|log in|all rights reserved|privacy policy|terms of (use|service)|javascript|newsletter|jump to|skip to|share this|click here|read more|جميع الحقوق|اشترك|سياسه الخصوصيه`)
	abbrevWord = set(`inc ltd co corp mr mrs ms dr st jr sr vs e.g i.e u.s u.k no fig etc approx dept`)
)

// Sentences splits page text into clean candidate sentences.
func Sentences(text string) []string {
	text = mdImage.ReplaceAllString(text, " ")
	text = mdLink.ReplaceAllString(text, "$1")
	text = bareURL.ReplaceAllString(text, " ")
	text = htmlTag.ReplaceAllString(text, " ")

	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(mdNoise.ReplaceAllString(line, " "))
		line = listMarker.ReplaceAllString(line, "")
		if line == "" {
			continue
		}
		for _, s := range splitSentences(line) {
			s = strings.TrimSpace(spaceRun.ReplaceAllString(s, " "))
			n := len([]rune(s))
			if n < 40 || n > 500 || len(strings.Fields(s)) < 6 || boilerRe.MatchString(s) {
				continue
			}
			key := normalise(s)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, s)
		}
	}
	return out
}

// splitSentences cuts at . ! ? ؟ followed by a space — except after an
// abbreviation or an initial, so "Acme Inc. raised" stays one sentence.
func splitSentences(line string) []string {
	rs := []rune(line)
	var out []string
	start := 0
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r != '.' && r != '!' && r != '?' && r != '؟' && r != '۔' {
			continue
		}
		if i+1 < len(rs) && !unicode.IsSpace(rs[i+1]) {
			continue
		}
		if r == '.' {
			// The word before the period.
			j := i - 1
			for j >= start && !unicode.IsSpace(rs[j]) {
				j--
			}
			word := strings.ToLower(strings.Trim(string(rs[j+1:i]), "()\"'"))
			if len([]rune(word)) <= 1 || abbrevWord[word] {
				continue
			}
		}
		out = append(out, string(rs[start:i+1]))
		start = i + 1
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// relevance is how much of the question a sentence is about, 0..1: phrases
// count double, and keywords match on their stem.
func relevance(p Plan, sentence string) float64 {
	text := " " + normalise(sentence) + " "
	num, den := 0.0, 0.0
	for _, ph := range p.Phrases {
		den += 2
		if strings.Contains(text, normalise(ph)) {
			num += 2
		}
	}
	for _, k := range p.Keywords {
		n := normalise(k)
		if yearRe.MatchString(n) {
			continue
		}
		den++
		if strings.Contains(text, stem(n)) {
			num++
		}
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// ExtractHeuristic picks a page's evidence without a model.
func ExtractHeuristic(p Plan, page Page) []Extracted {
	host := hostOf(page.URL)
	pageCo := pageCompany(page.URL, page.Title)
	published := publishedDate(page.Text)

	text := page.Text
	fromSnippet := false
	if strings.TrimSpace(text) == "" {
		// Not read: the engine's own excerpt is all there is. It is still the
		// page's words, but less of them, so it counts for less.
		text = page.Snippet
		fromSnippet = true
	}

	type cand struct {
		e     Extracted
		score float64
	}
	var cands []cand
	sentences := Sentences(text)
	// The title is the page's own words too, and on a job board it is the only
	// place the employer is named in a fixed form.
	titleText := ""
	if parts := splitTitle(page.Title); len(parts) > 0 && len(strings.Fields(parts[0])) >= 4 &&
		// A title the search engine cut short ("… to Accelerate MENA ...") is
		// not the page's sentence; the page's own heading will be, if read.
		!strings.HasSuffix(strings.TrimSpace(parts[0]), "...") && !strings.HasSuffix(strings.TrimSpace(parts[0]), "…") {
		titleText = parts[0]
		sentences = append([]string{titleText}, sentences...)
	}
	for _, s := range sentences {
		rel := relevance(p, s)
		sig := Signal(s)
		score := 0.6 * rel
		if sig != "" {
			score += 0.3
		}
		if hasDigit.MatchString(s) {
			score += 0.1
		}
		// A sentence that is only a signal, about nothing asked, is kept only
		// when it names who it is about.
		name, domain := companyIn(s, pageCo)
		if s == titleText {
			if m := titleHiring.FindStringSubmatch(s); m != nil && !notNameStart[strings.ToLower(strings.Fields(m[1])[0])] && len(strings.Fields(m[1])) <= 5 {
				name, domain = strings.TrimSpace(m[1]), pageCo.domainFor(m[1])
				sig = "hiring"
				score += 0.3
			}
		}
		if rel == 0 && name == "" {
			continue
		}
		if fromSnippet {
			score *= 0.7
		}
		if score < minScore {
			continue
		}
		if score > 1 {
			score = 1
		}
		cands = append(cands, cand{Extracted{
			Kind: "quote", Quote: s, CompanyName: name, CompanyDomain: domain,
			SignalType: sig, Confidence: round2(score), PublishedAt: published, Extractor: "heuristic",
		}, score})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	// The same line often appears twice — as the title and as the page's
	// heading, with the site's name tacked on one of them. One is evidence;
	// the other is a copy.
	var kept []cand
	for _, c := range cands {
		n := dedupeKey(c.e.Quote)
		dup := false
		for _, k := range kept {
			if kn := dedupeKey(k.e.Quote); strings.Contains(kn, n) || strings.Contains(n, kn) {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, c)
		}
	}
	cands = kept
	if len(cands) > perPage {
		cands = cands[:perPage]
	}
	out := make([]Extracted, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.e)
	}
	_ = host
	return out
}

// dedupeKey is a quote as compared for duplicates: normalised, without the
// "..." a search engine puts on a title it cut short.
func dedupeKey(q string) string {
	n := normalise(q)
	for _, suf := range []string{"...", "…"} {
		n = strings.TrimSpace(strings.TrimSuffix(n, suf))
	}
	return n
}

// ── companies ────────────────────────────────────────────────────────────

var (
	// "Acme Pay raised…", "Nile Logistics has announced…": a capitalised name
	// right before a news verb.
	subjectVerb = regexp.MustCompile(`(?:^|[\s,;:(“"])((?:[A-Z][\w&'.-]*)(?:\s+(?:[A-Z][\w&'.-]*|of|and|&)){0,3})\s+(?:has\s+|have\s+|is\s+|was\s+|today\s+|recently\s+|officially\s+)?` +
		`(raised|raises|announced|announces|launched|launches|unveiled|unveils|introduced|introduces|hired|hires|hiring|appointed|appoints|named|names|expanded|expands|opened|opens|partnered|partners|signed|signs|secured|secures|acquired|acquires|closed|closes|released|releases|adopted|adopts|migrated|migrates|deployed|deploys|selected|selects|entered|enters)\b`)
	// "Acme Ltd", "Nile Holdings".
	legalName = regexp.MustCompile(`\b([A-Z][\w&'-]*(?:\s+[A-Z][\w&'-]*){0,3})\s+(Inc\.?|Ltd\.?|LLC|GmbH|Corp\.?|Corporation|Limited|PLC|S\.A\.E?\.?|Group|Holdings)\b`)
	// "MNT Halan, a startup based in Giza" / "Blnk is an Egyptian fintech
	// startup": a capitalised name described as a business.
	described = regexp.MustCompile(`(?:^|[\s(“"])((?:[A-Z][\w&'.-]*)(?:\s+[A-Z][\w&'.-]*){0,3})(?:,\s+|\s+is\s+|\s+was\s+)(?:a|an|the)\s+(?:[\w-]+\s+){0,4}` +
		`(?:startup|start-up|company|firm|fintech|platform|provider|bank|lender|marketplace|agency|retailer|manufacturer|operator|scale-?up|unicorn)\b`)
	// "شركة أكمي للتقنية" — up to three words after "company".
	arCompany = regexp.MustCompile(`شركة\s+([^\s،,.:؛()]+(?:\s+[^\s،,.:؛()]+){0,2})`)
)

// Words a capitalised run starts with that make it a sentence, not a name.
var notNameStart = set(`the this that these those it its we our they their he she his her in on at for from by with as
today yesterday last next this after before according however meanwhile also and but or if when while where
january february march april may june july august september october november december
monday tuesday wednesday thursday friday saturday sunday new a an one two three several many most some every
ceo cto cfo coo founder co-founder chairman president mr mrs ms dr`)

// companyIn finds the company a sentence is about. pageCo is the company whose
// own website the page is on, if any: a sentence naming no one is about it.
func companyIn(sentence string, pageCo pageCo) (name, domain string) {
	if m := legalName.FindStringSubmatch(sentence); m != nil {
		if n := cleanName(m[1] + " " + strings.TrimSuffix(m[2], ".")); n != "" {
			return n, pageCo.domainFor(n)
		}
	}
	if m := described.FindStringSubmatch(sentence); m != nil {
		if n := cleanName(m[1]); n != "" {
			return n, pageCo.domainFor(n)
		}
	}
	if m := subjectVerb.FindStringSubmatch(sentence); m != nil {
		if n := cleanName(m[1]); n != "" {
			return n, pageCo.domainFor(n)
		}
	}
	if m := arCompany.FindStringSubmatch(sentence); m != nil {
		words := strings.Fields(m[1])
		var keep []string
		for _, w := range words {
			if arStop[normalise(w)] || strings.HasPrefix(w, "ب") && len(keep) > 0 || strings.HasPrefix(w, "ل") && len(keep) > 0 {
				break
			}
			keep = append(keep, w)
		}
		if len(keep) > 0 {
			n := strings.Join(keep, " ")
			return n, pageCo.domainFor(n)
		}
	}
	if pageCo.name != "" {
		return pageCo.name, pageCo.domain
	}
	return "", ""
}

// cleanName trims a captured name to what is a name.
func cleanName(n string) string {
	n = strings.Trim(strings.TrimSpace(n), ",.;:'\"“”()")
	n = strings.TrimSuffix(n, "'s")
	n = strings.TrimSuffix(n, "’s")
	words := strings.Fields(n)
	for len(words) > 0 && notNameStart[strings.ToLower(words[0])] {
		words = words[1:]
	}
	for len(words) > 0 {
		last := strings.ToLower(words[len(words)-1])
		if last == "of" || last == "and" || last == "&" {
			words = words[:len(words)-1]
			continue
		}
		break
	}
	if len(words) == 0 || len(words) > 5 {
		return ""
	}
	n = strings.Join(words, " ")
	if len([]rune(n)) < 2 || strings.ToUpper(n) == n && len(n) <= 2 {
		return ""
	}
	return n
}

// pageCo is the company whose own site a page is on.
type pageCo struct{ name, domain string }

func (p pageCo) domainFor(name string) string {
	if p.domain == "" || name == "" {
		return ""
	}
	if CompanyKey(name, "") == CompanyKey(p.name, "") || strings.Contains(strings.ToLower(strings.ReplaceAll(name, " ", "")), labelOf(p.domain)) {
		return p.domain
	}
	return ""
}

// publishers are sites that write ABOUT companies: an article on one is never
// evidence that the publisher is the company.
var publishers = set(`wikipedia.org linkedin.com facebook.com x.com twitter.com instagram.com youtube.com tiktok.com medium.com
reddit.com quora.com github.com gitlab.com stackoverflow.com techcrunch.com reuters.com bloomberg.com forbes.com cnbc.com
bbc.com bbc.co.uk cnn.com nytimes.com wsj.com ft.com theverge.com wired.com businessinsider.com crunchbase.com
glassdoor.com indeed.com wuzzuf.net bayt.com zawya.com arabnews.com gulfnews.com khaleejtimes.com thenationalnews.com
wamda.com magnitt.com menabytes.com entrepreneur.com inc.com prnewswire.com businesswire.com globenewswire.com
yahoo.com msn.com apnews.com aljazeera.net aljazeera.com alarabiya.net youm7.com masrawy.com ahram.org.eg enterprise.press
dailynewsegypt.com egypttoday.com google.com bing.com duckduckgo.com substack.com wordpress.com blogspot.com
producthunt.com g2.com capterra.com trustpilot.com techmeme.com venturebeat.com zdnet.com theguardian.com economist.com
arabianbusiness.com skynewsarabia.com cnbcarabia.com asharq.com alarabiya.net argaam.com mubasher.info
statista.com seedtable.com tracxn.com f6s.com dealroom.co pitchbook.com cbinsights.com owler.com zoominfo.com apollo.io
rocketreach.co startupmapafrica.com ellty.com wellfound.com angel.co builtin.com clutch.co goodfirms.co jooble.org
glassdoor.co.uk linkedin.cn disruptafrica.com techcabal.com weetracker.com tekedia.com fintechgate.net fintechnews.africa
africa.businessinsider.com liners.com similarweb.com`)

// jobBoards host a company's careers page under a path: jobs.lever.co/acme.
var jobBoards = map[string]bool{"jobs.lever.co": true, "boards.greenhouse.io": true, "job-boards.greenhouse.io": true,
	"apply.workable.com": true, "jobs.ashbyhq.com": true, "careers.smartrecruiters.com": true}

// pageCompany reads the company a page belongs to, when the page is on that
// company's own site (or its board on a job site).
func pageCompany(raw, title string) pageCo {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return pageCo{}
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if jobBoards[host] {
		seg := strings.Split(strings.Trim(u.Path, "/"), "/")[0]
		if seg == "" {
			return pageCo{}
		}
		return pageCo{name: titleCase(strings.ReplaceAll(seg, "-", " "))}
	}
	domain := RegistrableDomain(host)
	if domain == "" || publishers[domain] || isPublicSector(domain) {
		return pageCo{}
	}
	label := labelOf(domain)
	if mediaLabel(label) || !ownPage(host, u.Path) {
		return pageCo{}
	}
	// The page is the company's own only when its title names the site:
	// "Careers | Nile Pay" on nilepay.com. An unknown site whose title does
	// not name it (a blog, a news site this list has never heard of) is not
	// taken for a company — a missed company costs less than a newspaper in
	// the CRM's suggestions.
	for _, part := range splitTitle(title) {
		squashed := strings.ToLower(strings.NewReplacer(" ", "", "-", "", ".", "").Replace(part))
		if len(strings.Fields(part)) <= 5 && (squashed == label || strings.HasPrefix(squashed, label) && len(squashed)-len(label) <= 4) {
			return pageCo{name: part, domain: domain}
		}
	}
	return pageCo{}
}

// ownPage reports whether a URL is the kind of page a company writes about
// itself — its home, about, careers, team or newsroom — rather than a page a
// site publishes about others. Every site puts its own name in its titles, so
// the name alone would make every data site and directory a "company".
func ownPage(host, path string) bool {
	for _, sub := range []string{"careers.", "jobs.", "about.", "team.", "newsroom.", "press."} {
		if strings.HasPrefix(host, sub) {
			return true
		}
	}
	p := strings.ToLower(strings.Trim(path, "/"))
	if p == "" || p == "en" || p == "ar" {
		return true
	}
	first := strings.Split(p, "/")[0]
	if first == "en" || first == "ar" {
		if parts := strings.Split(p, "/"); len(parts) > 1 {
			first = parts[1]
		}
	}
	for _, own := range []string{"about", "about-us", "company", "careers", "career", "jobs", "join-us", "team", "our-team",
		"newsroom", "press", "news", "blog", "contact", "contact-us", "who-we-are"} {
		if first == own {
			return true
		}
	}
	return false
}

// mediaLabel is a site name that says it is a publication.
func mediaLabel(label string) bool {
	for _, w := range []string{"news", "times", "post", "daily", "journal", "magazine", "mag", "today", "tribune", "herald",
		"gazette", "blog", "media", "press", "wire", "report", "insider", "weekly", "digest", "review", "observer", "monitor"} {
		if strings.Contains(label, w) {
			return true
		}
	}
	return false
}

func isPublicSector(domain string) bool {
	for _, suf := range []string{".gov", ".edu", ".mil", ".int"} {
		if strings.HasSuffix(domain, suf) || strings.Contains(domain, suf+".") {
			return true
		}
	}
	return strings.Contains(domain, ".ac.") || strings.HasSuffix(domain, ".news")
}

func splitTitle(t string) []string {
	f := func(r rune) bool { return r == '|' || r == '–' || r == '—' || r == '·' || r == ':' }
	var out []string
	for _, p := range strings.FieldsFunc(strings.ReplaceAll(t, " - ", " | "), f) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// RegistrableDomain is a host without its subdomains: careers.acme.co.uk →
// acme.co.uk. A two-letter country code after a short second-level label
// (co, com, org, net, ac, gov, edu) keeps three labels.
func RegistrableDomain(host string) string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	host = strings.TrimPrefix(host, "www.")
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	n := 2
	if len(parts) >= 3 && len(parts[len(parts)-1]) == 2 {
		switch parts[len(parts)-2] {
		case "co", "com", "org", "net", "ac", "gov", "edu", "sch", "ltd", "plc":
			n = 3
		}
	}
	return strings.Join(parts[len(parts)-n:], ".")
}

func labelOf(domain string) string {
	if i := strings.Index(domain, "."); i > 0 {
		return domain[:i]
	}
	return domain
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

// legalSuffixes are dropped from a name's key, so "Acme" and "Acme Inc." are
// one company.
var legalSuffixes = set(`inc ltd llc gmbh corp corporation limited plc co company group holdings sae s.a.e sa`)

// CompanyKey is what suggestions are grouped under: the domain when one is
// known, otherwise the normalised name without its legal suffix.
func CompanyKey(name, domain string) string {
	if d := RegistrableDomain(domain); d != "" {
		return d
	}
	words := strings.Fields(strings.NewReplacer(".", " ", ",", " ", "'", "", "’", "").Replace(normalise(name)))
	for len(words) > 1 && legalSuffixes[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	if len(words) > 0 && (words[0] == "شركه" || words[0] == "the") && len(words) > 1 {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// ── dates and hashes ─────────────────────────────────────────────────────

var (
	isoDate  = regexp.MustCompile(`\b(20\d\d)-(\d\d)-(\d\d)\b`)
	longDate = regexp.MustCompile(`\b(January|February|March|April|May|June|July|August|September|October|November|December|Jan|Feb|Mar|Apr|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)\.?\s+(\d{1,2}),?\s+(20\d\d)\b`)
	dayFirst = regexp.MustCompile(`\b(\d{1,2})\s+(January|February|March|April|May|June|July|August|September|October|November|December)\s+(20\d\d)\b`)
)

// publishedDate is the first plausible date near the top of a page: the
// byline, usually. Only a date in the past five years counts — a copyright
// line's year or a date in the future is not when a page was published.
func publishedDate(text string) *time.Time {
	head := text
	if r := []rune(head); len(r) > 2000 {
		head = string(r[:2000])
	}
	var found []time.Time
	if m := isoDate.FindStringSubmatch(head); m != nil {
		if t, err := time.Parse("2006-01-02", m[0]); err == nil {
			found = append(found, t)
		}
	}
	if m := longDate.FindStringSubmatch(head); m != nil {
		mon := m[1]
		if len(mon) > 3 {
			mon = mon[:3]
		}
		if t, err := time.Parse("Jan 2 2006", mon+" "+m[2]+" "+m[3]); err == nil {
			found = append(found, t)
		}
	}
	if m := dayFirst.FindStringSubmatch(head); m != nil {
		if t, err := time.Parse("2 January 2006", m[0]); err == nil {
			found = append(found, t)
		}
	}
	now := time.Now().UTC()
	for _, t := range found {
		if t.Before(now.Add(24*time.Hour)) && t.After(now.AddDate(-5, 0, 0)) {
			t := t.UTC()
			return &t
		}
	}
	return nil
}

// evidenceHash identifies a quote from a page: the same sentence from the same
// URL in one run is stored once.
func evidenceHash(quote, sourceURL string) string {
	h := sha256.Sum256([]byte(normalise(quote) + "|" + strings.TrimSpace(sourceURL)))
	return hex.EncodeToString(h[:16])
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
