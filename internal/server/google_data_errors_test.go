package server

/*
TestGoogleDataRoutesAnswerInputErrorsAs4xx — MH-473.

Every Analytics, Search Console and YouTube read answers wrong input with a 4xx
that says what was wrong, and keeps 5xx for faults that are really the
server's. Google itself failing is not one of those: with nothing cached the
page is told why in `error` (200), and it is not dressed up as the server
breaking.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run GoogleDataRoutes -v
*/

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGoogleDataRoutesAnswerInputErrorsAs4xx(t *testing.T) {
	_, alice, _, fakeGoogle := bootWithGoogle(t)

	type errBody struct {
		Error   string   `json:"error"`
		Details []string `json:"details"`
	}
	check := func(path string, want int) errBody {
		t.Helper()
		code, b := alice.do("GET", path, nil)
		var e errBody
		if err := json.Unmarshal(b, &e); err != nil {
			t.Errorf("GET %s: not JSON (%d): %s", path, code, b)
		}
		if code != want {
			t.Errorf("GET %s: got %d, want %d: %s", path, code, want, b)
		} else if code >= 400 && e.Error == "" {
			t.Errorf("GET %s: %d without a message: %s", path, code, b)
		}
		return e
	}

	// With no parameters every page answers for the default property.
	for _, p := range []string{
		"/api/w/analytics/report",
		"/api/w/search-console/performance",
		"/api/w/search-console/inspections",
		"/api/w/search-console/sitemaps",
		"/api/w/youtube/channel",
		"/api/w/youtube/videos",
		"/api/w/youtube/analytics",
		"/api/w/youtube/reports",
		"/api/w/google/resources",
		"/api/w/google/connections",
		"/api/w/overview",
	} {
		check(p, http.StatusOK)
	}

	for _, c := range []struct {
		path string
		want int
	}{
		// days: a number, and one of three.
		{"/api/w/analytics/report?days=5", http.StatusUnprocessableEntity},
		{"/api/w/analytics/report?days=abc", http.StatusUnprocessableEntity},
		{"/api/w/analytics/report?days=-7", http.StatusUnprocessableEntity},
		{"/api/w/search-console/performance?days=5", http.StatusUnprocessableEntity},
		{"/api/w/youtube/analytics?days=365", http.StatusUnprocessableEntity},
		// refresh: a boolean.
		{"/api/w/analytics/report?refresh=maybe", http.StatusUnprocessableEntity},
		// resource: one of this workspace's enabled ones, whatever shape it has.
		{"/api/w/analytics/report?resource=garbage", http.StatusNotFound},
		{"/api/w/analytics/report?resource=00000000-0000-0000-0000-000000000000", http.StatusNotFound},
		{"/api/w/search-console/performance?resource=garbage", http.StatusNotFound},
		{"/api/w/search-console/inspections?resource=garbage", http.StatusNotFound},
		{"/api/w/search-console/sitemaps?resource=garbage", http.StatusNotFound},
		{"/api/w/youtube/channel?resource=garbage", http.StatusNotFound},
		{"/api/w/youtube/videos?resource=garbage", http.StatusNotFound},
		{"/api/w/youtube/analytics?resource=garbage", http.StatusNotFound},
		{"/api/w/youtube/reports?resource=garbage", http.StatusNotFound},
		{"/api/w/youtube/videos/garbage", http.StatusNotFound},
		{"/api/w/youtube/reports/garbage/csv", http.StatusNotFound},
		// ...and text at all (MH-472's class of 500).
		{"/api/w/analytics/report?resource=%FF", http.StatusBadRequest},
		{"/api/w/search-console/performance?query=%00", http.StatusBadRequest},
		{"/api/w/youtube/videos?q=%FF%FE", http.StatusBadRequest},
		// Filters with a declared shape.
		{"/api/w/search-console/performance?device=FRIDGE", http.StatusUnprocessableEntity},
		{"/api/w/search-console/performance?country=egypt", http.StatusUnprocessableEntity},
		{"/api/w/youtube/videos?privacy=secret", http.StatusUnprocessableEntity},
		{"/api/w/youtube/videos?limit=-1", http.StatusUnprocessableEntity},
		{"/api/w/youtube/videos?limit=1000", http.StatusUnprocessableEntity},
		{"/api/w/google/resources?kind=fax", http.StatusUnprocessableEntity},
		{"/api/w/google/connect", http.StatusUnprocessableEntity},
		{"/api/w/google/connect?product=myspace", http.StatusUnprocessableEntity},
		{"/api/w/google/connect?product=analytics&connection=garbage", http.StatusNotFound},
	} {
		check(c.path, c.want)
	}

	// Google failing is not the server failing. A refresh that GA4 refuses, with
	// the cache emptied first so there is nothing to fall back on, says why.
	fakeGoogle.Fail("runReport", http.StatusInternalServerError, "", -1)
	defer fakeGoogle.ClearFailures()
	code, b := alice.do("GET", "/api/w/analytics/report?days=90&refresh=true", nil)
	var out struct {
		Report *json.RawMessage `json:"report"`
		Error  string           `json:"error"`
	}
	_ = json.Unmarshal(b, &out)
	if code != http.StatusOK || out.Report != nil || out.Error == "" {
		t.Errorf("GA4 down with nothing cached: %d %s, want 200 with no report and the reason in error", code, b)
	}
}
