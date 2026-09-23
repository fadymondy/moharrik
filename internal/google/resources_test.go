package google_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
)

/*
MH-440: discovery of what each connected account can see, the picker, and
auto-enable — against the fake Google's Admin, webmasters and YouTube APIs.
*/

type resourcesOut struct {
	Resources   []google.Resource  `json:"resources"`
	Discoveries []google.Discovery `json:"discoveries"`
	NeedsChoice []google.Kind      `json:"needs_choice"`
	Available   []google.Kind      `json:"available"`
	CanManage   bool               `json:"can_manage"`
}

func (c *client) resources(t *testing.T, query string) resourcesOut {
	t.Helper()
	code, b, _ := c.do(t, "GET", "/api/w/google/resources"+query, nil)
	if code != 200 {
		t.Fatalf("resources: %d %s", code, b)
	}
	var out resourcesOut
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func byKind(list []google.Resource, k google.Kind) []google.Resource {
	out := []google.Resource{}
	for _, r := range list {
		if r.Kind == k {
			out = append(out, r)
		}
	}
	return out
}

func TestResourceDiscovery(t *testing.T) {
	f := newFixture(t)
	t.Setenv("GOOGLE_SYNC_INLINE", "") // this test is about the connect's own discovery
	ctx := context.Background()

	alice := f.newUser(t)
	wsID, _ := alice.workspace(t)

	t.Run("one of each is discovered and switched on with no step", func(t *testing.T) {
		f.setAccount("sub-one", "one@example.test")
		for _, p := range []string{"analytics", "search_console", "youtube"} {
			if r, _ := alice.connect(t, "product="+p); r != google.ResultConnected {
				t.Fatalf("connect %s: %s", p, r)
			}
		}
		google.WaitBackground()
		out := alice.resources(t, "")
		if !out.CanManage || len(out.NeedsChoice) != 0 {
			t.Fatalf("%+v", out)
		}
		for _, k := range google.Kinds {
			list := byKind(out.Resources, k)
			if len(list) != 1 || !list[0].Enabled || !list[0].AutoEnabled || !list[0].Available {
				t.Fatalf("%s: want one auto-enabled resource, got %+v", k, list)
			}
			if list[0].ConnectionEmail != "one@example.test" {
				t.Errorf("%s: an admin sees the account it is read through: %q", k, list[0].ConnectionEmail)
			}
		}
		ga := byKind(out.Resources, google.KindGA4)[0]
		if !strings.HasPrefix(ga.ExternalID, "properties/") || ga.DisplayName != "demo.example – GA4" || ga.ParentName != "Demo Co" {
			t.Errorf("GA4 property: %+v", ga)
		}
		yt := byKind(out.Resources, google.KindYT)[0]
		if !strings.HasPrefix(yt.ExternalID, "UC") || yt.DisplayName != "Demo Channel" || !strings.Contains(string(yt.Details), "avatar_url") {
			t.Errorf("channel: %+v %s", yt, yt.Details)
		}
		if len(out.Discoveries) != 3 {
			t.Errorf("one discovery per product: %+v", out.Discoveries)
		}
		for _, d := range out.Discoveries {
			if d.Error != "" || d.Found != 1 {
				t.Errorf("discovery %+v", d)
			}
		}
	})

	t.Run("several: paged discovery, nothing guessed, the picker asks", func(t *testing.T) {
		bob := f.newUser(t)
		bob.workspace(t)
		sub := "sub-many"
		f.fake.SetData(sub, &googletest.AccountData{
			GA4: []googletest.GA4Property{
				{ID: "properties/11", Name: "Shop", Account: "accounts/1", AccountName: "Acme"},
				{ID: "properties/12", Name: "Blog", Account: "accounts/1", AccountName: "Acme"},
				{ID: "properties/21", Name: "Docs", Account: "accounts/2", AccountName: "Acme Labs"},
				{ID: "properties/31", Name: "App", Account: "accounts/3", AccountName: "Side project"},
			},
			Sites: []googletest.GSCSite{
				{URL: "sc-domain:acme.test", Permission: "siteOwner"},
				{URL: "https://blog.acme.test/", Permission: "siteFullUser"},
				{URL: "https://unverified.test/", Permission: "siteUnverifiedUser"},
			},
			Channel: nil, // an account with no YouTube channel
		})
		f.setAccount(sub, "many@example.test")
		for _, p := range []string{"analytics", "search_console", "youtube"} {
			if r, _ := bob.connect(t, "product="+p); r != google.ResultConnected {
				t.Fatalf("connect %s: %s", p, r)
			}
		}
		google.WaitBackground()
		out := bob.resources(t, "")
		ga, gsc, yt := byKind(out.Resources, google.KindGA4), byKind(out.Resources, google.KindGSC), byKind(out.Resources, google.KindYT)
		if len(ga) != 4 {
			t.Fatalf("all three pages of accountSummaries: %+v", ga)
		}
		if len(gsc) != 2 {
			t.Fatalf("an unverified site is not offered: %+v", gsc)
		}
		if len(yt) != 0 {
			t.Fatalf("no channel: %+v", yt)
		}
		for _, r := range append(ga, gsc...) {
			if r.Enabled {
				t.Errorf("with several, nothing is switched on by itself: %+v", r)
			}
		}
		if strings.Join(kinds(out.NeedsChoice), ",") != "ga4_property,gsc_site" {
			t.Errorf("needs choice: %v", out.NeedsChoice)
		}
		if n := f.fake.Calls("api:admin"); n < 2 {
			t.Errorf("accountSummaries was not paged: %d calls", n)
		}

		// Choosing one: it is on, and discovery never overrides the choice.
		shop := ga[0]
		for _, r := range ga {
			if r.ExternalID == "properties/11" {
				shop = r
			}
		}
		code, b, _ := bob.do(t, "PATCH", "/api/w/google/resources/"+shop.ID, map[string]any{"enabled": true})
		if code != 200 || !strings.Contains(string(b), `"enabled":true`) {
			t.Fatalf("enable: %d %s", code, b)
		}
		code, b, _ = bob.do(t, "PATCH", "/api/w/google/resources/"+shop.ID, map[string]any{"enabled": false})
		if code != 200 {
			t.Fatalf("disable: %d %s", code, b)
		}
		// The account now sees just one property: still not auto-enabled,
		// because a person has chosen for this kind.
		f.fake.Set(func(s *googletest.Server) {})
		d := f.fake.Data(sub)
		f.fake.Set(func(*googletest.Server) { d.GA4 = d.GA4[:1] })
		code, b, _ = bob.do(t, "POST", "/api/w/google/resources/discover", nil)
		if code != 200 {
			t.Fatalf("discover: %d %s", code, b)
		}
		var again resourcesOut
		_ = json.Unmarshal(b, &again)
		for _, r := range byKind(again.Resources, google.KindGA4) {
			if r.Enabled {
				t.Errorf("a person's choice was overridden: %+v", r)
			}
			if r.ExternalID != "properties/11" && r.Available {
				t.Errorf("a property the account lost is kept but unavailable: %+v", r)
			}
		}
		if len(byKind(again.Resources, google.KindGA4)) != 4 {
			t.Error("a lost property must be kept, not deleted")
		}
	})

	t.Run("a member reads only what is enabled, and cannot change it", func(t *testing.T) {
		m := f.newUser(t)
		addMember(t, f, wsID, m.email, "member")
		m.ws = wsID
		out := m.resources(t, "?kind=ga4_property")
		if out.CanManage || len(out.Discoveries) != 0 || len(out.Resources) != 1 {
			t.Fatalf("%+v", out)
		}
		if out.Resources[0].ConnectionEmail != "" {
			t.Errorf("a member does not see which account: %q", out.Resources[0].ConnectionEmail)
		}
		if code, _, _ := m.do(t, "PATCH", "/api/w/google/resources/"+out.Resources[0].ID, map[string]any{"enabled": false}); code != 403 {
			t.Errorf("member PATCH: %d", code)
		}
		if code, _, _ := m.do(t, "POST", "/api/w/google/resources/"+out.Resources[0].ID+"/sync", nil); code != 403 {
			t.Errorf("member sync: %d", code)
		}
		c := f.newUser(t)
		addMember(t, f, wsID, c.email, "client")
		c.ws = wsID
		if code, _, _ := c.do(t, "GET", "/api/w/google/resources", nil); code != 403 {
			t.Errorf("a client reads no analytics: %d", code)
		}
	})

	t.Run("an API that is not enabled is recorded and said plainly", func(t *testing.T) {
		carol := f.newUser(t)
		carol.workspace(t)
		f.setAccount("sub-disabled", "disabled@example.test")
		f.fake.Fail("admin", http.StatusForbidden, "SERVICE_DISABLED", -1)
		defer f.fake.ClearFailures()
		if r, _ := carol.connect(t, "product=analytics"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		out := carol.resources(t, "")
		if len(out.Resources) != 0 || len(out.Discoveries) != 1 {
			t.Fatalf("%+v", out)
		}
		if e := out.Discoveries[0].Error; !strings.Contains(e, "not enabled in the Google Cloud project") {
			t.Errorf("error: %q", e)
		}
	})

	t.Run("429 is retried with backoff and then succeeds", func(t *testing.T) {
		dave := f.newUser(t)
		dave.workspace(t)
		f.setAccount("sub-429", "ratelimited@example.test")
		f.fake.Fail("sites", http.StatusTooManyRequests, "rateLimitExceeded", 2)
		defer f.fake.ClearFailures()
		if r, _ := dave.connect(t, "product=search_console"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		out := dave.resources(t, "")
		if len(out.Resources) != 1 || !out.Resources[0].Enabled || out.Discoveries[0].Error != "" {
			t.Fatalf("%+v", out)
		}
	})

	t.Run("invalid_grant marks the connection for reauth", func(t *testing.T) {
		erin := f.newUser(t)
		ews, _ := erin.workspace(t)
		f.setAccount("sub-reauth", "reauth@example.test")
		if r, _ := erin.connect(t, "product=search_console"); r != google.ResultConnected {
			t.Fatal(r)
		}
		google.WaitBackground()
		google.ForgetAccessTokens()
		f.fake.Set(func(s *googletest.Server) { s.RefreshFails = true })
		defer f.fake.Set(func(s *googletest.Server) { s.RefreshFails = false })
		code, b, _ := erin.do(t, "POST", "/api/w/google/resources/discover", nil)
		if code != 200 {
			t.Fatalf("%d %s", code, b)
		}
		var out resourcesOut
		_ = json.Unmarshal(b, &out)
		if len(out.Resources) != 1 || !out.Resources[0].Reauth {
			t.Fatalf("the resource says its account needs reconnecting: %+v", out.Resources)
		}
		l := erin.list(t)
		if l.Connections[0].Status != "reauth" {
			t.Errorf("connection: %+v", l.Connections[0])
		}
		// A reauth connection is not retried by the collector.
		var due int
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM google_resources r JOIN google_connections c ON c.id = r.connection_id
			WHERE r.workspace_id = $1 AND c.status = ''`, ews).Scan(&due)
		if due != 0 {
			t.Errorf("reauth resources in the queue: %d", due)
		}
	})

	t.Run("disconnect removes the account's resources", func(t *testing.T) {
		l := alice.list(t)
		for _, c := range l.Connections {
			alice.must(t, "DELETE", "/api/w/google/connections/"+c.ID, nil, 200)
		}
		var n int
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM google_resources WHERE workspace_id = $1`, wsID).Scan(&n)
		if n != 0 {
			t.Errorf("%d resources outlived their connection", n)
		}
	})
	_ = ctx
}

func kinds(list []google.Kind) []string {
	out := []string{}
	for _, k := range list {
		out = append(out, string(k))
	}
	return out
}

func TestBackoff(t *testing.T) {
	limited := &google.APIError{Status: 429, Reason: "rateLimitExceeded"}
	d1, b1 := google.Backoff(limited, 1)
	d3, _ := google.Backoff(limited, 3)
	if !b1 || d1 != 15*time.Minute || d3 != time.Hour {
		t.Errorf("429: %v %v %v", d1, b1, d3)
	}
	if d, b := google.Backoff(errors.New("boom"), 1); b || d != 30*time.Minute {
		t.Errorf("plain: %v %v", d, b)
	}
	if d, _ := google.Backoff(limited, 50); d != 12*time.Hour {
		t.Errorf("capped: %v", d)
	}
	if d, b := google.Backoff(google.ErrReauth, 1); b || d != 24*time.Hour {
		t.Errorf("reauth: %v %v", d, b)
	}
	if !strings.Contains(google.Describe(&google.APIError{Status: 403, Reason: "SERVICE_DISABLED", Message: "x"}), "not enabled") {
		t.Error("describe SERVICE_DISABLED")
	}
	if !strings.Contains(google.Describe(google.ErrReauth), "Reconnect") {
		t.Error("describe reauth")
	}
	e := google.ParseAPIError(429, []byte(`{"error":{"code":429,"message":"slow","status":"RESOURCE_EXHAUSTED"}}`))
	if e.Reason != "RESOURCE_EXHAUSTED" || !google.RateLimited(e) {
		t.Errorf("grpc envelope: %+v", e)
	}
	e = google.ParseAPIError(403, []byte(`{"error":{"code":403,"message":"q","errors":[{"reason":"quotaExceeded"}]}}`))
	if e.Reason != "quotaExceeded" || !google.RateLimited(e) {
		t.Errorf("json envelope: %+v", e)
	}
}
