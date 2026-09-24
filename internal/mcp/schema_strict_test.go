package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// MH-557: ChatGPT's connector refused Moharrik with "There was a problem
// connecting" because four tools published "properties": null. Walk every tool
// the server publishes, as JSON, and refuse anything a strict client rejects.
func TestPublishedToolSchemasAreStrict(t *testing.T) {
	s := &server{tools: UserTools(nil)}
	raw, err := json.Marshal(s.schemas())
	if err != nil {
		t.Fatal(err)
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools) == 0 {
		t.Fatal("no tools published")
	}
	var walk func(v any, path, tool string)
	walk = func(v any, path, tool string) {
		m, ok := v.(map[string]any)
		if !ok {
			if arr, ok := v.([]any); ok {
				for _, e := range arr {
					walk(e, path+"[]", tool)
				}
			}
			return
		}
		if m["type"] == "object" {
			if p, has := m["properties"]; has && p == nil {
				t.Errorf("%s: %s.properties is null", tool, path)
			}
		}
		if m["type"] == "array" && m["items"] == nil {
			t.Errorf("%s: %s is an array without items", tool, path)
		}
		for k, e := range m {
			if e == nil && k != "default" && k != "const" {
				t.Errorf("%s: %s.%s is null", tool, path, k)
			}
			walk(e, path+"."+k, tool)
		}
	}
	for _, tl := range tools {
		name, _ := tl["name"].(string)
		if name == "" || len(name) > 64 || strings.ContainsAny(name, " ./") {
			t.Errorf("bad tool name %q", name)
		}
		in, ok := tl["inputSchema"].(map[string]any)
		if !ok || in["type"] != "object" {
			t.Errorf("%s: inputSchema is not an object schema", name)
			continue
		}
		if _, ok := in["properties"].(map[string]any); !ok {
			t.Errorf("%s: inputSchema.properties is not an object", name)
		}
		walk(in, "input", name)
	}
}
