# Research with your own AI assistant (agent provider)

Moharrik can research companies and topics without a model key of its own. When
no model is configured, the AI assistant you already use (Claude, Codex,
Cursor, …) does the thinking over MCP. Moharrik keeps doing everything else:
it queues the work, validates the results, dedupes them against the CRM,
scores them against your ICP and stores them with provenance.

This fixes "no model is configured, so the pages were collected but not
summarised". That summary is now a task for your assistant.

## Provider modes

Each workspace has a research provider (**Research → Run via your AI
assistant**, or `set_research_provider` over MCP):

| Mode     | Who searches and reads                                  | Who summarises / judges |
| -------- | ------------------------------------------------------- | ----------------------- |
| `auto`   | the server if it has a model key, otherwise your agent  | same                    |
| `server` | the server (falls back to `agent` if it cannot search)  | the server's model      |
| `agent`  | the server when it can, otherwise your agent            | your agent              |

`auto` is the default. On a deployment with a search service and a page
reader but no model key, `auto` resolves to **agent**:

- **Research** (`start_research` or the Research page): the server still
  searches and reads the pages. The summarise stage then stays *pending* and a
  `summary` task waits for your assistant.
- **Without a search service**, a research question becomes a `research` task.
  Your assistant does the searching itself.
- **Prospect discovery and the Opportunity Finder** become `discovery` tasks.
- **A run that ended without a summary** can be handed over again with
  "Summarise via AI assistant" or `summarise_research`.

With a model key set and the mode on `server` or `auto`, nothing changes:
server mode works as before.

## Setup

1. Connect your assistant to Moharrik's MCP server, at the URL shown in
   **Settings → MCP**. For a token, use the `research:run` and
   `research:read` scopes, plus `contacts:read` so the brief can list your
   CRM companies for dedupe.
2. Give it the prompt below. The Research and Prospects pages show the same
   prompt, already filled in with your workspace name, with a copy button.

## The prompt

Replace `<workspace>` with your workspace name.

```text
You are the AI for my Moharrik workspace "<workspace>" (it has no model key of its own). Use the Moharrik MCP tools:
1. Call agent_tasks with agent {name, model} set to who you are, and do every task it hands you:
   - summary: Moharrik already searched and read the pages. Answer task.task.topic from task.pages only, citing them as [source], and send it with submit_research(task_id, answer, insights).
   - research: research the question with your own web search and page reading, then submit_research with the answer and the findings (each a public URL and a sentence copied exactly from it).
   - discovery: as in step 2.
   Close each task with complete_task (or fail_task with the reason). Repeat until agent_tasks returns nothing.
2. Then run my Opportunity Finder: call opportunity_tasks. For each task run its search_queries (and your own variations), judge every company against task.icp, skip anything in task.dedupe, and keep only companies with a real, recent signal. Submit them with submit_opportunities: every candidate needs at least one public URL and a sentence copied exactly from that page. Name the person to talk to only if a public page names them; never guess emails. Then complete_task.
3. Show me the result: the summaries (get_research) and the top prospects (find_opportunities). Do not contact anyone — openers are drafts for me to review.
```

The source of truth for this prompt is `AgentPrompt` in
`internal/opportunities/agent.go`. The `/api/w/agent-provider` endpoint
returns it filled in.

## Tools

All tools need the `research:run` scope. Summary tasks also need `research:read`,
because they hand over the pages a run collected. Without `contacts:read` /
`leads:read`, results do not include the ids of matching CRM records.

| Tool                                           | What it does                                                                                                                                                                                                                                                        |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `agent_tasks`                                  | Leases waiting tasks to you for 30 minutes. Each one carries a full brief: the collected pages and evidence for `summary`, the ICP, search queries, signal meanings, dedupe list and output schema for `discovery`, and how to answer (`submit.tool`). Filter with `kind`. |
| `opportunity_tasks`                            | The same queue. When nothing is waiting, it queues the ICP's next discovery query (`create_if_empty`, default true).                                                                                                                                                 |
| `submit_research`                              | Answers a `summary` or `research` task: `answer` (cite sources as [n]), `insights`, and for `research` tasks `findings` (a URL plus a sentence quoted exactly from it).                                                                                                |
| `submit_opportunities` (alias `submit_prospects`) | Submits companies for a `discovery` task. Each needs at least one public evidence URL with an exact quote. Results are deduped against the CRM and earlier finds, then scored against the ICP. An opener is stored as a draft only.                                |
| `complete_task` / `fail_task`                  | Closes a task. A summary task cannot complete before `submit_research`, nor a research task before it cites at least one page. Failing it (or cancelling it in the UI) marks the summarise stage *skipped*, and you can ask for it again later.                                                                              |
| `summarise_research`                           | Queues a summary task for a run that ended without a summary.                                                                                                                                                                                                         |
| `set_research_provider`                        | Sets the workspace mode: `auto`, `server` or `agent`.                                                                                                                                                                                                                 |

Results land where server-side research puts its own. So `get_research`,
`find_opportunities`, `get_opportunity`, `find_evidence`, `research_overview`
and `prospect_digest` read them unchanged.

## Guarantees

- **Provenance.** Everything an agent submits has `author_kind = 'agent'` and
  model `agent:<name>/<model>`. Agent evidence confidence is capped.
- **Validation.** Evidence URLs must be public: no IPs, no single-label or
  private hosts. Quotes must be real sentences. Unknown signal types are
  rejected, and markup is stripped.
- **Limits per token.** At most 25 candidates and 60 findings per call, and
  60 submissions or 400 items per hour. Each submission is counted before anything
  is written, so failed or concurrent calls count too.
- **Isolation.** A token only sees and answers its own workspace's tasks.
- **No outreach.** Nothing is sent to anyone. Openers are drafts for a human
  to review.
