package notifications

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MH-381: captured-style Sentry and GitHub payloads (testdata/, real field
// layout, trimmed) become items with the right title, link and severity, and
// shapes nobody anticipated still become an item instead of a 422.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestNormaliseFixtures(t *testing.T) {
	cases := []struct {
		fixture, kind, event   string
		title, url, severity   string
		itemKind, bodyContains string
	}{
		{
			fixture: "sentry_event_alert.json", kind: "sentry", event: "event_alert",
			title:    "Alert: ApiError: Unable to post message to Slack channel",
			url:      "https://acme.sentry.io/issues/1170820242/events/4d2de9b7a0ad4b4c8b9bb0e7e3d3b1a5/",
			severity: "error", itemKind: "issue_alert",
			bodyContains: "sentry.tasks.integrations.slack.post_message",
		},
		{
			// level "warning" on the issue carries through as the severity.
			fixture: "sentry_issue_created.json", kind: "sentry", event: "issue",
			title:    "Issue created: Slow DB query: SELECT * FROM orders WHERE ...",
			url:      "https://acme.sentry.io/issues/1170820243/",
			severity: "warning", itemKind: "issue",
			bodyContains: "/api/orders",
		},
		{
			fixture: "github_push.json", kind: "github", event: "push",
			title:    "fadymondy pushed 2 commits to main in fadymondy/moharrik",
			url:      "https://github.com/fadymondy/moharrik/compare/6113728f27ae...0d1a26e67d8f",
			severity: "info", itemKind: "push",
			bodyContains: "feat(notifications): Sentry and GitHub webhooks verify",
		},
		{
			fixture: "github_pull_request_merged.json", kind: "github", event: "pull_request",
			title:    "PR #42 merged: Verify Sentry and GitHub webhooks in fadymondy/moharrik",
			url:      "https://github.com/fadymondy/moharrik/pull/42",
			severity: "info", itemKind: "pull_request",
			bodyContains: "Closes MH-381.",
		},
		{
			fixture: "github_issues_opened.json", kind: "github", event: "issues",
			title:    "Issue #7 opened: Webhook deliveries from Sentry return 401 in fadymondy/moharrik",
			url:      "https://github.com/fadymondy/moharrik/issues/7",
			severity: "info", itemKind: "issues",
			bodyContains: "Every Sentry delivery fails.",
		},
		{
			fixture: "github_workflow_run_failed.json", kind: "github", event: "workflow_run",
			title:    "Workflow CI failed on main in fadymondy/moharrik",
			url:      "https://github.com/fadymondy/moharrik/actions/runs/30433642",
			severity: "error", itemKind: "workflow_run",
		},
		{
			fixture: "github_release_published.json", kind: "github", event: "release",
			title:    "Release v1.4.0 published in fadymondy/moharrik",
			url:      "https://github.com/fadymondy/moharrik/releases/tag/v1.4.0",
			severity: "info", itemKind: "release",
		},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			it := Normalise(c.kind, c.event, "Source", fixture(t, c.fixture))
			if it.Title != c.title {
				t.Errorf("title = %q\n want %q", it.Title, c.title)
			}
			if it.URL != c.url {
				t.Errorf("url = %q\n want %q", it.URL, c.url)
			}
			if it.Severity != c.severity {
				t.Errorf("severity = %q, want %q", it.Severity, c.severity)
			}
			if it.Kind != c.itemKind {
				t.Errorf("kind = %q, want %q", it.Kind, c.itemKind)
			}
			if c.bodyContains != "" && !strings.Contains(it.Body, c.bodyContains) {
				t.Errorf("body %q does not contain %q", it.Body, c.bodyContains)
			}
			if strings.Contains(it.Title, "\n") {
				t.Errorf("title spans lines: %q", it.Title)
			}
		})
	}
}

func TestNormaliseUnknownShapesStillBecomeItems(t *testing.T) {
	cases := []struct {
		name, kind, event, body, wantTitle string
	}{
		// Used to be a 422: verified JSON with no "title".
		{"generic without title", "generic", "", `{"status":"ok","count":3}`, "Billing delivery"},
		{"generic not JSON", "generic", "", "disk /dev/sda1 at 91%\nsecond line", "disk /dev/sda1 at 91%"},
		{"generic message alias", "generic", "", `{"message":"Nightly backup failed","level":"error"}`, "Nightly backup failed"},
		{"github unknown event", "github", "star", `{"action":"created","repository":{"full_name":"a/b"}}`, "GitHub star created in a/b"},
		{"github not JSON", "github", "push", `not json`, "GitHub push"},
		{"sentry unknown resource", "sentry", "comment", `{"action":"created","data":{}}`, "Sentry comment created"},
		{"sentry no resource", "sentry", "", `{}`, "Sentry event"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := Normalise(c.kind, c.event, "Billing", []byte(c.body))
			if it.Title != c.wantTitle {
				t.Fatalf("title = %q, want %q", it.Title, c.wantTitle)
			}
			if it.Severity == "" || it.Kind == "" {
				t.Fatalf("empty severity/kind: %+v", it)
			}
		})
	}
}

func TestNormaliseGeneric(t *testing.T) {
	it := Normalise("generic", "", "Src", []byte(`{"title":"Deploy failed","body":"step 3","kind":"deploy","severity":"critical","actor":"ci","url":"https://ci.example/run/9","external_id":"run-9"}`))
	if it.Title != "Deploy failed" || it.Body != "step 3" || it.Kind != "deploy" || it.Severity != "error" ||
		it.Actor != "ci" || it.URL != "https://ci.example/run/9" || it.ExternalID != "run-9" {
		t.Fatalf("generic = %+v", it)
	}
	// Only http(s) links survive; a javascript: link is not handed to the feed.
	it = Normalise("generic", "", "Src", []byte(`{"title":"x","url":"javascript:alert(1)"}`))
	if it.URL != "" {
		t.Fatalf("unsafe url kept: %q", it.URL)
	}
	// An over-long title is clipped rather than refused.
	it = Normalise("generic", "", "Src", []byte(`{"title":"`+strings.Repeat("a", 1000)+`"}`))
	if len(it.Title) != maxTitle {
		t.Fatalf("title length %d, want %d", len(it.Title), maxTitle)
	}
}
