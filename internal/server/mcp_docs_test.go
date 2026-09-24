package server

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

/*
TestMCPDocsMatchTheServer (MH-428): the public tool reference an anonymous
visitor reads at /api/mcp/docs lists exactly the tools a real token gets from
tools/list on the running server, with the scope each one is served under, and
names the server's own canonical URL as the one to connect to. Docs and server
are built from one registry; this proves it through the whole stack (guard
included, which must let the docs through without a session).
*/
func TestMCPDocsMatchTheServer(t *testing.T) {
	sf := bootSurface(t)
	anon := newClient(t, sf.srv.URL)

	type catalog struct {
		Lang      string `json:"lang"`
		ServerURL string `json:"server_url"`
		ToolCount int    `json:"tool_count"`
		Groups    []struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Scope       string `json:"scope"`
			} `json:"tools"`
		} `json:"groups"`
	}
	fetch := func(lang string) catalog {
		code, body := anon.do("GET", "/api/mcp/docs?lang="+lang, nil)
		if code != 200 {
			t.Fatalf("anonymous GET /api/mcp/docs?lang=%s: %d %s", lang, code, body)
		}
		var c catalog
		if err := json.Unmarshal(body, &c); err != nil {
			t.Fatal(err)
		}
		return c
	}

	// What a token with every scope is actually served.
	agent, _ := mint(t, sf.alice, "docs check", allUserScopes())
	code, body := agent.do("POST", "/api/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if code != 200 {
		t.Fatalf("tools/list: %d %s", code, body)
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	served := map[string]string{}
	for _, tool := range listed.Result.Tools {
		served[tool.Name] = tool.Description
	}

	for _, lang := range []string{"en", "ar"} {
		c := fetch(lang)
		if c.Lang != lang {
			t.Errorf("lang=%s answered in %q", lang, c.Lang)
		}
		if c.ServerURL != sf.srv.URL+"/api/mcp" {
			t.Errorf("%s: server_url = %q, want this server's %q", lang, c.ServerURL, sf.srv.URL+"/api/mcp")
		}
		documented := []string{}
		for _, g := range c.Groups {
			for _, tool := range g.Tools {
				documented = append(documented, tool.Name)
				desc, ok := served[tool.Name]
				if !ok {
					t.Errorf("%s: the docs list %q, which tools/list does not serve", lang, tool.Name)
					continue
				}
				// tools/list appends "(scope: …)" to the description; the docs must agree on the scope.
				if !strings.HasSuffix(desc, "(scope: "+tool.Scope+")") {
					t.Errorf("%s: %q is documented under %q but served as %q", lang, tool.Name, tool.Scope, desc)
				}
			}
		}
		if len(documented) != len(served) || c.ToolCount != len(served) {
			sort.Strings(documented)
			t.Errorf("%s: %d tools documented (tool_count %d), %d served: %v", lang, len(documented), c.ToolCount, len(served), documented)
		}
	}
}
