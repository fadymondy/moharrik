# Moharrik redesign — from CRM to signal engine (2026-10)

FIND → UNDERSTAND → ENGAGE → AUTOMATE → CONVERT.
This is an information-architecture and product redesign on top of what is built. Nothing
working is removed; nothing is shown that the backend cannot back. Each row below says what
exists today, so every screen is designed against real data.

## 1. Capability audit (2026-10-05, main @ e7c3012)

| Area | Exists | Missing (must be built before it is shown) |
|---|---|---|
| Today numbers | `GET /api/w/overview` → `today` (leads by status, leads today, unread conversations, unread notifications, live research) | "signals", "actions done", "needs attention" aggregates |
| Activity stream | `audit_log` (every change, actor, via, entity), SSE `/api/w/events` (conversation, message, lead, run, rules, schedule, trends, notification) | a stored, readable workspace activity feed |
| Scoring | ICP fit score 0–100 (`icp_scores`) on contacts/companies, prospect score | **intent / engagement score** — none. "Hot lead" must be derived from real events (recent inbound + open lead/deal), never a fake score |
| Customers | contacts, companies, lifecycle + history, identities, AI context (`/context/ask`, reports), deals table, timeline (activities + deals' activities + conversations) | unified contact timeline with deal stage, lifecycle, lead, campaign and automation events (`company_timeline` merges some, MCP only) |
| Opportunities | deals (`value_minor`, currency, stage kinds open/won/lost, stage events), prospects (= research discoveries, scored), leads (UTM) | an "Opportunities" view combining them; at-risk detection |
| Research | runs with `stages` jsonb (plan/search/read/extract/summarise), found/read/evidence counts, schedules, budget, discovery → prospects, company suggestions | a "qualified / high-fit" count per run (prospect scores give it for discovery) |
| Inbox | list + thread, channel tabs with counts, status open/snoozed/closed, unread/favourites/groups/labels, assign, snooze, close, canned replies, templates, attachments, voice, CRM strip (link/create contact, open deal) | right-hand intelligence panel (data exists: contact, company, ICP, deals, lifecycle, activities), AI summary/draft/translate for conversations, "pending" status, author kinds (bot/automation) |
| Inbox bug | — | **email previews show raw CSS**: `stripHTML` keeps `<style>` text (internal/inbox/email.go) |
| Automation | workflows = linear browser step lists on an xyflow canvas, versions, layout; rules (15 triggers, 14 actions, conditions, runs with per-condition/per-action results); presets; schedules; browser runs with per-step status/summary/result/error/duration/screenshot; kill switch, limits | branching workflows, natural-language → draft workflow, run retry, run analytics |
| Journeys | rules with customer triggers (lead.created, deal.stage_changed, contact.lifecycle_changed…) | a journey model/view — first version = rules on customer triggers, shown as a lifecycle |
| Campaigns | email/WhatsApp, recipients, sent/failed/skipped | opens/clicks/replies tracking |
| Insights / reports | CRM report (pipeline, won/lost, conversion, lead sources, first response), GA4, Search Console, YouTube | question-led insight cards |
| Notifications | kinds, severity, read, saved, webhook + collected sources | snooze, resolve |
| Integrations | Google (GA4, GSC, YouTube, Contacts), CircleXO hub (`GET /api/w/circlexo`: plan, features, usage), federated MCP | Matjar / Mahaam / Zekra connections — **not built, not shown** |
| MCP | ~215 tools, scopes (read/write/manage/run), per-role narrowing, tokens, OAuth grants, `confirm` on writing workflows | per-tool read/write/external-impact annotations to explain permissions |
| Ask Moharrik | per-contact AI ask + reports | **workspace assistant** — not built; no entry point until it is |
| Design system | Nasaq (`@fadymondy/nasaq/web`): Card, Badge tones, StatusDot, Stepper, Timeline, ActivityTimeline, ProgressRing, Meter, StatCard, Sparkline, AiSummary/AiInsightCard, EmptyState… tokens `--nq-success/info/warning/danger`, violet tag for AI | — |

## 2. Navigation (target)

PRIMARY: Home · Customers · Inbox · Opportunities · Research
AUTOMATE: Journeys · Automations · Campaigns · Schedules
UNDERSTAND: Insights · Reports
CONNECT: Channels · Integrations · MCP
BOTTOM: Team · Settings

Everything else stays reachable in context (presets/rules/runs/browsers under Automations;
canned replies/templates/channel config under Inbox and Channels; companies/leads/deals under
Customers and Opportunities; notifications from the bell). Routes do not change — the nav does.

## 3. Phases (Mahaam cards under the redesign epic)

1. **Navigation IA** — the grouping above, no route changes, en/ar, command palette follows.
2. **Inbox preview fix** — strip `<style>/<script>/<head>`/comments, decode entities, clean
   existing previews at read time.
3. **Home** — new `GET /api/w/home`: today numbers, attention signals (derived, explained),
   "Moharrik is working" (Discover/Listen/Understand/Act from real runs and channels),
   activity stream (audit log + messages + runs), deals as journeys, connected business.
4. **Inbox intelligence panel** — contact, company, fit score, lifecycle journey, deal,
   recent activity, actions that exist (link, create contact, open deal, assign, snooze).
5. **Customer 360** — unified contact timeline endpoint + journey + opportunities +
   conversations + automations that touched the contact.
6. **Opportunities** — board (deals) / list / signals (prospects + leads + at-risk deals).
7. **Research flagship** — describe a target → run with live stages → found/qualified/high-fit →
   accept to CRM.
8. **Automation overview + run detail** — per-workflow header metrics from runs, step
   input/output/error/screenshot, re-run.
9. **Journeys** — rules on customer triggers presented as lifecycle journeys.
10. **Notifications resolve/snooze**, **MCP permission explainer**, **Insights as questions**.
11. **Ask Moharrik** — workspace assistant over the MCP tools with the same scopes and
    confirmations; only then the Home/Inbox entry points.
12. **AI in Inbox** — summarize / draft / translate for conversations (never auto-send).
13. **Natural-language → draft workflow**, run retry, branching workflows.
