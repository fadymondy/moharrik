/*
GET /api/w/opportunities/signals (MH-1065). Mirrors internal/opportunities. A count that is `null` is
a kind the viewer's role may not read, so the page leaves it out rather than showing a zero. Reasons
are codes plus the data behind them (same shape as Home's) and are turned into words by the page.
*/

import type { ProspectReason } from "@/lib/api"
import type { HomeMoney, HomeReason } from "@/lib/home-types"

export type SignalKind = "at_risk_deal" | "new_lead" | "high_fit_prospect"

export type SignalAction = "open" | "convert_lead" | "accept_prospect" | "dismiss_prospect"

export type SignalItem = {
  kind: SignalKind
  tone: "warning" | "info" | "violet"
  id: string
  title: string
  subtitle?: string
  value?: HomeMoney
  score?: number
  reasons?: ProspectReason[]
  why: HomeReason[]
  occurred_at: string
  actions: SignalAction[]
}

export type OpportunitySignals = {
  currency: string
  thresholds: { quiet_days: number; new_lead_days: number; high_fit_score: number }
  counts: {
    at_risk_deals: number | null
    new_leads: number | null
    high_fit_prospects: number | null
  }
  sources: string[]
  items: SignalItem[]
}
