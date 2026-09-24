package automation

import "testing"

// Only sites still on the browser's list are reported waiting (MH-483).
func TestApplyAccessReportsOnlyListedSites(t *testing.T) {
	x := AgentOut{ID: "b1", AllowedHosts: []string{"example.com", "News.Google.com"}}
	applyAccess(&x, map[string]SiteAccess{"b1": {Mode: "sites", Pending: []string{"news.google.com", "removed.com"}}})
	if x.SiteAccess != "sites" || len(x.PendingHosts) != 1 || x.PendingHosts[0] != "news.google.com" {
		t.Fatalf("%+v", x)
	}
	y := AgentOut{ID: "b2", AllowedHosts: []string{"example.com"}}
	applyAccess(&y, map[string]SiteAccess{})
	if y.SiteAccess != "" || y.PendingHosts == nil || len(y.PendingHosts) != 0 {
		t.Fatalf("an offline browser: %+v", y)
	}
}
