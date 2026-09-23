package googletest

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

/*
Search Console's URL Inspection and Sitemaps (MH-446):

	POST /searchconsole/v1/urlInspection/index:inspect  {inspectionUrl, siteUrl, languageCode}
	GET  /webmasters/v3/sites/{site}/sitemaps
	PUT  /webmasters/v3/sites/{site}/sitemaps/{feedpath}   (needs the full webmasters scope)

Inspection answers by path: a path containing "new" is unknown to Google, one
containing "blocked" is disallowed by robots.txt, anything else is indexed. A
URL outside the property is refused as Google refuses it. Sitemap counts are
strings, as Google sends them.
*/

func (s *Server) gscTools(w http.ResponseWriter, r *http.Request, p string) bool {
	switch {
	case p == "/searchconsole/v1/urlInspection/index:inspect" && r.Method == http.MethodPost:
		s.inspect(w, r)
		return true
	case strings.HasPrefix(p, "/webmasters/v3/sites/") && strings.Contains(p, "/sitemaps"):
		rest := strings.TrimPrefix(p, "/webmasters/v3/sites/")
		site, tail, _ := strings.Cut(rest, "/sitemaps")
		site = unescapeSegment(site)
		feed := unescapeSegment(strings.TrimPrefix(tail, "/"))
		switch {
		case r.Method == http.MethodGet && feed == "":
			s.sitemaps(w, r, site)
		case r.Method == http.MethodPut && feed != "":
			s.submitSitemap(w, r, site, feed)
		default:
			return false
		}
		return true
	}
	return false
}

func (s *Server) siteOf(d *AccountData, site string) *GSCSite {
	for i := range d.Sites {
		if d.Sites[i].URL == site && d.Sites[i].Permission != "siteUnverifiedUser" {
			return &d.Sites[i]
		}
	}
	return nil
}

func inProperty(site, u string) bool {
	x, err := url.Parse(u)
	if err != nil || x.Host == "" {
		return false
	}
	if strings.HasPrefix(site, "sc-domain:") {
		dom := strings.TrimPrefix(site, "sc-domain:")
		return x.Hostname() == dom || strings.HasSuffix(x.Hostname(), "."+dom)
	}
	return strings.HasPrefix(u, site)
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	_, d, ok := s.begin(w, r, "inspect")
	if !ok {
		return
	}
	var req struct {
		InspectionURL string `json:"inspectionUrl"`
		SiteURL       string `json:"siteUrl"`
	}
	if err := readJSONBody(r, &req); err != nil {
		writeAPIError(w, "inspect", http.StatusBadRequest, "", "Invalid JSON payload received.")
		return
	}
	s.mu.Lock()
	st := s.siteOf(d, req.SiteURL)
	s.mu.Unlock()
	if st == nil {
		writeAPIError(w, "inspect", http.StatusForbidden, "", "You do not own this site, or the inspected URL is not part of this property.")
		return
	}
	if !inProperty(req.SiteURL, req.InspectionURL) {
		writeAPIError(w, "inspect", http.StatusBadRequest, "", "URL is not part of this property.")
		return
	}
	link := "https://search.google.com/search-console/inspect?resource_id=" + url.QueryEscape(req.SiteURL) + "&id=fake"
	crawl := time.Now().UTC().Add(-52 * time.Hour).Format(time.RFC3339)
	idx := map[string]any{"verdict": "PASS", "coverageState": "Submitted and indexed", "robotsTxtState": "ALLOWED",
		"indexingState": "INDEXING_ALLOWED", "lastCrawlTime": crawl, "pageFetchState": "SUCCESSFUL",
		"googleCanonical": req.InspectionURL, "userCanonical": req.InspectionURL, "crawledAs": "MOBILE",
		"sitemap": []string{siteOrigin(req.SiteURL) + "/sitemap.xml"}}
	res := map[string]any{"inspectionResultLink": link, "indexStatusResult": idx,
		"mobileUsabilityResult": map[string]any{"verdict": "PASS"},
		"richResultsResult":     map[string]any{"verdict": "PASS", "detectedItems": []map[string]any{{"richResultType": "Breadcrumbs", "items": []map[string]any{{"name": "Unnamed item"}}}}}}
	switch {
	case strings.Contains(req.InspectionURL, "new"):
		res["indexStatusResult"] = map[string]any{"verdict": "NEUTRAL", "coverageState": "URL is unknown to Google",
			"robotsTxtState": "ROBOTS_TXT_STATE_UNSPECIFIED", "indexingState": "INDEXING_STATE_UNSPECIFIED",
			"pageFetchState": "PAGE_FETCH_STATE_UNSPECIFIED"}
		delete(res, "mobileUsabilityResult")
		delete(res, "richResultsResult")
	case strings.Contains(req.InspectionURL, "blocked"):
		idx["verdict"], idx["coverageState"], idx["robotsTxtState"], idx["pageFetchState"] = "FAIL", "Blocked by robots.txt", "DISALLOWED", "BLOCKED_ROBOTS_TXT"
		delete(idx, "googleCanonical")
		delete(res, "richResultsResult")
	}
	writeJSON(w, http.StatusOK, map[string]any{"inspectionResult": res})
}

func (s *Server) sitemaps(w http.ResponseWriter, r *http.Request, site string) {
	_, d, ok := s.begin(w, r, "sitemaps")
	if !ok {
		return
	}
	s.mu.Lock()
	st := s.siteOf(d, site)
	var list []Sitemap
	if d.Sitemaps != nil {
		list = append(list, d.Sitemaps[site]...)
	}
	s.mu.Unlock()
	if st == nil {
		writeAPIError(w, "sitemaps", http.StatusForbidden, "forbidden", "User does not have sufficient permission for site '"+site+"'.")
		return
	}
	out := map[string]any{}
	items := []map[string]any{}
	for _, m := range list {
		it := map[string]any{"path": m.Path, "lastSubmitted": m.LastSubmitted.Format(time.RFC3339), "isPending": m.IsPending,
			"isSitemapsIndex": false, "type": "sitemap", "warnings": strconv.FormatInt(m.Warnings, 10), "errors": strconv.FormatInt(m.Errors, 10)}
		if !m.LastDownloaded.IsZero() {
			it["lastDownloaded"] = m.LastDownloaded.Format(time.RFC3339)
			it["contents"] = []map[string]string{{"type": "web", "submitted": strconv.FormatInt(m.Submitted, 10), "indexed": strconv.FormatInt(m.Indexed, 10)}}
		}
		items = append(items, it)
	}
	if len(items) > 0 {
		out["sitemap"] = items
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) submitSitemap(w http.ResponseWriter, r *http.Request, site, feed string) {
	_, d, ok := s.begin(w, r, "sitemapSubmit")
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.siteOf(d, site) == nil {
		writeAPIError(w, "sitemapSubmit", http.StatusForbidden, "forbidden", "User does not have sufficient permission for site '"+site+"'.")
		return
	}
	if !inProperty(site, feed) {
		writeAPIError(w, "sitemapSubmit", http.StatusBadRequest, "invalidParameter", "The sitemap URL is not part of this property.")
		return
	}
	if d.Sitemaps == nil {
		d.Sitemaps = map[string][]Sitemap{}
	}
	for i, m := range d.Sitemaps[site] {
		if m.Path == feed {
			d.Sitemaps[site][i].LastSubmitted, d.Sitemaps[site][i].IsPending = time.Now().UTC(), true
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	d.Sitemaps[site] = append(d.Sitemaps[site], Sitemap{Path: feed, LastSubmitted: time.Now().UTC(), IsPending: true})
	w.WriteHeader(http.StatusNoContent)
}
