// Package trends watches feeds for a workspace: the sources it polls, the
// topics it cares about, and the items it collects from them.
//
// Same rules as every other module here: workspace-scoped handlers run on the
// app (RLS) pool AND name workspace_id in every predicate. The collector is the
// one part with no request behind it, so it scopes itself with
// tenant.WithWorkspace, one workspace at a time (collector.go).
package trends

import (
	"encoding/xml"
	"errors"
	"html"
	"io"
	"regexp"
	"strings"
	"time"
)

// Item is one entry, after RSS and Atom have been flattened into the same shape.
type Item struct {
	ExternalID  string
	Title       string
	URL         string
	Summary     string
	Author      string
	PublishedAt time.Time
}

// Feed is a parsed document: the feed's own title, and its entries.
type Feed struct {
	Title string
	Kind  string // rss | atom
	Items []Item
}

// The two shapes, read by one parser. RSS 2.0 and Atom disagree about almost
// every name, and a feed in the wild is often a little of both, so each field
// takes whichever spelling turned up.
// encoding/xml refuses a struct with two fields mapped to the same element, so
// each element name appears exactly once and the RSS/Atom difference is handled
// inside the type: RSS writes <link>url</link>, Atom writes <link href="url"/>,
// and both land in xmlLink. Same for <author>, which is a string in RSS and a
// nested <name> in Atom.
type xmlFeed struct {
	Channel xmlChannel `xml:"channel"`
	Title   string     `xml:"title"`
	Entries []xmlEntry `xml:"entry"`
}

type xmlChannel struct {
	Title string     `xml:"title"`
	Items []xmlEntry `xml:"item"`
}

type xmlEntry struct {
	Title       string    `xml:"title"`
	Links       []xmlLink `xml:"link"`
	GUID        string    `xml:"guid"`
	ID          string    `xml:"id"`
	Description string    `xml:"description"`
	Summary     string    `xml:"summary"`
	Content     string    `xml:"content"`
	Encoded     string    `xml:"encoded"` // content:encoded
	PubDate     string    `xml:"pubDate"`
	Published   string    `xml:"published"`
	Updated     string    `xml:"updated"`
	Date        string    `xml:"date"` // dc:date
	Creator     string    `xml:"creator"`
	Author      xmlAuthor `xml:"author"`
}

type xmlLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Text string `xml:",chardata"`
}

type xmlAuthor struct {
	Name string `xml:"name"`
	Text string `xml:",chardata"`
}

// href returns the entry's canonical link, whichever way the feed spelled it.
func (e xmlEntry) href() string {
	for _, l := range e.Links {
		if v := strings.TrimSpace(l.Text); v != "" {
			return v // RSS
		}
	}
	for _, l := range e.Links {
		if l.Rel == "" || l.Rel == "alternate" {
			if v := strings.TrimSpace(l.Href); v != "" {
				return v // Atom
			}
		}
	}
	return ""
}

var ErrNotAFeed = errors.New("this does not look like an RSS or Atom feed")

// maxFeed caps what is read from a URL. Generous for a real feed, small enough
// that a misbehaving (or hostile) endpoint cannot stream forever.
const maxFeed = 4 << 20 // 4 MiB

var tagRE = regexp.MustCompile(`<[^>]*>`)

// Parse reads an RSS or Atom document. It is deliberately forgiving: a feed
// that is missing dates, ids or summaries still yields items, because the
// alternative is collecting nothing from half the web.
func Parse(r io.Reader) (*Feed, error) {
	var f xmlFeed
	dec := xml.NewDecoder(io.LimitReader(r, maxFeed))
	// Feeds are full of encodings Go does not ship with (windows-1256 is common
	// in Arabic feeds). Rather than fail, hand those bytes through as-is: the
	// text may be slightly wrong, but the entry is still collected.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	dec.Strict = false
	if err := dec.Decode(&f); err != nil {
		return nil, ErrNotAFeed
	}

	out := &Feed{Title: strings.TrimSpace(firstNonEmpty(f.Channel.Title, f.Title))}
	entries := f.Channel.Items
	out.Kind = "rss"
	if len(entries) == 0 && len(f.Entries) > 0 {
		entries = f.Entries
		out.Kind = "atom"
	}
	if len(entries) == 0 {
		return nil, ErrNotAFeed
	}

	for _, e := range entries {
		link := e.href()
		title := clean(firstNonEmpty(e.Title, link))
		if title == "" {
			continue // an entry with neither a title nor a link is not an entry
		}
		id := strings.TrimSpace(firstNonEmpty(e.GUID, e.ID, link, title))
		item := Item{
			ExternalID:  id,
			Title:       title,
			URL:         link,
			Summary:     trimTo(clean(firstNonEmpty(e.Summary, e.Description, e.Encoded, e.Content)), 1000),
			Author:      clean(firstNonEmpty(e.Author.Name, e.Creator, e.Author.Text)),
			PublishedAt: parseTime(e.PubDate, e.Published, e.Updated, e.Date),
		}
		out.Items = append(out.Items, item)
	}
	if len(out.Items) == 0 {
		return nil, ErrNotAFeed
	}
	return out, nil
}

// Match returns the ids of the topics an item belongs to: any keyword appearing
// in its title or summary, case-insensitively. A topic with no keywords matches
// on its own name, so a topic is useful the moment it is created.
func Match(topics []Topic, item Item) []string {
	hay := strings.ToLower(item.Title + " " + item.Summary)
	var ids []string
	for _, t := range topics {
		words := t.Keywords
		if len(words) == 0 {
			words = []string{t.Name}
		}
		for _, w := range words {
			w = strings.ToLower(strings.TrimSpace(w))
			if w != "" && strings.Contains(hay, w) {
				ids = append(ids, t.ID)
				break
			}
		}
	}
	return ids
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// clean strips markup and entities: a feed summary is usually HTML, and this is
// rendered as text.
func clean(s string) string {
	s = tagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func trimTo(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// The layouts feeds actually use, in the order they are worth trying.
var layouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC3339, time.RFC3339Nano, time.RFC822Z, time.RFC822,
	"Mon, 02 Jan 2006 15:04:05 -0700", "Mon, 02 Jan 2006 15:04:05 MST", "2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05", "2006-01-02",
}

// parseTime takes the first value that parses. An entry with no usable date is
// dated now — it was just collected, and sorting it to 1970 would bury it.
func parseTime(vals ...string) time.Time {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		for _, l := range layouts {
			if t, err := time.Parse(l, v); err == nil {
				return t
			}
		}
	}
	return time.Now()
}
