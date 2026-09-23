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
		if isErr || !strings.Contains(text, "Nothing is enabled") {
			t.Fatalf("%v %q", isErr, text)
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

	t.Run("every read tool answers from the workspace's own data", func(t *testing.T) {
		text, _ := f.mcp(t, read, "google_resources", nil)
		for _, want := range []string{"GA4 property: demo.example – GA4", "Search Console site: demo.example", "YouTube channel: Demo Channel"} {
			if !strings.Contains(text, want) {
				t.Errorf("google_resources lacks %q:\n%s", want, text)
			}
		}
		cases := map[string][]string{
			"ga4_report":        {"users ", "top pages", "source / medium", "google / organic"},
			"gsc_performance":   {"clicks ", "top queries", "newest day with data"},
			"gsc_sitemaps":      {"https://demo.example/sitemap.xml"},
			"gsc_inspections":   {"inspections today"},
			"youtube_channel":   {"Demo Channel", "subscribers 12,840", "uploads read 6", "YouTube quota today"},
			"youtube_videos":    {"6 uploads match", "How we plan a launch"},
			"youtube_analytics": {"views ", "not published by YouTube yet", "traffic: YT_SEARCH"},
			"youtube_reports":   {"job channel_basic_a3: no reports yet"},
		}
		for tool, wants := range cases {
			text, isErr := f.mcp(t, read, tool, map[string]any{"days": 28, "compare": true})
			if isErr {
				t.Errorf("%s failed: %s", tool, text)
				continue
			}
			for _, w := range wants {
				if !strings.Contains(text, w) {
					t.Errorf("%s lacks %q:\n%s", tool, w, text)
				}
			}
		}
		vids, _ := f.mcp(t, read, "youtube_videos", map[string]any{"q": "launch"})
		id := vids[strings.Index(vids, "id=")+3:]
		id = strings.TrimSpace(strings.SplitN(id, "\n", 2)[0])
		text, isErr := f.mcp(t, read, "youtube_video", map[string]any{"id": id})
		if isErr || !strings.Contains(text, "reply: https://www.youtube.com/watch?v=") {
			t.Errorf("youtube_video: %v %s", isErr, text)
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
		if isErr || !strings.Contains(text, "verdict PASS") {
			t.Errorf("inspect: %v %s", isErr, text)
		}
		text, isErr = f.mcp(t, write, "gsc_submit_sitemap", map[string]any{"url": "https://demo.example/sitemap-2.xml"})
		if isErr || !strings.Contains(text, "sitemap-2.xml · pending") {
			t.Errorf("submit: %v %s", isErr, text)
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
		text, _ := f.mcp(t, btok, "google_resources", nil)
		if strings.Contains(text, "demo.example") || strings.Contains(text, "Demo Channel") {
			t.Errorf("bob's token saw alice's resources: %s", text)
		}
		ga := alice.resources(t, "?kind=ga4_property").Resources[0]
		if text, isErr := f.mcp(t, btok, "ga4_report", map[string]any{"resource": ga.ID}); !isErr || strings.Contains(text, "users ") {
			t.Errorf("bob naming alice's property: %v %s", isErr, text)
		}
	})
}
