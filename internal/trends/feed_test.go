package trends

import (
	"strings"
	"testing"
	"time"
)

const rssDoc = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>Example Engineering</title>
    <item>
      <title>Postgres 18 is out</title>
      <link>https://example.test/pg18</link>
      <guid isPermaLink="false">tag:example,2026:pg18</guid>
      <description>&lt;p&gt;The &lt;b&gt;release&lt;/b&gt; ships async I/O.&lt;/p&gt;</description>
      <pubDate>Mon, 15 Sep 2026 09:30:00 +0000</pubDate>
      <dc:creator>A. Maintainer</dc:creator>
    </item>
    <item>
      <title>A post with no date or guid</title>
      <link>https://example.test/other</link>
    </item>
  </channel>
</rss>`

const atomDoc = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example Atom</title>
  <entry>
    <title>Go 1.27 released</title>
    <link rel="alternate" href="https://example.test/go127"/>
    <id>urn:uuid:go127</id>
    <summary>Generics get faster.</summary>
    <published>2026-09-10T12:00:00Z</published>
    <author><name>The Go Team</name></author>
  </entry>
</feed>`

func TestParseRSS(t *testing.T) {
	f, err := Parse(strings.NewReader(rssDoc))
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Example Engineering" || f.Kind != "rss" {
		t.Fatalf("feed = %q / %q", f.Title, f.Kind)
	}
	if len(f.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(f.Items))
	}
	one := f.Items[0]
	if one.ExternalID != "tag:example,2026:pg18" {
		t.Errorf("external id = %q — the guid is what makes a re-poll the same row", one.ExternalID)
	}
	if one.Summary != "The release ships async I/O." {
		t.Errorf("summary = %q — markup and entities should be gone", one.Summary)
	}
	if one.Author != "A. Maintainer" {
		t.Errorf("author = %q (dc:creator)", one.Author)
	}
	if !one.PublishedAt.Equal(time.Date(2026, 9, 15, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("published = %s", one.PublishedAt)
	}
	// An entry with no guid and no date is still collected: half the web is like this.
	two := f.Items[1]
	if two.ExternalID != "https://example.test/other" {
		t.Errorf("fallback id = %q, want the link", two.ExternalID)
	}
	if two.PublishedAt.IsZero() {
		t.Error("an undated entry must not sort to 1970; it is dated now")
	}
}

func TestParseAtom(t *testing.T) {
	f, err := Parse(strings.NewReader(atomDoc))
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind != "atom" || len(f.Items) != 1 {
		t.Fatalf("kind = %q, items = %d", f.Kind, len(f.Items))
	}
	it := f.Items[0]
	if it.URL != "https://example.test/go127" {
		t.Errorf("url = %q — atom puts it in link/@href", it.URL)
	}
	if it.Author != "The Go Team" {
		t.Errorf("author = %q — atom nests it in author/name", it.Author)
	}
}

func TestParseRejectsNonFeeds(t *testing.T) {
	for name, doc := range map[string]string{
		"html":       "<!doctype html><html><body>hello</body></html>",
		"empty feed": `<rss version="2.0"><channel><title>Nothing</title></channel></rss>`,
		"garbage":    "not xml at all",
	} {
		if _, err := Parse(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: parsed as a feed, want an error", name)
		}
	}
}

func TestMatchTopics(t *testing.T) {
	topics := []Topic{
		{ID: "t1", Name: "Postgres", Keywords: []string{"postgres", "pgsql"}},
		{ID: "t2", Name: "Go"}, // no keywords: matches its own name
		{ID: "t3", Name: "Kubernetes", Keywords: []string{"k8s"}},
	}

	got := Match(topics, Item{Title: "Postgres 18 is out", Summary: "async I/O"})
	if len(got) != 1 || got[0] != "t1" {
		t.Errorf("postgres item matched %v, want [t1]", got)
	}

	// Case-insensitive, and the summary counts too.
	got = Match(topics, Item{Title: "Release notes", Summary: "Now built with GO and pgSQL"})
	if len(got) != 2 {
		t.Errorf("matched %v, want both t1 and t2", got)
	}

	if got := Match(topics, Item{Title: "Something else entirely"}); len(got) != 0 {
		t.Errorf("matched %v, want nothing", got)
	}
}

func TestPGTextArrayRoundTrip(t *testing.T) {
	// The array literal the driver hands back, including a value with a comma
	// and one with a quote — the two that break a naive split.
	var a pgTextArray
	if err := a.Scan(`{plain,"has, comma","say \"hi\""}`); err != nil {
		t.Fatal(err)
	}
	want := []string{"plain", "has, comma", `say "hi"`}
	if len(a) != len(want) {
		t.Fatalf("got %q, want %q", a, want)
	}
	for i := range want {
		if a[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, a[i], want[i])
		}
	}

	var empty pgTextArray
	if err := empty.Scan("{}"); err != nil || len(empty) != 0 {
		t.Errorf("empty array = %q, err %v", empty, err)
	}

	if got := pgArray([]string{`a"b`, "c,d"}); got != `{"a\"b","c,d"}` {
		t.Errorf("pgArray = %s", got)
	}
	if got := pgArray(nil); got != "{}" {
		t.Errorf("pgArray(nil) = %s", got)
	}
}
