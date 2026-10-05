package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/research"
)

/*
The permission explainer (MH-1069) is derived from the registry; these tests are
what keeps it honest. A new tool lands in exactly one list, a confirmation rule
is carried by a tool that can change something and has Arabic text, and a
"never" is shown only while no registered tool contradicts it.
*/

var arabic = regexp.MustCompile(`[\x{0600}-\x{06FF}]`)

func TestPermissionsPartitionsTheRegistry(t *testing.T) {
	tools := UserTools(&research.Engine{})
	doc := Permissions(tools, "en")
	if doc.ToolCount != len(tools) {
		t.Fatalf("tool_count %d, registry has %d", doc.ToolCount, len(tools))
	}
	seen := map[string]int{}
	for _, s := range doc.Scopes {
		for _, list := range [][]PermTool{s.CanRead, s.CanChange, s.NeedsConfirm} {
			for _, pt := range list {
				seen[pt.Name]++
				if pt.Summary == "" {
					t.Errorf("%s has no summary", pt.Name)
				}
			}
		}
	}
	for _, tool := range tools {
		if seen[tool.Name] != 1 {
			t.Errorf("tool %s is listed %d times, want exactly once", tool.Name, seen[tool.Name])
		}
	}
	// Each list obeys the registry's own flags.
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	for _, s := range doc.Scopes {
		for _, pt := range s.CanRead {
			if ChangesData(byName[pt.Name]) {
				t.Errorf("%s is a change tool listed under can read", pt.Name)
			}
		}
		for _, pt := range s.CanChange {
			if !ChangesData(byName[pt.Name]) || byName[pt.Name].Confirm != "" {
				t.Errorf("%s misfiled under can change", pt.Name)
			}
		}
		for _, pt := range s.NeedsConfirm {
			if byName[pt.Name].Confirm == "" || pt.Confirm == "" {
				t.Errorf("%s under needs confirmation without a Confirm rule", pt.Name)
			}
		}
	}
}

func TestPermissionsFlagsAreConsistent(t *testing.T) {
	for _, tool := range UserTools(&research.Engine{}) {
		if tool.ReadOnly && strings.HasSuffix(tool.Scope, ":read") {
			t.Errorf("%s: ReadOnly is for a tool in a WRITE scope that only reads; its scope already says read", tool.Name)
		}
		if tool.Confirm != "" && !ChangesData(tool) {
			t.Errorf("%s: a read-only tool cannot need a confirmation", tool.Name)
		}
		if tool.Confirm != "" && confirmAR[tool.Confirm] == "" {
			t.Errorf("%s: its Confirm rule has no Arabic text in confirmAR", tool.Name)
		}
		// A tool that takes a confirm flag asks for confirmation: say so in the registry.
		if props, _ := tool.InputSchema["properties"].(map[string]any); props != nil {
			if _, ok := props["confirm"]; ok && tool.Confirm == "" {
				t.Errorf("%s takes a confirm argument but has no Confirm rule", tool.Name)
			}
		}
	}
}

func TestPermissionsNeverIsTrue(t *testing.T) {
	tools := UserTools(&research.Engine{})
	have := map[string]bool{}
	for _, tool := range tools {
		have[tool.Name] = true
	}
	for _, n := range neverEntries {
		for _, name := range n.Forbidden {
			if have[name] {
				t.Errorf("%q is promised as never possible, but tool %s exists", n.Label[0], name)
			}
		}
	}
	for _, lang := range []string{"en", "ar"} {
		if got := len(Permissions(tools, lang).Never); got != len(neverEntries) {
			t.Errorf("%s: %d of %d never entries shown", lang, got, len(neverEntries))
		}
	}
}

func TestPermissionsArabic(t *testing.T) {
	doc := Permissions(UserTools(&research.Engine{}), "ar")
	if doc.Lang != "ar" {
		t.Fatalf("lang %q", doc.Lang)
	}
	for _, s := range doc.Scopes {
		if !arabic.MatchString(s.Description) {
			t.Errorf("%s: scope description not Arabic: %q", s.Scope, s.Description)
		}
		for _, pt := range append(append(append([]PermTool{}, s.CanRead...), s.CanChange...), s.NeedsConfirm...) {
			if !arabic.MatchString(pt.Summary) {
				t.Errorf("%s: summary not Arabic: %q", pt.Name, pt.Summary)
			}
		}
		for _, pt := range s.NeedsConfirm {
			if !arabic.MatchString(pt.Confirm) {
				t.Errorf("%s: confirm not Arabic: %q", pt.Name, pt.Confirm)
			}
		}
	}
	for _, n := range doc.Never {
		if !arabic.MatchString(n.Label) {
			t.Errorf("never %s not Arabic: %q", n.ID, n.Label)
		}
	}
}

func TestPermissionsKnownTools(t *testing.T) {
	doc := Permissions(UserTools(&research.Engine{}), "en")
	where := map[string]string{}
	for _, s := range doc.Scopes {
		for _, pt := range s.CanRead {
			where[pt.Name] = "read"
		}
		for _, pt := range s.CanChange {
			where[pt.Name] = "change"
		}
		for _, pt := range s.NeedsConfirm {
			where[pt.Name] = "confirm"
		}
	}
	for name, want := range map[string]string{
		"list_contacts": "read", "get_deal": "read", "smtp_services": "read", "test_rule": "read",
		"create_contact": "change", "move_deal": "change", "resolve_notification": "change", "snooze_notification": "change",
		"send_campaign": "change", "start_workflow": "confirm", "browser_plugin_run": "confirm", "send_whatsapp": "confirm",
	} {
		if where[name] != want {
			t.Errorf("%s is %q, want %q", name, where[name], want)
		}
	}
}

func TestPermissionsEndpoint(t *testing.T) {
	tools := UserTools(&research.Engine{})
	for _, lang := range []string{"en", "ar"} {
		rec := httptest.NewRecorder()
		permissionsHandler(tools).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mcp/permissions?lang="+lang, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", lang, rec.Code)
		}
		var doc PermissionsDoc
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.Lang != lang || len(doc.Scopes) == 0 {
			t.Fatalf("%s: bad body (%v) lang=%q scopes=%d", lang, err, doc.Lang, len(doc.Scopes))
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("explainer must not be cached")
		}
	}
}
