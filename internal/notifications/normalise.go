package notifications

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

/*
Normalise turns a VERIFIED delivery into a readable item (MH-381, ported from
fadymondy.com's internal/center/normalise.go).

The raw payload is stored alongside, so a title this gets wrong can be fixed
later from the original. Nothing here refuses: a delivery that verified always
becomes an item — an unknown event or shape gets a plain title (falling back to
the source's name) instead of the 422 it used to get.
*/

// Item is a normalised delivery, before it is scoped and stored.
type Item struct {
	Kind       string
	Severity   string
	Title      string
	Body       string
	Actor      string
	URL        string
	ExternalID string
}

const (
	maxTitle = 300
	maxBody  = 4000
)

// Normalise reads a verified body. sourceName is the fallback title.
func Normalise(kind, event, sourceName string, body []byte) Item {
	var doc map[string]any
	if json.Unmarshal(body, &doc) != nil {
		doc = nil
	}
	var it Item
	switch kind {
	case "github":
		it = normaliseGitHub(event, doc)
	case "sentry":
		it = normaliseSentry(event, doc)
	default:
		it = normaliseGeneric(doc, body)
	}
	it.Severity = clampSeverity(it.Severity)
	if strings.TrimSpace(it.Kind) == "" {
		it.Kind = "event"
	}
	it.Title = strings.TrimSpace(it.Title)
	if it.Title == "" {
		it.Title = firstLine(it.Body)
	}
	if it.Title == "" {
		it.Title = strings.TrimSpace(sourceName + " delivery")
	}
	it.Title = clip(it.Title, maxTitle)
	it.Body = clip(strings.TrimSpace(it.Body), maxBody)
	it.Actor = clip(strings.TrimSpace(it.Actor), 200)
	it.Kind = clip(it.Kind, 80)
	it.ExternalID = clip(strings.TrimSpace(it.ExternalID), 200)
	// Only http(s) links are rendered as links; anything else (javascript:,
	// data:) is dropped rather than handed to the feed.
	if u := strings.TrimSpace(it.URL); strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		it.URL = clip(u, 2000)
	} else {
		it.URL = ""
	}
	return it
}

// str walks a path of object keys and returns a string (numbers formatted
// without a decimal part, bools as true/false).
func str(doc map[string]any, path ...string) string {
	var cur any = doc
	for _, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[key]
	}
	switch v := cur.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func suffix(repo string) string {
	if repo == "" {
		return ""
	}
	return " in " + repo
}

// clip cuts s to at most n runes.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// levelSeverity maps a Sentry/syslog-style level to an item severity.
func levelSeverity(level, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "fatal", "error", "critical":
		return "error"
	case "warning", "warn":
		return "warning"
	case "info", "debug", "sample", "log":
		return "info"
	}
	return fallback
}

// ---- GitHub ------------------------------------------------------------------

func normaliseGitHub(event string, doc map[string]any) Item {
	repo := str(doc, "repository", "full_name")
	action := str(doc, "action")
	it := Item{Kind: firstNonEmpty(event, "github"), Actor: str(doc, "sender", "login"), URL: str(doc, "repository", "html_url")}
	if doc == nil {
		it.Title = "GitHub " + strings.ReplaceAll(firstNonEmpty(event, "delivery"), "_", " ")
		return it
	}

	switch event {
	case "ping":
		it.Title = "Webhook connected" + suffix(repo)
		it.Body = str(doc, "zen")

	case "push":
		rawRef := str(doc, "ref")
		ref := strings.TrimPrefix(strings.TrimPrefix(rawRef, "refs/heads/"), "refs/tags/")
		it.Actor = firstNonEmpty(str(doc, "pusher", "name"), it.Actor)
		it.URL = firstNonEmpty(str(doc, "compare"), it.URL)
		switch {
		case str(doc, "deleted") == "true":
			it.Title = fmt.Sprintf("%s deleted %s%s", it.Actor, ref, suffix(repo))
		default:
			count := 0
			if commits, ok := doc["commits"].([]any); ok {
				count = len(commits)
			}
			noun := "commits"
			if count == 1 {
				noun = "commit"
			}
			verb := "pushed"
			if str(doc, "forced") == "true" {
				verb = "force-pushed"
				it.Severity = "warning"
			}
			it.Title = fmt.Sprintf("%s %s %d %s to %s%s", it.Actor, verb, count, noun, ref, suffix(repo))
			it.Body = firstLine(str(doc, "head_commit", "message"))
		}

	case "pull_request":
		verb := action
		if action == "closed" && str(doc, "pull_request", "merged") == "true" {
			verb = "merged"
		}
		it.Title = fmt.Sprintf("PR #%s %s: %s%s", str(doc, "pull_request", "number"), verb, str(doc, "pull_request", "title"), suffix(repo))
		it.Body = firstLine(str(doc, "pull_request", "body"))
		it.URL = firstNonEmpty(str(doc, "pull_request", "html_url"), it.URL)

	case "issues":
		it.Title = fmt.Sprintf("Issue #%s %s: %s%s", str(doc, "issue", "number"), action, str(doc, "issue", "title"), suffix(repo))
		it.Body = firstLine(str(doc, "issue", "body"))
		it.URL = firstNonEmpty(str(doc, "issue", "html_url"), it.URL)

	case "issue_comment":
		it.Title = fmt.Sprintf("Comment on #%s: %s%s", str(doc, "issue", "number"), str(doc, "issue", "title"), suffix(repo))
		it.Body = firstLine(str(doc, "comment", "body"))
		it.URL = firstNonEmpty(str(doc, "comment", "html_url"), it.URL)

	case "release":
		it.Title = fmt.Sprintf("Release %s %s%s", firstNonEmpty(str(doc, "release", "tag_name"), str(doc, "release", "name")), action, suffix(repo))
		it.Body = firstLine(str(doc, "release", "name"))
		it.URL = firstNonEmpty(str(doc, "release", "html_url"), it.URL)

	case "workflow_run":
		name := str(doc, "workflow_run", "name")
		branch := str(doc, "workflow_run", "head_branch")
		conclusion := str(doc, "workflow_run", "conclusion")
		it.URL = firstNonEmpty(str(doc, "workflow_run", "html_url"), it.URL)
		it.Body = firstLine(str(doc, "workflow_run", "display_title"))
		switch {
		case action == "completed" && (conclusion == "failure" || conclusion == "timed_out" || conclusion == "startup_failure"):
			it.Severity = "error"
			it.Title = fmt.Sprintf("Workflow %s failed on %s%s", name, branch, suffix(repo))
		case action == "completed" && conclusion == "cancelled":
			it.Severity = "warning"
			it.Title = fmt.Sprintf("Workflow %s cancelled on %s%s", name, branch, suffix(repo))
		case action == "completed":
			it.Title = fmt.Sprintf("Workflow %s %s on %s%s", name, firstNonEmpty(conclusion, "completed"), branch, suffix(repo))
		default:
			it.Title = fmt.Sprintf("Workflow %s %s on %s%s", name, action, branch, suffix(repo))
		}

	default:
		it.Title = strings.TrimSpace(fmt.Sprintf("GitHub %s %s%s", strings.ReplaceAll(firstNonEmpty(event, "event"), "_", " "), action, suffix(repo)))
	}
	return it
}

// ---- Sentry ------------------------------------------------------------------

func normaliseSentry(resource string, doc map[string]any) Item {
	action := str(doc, "action")
	it := Item{Kind: firstNonEmpty(resource, "sentry"), Actor: str(doc, "actor", "name")}
	if doc == nil {
		it.Title = "Sentry " + strings.ReplaceAll(firstNonEmpty(resource, "delivery"), "_", " ")
		return it
	}

	switch resource {
	case "event_alert":
		// An issue-alert rule fired on an event.
		rule := str(doc, "data", "triggered_rule")
		it.Kind = "issue_alert"
		it.Title = "Alert: " + firstNonEmpty(str(doc, "data", "event", "title"), str(doc, "data", "event", "message"), rule)
		it.Body = str(doc, "data", "event", "culprit")
		if rule != "" {
			it.Body = strings.TrimSpace(it.Body + "\nRule: " + rule)
		}
		it.Severity = levelSeverity(str(doc, "data", "event", "level"), "error")
		it.URL = firstNonEmpty(str(doc, "data", "event", "web_url"), str(doc, "data", "event", "issue_url"))

	case "issue":
		it.Title = fmt.Sprintf("Issue %s: %s", action, str(doc, "data", "issue", "title"))
		it.Body = str(doc, "data", "issue", "culprit")
		it.URL = firstNonEmpty(str(doc, "data", "issue", "web_url"), str(doc, "data", "issue", "permalink"))
		if action == "created" || action == "unresolved" {
			it.Severity = levelSeverity(str(doc, "data", "issue", "level"), "error")
		}

	case "error":
		it.Title = "Error: " + firstNonEmpty(str(doc, "data", "error", "title"), str(doc, "data", "error", "message"))
		it.Body = str(doc, "data", "error", "culprit")
		it.Severity = levelSeverity(str(doc, "data", "error", "level"), "error")
		it.URL = str(doc, "data", "error", "web_url")

	case "metric_alert":
		it.Title = "Metric alert " + action + ": " + firstNonEmpty(str(doc, "data", "description_title"), str(doc, "data", "metric_alert", "alert_rule", "name"))
		it.Body = str(doc, "data", "description_text")
		it.URL = str(doc, "data", "web_url")
		switch action {
		case "critical":
			it.Severity = "error"
		case "warning":
			it.Severity = "warning"
		}

	case "installation":
		it.Title = "Sentry integration " + firstNonEmpty(action, "updated")

	default:
		// The legacy Sentry "webhooks" plugin shape, or something new: read
		// what is commonly there before falling back to a plain title.
		if t := firstNonEmpty(str(doc, "message"), str(doc, "event", "title")); t != "" {
			it.Title = t
			it.Body = str(doc, "culprit")
			it.URL = str(doc, "url")
			it.Severity = levelSeverity(str(doc, "level"), "")
		} else {
			it.Title = strings.TrimSpace("Sentry " + strings.ReplaceAll(firstNonEmpty(resource, "event"), "_", " ") + " " + action)
		}
	}
	return it
}

// ---- generic -------------------------------------------------------------------

// normaliseGeneric reads the documented {title, body, kind, severity, actor,
// url, external_id} shape, plus the common aliases other senders use.
func normaliseGeneric(doc map[string]any, body []byte) Item {
	if doc == nil {
		// Not a JSON object: keep the text, so it is still readable.
		return Item{Kind: "event", Body: string(body)}
	}
	it := Item{
		Kind:       firstNonEmpty(str(doc, "kind"), str(doc, "type"), str(doc, "event"), "event"),
		Title:      firstNonEmpty(str(doc, "title"), str(doc, "subject"), str(doc, "summary")),
		Body:       firstNonEmpty(str(doc, "body"), str(doc, "text"), str(doc, "message"), str(doc, "description")),
		Actor:      firstNonEmpty(str(doc, "actor"), str(doc, "author"), str(doc, "user")),
		URL:        firstNonEmpty(str(doc, "url"), str(doc, "link")),
		ExternalID: str(doc, "external_id"),
	}
	it.Severity = levelSeverity(firstNonEmpty(str(doc, "severity"), str(doc, "level")), "info")
	return it
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
