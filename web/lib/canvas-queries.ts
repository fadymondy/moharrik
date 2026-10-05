"use client"

/*
Data for the workflow canvas (MH-480): a workflow's layout and its executions.
Kept apart from lib/queries.ts so the canvas's hooks travel together.
*/

import useSWR from "swr"
import useSWRInfinite from "swr/infinite"

import { api, ApiError } from "@/lib/api"

export type CanvasPoint = { x: number; y: number }

export type WorkflowLayout = {
  layout: { trigger?: CanvasPoint; nodes: CanvasPoint[] }
  /** The step commands the layout was drawn for, joined with "|". Empty: none saved. */
  signature: string
  updated_at: string | null
}

export type WorkflowExecution = {
  id: string
  workflow_id: string
  workflow_name: string
  agent_id: string
  agent_name: string
  trigger: "manual" | "schedule" | "mcp" | "test" | string
  status: "queued" | "running" | "ok" | "failed" | "cancelled"
  error: string
  step_count: number
  ok_count: number
  started_at: string | null
  finished_at: string | null
  created_at: string
}

type ExecutionsPage = { executions: WorkflowExecution[]; next_before?: string }

const quiet = {
  shouldRetryOnError: (err: unknown) => !(err instanceof ApiError && err.status < 500),
  revalidateOnFocus: false,
}

export function useWorkflowLayout(workspaceId: string | null | undefined, id?: string) {
  return useSWR<WorkflowLayout>(
    workspaceId && id ? [`/api/w/workflows/${encodeURIComponent(id)}/layout`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<WorkflowLayout>(p, { workspaceId: ws }),
    quiet,
  )
}

const live = (pages?: ExecutionsPage[]) =>
  (pages ?? []).some((p) =>
    p.executions.some((e) => e.status === "queued" || e.status === "running"),
  )

/**
 * Executions, newest first, a page at a time: one workflow's, or — with no id —
 * the workspace's runs of any workflow. A page with a run still going is polled
 * until it settles; the workspace's "run" events refetch it sooner.
 */
export function useExecutions(
  workspaceId: string | null | undefined,
  workflowId?: string,
  enabled = true,
) {
  const base = workflowId
    ? `/api/w/workflows/${encodeURIComponent(workflowId)}/executions`
    : "/api/w/workflow-executions"
  return useSWRInfinite<ExecutionsPage>(
    (index, prev: ExecutionsPage | null) => {
      if (!workspaceId || !enabled) return null
      if (index > 0 && !prev?.next_before) return null
      const q =
        index === 0 ? "?limit=30" : `?limit=30&before=${encodeURIComponent(prev!.next_before!)}`
      return [base + q, workspaceId]
    },
    ([p, ws]: [string, string]) => api<ExecutionsPage>(p, { workspaceId: ws }),
    { ...quiet, refreshInterval: (pages) => (live(pages) ? 2000 : 0) },
  )
}

/** One step's record across runs of the current version (MH-1067). */
export type WorkflowStepStat = {
  position: number
  ran: number
  ok: number
  failed: number
  last_status: string
  last_items: number | null
  last_duration_ms: number | null
}

/** The workflow page's header numbers, all counted from real run rows. */
export type WorkflowOverview = {
  workflow_id: string
  name: string
  version: number
  status: "enabled" | "paused"
  workspace_paused: boolean
  paused_reason?: string
  window_days: number
  runs: { total: number; succeeded: number; failed: number; cancelled: number; in_progress: number }
  success_rate: { succeeded: number; finished: number; percent: number | null }
  last_run: null | {
    id: string
    status: string
    trigger: string
    version: number
    created_at: string
    finished_at: string | null
    duration_ms: number | null
    error: string
    items: number | null
  }
  items: { total: number | null; runs: number }
  schedules: { total: number; enabled: number; next_run_at: string | null }
  steps: WorkflowStepStat[]
  sampled: boolean
}

export function useWorkflowOverview(workspaceId: string | null | undefined, id?: string) {
  return useSWR<WorkflowOverview>(
    workspaceId && id && id !== "new"
      ? [`/api/w/workflows/${encodeURIComponent(id)}/overview`, workspaceId]
      : null,
    ([p, ws]: [string, string]) => api<WorkflowOverview>(p, { workspaceId: ws }),
    quiet,
  )
}
