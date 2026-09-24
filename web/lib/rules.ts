"use client"

/*
Automation rules (MH-427): the API's shapes and the hooks the Rules pages read.
Kept in their own module rather than in api.ts / queries.ts so the feature is
one place to read.
*/

import useSWR from "swr"
import useSWRInfinite from "swr/infinite"

import { api, ApiError } from "@/lib/api"

export type RuleCondition = { field: string; op: string; value?: string }
export type RuleAction = { type: string; params?: Record<string, string> }

export type Rule = {
  id: string
  name: string
  description: string
  enabled: boolean
  trigger: string
  match: "all" | "any"
  conditions: RuleCondition[]
  actions: RuleAction[]
  cron: string
  timezone: string
  next_run_at: string | null
  once_per_subject: boolean
  max_runs_per_hour: number
  run_count: number
  last_run_at: string | null
  last_status: string
  last_error: string
  has_webhook_secret: boolean
  created_by: string
  updated_by: string
  created_at: string
  updated_at: string
}

export type RuleInput = {
  name: string
  description?: string
  enabled?: boolean
  trigger: string
  match?: "all" | "any"
  conditions?: RuleCondition[]
  actions: RuleAction[]
  cron?: string
  timezone?: string
  once_per_subject?: boolean
  max_runs_per_hour?: number
}

export type EventField = { name: string; type: "string" | "number" | "bool" }
export type TriggerDef = {
  type: string
  subject: string
  fields: EventField[]
  sample: Record<string, unknown>
}
export type ActionParam = { name: string; kind: string; required?: boolean; default?: string }
export type ActionDef = { type: string; params: ActionParam[]; to_customer?: boolean }
export type RuleCatalog = {
  triggers: TriggerDef[]
  actions: ActionDef[]
  operators: string[]
  max_depth: number
  workspace_hourly_cap: number
}

export type RuleConditionResult = {
  field: string
  op: string
  value?: string
  actual: string
  passed: boolean
}
export type RuleActionResult = {
  type: string
  status: "ok" | "failed" | "skipped" | "would"
  summary: string
  error?: string
  ref?: string
}
export type RuleRunStatus =
  "ok" | "partial" | "failed" | "skipped" | "rate_limited" | "loop_blocked"
export type RuleRun = {
  id: string
  rule_id: string
  rule_name: string
  event_id: string
  trigger: string
  subject_id: string
  status: RuleRunStatus
  dry_run: boolean
  reason: string
  event: Record<string, unknown>
  conditions: RuleConditionResult[]
  actions: RuleActionResult[]
  depth: number
  tested_by: string
  started_at: string
  finished_at: string | null
}
export type RuleRunPage = { runs: RuleRun[]; next?: string }

const quiet = {
  shouldRetryOnError: (err: unknown) => !(err instanceof ApiError && err.status < 500),
  revalidateOnFocus: false,
}

export const useRuleCatalog = (ws?: string | null) =>
  useSWR<RuleCatalog>(
    ws ? ["/api/w/rules/catalog", ws] : null,
    ([p, id]: [string, string]) => api<RuleCatalog>(p, { workspaceId: id }),
    {
      ...quiet,
      revalidateIfStale: false,
    },
  )

export const useRules = (ws?: string | null) =>
  useSWR<{ rules: Rule[] }>(
    ws ? ["/api/w/rules", ws] : null,
    ([p, id]: [string, string]) => api<{ rules: Rule[] }>(p, { workspaceId: id }),
    quiet,
  )

export const useRule = (ws?: string | null, id?: string | null) =>
  useSWR<{ rule: Rule }>(
    ws && id ? [`/api/w/rules/${encodeURIComponent(id)}`, ws] : null,
    ([p, w]: [string, string]) => api<{ rule: Rule }>(p, { workspaceId: w }),
    quiet,
  )

/** The run log, newest first, a page at a time; ruleId narrows it to one rule. */
export function useRuleRuns(ws: string | null | undefined, ruleId?: string, status?: string) {
  return useSWRInfinite<RuleRunPage>(
    (i, prev: RuleRunPage | null) => {
      if (!ws) return null
      if (i > 0 && !prev?.next) return null
      const q = new URLSearchParams({ limit: "30" })
      if (ruleId) q.set("rule_id", ruleId)
      if (status) q.set("status", status)
      if (i > 0 && prev?.next) q.set("before", prev.next)
      return [`/api/w/rule-runs?${q}`, ws]
    },
    ([p, w]: [string, string]) => api<RuleRunPage>(p, { workspaceId: w }),
    quiet,
  )
}

/** Fills {{field}} from a sample, the way the server does, for previews. */
export function render(tpl: string, data: Record<string, unknown>): string {
  return tpl.replace(/\{\{\s*([a-z][a-z0-9_]*)\s*\}\}/g, (_, name: string) => {
    const v = data[name]
    return v == null ? "" : String(v)
  })
}
