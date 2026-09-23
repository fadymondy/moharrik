package research

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

/*
The two outside services a research run uses (MH-227).

SEARCH is SearXNG: our own metasearch instance, so a scheduled search is not
rate-limited or served a bot challenge the way a public one would be.

READING is Firecrawl: it runs a real browser, so a page whose text arrives from
JavaScript comes back as text rather than as an empty shell. A plain HTTP
fetcher "succeeds" on those pages and stores the scaffolding — which is the
worst failure available here, because a summary built on it looks sourced and
says nothing.

Both are unset-means-skipped rather than unset-means-error. A workspace with
search but no reader still gets a useful run: titles, snippets and links, with
`fetched=false` on every source and the gap visible on the page.
*/

// ErrNoSearch is returned when SEARXNG_URL is not configured. Without search
// there is no run at all — unlike the reader, which degrades.
var ErrNoSearch = errors.New("search is not configured for this deployment (SEARXNG_URL)")

const (
	searchTimeout = 25 * time.Second
	// A page takes a real browser to render, so this is generous. The run's own
	// deadline is what actually bounds the whole thing.
	readTimeout = 60 * time.Second
)

// Finding is one search result, before anything has been read.
type Finding struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Searcher queries SearXNG.
type Searcher struct {
	BaseURL string
	Client  *http.Client
}

// NewSearcher reads SEARXNG_URL. A nil return means the tier is unconfigured.
func NewSearcher() *Searcher {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SEARXNG_URL")), "/")
	if base == "" {
		return nil
	}
	return &Searcher{BaseURL: base, Client: &http.Client{Timeout: searchTimeout}}
}

// Search plans the question (query.go) and returns up to limit results.
func (s *Searcher) Search(ctx context.Context, question string, limit int) ([]Finding, error) {
	if strings.TrimSpace(question) == "" {
		return nil, errors.New("a research run needs something to search for")
	}
	return s.SearchPlan(ctx, PlanQuery(question), limit)
}

// SearchPlan searches the plan's variants in order, merges them by rank
// (reciprocal rank fusion: a page high in two variants beats one high in one),
// drops what is not about the question, and returns up to limit.
//
// The next variant is searched only while the relevant results are fewer than
// limit. Each variant is a query to every engine the instance has, and a
// metasearch instance that asks too often is served CAPTCHAs and suspended —
// so a question the first phrasing answers costs one query, not three.
//
// One variant failing is not a failed run; all of them failing is.
func (s *Searcher) SearchPlan(ctx context.Context, p Plan, limit int) ([]Finding, error) {
	if s == nil {
		return nil, ErrNoSearch
	}
	if len(p.Queries) == 0 {
		return nil, errors.New("a research run needs something to search for")
	}

	const k = 60.0 // the usual RRF constant: rank 1 and rank 5 differ, rank 40 and 45 barely
	type scored struct {
		f     Finding
		score float64
	}
	byURL := map[string]*scored{}
	var order []*scored
	var firstErr error
	answered := 0
	ranked := func() (relevant, all []Finding) {
		sorted := append([]*scored(nil), order...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].score > sorted[j].score })
		for _, sc := range sorted {
			all = append(all, sc.f)
			if p.Relevant(sc.f.Title, sc.f.Snippet) {
				relevant = append(relevant, sc.f)
			}
		}
		return relevant, all
	}

	for _, q := range p.Queries {
		list, err := s.query(ctx, q, p.Language)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}
		answered++
		for rank, f := range list {
			sc := byURL[f.URL]
			if sc == nil {
				sc = &scored{f: f}
				byURL[f.URL] = sc
				order = append(order, sc)
			}
			sc.score += 1 / (k + float64(rank+1))
		}
		if relevant, _ := ranked(); len(relevant) >= limit {
			break
		}
	}
	if answered == 0 {
		return nil, firstErr
	}

	relevant, all := ranked()
	// If NOTHING reads as relevant, the pages probably phrase it differently
	// from the question; the merged list is still the best there is, and
	// "nothing found" would be the less honest answer.
	if len(relevant) == 0 {
		relevant = all
	}
	if len(relevant) > limit {
		relevant = relevant[:limit]
	}
	return relevant, nil
}

// query runs one SearXNG query and returns its results in rank order.
//
// The instance must list "json" under search.formats, or it answers 403 to a
// format=json request while the same query works in a browser — a confusing
// failure worth naming in the error.
func (s *Searcher) query(ctx context.Context, query, language string) ([]Finding, error) {
	v := url.Values{"format": {"json"}, "q": {query}}
	if language != "" {
		v.Set("language", language)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/search?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}

	res, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("the search service did not answer: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusForbidden {
		return nil, errors.New("the search service refused a JSON request — add \"json\" to search.formats in its settings.yml")
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the search service answered %d", res.StatusCode)
	}

	var doc struct {
		Results []struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("the search service sent something unreadable: %w", err)
	}

	out := make([]Finding, 0, len(doc.Results))
	seen := map[string]bool{}
	for _, r := range doc.Results {
		link := strings.TrimSpace(r.URL)
		// Metasearch merges several engines, so the same page arrives more than
		// once. Reading it twice costs a fetch and pads the source list.
		if link == "" || seen[link] {
			continue
		}
		if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
			continue
		}
		seen[link] = true
		out = append(out, Finding{
			URL:     link,
			Title:   strings.TrimSpace(r.Title),
			Snippet: strings.TrimSpace(r.Content),
		})
	}
	return out, nil
}

// Reader turns a URL into markdown, through Firecrawl.
type Reader struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

// NewReader reads FIRECRAWL_URL / FIRECRAWL_API_KEY. A nil return means the
// tier is skipped and a run keeps search results only.
func NewReader() *Reader {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("FIRECRAWL_URL")), "/")
	key := strings.TrimSpace(os.Getenv("FIRECRAWL_API_KEY"))
	if base == "" {
		if key == "" {
			return nil
		}
		base = "https://api.firecrawl.dev"
	}
	return &Reader{BaseURL: base, APIKey: key, Client: &http.Client{Timeout: readTimeout}}
}

// Read returns the page as markdown.
func (r *Reader) Read(ctx context.Context, pageURL string) (string, error) {
	if r == nil {
		return "", errors.New("no reader is configured")
	}

	body, err := json.Marshal(map[string]any{
		"url":     pageURL,
		"formats": []string{"markdown"},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+"/v1/scrape", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}

	res, err := r.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("the reader did not answer: %w", err)
	}
	defer res.Body.Close()

	// Named explicitly, because a stale key is the failure this deployment has
	// actually had (MH-315) and "401" alone sends people to the wrong place.
	if res.StatusCode == http.StatusUnauthorized {
		return "", errors.New("the reader rejected our key (FIRECRAWL_API_KEY)")
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the reader answered %d", res.StatusCode)
	}

	var doc struct {
		Success bool `json:"success"`
		Data    struct {
			Markdown string `json:"markdown"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("the reader sent something unreadable: %w", err)
	}
	if !doc.Success && doc.Error != "" {
		return "", errors.New(doc.Error)
	}

	md := strings.TrimSpace(doc.Data.Markdown)
	if md == "" {
		return "", errors.New("the page had no readable text")
	}
	return md, nil
}
