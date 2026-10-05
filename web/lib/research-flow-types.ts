/*
GET /api/w/research-overview (MH-1066). Mirrors internal/researchflow. Every figure is a count of rows
that exist; `prospects` and `high_fit` are only ever non-zero for discovery runs, the only runs that
score prospects. Stages are the run's own stages jsonb, as the engine stored it.
*/

import type { ResearchStage, ResearchStatus } from "@/lib/api"

export type FlowCounts = {
  found: number
  evidence: number
  prospects: number
  high_fit: number
  accepted: number
}

export type FlowRun = {
  id: string
  topic: string
  status: ResearchStatus
  origin: "manual" | "schedule" | "discovery"
  mode: "search" | "urls"
  stage: string
  stages: ResearchStage[]
  error: string
  counts: FlowCounts
  started_at: string | null
  finished_at: string | null
  created_at: string
}

export type ResearchOverview = {
  thresholds: { high_fit_score: number }
  totals: FlowCounts
  runs: FlowRun[]
  schedules: { total: number; enabled: number; next_run_at: string | null }
}
