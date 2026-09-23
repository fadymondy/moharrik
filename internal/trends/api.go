package trends

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

/*
API sources: where there is no feed worth reading (MH-387).

Ported from fadymondy.com internal/collector/api.go (the hn and devto kinds).
Both are documented public JSON APIs; neither needs a key.

	hn     Hacker News Firebase API (github.com/HackerNews/API). Public, no key,
	       no stated rate limit. One list read, then at most `limit` (default
	       30) item reads, five at a time. The API gives what the RSS mirror
	       does not: each story's position on the front page and its points,
	       which is the velocity rank.go scores.
	devto  Forem API v1 (developers.forem.com). Public reads need no key;
	       rate-limited per IP (429 on excess). One request per collection.

A source's `provider` says which of these (or "feed") collects it, and its
`params` carry the list/tag. Browser-tier sources (Google Trends Explore, X
trends) wait for the automation plugins and are not built here.
*/

// Providers a source can be collected by.
const (
	ProviderFeed  = "feed"
	ProviderHN    = "hn"
	ProviderDevto = "devto"
)

// API base URLs; tests point them at fake servers.
var (
	HNBase    = "https://hacker-news.firebaseio.com/v0"
	DevtoBase = "https://dev.to/api"
)

// maxAPIBody caps one JSON response.
const maxAPIBody = 4 << 20

func getJSON(ctx context.Context, rawURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s answered %d", hostOf(rawURL), res.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxAPIBody)).Decode(dst); err != nil {
		return fmt.Errorf("could not read the answer from %s", hostOf(rawURL))
	}
	return nil
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return raw
}

func param(params map[string]string, key, def string) string {
	if v := strings.TrimSpace(params[key]); v != "" {
		return v
	}
	return def
}

func atoi(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return def
}

// ---- Hacker News ------------------------------------------------------------

// HNItem is the Firebase item shape.
type HNItem struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"`
	By          string `json:"by"`
	Time        int64  `json:"time"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Text        string `json:"text"`
	Score       int    `json:"score"`
	Descendants int    `json:"descendants"`
	Dead        bool   `json:"dead"`
	Deleted     bool   `json:"deleted"`
}

// HNLists are the lists the API offers that are worth watching.
var HNLists = map[string]bool{"topstories": true, "beststories": true, "newstories": true, "askstories": true, "showstories": true}

// HNToItem normalises one story; ok is false for anything that is not a live story.
func HNToItem(h HNItem, rank int) (Item, bool) {
	if h.Dead || h.Deleted || h.Type != "story" || strings.TrimSpace(h.Title) == "" {
		return Item{}, false
	}
	discuss := "https://news.ycombinator.com/item?id=" + strconv.FormatInt(h.ID, 10)
	link := firstNonEmpty(h.URL, discuss)
	return Item{
		ExternalID: "hn:" + strconv.FormatInt(h.ID, 10), Title: clean(h.Title), URL: link, Author: h.By,
		Summary: trimTo(clean(h.Text), 1000), PublishedAt: time.Unix(h.Time, 0).UTC(),
		Rank: rank, Points: h.Score, Comments: h.Descendants,
	}, true
}

// CollectHN reads one Hacker News list.
func CollectHN(ctx context.Context, params map[string]string) ([]Item, error) {
	list := param(params, "list", "topstories")
	if !HNLists[list] {
		return nil, fmt.Errorf("unknown Hacker News list %q", list)
	}
	root := strings.TrimRight(HNBase, "/")
	var ids []int64
	if err := getJSON(ctx, root+"/"+list+".json", &ids); err != nil {
		return nil, err
	}
	limit := atoi(params["limit"], 30)
	if limit > 60 {
		limit = 60
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	items := make([]*Item, len(ids))
	errs := make([]error, len(ids))
	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var h HNItem
			if err := getJSON(ctx, fmt.Sprintf("%s/item/%d.json", root, id), &h); err != nil {
				errs[i] = err
				return
			}
			if it, ok := HNToItem(h, i+1); ok {
				items[i] = &it
			}
		}(i, id)
	}
	wg.Wait()
	var out []Item
	var firstErr error
	for i, it := range items {
		if it != nil {
			out = append(out, *it)
		} else if errs[i] != nil && firstErr == nil {
			firstErr = errs[i]
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Hacker News returned no stories")
	}
	return out, nil
}

// ---- dev.to (Forem) ---------------------------------------------------------

// DevtoArticle is the Forem article list shape.
type DevtoArticle struct {
	ID                   int64  `json:"id"`
	Title                string `json:"title"`
	Description          string `json:"description"`
	URL                  string `json:"url"`
	PublishedAt          string `json:"published_at"`
	PublicReactionsCount int    `json:"public_reactions_count"`
	CommentsCount        int    `json:"comments_count"`
	User                 struct {
		Name string `json:"name"`
	} `json:"user"`
}

// DevtoToItems normalises a Forem article list.
func DevtoToItems(list []DevtoArticle) []Item {
	var out []Item
	for i, d := range list {
		if strings.TrimSpace(d.Title) == "" {
			continue
		}
		out = append(out, Item{
			ExternalID: "devto:" + strconv.FormatInt(d.ID, 10), Title: clean(d.Title), URL: d.URL,
			Summary: trimTo(clean(d.Description), 1000), Author: d.User.Name, PublishedAt: parseTime(d.PublishedAt),
			Rank: i + 1, Points: d.PublicReactionsCount, Comments: d.CommentsCount,
		})
	}
	return out
}

// CollectDevto reads one dev.to article list: by tag, or the top of a period.
func CollectDevto(ctx context.Context, params map[string]string) ([]Item, error) {
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(min(atoi(params["limit"], 30), 60)))
	if tag := strings.TrimSpace(params["tag"]); tag != "" {
		q.Set("tag", tag)
	}
	if top := strings.TrimSpace(params["top"]); top != "" {
		q.Set("top", top)
	}
	var list []DevtoArticle
	if err := getJSON(ctx, strings.TrimRight(DevtoBase, "/")+"/articles?"+q.Encode(), &list); err != nil {
		return nil, err
	}
	items := DevtoToItems(list)
	if len(items) == 0 {
		return nil, fmt.Errorf("DEV returned no articles")
	}
	return items, nil
}

// APIURL is the address an API source is recorded under: what the page shows,
// and what "one source per URL" dedupes on.
func APIURL(provider string, params map[string]string) string {
	switch provider {
	case ProviderHN:
		return strings.TrimRight(HNBase, "/") + "/" + param(params, "list", "topstories") + ".json"
	case ProviderDevto:
		q := url.Values{}
		if tag := strings.TrimSpace(params["tag"]); tag != "" {
			q.Set("tag", tag)
		}
		if top := strings.TrimSpace(params["top"]); top != "" {
			q.Set("top", top)
		}
		u := strings.TrimRight(DevtoBase, "/") + "/articles"
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		return u
	}
	return ""
}
