package google_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/google"
)

/*
MH-447: the Google data over MCP, as an agent sees it — scoped, in the
token's workspace only, and saying so when there is nothing to read.
*/

func (f *fixture) mcp(t *testing.T, token, tool string, args map[string]any) (string, bool) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	req, _ := http.NewRequest("POST", f.srv.URL+"/api/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || res.StatusCode != 200 {
		t.Fatalf("%s: %d %s", tool, res.StatusCode, raw)
	}
	text := ""
	if len(out.Result.Content) > 0 {
		text = out.Result.Content[0].Text
	}
	return text, out.Result.IsError
}

func (c *client) token(t *testing.T, scopes ...string) string {
	t.Helper()
	out := c.must(t, "POST", "/api/w/mcp_tokens", map[string]any{"name": "agent " + rnd(), "scopes": scopes}, 201)
	return out["plaintext"].(string)
}

func TestGoogleMCPTools(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "")
	t.Setenv("YOUTUBE_API_KEY", f.fake.APIKey)

	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)
	read := alice.token(t, "analytics:read")

	t.Run("nothing enabled is said, not an error", func(t *testing.T) {
		text, isErr := f.mcp(t, read, "google_resources", nil)
		var res struct {
			Count int              `json:"count"`
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &res); isErr || err != nil || res.Count != 0 || len(res.Items) != 0 {
			t.Fatalf("%v %v %q", isErr, err, text)
		}
		text, isErr = f.mcp(t, read, "ga4_report", nil)
		if !isErr || !strings.Contains(text, "no GA4 property is enabled") {
			t.Fatalf("%v %q", isErr, text)
		}
	})

	f.setAccount("sub-mcp", "mcp@example.test")
	for _, p := range []string{"analytics", "search_console", "youtube"} {
		if r, _ := alice.connect(t, "product="+p); r != google.ResultConnected {
			t.Fatal(r)
		}
	}
	google.WaitBackground()

	var videoID string

	t.Run("every read tool answers from the workspace's own data", func(t *testing.T) {
		text, isErr := f.mcp(t, read, "google_resources", nil)
		var res struct {
			Count int `json:"count"`
			Items []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &res); isErr || err != nil || res.Count != 3 {
			t.Fatalf("google_resources: %v %v %q", isErr, err, text)
		}
		want := map[string]string{"GA4 property": "demo.example – GA4", "Search Console site": "demo.example", "YouTube channel": "Demo Channel"}
		for _, it := range res.Items {
			if n, ok := want[it.Kind]; !ok || n != it.Name {
				t.Errorf("google_resources: unexpected item kind=%q name=%q", it.Kind, it.Name)
			} else {
				delete(want, it.Kind)
			}
		}
		if len(want) != 0 {
			t.Errorf("google_resources: missing %v", want)
		}

		text, isErr = f.mcp(t, read, "ga4_report", map[string]any{"days": 28, "compare": true})
		var ga4 struct {
			Empty  bool `json:"empty"`
			Totals struct {
				Users float64 `json:"users"`
			} `json:"totals"`
			TopPages []map[string]any `json:"top_pages"`
			Sources  []struct {
				Source string `json:"source"`
				Medium string `json:"medium"`
			} `json:"sources"`
		}
		if err := json.Unmarshal([]byte(text), &ga4); isErr || err != nil || ga4.Empty {
			t.Fatalf("ga4_report: %v %v %q", isErr, err, text)
		}
		if len(ga4.TopPages) == 0 {
			t.Error("ga4_report: no top_pages")
		}
		found := false
		for _, s := range ga4.Sources {
			if s.Source == "google" && s.Medium == "organic" {
				found = true
			}
		}
		if !found {
			t.Errorf("ga4_report: sources lack google / organic: %q", text)
		}

		text, isErr = f.mcp(t, read, "gsc_performance", map[string]any{"days": 28, "compare": true})
		var gsc struct {
			Empty      bool             `json:"empty"`
			TopQueries []map[string]any `json:"top_queries"`
			Totals     struct {
				Clicks float64 `json:"clicks"`
			} `json:"totals"`
		}
		if err := json.Unmarshal([]byte(text), &gsc); isErr || err != nil {
			t.Fatalf("gsc_performance: %v %v %q", isErr, err, text)
		}
		if len(gsc.TopQueries) == 0 {
			t.Error("gsc_performance: no top_queries")
		}

		text, isErr = f.mcp(t, read, "gsc_sitemaps", nil)
		var sm struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &sm); isErr || err != nil {
			t.Fatalf("gsc_sitemaps: %v %v %q", isErr, err, text)
		}
		sawSitemap := false
		for _, s := range sm.Items {
			if s.Path == "https://demo.example/sitemap.xml" {
				sawSitemap = true
			}
		}
		if !sawSitemap {
			t.Errorf("gsc_sitemaps lacks https://demo.example/sitemap.xml:\n%s", text)
		}

		text, isErr = f.mcp(t, read, "gsc_inspections", nil)
		var insp struct {
			QuotaUsedToday int64 `json:"quota_used_today"`
			QuotaLimit     int64 `json:"quota_limit"`
		}
		if err := json.Unmarshal([]byte(text), &insp); isErr || err != nil || insp.QuotaLimit == 0 {
			t.Fatalf("gsc_inspections: %v %v %q", isErr, err, text)
		}

		text, isErr = f.mcp(t, read, "youtube_channel", nil)
		var yc struct {
			Synced  bool `json:"synced"`
			Channel struct {
				Title       string `json:"title"`
				Subscribers int64  `json:"subscribers"`
				Uploads     int64  `json:"uploads"`
			} `json:"channel"`
			Quota map[string]any `json:"quota"`
		}
		if err := json.Unmarshal([]byte(text), &yc); isErr || err != nil || !yc.Synced ||
			yc.Channel.Title != "Demo Channel" || yc.Channel.Subscribers != 12840 || yc.Channel.Uploads != 6 || yc.Quota == nil {
			t.Fatalf("youtube_channel: %v %v %q", isErr, err, text)
		}

		text, isErr = f.mcp(t, read, "youtube_videos", nil)
		var yv struct {
			Total int `json:"total"`
			Items []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &yv); isErr || err != nil || yv.Total != 6 {
			t.Fatalf("youtube_videos: %v %v %q", isErr, err, text)
		}
		for _, v := range yv.Items {
			if v.Title == "How we plan a launch" {
				videoID = v.ID
			}
		}
		if videoID == "" {
			t.Fatalf("youtube_videos lacks 'How we plan a launch':\n%s", text)
		}

		text, isErr = f.mcp(t, read, "youtube_analytics", map[string]any{"days": 28, "compare": true})
		var ya struct {
			Empty   bool `json:"empty"`
			Totals  struct {
				Views float64 `json:"views"`
			} `json:"totals"`
			Traffic []struct {
				Source string `json:"source"`
			} `json:"traffic"`
		}
		if err := json.Unmarshal([]byte(text), &ya); isErr || err != nil {
			t.Fatalf("youtube_analytics: %v %v %q", isErr, err, text)
		}
		if !ya.Empty {
			sawSearch := false
			for _, tr := range ya.Traffic {
				if tr.Source == "YT_SEARCH" {
					sawSearch = true
				}
			}
			if !sawSearch {
				t.Errorf("youtube_analytics traffic lacks YT_SEARCH:\n%s", text)
			}
		}

		text, isErr = f.mcp(t, read, "youtube_reports", map[string]any{"days": 28, "compare": true})
		var yr struct {
			Jobs []struct {
				ReportTypeID string `json:"report_type_id"`
				Reports      int64  `json:"reports"`
			} `json:"jobs"`
		}
		if err := json.Unmarshal([]byte(text), &yr); isErr || err != nil {
			t.Fatalf("youtube_reports: %v %v %q", isErr, err, text)
		}
		sawJob := false
		for _, j := range yr.Jobs {
			if j.ReportTypeID == "channel_basic_a3" && j.Reports == 0 {
				sawJob = true
			}
		}
		if !sawJob {
			t.Errorf("youtube_reports lacks channel_basic_a3 with no reports:\n%s", text)
		}

		text, isErr = f.mcp(t, read, "youtube_video", map[string]any{"id": videoID})
		var vid struct {
			Video struct {
				TopComments []struct {
					ReplyURL string `json:"reply_url"`
				} `json:"top_comments"`
			} `json:"video"`
		}
		if err := json.Unmarshal([]byte(text), &vid); isErr || err != nil {
			t.Fatalf("youtube_video: %v %v %q", isErr, err, text)
		}
		if len(vid.Video.TopComments) == 0 || !strings.HasPrefix(vid.Video.TopComments[0].ReplyURL, "https://www.youtube.com/watch?v=") {
			t.Errorf("youtube_video top comment reply_url: %q", text)
		}
		if _, isErr := f.mcp(t, read, "ga4_report", map[string]any{"days": 30}); !isErr {
			t.Error("days=30 must be refused")
		}
	})

	t.Run("the write tools need search_console:write, which a member cannot hold", func(t *testing.T) {
		if text, isErr := f.mcp(t, read, "gsc_inspect_url", map[string]any{"url": "https://demo.example/"}); !isErr || !strings.Contains(text, "search_console:write") {
			t.Errorf("read token inspecting: %v %q", isErr, text)
		}
		write := alice.token(t, "analytics:read", "search_console:write")
		text, isErr := f.mcp(t, write, "gsc_inspect_url", map[string]any{"url": "https://demo.example/pricing"})
		var insp struct {
			Inspection struct {
				Verdict string `json:"verdict"`
			} `json:"inspection"`
		}
		if err := json.Unmarshal([]byte(text), &insp); isErr || err != nil || insp.Inspection.Verdict != "PASS" {
			t.Errorf("inspect: %v %v %q", isErr, err, text)
		}
		text, isErr = f.mcp(t, write, "gsc_submit_sitemap", map[string]any{"url": "https://demo.example/sitemap-2.xml"})
		var sm struct {
			OK    bool `json:"ok"`
			Items []struct {
				Path      string `json:"path"`
				IsPending bool   `json:"is_pending"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &sm); isErr || err != nil || !sm.OK {
			t.Fatalf("submit: %v %v %q", isErr, err, text)
		}
		sawPending := false
		for _, it := range sm.Items {
			if it.Path == "https://demo.example/sitemap-2.xml" && it.IsPending {
				sawPending = true
			}
		}
		if !sawPending {
			t.Errorf("gsc_submit_sitemap lacks a pending sitemap-2.xml:\n%s", text)
		}
		if _, isErr := f.mcp(t, write, "gsc_inspect_url", map[string]any{"url": "https://elsewhere.test/"}); !isErr {
			t.Error("a URL outside the site must be refused")
		}
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		if code, _, _ := m.do(t, "POST", "/api/w/mcp_tokens", map[string]any{"name": "x", "scopes": []string{"search_console:write"}}); code/100 == 2 {
			t.Error("a member must not mint search_console:write")
		}
	})

	t.Run("a token reads only its own workspace", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		btok := bob.token(t, "analytics:read")
		text, isErr := f.mcp(t, btok, "google_resources", nil)
		var res struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal([]byte(text), &res); isErr || err != nil || res.Count != 0 {
			t.Errorf("bob's token saw alice's resources: %v %v %s", isErr, err, text)
		}
		ga := alice.resources(t, "?kind=ga4_property").Resources[0]
		if _, isErr := f.mcp(t, btok, "ga4_report", map[string]any{"resource": ga.ID}); !isErr {
			t.Error("bob naming alice's property must be refused")
		}
	})
}
