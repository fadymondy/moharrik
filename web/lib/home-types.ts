/*
GET /api/w/home (MH-1062): the workspace Home. Mirrors internal/home. A `null` section is one the
viewer's role may not read; the page leaves it out rather than showing zeros. Reasons and events are
structured (a code plus the data it came from) and are turned into words by components/home/home-text.
*/

export type HomeMoney = { currency: string; minor: number; deals?: number }

export type HomeEntityRef = {
  type:
    | "contact"
    | "company"
    | "deal"
    | "lead"
    | "conversation"
    | "prospect"
    | "rule"
    | "run"
    | "research"
    | "campaign"
    | string
  id: string
}

export type HomeReason = {
  code:
    | "replied"
    | "new_lead"
    | "utm"
    | "form"
    | "quiet_days"
    | "in_stage"
    | "high_fit"
    | "rule_failed"
    | "browser_failed"
    | "waiting_for_reply"
    | string
  channel?: string
  text?: string
  n?: number
  at?: string
}

export type HomeAction = { key: string; route: string }

export type HomeAttentionItem = {
  kind: "hot_lead" | "at_risk" | "new_prospects" | "failed_automation" | "unanswered"
  tone: "warning" | "danger" | "info" | "success"
  title: string
  count?: number
  why: HomeReason[]
  entity?: HomeEntityRef
  value?: HomeMoney
  occurred_at?: string
  actions: HomeAction[]
}

export type HomeLaneItem = { key: string; n?: number; text?: string; at?: string }

export type HomeLane = {
  key: "discover" | "listen" | "understand" | "act"
  active: boolean
  headline: number | null
  subs: HomeLaneItem[]
}

export type HomeEvent = {
  kind: string
  occurred_at: string
  subject?: string
  channel?: string
  snippet?: string
  status?: string
  n?: number
  actor?: { kind: string; name: string; via: string }
  link?: HomeEntityRef
}

export type HomeStage = { id: string; name: string; kind: "open" | "won" }

export type HomeJourney = {
  id: string
  title: string
  stage_index: number
  stage_name: string
  value: HomeMoney
  contact?: string
  company?: string
  last_activity_at: string
  days_since_activity: number
  quiet: boolean
}

export type HomeChannel = {
  id: string
  type: string
  network?: string
  name: string
  status: "connected" | "error" | "paused"
  has_error: boolean
  last_message_at: string | null
  conversations_30d: number
}

export type Home = {
  timezone: string
  currency: string
  today: {
    signals: {
      total: number
      leads: number | null
      prospects: number | null
      companies: number | null
      trends: number | null
    }
    opportunities: { open: number | null; value: HomeMoney[] }
    actions: {
      total: number
      rule_runs: number | null
      browser_runs: number | null
      campaign_sends: number | null
    }
    attention: number
  }
  attention: { total: number; items: HomeAttentionItem[] } | null
  working: {
    lanes: HomeLane[]
    pulse: { bucket_minutes: number; buckets: number[]; total: number }
  }
  activity: HomeEvent[]
  journeys: { stages: HomeStage[]; deals: HomeJourney[] } | null
  connections: {
    channels: HomeChannel[]
    google: {
      connections: number
      has_error: boolean
      services: { kind: string; count: number }[]
    } | null
    circlexo: { linked: boolean; plan?: string; status?: string; active: boolean } | null
  } | null
  has: {
    contacts: boolean | null
    conversations: boolean | null
    automations: boolean | null
  }
}
