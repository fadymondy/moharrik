"use client"

import useSWR from "swr"

import {
  api,
  ApiError,
  type Activity,
  type Campaign,
  type Company,
  type ContactDetail,
  type ContactRef,
  type Deal,
  type Stage,
  type Contact,
  type CurrentWorkspace,
  type MailList,
  type SmtpSettings,
  type Invitation,
  type McpScope,
  type McpToken,
  type Member,
  type NotificationItem,
  type User,
  type WebhookSource,
  type WorkspaceSummary,
} from "@/lib/api"

const noRetryOn4xx = {
  shouldRetryOnError: (err: unknown) => !(err instanceof ApiError && err.status < 500),
  revalidateOnFocus: false,
}

/** The signed-in user, `null` when signed out (401), undefined while loading. */
export function useMe() {
  return useSWR<User | null>(
    "/api/auth/me",
    async () => {
      try {
        return await api<User>("/api/auth/me")
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) return null
        throw err
      }
    },
    noRetryOn4xx,
  )
}

export function useMyWorkspaces(enabled = true) {
  return useSWR(
    enabled ? "/api/me/workspaces" : null,
    () => api<{ workspaces: WorkspaceSummary[] | null }>("/api/me/workspaces").then((r) => r.workspaces ?? []),
    noRetryOn4xx,
  )
}

function useScoped<T>(path: string, workspaceId: string | null | undefined, enabled = true) {
  return useSWR<T>(
    workspaceId && enabled ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<T>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}

export const useCurrentWorkspace = (id?: string | null) => useScoped<CurrentWorkspace>("/api/w/current", id)

export const useMembers = (id?: string | null) => useScoped<{ members: Member[] | null }>("/api/w/members", id)

export const useInvitations = (id?: string | null, enabled = true) =>
  useScoped<{ invitations: Invitation[] | null }>("/api/w/invitations", id, enabled)

export const useMcpTokens = (id?: string | null) =>
  useScoped<{ tokens: McpToken[] | null; scopes: McpScope[] | null }>("/api/w/mcp_tokens", id)

export const useMailLists = (id?: string | null) => useScoped<{ lists: MailList[] | null }>("/api/w/mail/lists", id)

export const useContacts = (id?: string | null) => useScoped<{ contacts: Contact[] | null }>("/api/w/contacts", id)

/** Polls while a campaign is sending, so progress moves on screen. */
export function useCampaigns(workspaceId?: string | null) {
  return useSWR<{ campaigns: Campaign[] | null; smtp_configured: boolean }>(
    workspaceId ? ["/api/w/campaigns", workspaceId] : null,
    ([p, id]: [string, string]) => api<{ campaigns: Campaign[] | null; smtp_configured: boolean }>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => ((data?.campaigns ?? []).some((c) => c.status === "sending") ? 2000 : 0),
    },
  )
}

export const useSmtpSettings = (id?: string | null, enabled = true) => useScoped<SmtpSettings>("/api/w/mail/smtp", id, enabled)

// ── CRM ──────────────────────────────────────────────────────────────────

export const useCompanies = (id?: string | null) => useScoped<{ companies: Company[] | null }>("/api/w/companies", id)

export const useCompany = (wsId?: string | null, id?: string) =>
  useScoped<{ company: Company; contacts: ContactRef[]; deals: Deal[]; activities: Activity[] }>(
    `/api/w/companies/${encodeURIComponent(id ?? "")}`,
    wsId,
    !!id,
  )

export const useContact = (wsId?: string | null, id?: string) =>
  useScoped<{ contact: ContactDetail; deals: Deal[]; activities: Activity[] }>(`/api/w/contacts/${encodeURIComponent(id ?? "")}`, wsId, !!id)

export const useDeals = (id?: string | null) => useScoped<{ deals: Deal[]; stages: Stage[]; currency: string }>("/api/w/deals", id)

export const useDeal = (wsId?: string | null, id?: string) =>
  useScoped<{ deal: Deal; activities: Activity[] }>(`/api/w/deals/${encodeURIComponent(id ?? "")}`, wsId, !!id)

// ── Notifications ────────────────────────────────────────────────────────

export const useWebhookSources = (id?: string | null) => useScoped<{ sources: WebhookSource[] | null }>("/api/w/webhook_sources", id)

export type NotificationFilter = { unread?: boolean; saved?: boolean; sourceId?: string; severity?: string }

/** The notification feed. Query params vary per filter, so this bypasses useScoped's fixed path. */
export function useNotifications(workspaceId: string | null | undefined, filter: NotificationFilter = {}) {
  const qs = new URLSearchParams()
  if (filter.unread) qs.set("unread", "true")
  if (filter.saved) qs.set("saved", "true")
  if (filter.sourceId) qs.set("source_id", filter.sourceId)
  if (filter.severity) qs.set("severity", filter.severity)
  const path = "/api/w/notifications" + (qs.toString() ? `?${qs.toString()}` : "")
  return useSWR<{ items: NotificationItem[]; unread_count: number }>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<{ items: NotificationItem[]; unread_count: number }>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}
