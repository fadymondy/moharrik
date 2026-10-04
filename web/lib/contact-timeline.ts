/*
GET /api/w/contacts/{id}/timeline (MH-1064). The API sends kind codes and the data behind them, never
prose; components/contacts/c360-text.ts turns them into en/ar sentences. A kind the role cannot read is
absent (its source is missing from `sources`), never zeroed.
*/

import useSWR from "swr"

import { api, type ContactReport } from "@/lib/api"

export type ContactEventKind =
  | "contact.created"
  | "activity.note"
  | "activity.call"
  | "activity.meeting"
  | "activity.task"
  | "conversation"
  | "deal.opened"
  | "deal.stage_changed"
  | "deal.won"
  | "deal.lost"
  | "lifecycle.changed"
  | "lead.submitted"
  | "rule.ran"

export type ContactEvent = {
  at: string
  kind: ContactEventKind | (string & {})
  id: string
  data: Record<string, unknown>
}

export type ContactConversationRow = {
  id: string
  channel: string
  subject: string
  status: string
  unread: boolean
  messages: number
  last_direction: "in" | "out" | ""
  started_at: string
  last_message_at: string
}

export type ContactAutomationRow = {
  rule_id: string
  rule_name: string
  trigger: string
  runs: number
  last_status: string
  last_at: string
}

export type ContactTimelinePage = {
  contact: { id: string; name: string }
  events: ContactEvent[] | null
  next_cursor: string
  sources: string[] | null
  overview: {
    conversations: ContactConversationRow[] | null
    automations: ContactAutomationRow[] | null
  } | null
}

export const contactTimelinePath = (id: string, cursor = "") =>
  `/api/w/contacts/${encodeURIComponent(id)}/timeline${cursor ? `?cursor=${encodeURIComponent(cursor)}` : ""}`

/** The first page (events, overview, sources); older pages are fetched on demand with `api`. */
export function useContactTimeline(wsId: string | null | undefined, id: string, enabled = true) {
  return useSWR<ContactTimelinePage>(
    wsId && id && enabled ? [contactTimelinePath(id), wsId] : null,
    ([p, w]: [string, string]) => api<ContactTimelinePage>(p, { workspaceId: w }),
    { shouldRetryOnError: false },
  )
}

/** The stored relationship reports, newest version first (the same list the context panel shows). */
export function useContactReports(wsId: string | null | undefined, id: string, enabled = true) {
  return useSWR<ContactReport[]>(
    wsId && id && enabled ? [`/api/w/contacts/${encodeURIComponent(id)}/context/reports`, wsId] : null,
    ([p, w]: [string, string]) => api<ContactReport[]>(p, { workspaceId: w }).then((r) => r ?? []),
    { shouldRetryOnError: false },
  )
}
