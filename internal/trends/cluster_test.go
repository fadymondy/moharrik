package trends

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

/*
Clustering against REAL headlines (MH-385).

testdata/*.xml are the feeds exactly as they answered on 2026-09-23: BBC
Business / World / Arabic, The Guardian: Business, NPR: Business, Hacker News,
Daily Coffee News, DW عربية, CNN بالعربية and RT Arabic. Every assertion below
names two headlines as the outlets printed them; nothing is paraphrased.
*/

type corpus struct {
	docs   []*Doc
	titles map[string]string // doc id -> "file: title"
	items  map[string]Item
}

func loadCorpus(t *testing.T, files ...string) corpus {
	t.Helper()
	c := corpus{titles: map[string]string{}, items: map[string]Item{}}
	n := 0
	for _, f := range files {
		fh, err := os.Open(filepath.Join("testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		feed, err := Parse(fh)
		fh.Close()
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, it := range feed.Items {
			n++
			id := strconv.Itoa(n)
			c.docs = append(c.docs, NewDoc(id, "", it.Title, it.URL, it.PublishedAt))
			c.titles[id] = f + ": " + it.Title
			c.items[id] = it
		}
	}
	return c
}

// find is the one doc whose title contains every fragment.
func (c corpus) find(t *testing.T, fragments ...string) string {
	t.Helper()
	var hits []string
	for id, title := range c.titles {
		ok := true
		for _, f := range fragments {
			if !strings.Contains(title, f) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, id)
		}
	}
	if len(hits) == 0 {
		t.Fatalf("no headline contains %q", fragments)
	}
	return hits[0]
}

func assertTogether(t *testing.T, c corpus, got map[string]string, pairs [][2][]string) {
	t.Helper()
	for _, p := range pairs {
		a, b := c.find(t, p[0]...), c.find(t, p[1]...)
		if got[a] != got[b] {
			t.Errorf("should be one topic:\n  %s\n  %s", c.titles[a], c.titles[b])
		}
	}
}

func assertApart(t *testing.T, c corpus, got map[string]string, pairs [][2][]string) {
	t.Helper()
	for _, p := range pairs {
		a, b := c.find(t, p[0]...), c.find(t, p[1]...)
		if got[a] == got[b] {
			t.Errorf("should be different topics:\n  %s\n  %s", c.titles[a], c.titles[b])
		}
	}
}

func s(v ...string) []string { return v }

func TestClusterRealEnglishHeadlines(t *testing.T) {
	c := loadCorpus(t, "bbc-business.xml", "bbc-world.xml", "guardian-business.xml", "npr.xml", "hn.xml", "dailycoffeenews.xml")
	got := Assign(c.docs)

	assertTogether(t, c, got, [][2][]string{
		// Two outlets, different wording, one story: the rare names carry it.
		{s("guardian", "Jim Ratcliffe halts production at Hull"), s("bbc-business", "Sir Jim Ratcliffe suspends production")},
		{s("guardian", "UK borrows £18bn in August"), s("bbc-business", "Unexpected UK borrowing surge")},
		{s("guardian", "EU removes two oligarchs"), s("bbc-world", "Ukraine anger as EU removes Russian oligarchs")},
		{s("npr", "Paramount makes concessions"), s("bbc-business", "Paramount settles lawsuit")},
		{s("npr", "banning CNN, MS NOW and Politico"), s("npr", "CNN, MS NOW and Politico sue Trump")},
	})
	assertApart(t, c, got, [][2][]string{
		{s("bbc-business", "Vet prescription fees capped"), s("bbc-business", "Sir Jim Ratcliffe suspends production")},
		{s("guardian", "UK borrows £18bn"), s("guardian", "Jim Ratcliffe halts production")},
		{s("npr", "Paramount makes concessions"), s("npr", "CNN, MS NOW and Politico sue Trump")},
		// Two shared names and nothing else: "Trump" and "AI".
		{s("bbc-business", "Will Trump's AI rebrand"), s("bbc-business", "US and China discuss AI safety plan")},
	})
}

func TestClusterRealArabicHeadlines(t *testing.T) {
	c := loadCorpus(t, "bbc-arabic.xml", "cnn-ar.xml", "dw-ar.xml", "rt-ar.xml")
	got := Assign(c.docs)

	assertTogether(t, c, got, [][2][]string{
		// The school shooting in Turkey: "مدرسة تركية" / "مدرسة في تركيا", "نار" / "النار".
		{s("bbc-arabic", "إصابة 11 شخصاً في حادث إطلاق نار"), s("cnn-ar", "طالب يفتح النار قرب مدرسة في تركيا")},
		// Merz: "الألمان" / "الألمان", "استقالة المستشار ميرتس" in both.
		{s("rt-ar", "غالبية الألمان تؤيد استقالة المستشار ميرتس"), s("dw-ar", "أكثر من نصف الألمان يؤيدون استقالة")},
		// The 25 tankers: "بشرائها" / "شرائها", "العراقي" / "عراقية".
		{s("rt-ar", "السعودية ترد على إدعاءات عراقية"), s("cnn-ar", "السعودية تنفي تصريح وزير النفط العراقي")},
		// Bab al-Mandab: "الحوثيون" / "الحوثيين".
		{s("bbc-arabic", "باب المندب: كيف وصل الحوثيون"), s("rt-ar", "لماذا يصعب إخراج الحوثيين")},
		// Trump's threat at the UN: "بـ\"إبادة\" إيران" / "اتفاق أو إبادة".
		{s("dw-ar", "ترامب يتوعد أمام الأمم المتحدة"), s("cnn-ar", "إما اتفاق أو إبادة")},
	})
	assertApart(t, c, got, [][2][]string{
		// Stock phrases ("الاتحاد الأوروبي", "روسيا") are not a story.
		{s("rt-ar", "الخارجية الروسية: الاتحاد الأوروبي"), s("rt-ar", "الاتحاد الأوروبي يرفع العقوبات عن الروسيين")},
		// Two pieces that both mention artificial intelligence.
		{s("bbc-arabic", "كيف يساعد الذكاء الاصطناعي"), s("bbc-arabic", "كيف يُستخدم الذكاء الاصطناعي في صراعات")},
		{s("bbc-arabic", "إصابة 11 شخصاً"), s("bbc-arabic", "باب المندب")},
	})
}

func TestArabicStemMeetsItsVariants(t *testing.T) {
	groups := [][]string{
		{"إيران", "إيرانيين", "الإيراني", "وإيران"},
		{"تركيا", "تركية", "التركي"},
		{"إبادة", "بإبادتها", "الإبادة"},
		{"مدرسة", "المدرسة", "للمدرسة"},
		{"الحوثيون", "الحوثيين", "الحوثي"},
		{"أمريكا", "الأمريكي", "أمريكيين", "الأمريكيين"},
	}
	for _, g := range groups {
		want := stem(normaliseArabic(g[0]))
		for _, w := range g[1:] {
			if got := stem(normaliseArabic(w)); got != want {
				t.Errorf("%s -> %q, but %s -> %q", g[0], want, w, got)
			}
		}
	}
	// Function words never become tokens.
	if toks := Tokens("ماذا قال ترامب بشأن إيران؟"); strings.Join(toks, " ") != "ترامب اير" {
		t.Errorf("tokens = %q", toks)
	}
}

func TestCanonicalURLDedupesTheBBC(t *testing.T) {
	cases := map[string]string{
		"https://www.bbc.co.uk/news/articles/c6vgyq598k9po?at_medium=RSS&at_campaign=rss": "bbc.com/news/articles/c6vgyq598k9po",
		"https://www.bbc.com/news/articles/c6vgyq598k9po#0":                               "bbc.com/news/articles/c6vgyq598k9po",
		"https://example.test/post/?utm_source=x&id=7&fbclid=y":                           "example.test/post?id=7",
		"https://news.google.com/rss/articles/abc":                                        "",
		"": "",
	}
	for in, want := range cases {
		if got := CanonicalURL(in); got != want {
			t.Errorf("CanonicalURL(%q) = %q, want %q", in, got, want)
		}
	}

	// The captured BBC business feed lists three stories twice under different
	// guids ("…#0", "…#1"). The identity index would keep all six rows; the
	// canonical URL keeps three.
	fh, _ := os.Open("testdata/bbc-business.xml")
	defer fh.Close()
	feed, err := Parse(fh)
	if err != nil {
		t.Fatal(err)
	}
	guids, canon := map[string]bool{}, map[string]int{}
	for _, it := range feed.Items {
		guids[it.ExternalID] = true
		canon[CanonicalURL(it.URL)]++
	}
	dups := 0
	for _, n := range canon {
		if n > 1 {
			dups++
		}
	}
	if len(guids) != len(feed.Items) || dups != 3 {
		t.Fatalf("items %d, distinct guids %d, URLs carried twice %d (want every guid distinct and 3 duplicate URLs)",
			len(feed.Items), len(guids), dups)
	}
}

// Anti-chaining: a doc has to match a third of a topic to join it.
func TestAssignDoesNotChain(t *testing.T) {
	docs := []*Doc{
		NewDoc("1", "", "Nvidia starts shipping Blackwell chips to Microsoft data centres", "", at(0)),
		NewDoc("2", "", "Microsoft gets its first Blackwell racks from Nvidia", "", at(1)),
		NewDoc("3", "", "Why the Blackwell delay at Nvidia mattered to Microsoft", "", at(2)),
		// Shares only "Microsoft" with the three above.
		NewDoc("4", "", "Microsoft Teams outage hits users across Europe", "", at(3)),
	}
	got := Assign(docs)
	if got["1"] != got["2"] || got["2"] != got["3"] {
		t.Fatalf("the Blackwell story split: %v", got)
	}
	if got["4"] == got["1"] {
		t.Fatalf("the Teams outage chained into the Blackwell story: %v", got)
	}
}
