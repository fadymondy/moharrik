package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fadymondy/moharrik/internal/research"
)

/*
TestEveryToolIsDocumented is the drift guard for the MCP reference (MH-428):
a tool added to UserTools() without its docs — the Arabic text, an example
call and its result — fails here, and so does an example that no longer fits
the tool's arguments, an argument description with no Arabic, or a doc left
behind for a tool that is gone.
*/
func TestEveryToolIsDocumented(t *testing.T) {
	tools := UserTools(&research.Engine{})
	docs, args := toolDocs(), argAR()
	registered := map[string]bool{}

	for _, tool := range tools {
		registered[tool.Name] = true
		doc, ok := docs[tool.Name]
		if !ok {
			t.Errorf("tool %q has no docs: add it to a docs_*.go map (Arabic text, example arguments, example result)", tool.Name)
			continue
		}
		if len([]rune(strings.TrimSpace(doc.AR))) < 15 {
			t.Errorf("tool %q: its Arabic description is missing or too short (%q)", tool.Name, doc.AR)
		} else if !hasArabic(doc.AR) {
			t.Errorf("tool %q: its Arabic description has no Arabic in it (%q)", tool.Name, doc.AR)
		}
		if strings.TrimSpace(doc.Result) == "" {
			t.Errorf("tool %q has no example result", tool.Name)
		} else if r := strings.TrimSpace(doc.Result); strings.HasPrefix(r, "{") || strings.HasPrefix(r, "[") {
			if !json.Valid([]byte(r)) {
				t.Errorf("tool %q: its example result looks like JSON but does not parse: %s", tool.Name, r)
			}
		}
		checkExample(t, tool, doc.Example)
		if doc.Group != "" {
			known := false
			for _, g := range docGroups {
				known = known || g.ID == doc.Group
			}
			if !known {
				t.Errorf("tool %q: its docs put it in section %q, which does not exist", tool.Name, doc.Group)
			}
		}

		props, _ := tool.InputSchema["properties"].(map[string]any)
		for name, raw := range props {
			p, _ := raw.(map[string]any)
			if d, _ := p["description"].(string); d != "" && args[d] == "" {
				t.Errorf("tool %q argument %q: no Arabic for its description %q (add it to an argsAR* map)", tool.Name, name, d)
			}
		}
	}
	for name := range docs {
		if !registered[name] {
			t.Errorf("docs describe %q, which is not a registered tool — remove them", name)
		}
	}
}

// checkExample: every argument the example passes exists and has the right
// type (and enum value), and every required argument is there.
func checkExample(t *testing.T, tool Tool, example map[string]any) {
	t.Helper()
	props, _ := tool.InputSchema["properties"].(map[string]any)
	for name, v := range example {
		raw, ok := props[name]
		if !ok {
			t.Errorf("tool %q: its example passes %q, which is not one of its arguments", tool.Name, name)
			continue
		}
		p, _ := raw.(map[string]any)
		if !typeFits(p["type"], v) {
			t.Errorf("tool %q: its example gives %q the value %v, which is not a %v", tool.Name, name, v, p["type"])
		}
		if enum := enumOf(p); enum != nil && !inEnum(enum, v) {
			t.Errorf("tool %q: its example gives %q the value %v, which is not one of %v", tool.Name, name, v, enum)
		}
	}
	for _, r := range requiredOf(tool.InputSchema) {
		if _, ok := example[r]; !ok {
			t.Errorf("tool %q: its example leaves out the required argument %q", tool.Name, r)
		}
	}
}

func typeFits(typ any, v any) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "integer":
		switch n := v.(type) {
		case int, int64:
			return true
		case float64:
			return n == float64(int64(n))
		}
		return false
	case "number":
		switch v.(type) {
		case int, int64, float64:
			return true
		}
		return false
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		switch v.(type) {
		case []any, []string, []map[string]any:
			return true
		}
		return false
	case "object":
		switch v.(type) {
		case map[string]any, map[string]string, map[string]int, map[string]float64:
			return true
		}
		return false
	}
	return true // an untyped property takes anything
}

func inEnum(enum []any, v any) bool {
	for _, e := range enum {
		if e == v {
			return true
		}
		// integer enums are declared as ints, examples may hold ints too
		if ei, ok := e.(int); ok {
			if vi, ok := v.(int); ok && ei == vi {
				return true
			}
		}
	}
	return false
}

func hasArabic(s string) bool {
	for _, r := range s {
		if r >= 0x0600 && r <= 0x06FF {
			return true
		}
	}
	return false
}

// TestEveryScopeIsInOneDocGroup: a scope no section lists would hide its tools
// from the page; one listed twice would show them twice.
func TestEveryScopeIsInOneDocGroup(t *testing.T) {
	seen := map[string]string{}
	for _, g := range docGroups {
		for _, s := range g.Scopes {
			if prev, dup := seen[s]; dup {
				t.Errorf("scope %q is in both the %q and %q sections", s, prev, g.ID)
			}
			seen[s] = g.ID
			if _, ok := userScope(s); !ok {
				t.Errorf("section %q lists %q, which is not a user scope", g.ID, s)
			}
		}
	}
	for _, s := range UserScopes {
		if seen[s.Scope] == "" {
			t.Errorf("scope %q is in no section of the reference", s.Scope)
		}
		if !hasArabic(scopeAR[s.Scope]) {
			t.Errorf("scope %q has no Arabic description in scopeAR", s.Scope)
		}
	}
}

// TestDocsCatalogListsEveryTool: the served reference has every registered
// tool exactly once, in both languages, with its example call ready to send.
func TestDocsCatalogListsEveryTool(t *testing.T) {
	tools := UserTools(&research.Engine{})
	for _, lang := range []string{"en", "ar"} {
		rec := httptest.NewRecorder()
		docsHandler(tools)(rec, httptest.NewRequest("GET", "/api/mcp/docs?lang="+lang, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: status %d", lang, rec.Code)
		}
		var cat Catalog
		if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if cat.Lang != lang || cat.ToolCount != len(tools) {
			t.Fatalf("%s: lang=%q tool_count=%d, want %d", lang, cat.Lang, cat.ToolCount, len(tools))
		}
		listed := map[string]int{}
		for _, g := range cat.Groups {
			if g.Title == "" || g.Intro == "" {
				t.Errorf("%s: section %q has no title or intro", lang, g.ID)
			}
			for _, tool := range g.Tools {
				listed[tool.Name]++
				params, _ := tool.ExampleCall["params"].(map[string]any)
				if tool.ExampleCall["method"] != "tools/call" || params["name"] != tool.Name {
					t.Errorf("%s: %s: example call is not a tools/call of itself: %v", lang, tool.Name, tool.ExampleCall)
				}
				if lang == "ar" && !hasArabic(tool.Description) {
					t.Errorf("ar: %s: description is not Arabic", tool.Name)
				}
			}
		}
		for _, tool := range tools {
			if listed[tool.Name] != 1 {
				t.Errorf("%s: %q is listed %d times, want once", lang, tool.Name, listed[tool.Name])
			}
		}
		if len(cat.Scopes) != len(UserScopes) {
			t.Errorf("%s: %d scopes, want %d", lang, len(cat.Scopes), len(UserScopes))
		}
	}
}

// TestDocsServerURL: the docs name the configured public MCP address
// (MH-516), and the app's own /api/mcp as the one older connections still use.
func TestDocsServerURL(t *testing.T) {
	t.Setenv("OAUTH_ISSUER", "")
	t.Setenv("APP_URL", "https://app.moharrik.com")
	t.Setenv("MCP_PUBLIC_URL", "https://mcp.moharrik.com/")
	cat := DocsCatalog(nil, "en")
	if cat.ServerURL != "https://mcp.moharrik.com" || cat.LegacyURL != "https://app.moharrik.com/api/mcp" {
		t.Fatalf("server_url=%q legacy_url=%q", cat.ServerURL, cat.LegacyURL)
	}
	t.Setenv("MCP_PUBLIC_URL", "")
	cat = DocsCatalog(nil, "en")
	if cat.ServerURL != "https://app.moharrik.com/api/mcp" || cat.LegacyURL != "" {
		t.Fatalf("without MCP_PUBLIC_URL: server_url=%q legacy_url=%q", cat.ServerURL, cat.LegacyURL)
	}
}
