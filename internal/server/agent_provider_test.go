package server

/*
Bring-your-own-agent research over MCP, end to end (the "no model is
configured" fix). The deployment has a search service and a page reader but
no model key, so a workspace left on auto is in agent mode: start_research
collects the pages on the server and then hands the summary to the caller's
agent. A fake agent — this test — holds an MCP token, takes the task with
agent_tasks, answers with submit_research, closes it with complete_task, and
get_research shows the run summarised by it.
*/

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func fakeWeb(t *testing.T) (search, reader string) {
	t.Helper()
	pages := map[string]string{
		"https://news.example.com/nile-pay": "# Nile Pay raises $12 million\n\nNile Pay has raised $12 million in a Series A round to grow its fintech payments business in Egypt.\nThe fintech company is hiring 40 engineers in Cairo to build its merchant platform.",
		"https://jobs.example.com/nile-pay": "# Careers at Nile Pay\n\nNile Pay is hiring backend engineers in Cairo to scale fintech payments across Egypt.",
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		type res struct {
			URL     string `json:"url"`
			Title   string `json:"title"`
			Content string `json:"content"`
		}
		out := struct {
			Results []res `json:"results"`
		}{}
		for u := range pages {
			out.Results = append(out.Results, res{URL: u, Title: "Nile Pay", Content: "Nile Pay is hiring engineers in Cairo."})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(s.Close)
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		md, ok := pages[in.URL]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]string{"markdown": md}})
	}))
	t.Cleanup(rd.Close)
	return s.URL, rd.URL
}

func TestAgentProviderOverMCP(t *testing.T) {
	search, reader := fakeWeb(t)
	t.Setenv("SEARXNG_URL", search)
	t.Setenv("FIRECRAWL_URL", reader)
	t.Setenv("AI_PROVIDERS", "")
	_, base := bootAudit(t)

	owner := newClient(t, base)
	owner.must("POST", "/api/auth/register", map[string]string{"email": "agent-owner-" + rnd() + "@mail.example", "password": "correct-horse-1"}, 201)
	ws := owner.must("POST", "/api/me/workspaces", map[string]string{"name": "Agent " + rnd()}, 201)["workspace"].(map[string]any)
	owner.ws = ws["id"].(string)

	out := owner.must("GET", "/api/w/agent-provider", nil, 200)
	if prov := obj(out["provider"]); prov["mode"] != "auto" || prov["effective"] != "agent" || prov["can_summarise"] != false {
		t.Fatalf("a deployment with no model key defaults to the agent: %v", prov)
	}
	if p, _ := out["prompt"].(string); !strings.Contains(p, "agent_tasks") || !strings.Contains(p, "submit_research") {
		t.Fatalf("the page shows the prompt to give the agent: %q", p)
	}

	tk := owner.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "fake agent", "scopes": []string{"research:run", "research:read"}}, 201)
	agent := newClient(t, base)
	agent.bearer = tk["plaintext"].(string)
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		code, text, isErr := mcpCall(agent, "/api/mcp", tool, args)
		if code != 200 || isErr {
			t.Fatalf("%s: %d %s", tool, code, text)
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("%s: %v %s", tool, err, text)
		}
		return out
	}

	started := call("start_research", map[string]any{"topic": "fintech companies hiring engineers in Egypt"})
	runID, _ := started["run_id"].(string)
	if runID == "" || started["provider"] != "agent" || started["agent_summary"] != true {
		t.Fatalf("start_research: %v", started)
	}

	who := map[string]any{"name": "fake-agent", "model": "test-1"}
	var task map[string]any
	deadline := time.Now().Add(30 * time.Second)
	for task == nil && time.Now().Before(deadline) {
		got := call("agent_tasks", map[string]any{"kind": "summary", "agent": who})
		for _, b := range arr(got["tasks"]) {
			if tk := obj(obj(b)["task"]); tk["run_id"] == runID {
				task = obj(b)
			}
		}
		if task == nil {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if task == nil {
		t.Fatal("no summary task appeared for the run")
	}
	taskID := obj(task["task"])["id"].(string)
	pages := arr(task["pages"])
	if len(pages) == 0 || !strings.Contains(obj(pages[0])["content"].(string), "Nile Pay") {
		t.Fatalf("the task carries the collected pages: %v", task["pages"])
	}
	if obj(task["submit"])["tool"] != "submit_research" {
		t.Fatalf("the task says how to answer: %v", task["submit"])
	}

	// Closing before answering is refused.
	if code, text, isErr := mcpCall(agent, "/api/mcp", "complete_task", map[string]any{"task_id": taskID, "agent": who}); code != 200 || !isErr {
		t.Fatalf("complete_task with no summary: %d %v %s", code, isErr, text)
	}
	call("submit_research", map[string]any{"task_id": taskID, "agent": who,
		"answer":   "Nile Pay raised $12 million and is hiring 40 engineers in Cairo [1].",
		"insights": []string{"Hiring follows the Series A"}})
	call("complete_task", map[string]any{"task_id": taskID, "agent": who})

	got := call("get_research", map[string]any{"run_id": runID})
	run := obj(got["run"])
	if run == nil {
		run = got
	}
	if run["status"] != "ok" || !strings.Contains(run["summary"].(string), "40 engineers") || run["model"] != "agent:fake-agent/test-1" {
		t.Fatalf("the run is summarised by the agent: status %v model %v summary %v", run["status"], run["model"], run["summary"])
	}
	for _, s := range arr(run["stages"]) {
		if st := obj(s); st["stage"] == "summarise" && st["status"] != "done" {
			t.Fatalf("summarise stage: %v", st)
		}
	}

	// Another workspace's token sees no task of this one.
	other := newClient(t, base)
	other.must("POST", "/api/auth/register", map[string]string{"email": "agent-other-" + rnd() + "@mail.example", "password": "correct-horse-1"}, 201)
	ows := other.must("POST", "/api/me/workspaces", map[string]string{"name": "Other " + rnd()}, 201)["workspace"].(map[string]any)
	other.ws = ows["id"].(string)
	otk := other.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "other agent", "scopes": []string{"research:run", "research:read"}}, 201)
	intruder := newClient(t, base)
	intruder.bearer = otk["plaintext"].(string)
	if code, text, isErr := mcpCall(intruder, "/api/mcp", "submit_research", map[string]any{"task_id": taskID, "answer": "hijack"}); code != 200 || !isErr {
		t.Fatalf("another workspace answered this task: %d %v %s", code, isErr, text)
	}
}
