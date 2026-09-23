"use client"

import useSWR, { type SWRConfiguration } from "swr"
import useSWRInfinite from "swr/infinite"

import {
  api,
  ApiError,
  type AutomationRun,
  type AutomationSchedule,
  type AutomationWorkflow,
  type AutomationSettings,
  type ExtensionInfo,
  type WorkflowVersion,
  type BrowserAgent,
  type MailboxSettings,
  type WhatsAppLink,
  type RunStep,
  type ResearchDetail,
  type ResearchList,
  type Activity,
  type Bell,
  type PushStatus,
  type Campaign,
  type SubscribeForm,
  type Company,
  type ContactDetail,
  type ContactRef,
  type Deal,
  type Lead,
  type LeadStatus,
  type Stage,
  type ContactPage,
  type Page,
  type TimelinePage,
  type CurrentWorkspace,
  type MailList,
  type SmtpSettings,
  type Invitation,
  type McpScope,
  type McpToken,
  type Member,
  type ChatWidget,
  type Conversation,
  type ConversationDetail,
  type ConversationRef,
  type CannedReply,
  type InboxStats,
  type InboxMessage,
  type FeedSource,
  type GoogleConnections,
  type FeedCatalogEntry,
  type NewsPage,
  type TrendSettings,
  type TrendTopicDetail,
  type TrendTopicPage,
  type GoogleResources,
  type GaReportOut,
  type GscPerformanceOut,
  type GscInspectionsOut,
  type GscSitemapsOut,
  type YtChannelOut,
  type YtAnalyticsOut,
  type YtVideoDetail,
  type YtVideoPage,
  type NewsItem,
  type NotificationItem,
  type Overview,
  type Topic,
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
  useScoped<{ tokens: McpToken[] | null; scopes: McpScope[] | null; all?: boolean }>("/api/w/mcp_tokens", id)

export const useMailLists = (id?: string | null) => useScoped<{ lists: MailList[] | null }>("/api/w/mail/lists", id)

export type ContactQuery = { listId?: string; status?: string; q?: string; cursor?: string; limit?: number }

export function contactsPath(p: ContactQuery): string {
  const qs = new URLSearchParams()
  if (p.listId) qs.set("list_id", p.listId)
  if (p.listId && p.status) qs.set("status", p.status)
  if (p.q?.trim()) qs.set("q", p.q.trim())
  if (p.cursor) qs.set("cursor", p.cursor)
  if (p.limit) qs.set("limit", String(p.limit))
  const s = qs.toString()
  return s ? `/api/w/contacts?${s}` : "/api/w/contacts"
}

/**
 * One page of contacts (MH-408). The list used to arrive whole and silently stop
 * at 1,000; now it is paged, and `total` says how many match.
 */
export function useContactsPage(workspaceId: string | null | undefined, p: ContactQuery, enabled = true) {
  const path = contactsPath(p)
  const res = useSWR<ContactPage & { path: string }>(
    workspaceId && enabled ? [path, workspaceId] : null,
    ([key, id]: [string, string]) => api<ContactPage>(key, { workspaceId: id }).then((d) => ({ ...d, path: key })),
    { ...noRetryOn4xx, keepPreviousData: true },
  )
  // keepPreviousData shows the last page while the next one loads. `current` says
  // whether what is on screen is the page that was asked for — a pager that acts
  // on a stale page's next_cursor would fetch the same page twice.
  return { ...res, current: res.data?.path === path }
}

/**
 * A keyset-paged list (MH-467). keepPreviousData shows the last page while the next one loads;
 * `current` says whether what is on screen is the page that was asked for, so a pager never acts
 * on a stale page's next_cursor.
 */
function usePaged<T>(path: string, workspaceId: string | null | undefined, extra?: SWRConfiguration<T & { path: string }>) {
  const res = useSWR<T & { path: string }>(
    workspaceId ? [path, workspaceId] : null,
    ([key, id]: [string, string]) => api<T>(key, { workspaceId: id }).then((d) => ({ ...d, path: key })),
    { ...noRetryOn4xx, keepPreviousData: true, ...extra },
  )
  return { ...res, current: res.data?.path === path }
}

function withQuery(path: string, q: Record<string, string | number | undefined>): string {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) if (v !== undefined && v !== "" && v !== 0) qs.set(k, String(v))
  const s = qs.toString()
  return s ? `${path}?${s}` : path
}

/** Campaigns a page at a time; polls while one on the page is sending, so progress moves on screen. */
export function useCampaigns(workspaceId: string | null | undefined, p: { cursor?: string; limit?: number } = {}) {
  type R = Page & { campaigns: Campaign[] | null; smtp_configured: boolean; whatsapp_linked: boolean; max_audience: number }
  return usePaged<R>(withQuery("/api/w/campaigns", { cursor: p.cursor, limit: p.limit }), workspaceId, {
    refreshInterval: (data) => ((data?.campaigns ?? []).some((c) => c.status === "sending") ? 2000 : 0),
  })
}

/** Who a WhatsApp broadcast to a list would reach right now (MH-320). */
export const useWhatsAppAudience = (workspaceId?: string | null, listId?: string | null) =>
  useScoped<{ reachable: number }>(`/api/w/mail/lists/${encodeURIComponent(listId ?? "")}/whatsapp-audience`, workspaceId, !!listId)

export const useSubscribeForms = (id?: string | null) => useScoped<{ forms: SubscribeForm[] | null }>("/api/w/subscribe-forms", id)

export const useSmtpSettings = (id?: string | null, enabled = true) => useScoped<SmtpSettings>("/api/w/mail/smtp", id, enabled)

// ── CRM ──────────────────────────────────────────────────────────────────

/** Companies a page at a time, A–Z, optionally searched (MH-467). */
export const useCompaniesPage = (workspaceId: string | null | undefined, p: { q?: string; cursor?: string; limit?: number }) =>
  usePaged<Page & { companies: Company[] }>(withQuery("/api/w/companies", { q: p.q?.trim(), cursor: p.cursor, limit: p.limit }), workspaceId)

export const useCompany = (wsId?: string | null, id?: string) =>
  useScoped<{ company: Company; contacts: ContactRef[]; deals: Deal[]; deals_total: number } & TimelinePage>(
    `/api/w/companies/${encodeURIComponent(id ?? "")}`,
    wsId,
    !!id,
  )

export const useContact = (wsId?: string | null, id?: string) =>
  useScoped<{ contact: ContactDetail; deals: Deal[]; deals_total: number } & TimelinePage>(`/api/w/contacts/${encodeURIComponent(id ?? "")}`, wsId, !!id)

export type DealsPage = Page & { deals: Deal[]; stages: Stage[]; currency: string }

/** The board: the first cards of every stage, each stage with its own next_cursor (MH-467). */
export const useDeals = (id?: string | null) => useScoped<DealsPage>("/api/w/deals", id)

/** The list view: the whole pipeline in board order, a page at a time. */
export const useDealsList = (workspaceId: string | null | undefined, p: { cursor?: string; limit?: number }, enabled = true) =>
  usePaged<DealsPage>(withQuery("/api/w/deals", { view: "list", cursor: p.cursor, limit: p.limit }), enabled ? workspaceId : null)

export const useDeal = (wsId?: string | null, id?: string) =>
  useScoped<{ deal: Deal } & TimelinePage>(`/api/w/deals/${encodeURIComponent(id ?? "")}`, wsId, !!id)

// ── Leads (MH-390) ───────────────────────────────────────────────────────

export type LeadFilter = { status?: LeadStatus | ""; q?: string }

/** The Leads list. Filters vary, so this builds its own key (like useNews). */
export function useLeads(workspaceId: string | null | undefined, filter: LeadFilter = {}) {
  const qs = new URLSearchParams()
  if (filter.status) qs.set("status", filter.status)
  if (filter.q?.trim()) qs.set("q", filter.q.trim())
  const path = "/api/w/leads" + (qs.toString() ? `?${qs.toString()}` : "")
  type R = { leads: Lead[] | null; counts: Record<LeadStatus, number> }
  return useSWR<R>(workspaceId ? [path, workspaceId] : null, ([p, id]: [string, string]) => api<R>(p, { workspaceId: id }), noRetryOn4xx)
}

export const useLead = (wsId?: string | null, id?: string) =>
  useScoped<{ lead: Lead }>(`/api/w/leads/${encodeURIComponent(id ?? "")}`, wsId, !!id)

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

// ── Inbox ────────────────────────────────────────────────────────────────

export type ConversationFilter = { status?: string; unread?: boolean; channel?: string }

/**
 * The inbox list.
 *
 * No polling: the page subscribes to the workspace's event stream
 * (useWorkspaceEvents) and revalidates when a message actually arrives. SWR
 * still revalidates on focus and reconnect, which covers the case of a stream
 * that dropped while the tab was in the background.
 */
export function useConversations(workspaceId: string | null | undefined, filter: ConversationFilter = {}) {
  const qs = new URLSearchParams()
  if (filter.status) qs.set("status", filter.status)
  if (filter.unread) qs.set("unread", "true")
  if (filter.channel) qs.set("channel", filter.channel)
  const path = "/api/w/conversations" + (qs.toString() ? `?${qs.toString()}` : "")
  return useSWR<{ conversations: Conversation[]; unread_count: number }>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<{ conversations: Conversation[]; unread_count: number }>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}

/** One thread. Same as the list: revalidated by the event stream, not by a timer. */
export function useConversation(workspaceId: string | null | undefined, id?: string) {
  return useSWR<ConversationDetail>(
    workspaceId && id ? [`/api/w/conversations/${encodeURIComponent(id)}`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<ConversationDetail>(p, { workspaceId: ws }),
    noRetryOn4xx,
  )
}

export const useChatWidgets = (id?: string | null) => useScoped<{ widgets: ChatWidget[] | null }>("/api/w/chat_widgets", id)

/** The workspace's canned replies (MH-383), in picker order. */
export const useCannedReplies = (id?: string | null) => useScoped<{ replies: CannedReply[] | null }>("/api/w/canned_replies", id)

/** Per-channel inbox stats over the last `days` days (MH-383). */
export const useInboxStats = (id: string | null | undefined, days: number) =>
  useScoped<InboxStats>(`/api/w/inbox/stats?days=${days}`, id)

/** The pipeline's stages, for opening a deal from a conversation (MH-379). */
export const usePipelineStages = (id?: string | null, enabled = true) =>
  useScoped<{ stages: Stage[] | null }>("/api/w/pipeline/stages", id, enabled)

// ── Trends ───────────────────────────────────────────────────────────────

export const useFeedSources = (id?: string | null) => useScoped<{ sources: FeedSource[] | null }>("/api/w/feed_sources", id)

export const useTopics = (id?: string | null) => useScoped<{ topics: Topic[] | null }>("/api/w/topics", id)

export type NewsFilter = { topic?: string; source?: string; saved?: boolean; q?: string; day?: string }

/**
 * The raw collected items, newest first, a page at a time (MH-385). The list
 * used to arrive whole — 120 items on one 10,000px page; now it is cursor-paged
 * and the page asks for more.
 */
export function useNewsPages(workspaceId: string | null | undefined, filter: NewsFilter = {}) {
  const qs = new URLSearchParams()
  if (filter.topic) qs.set("topic", filter.topic)
  if (filter.source) qs.set("source", filter.source)
  if (filter.saved) qs.set("saved", "true")
  if (filter.q) qs.set("q", filter.q)
  if (filter.day) qs.set("day", filter.day)
  qs.set("limit", "30")
  const base = "/api/w/news?" + qs.toString()
  return useSWRInfinite<NewsPage>(
    (i, prev: NewsPage | null) => {
      if (!workspaceId) return null
      if (i === 0) return [base, workspaceId]
      if (!prev?.has_more || !prev.next_cursor) return null
      return [`${base}&cursor=${encodeURIComponent(prev.next_cursor)}`, workspaceId]
    },
    ([p, id]: [string, string]) => api<NewsPage>(p, { workspaceId: id }),
    { ...noRetryOn4xx, revalidateFirstPage: true },
  )
}

export type TrendFilter = { state?: string; source?: string; day?: string; q?: string }

/** Trending topics (stories), newest day and best first, 20 at a time. */
export function useTrendTopics(workspaceId: string | null | undefined, filter: TrendFilter = {}) {
  const qs = new URLSearchParams()
  qs.set("state", filter.state || "open")
  if (filter.source) qs.set("source", filter.source)
  if (filter.day) qs.set("day", filter.day)
  if (filter.q) qs.set("q", filter.q)
  qs.set("limit", "20")
  const base = "/api/w/trends?" + qs.toString()
  return useSWRInfinite<TrendTopicPage>(
    (i, prev: TrendTopicPage | null) => {
      if (!workspaceId) return null
      if (i === 0) return [base, workspaceId]
      if (!prev?.has_more || !prev.next_cursor) return null
      return [`${base}&cursor=${encodeURIComponent(prev.next_cursor)}`, workspaceId]
    },
    ([p, id]: [string, string]) => api<TrendTopicPage>(p, { workspaceId: id }),
    { ...noRetryOn4xx, revalidateFirstPage: true },
  )
}

export function useTrendTopic(workspaceId: string | null | undefined, id?: string) {
  return useSWR<TrendTopicDetail>(
    workspaceId && id ? [`/api/w/trends/${encodeURIComponent(id)}`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<TrendTopicDetail>(p, { workspaceId: ws }),
    noRetryOn4xx,
  )
}

export const useFeedCatalog = (id?: string | null) => useScoped<{ entries: FeedCatalogEntry[] }>("/api/w/feed_catalog", id)

export const useTrendSettings = (id?: string | null) => useScoped<{ settings: TrendSettings }>("/api/w/trend_settings", id)

// ── Automation ───────────────────────────────────────────────────────────

export const useBrowsers = (id?: string | null) =>
  useScoped<{ browsers: BrowserAgent[] | null; latest_extension_version: string }>("/api/w/browsers", id)

/** The extension this server offers for download (MH-399). Not workspace-scoped. */
export const useExtension = () =>
  useSWR<ExtensionInfo>("/api/automation/extension", (p: string) => api<ExtensionInfo>(p), noRetryOn4xx)

export const useWorkflow = (workspaceId?: string | null, id?: string) =>
  useScoped<{ workflow: AutomationWorkflow }>(`/api/w/workflows/${encodeURIComponent(id ?? "")}`, workspaceId, !!id)

export const useWorkflowVersions = (workspaceId?: string | null, id?: string) =>
  useScoped<{ versions: WorkflowVersion[] }>(`/api/w/workflows/${encodeURIComponent(id ?? "")}/versions`, workspaceId, !!id)

export const useAutomationSettings = (id?: string | null) =>
  useScoped<AutomationSettings>("/api/w/automation/settings", id)

/**
 * Recent runs. A run that is queued or going is polled: it is driven by a
 * browser out in the world, so nothing on this side can push an event when a
 * step finishes. Once everything has settled the polling stops.
 */
export function useRuns(workspaceId: string | null | undefined, agent?: string) {
  const path = "/api/w/runs" + (agent ? `?agent=${encodeURIComponent(agent)}` : "")
  return useSWR<{ runs: AutomationRun[] }>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<{ runs: AutomationRun[] }>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => ((data?.runs ?? []).some((r) => r.status === "queued" || r.status === "running") ? 2000 : 0),
    },
  )
}

/** One run with its steps. Polled while it is still going, for the same reason. */
export function useRun(workspaceId: string | null | undefined, id?: string) {
  return useSWR<{ run: AutomationRun; steps: RunStep[] }>(
    workspaceId && id ? [`/api/w/runs/${encodeURIComponent(id)}`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<{ run: AutomationRun; steps: RunStep[] }>(p, { workspaceId: ws }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => (data?.run.status === "queued" || data?.run.status === "running" ? 1500 : 0),
    },
  )
}

export const useMailbox = (id?: string | null) => useScoped<MailboxSettings>("/api/w/mailbox", id)

export const useWhatsApp = (id?: string | null) => useScoped<WhatsAppLink>("/api/w/whatsapp", id)

export const useSchedules = (id?: string | null) => useScoped<{ schedules: AutomationSchedule[] | null }>("/api/w/schedules", id)

export const useWorkflows = (id?: string | null) => useScoped<{ workflows: AutomationWorkflow[] | null }>("/api/w/workflows", id)

// ── Research (MH-227) ────────────────────────────────────────────────────

const researchLive = (status?: string) => status === "queued" || status === "running"

/**
 * Past research runs and what this deployment can do. Research publishes no
 * workspace events, so a live run is polled until it settles.
 */
export function useResearchRuns(workspaceId: string | null | undefined, enabled = true) {
  return useSWR<ResearchList>(
    workspaceId && enabled ? ["/api/w/research", workspaceId] : null,
    ([p, id]: [string, string]) => api<ResearchList>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => ((data?.runs ?? []).some((r) => researchLive(r.status)) ? 3000 : 0),
    },
  )
}

/** One run and its sources, without their page text. Polled while it is going. */
export function useResearchRun(workspaceId: string | null | undefined, id?: string) {
  return useSWR<ResearchDetail>(
    workspaceId && id ? [`/api/w/research/${encodeURIComponent(id)}`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<ResearchDetail>(p, { workspaceId: ws }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => (researchLive(data?.run.status) ? 2000 : 0),
    },
  )
}

/**
 * The same run WITH the fetched page text. The API withholds it by default
 * (it is up to five articles), so this is only asked for once somebody opens a
 * source's text — `enabled` stays false until then.
 */
export function useResearchContent(workspaceId: string | null | undefined, id: string | undefined, enabled: boolean) {
  return useSWR<ResearchDetail>(
    workspaceId && id && enabled ? [`/api/w/research/${encodeURIComponent(id)}?content=true`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<ResearchDetail>(p, { workspaceId: ws }),
    noRetryOn4xx,
  )
}

// ── Integrations ────────────────────────────────────────────────────────

export const useGoogleConnections = (id?: string | null, enabled = true) =>
  useScoped<GoogleConnections>("/api/w/google/connections", id, enabled)

/**
 * The properties, sites and channels (MH-440). An admin gets all of them; a reader the enabled
 * ones. Polled while one is syncing, so its "last synced" / error lands without a reload.
 */
export function useGoogleResources(workspaceId: string | null | undefined, kind?: string, enabled = true) {
  const path = kind ? `/api/w/google/resources?kind=${kind}` : "/api/w/google/resources"
  return useSWR<GoogleResources>(
    workspaceId && enabled ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<GoogleResources>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => (data?.resources?.some((r) => r.syncing) ? 3000 : 0),
    },
  )
}

/** The GA4 report for one property and window (MH-444). */
export function useGaReport(workspaceId: string | null | undefined, resource: string, days: number, enabled = true) {
  const q = new URLSearchParams({ days: String(days) })
  if (resource) q.set("resource", resource)
  return useSWR<GaReportOut>(
    workspaceId && enabled ? [`/api/w/analytics/report?${q}`, workspaceId] : null,
    ([p, id]: [string, string]) => api<GaReportOut>(p, { workspaceId: id }),
    { ...noRetryOn4xx, keepPreviousData: true },
  )
}

/** Search Console performance (MH-445). Polled while the first import has nothing yet. */
export function useGscPerformance(workspaceId: string | null | undefined, query: string, enabled = true) {
  return useSWR<GscPerformanceOut>(
    workspaceId && enabled ? [`/api/w/search-console/performance?${query}`, workspaceId] : null,
    ([p, id]: [string, string]) => api<GscPerformanceOut>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      keepPreviousData: true,
      refreshInterval: (data) => (data?.performance?.nothing_yet && data.resource?.syncing ? 4000 : 0),
    },
  )
}

/** A site's latest URL inspections and today's inspection budget (MH-446). */
export const useGscInspections = (workspaceId: string | null | undefined, resource: string) =>
  useScoped<GscInspectionsOut>(`/api/w/search-console/inspections?resource=${encodeURIComponent(resource)}`, workspaceId, !!resource)

/** A site's sitemaps, live from Google (MH-446). */
export const useGscSitemaps = (workspaceId: string | null | undefined, resource: string) =>
  useScoped<GscSitemapsOut>(`/api/w/search-console/sitemaps?resource=${encodeURIComponent(resource)}`, workspaceId, !!resource)

/** An enabled YouTube channel and today's quota (MH-441). Polled while its first sync runs. */
export function useYtChannel(workspaceId: string | null | undefined, resource: string, enabled = true) {
  const path = `/api/w/youtube/channel${resource ? `?resource=${encodeURIComponent(resource)}` : ""}`
  return useSWR<YtChannelOut>(workspaceId && enabled ? [path, workspaceId] : null, ([p, id]: [string, string]) => api<YtChannelOut>(p, { workspaceId: id }), {
    ...noRetryOn4xx,
    refreshInterval: (data) => (data?.resource?.syncing ? 3000 : 0),
  })
}

/** A channel's uploads, 24 at a time. */
export function useYtVideos(workspaceId: string | null | undefined, resource: string, filter: { q?: string; privacy?: string }, enabled = true) {
  const qs = new URLSearchParams({ resource, limit: "24" })
  if (filter.q) qs.set("q", filter.q)
  if (filter.privacy) qs.set("privacy", filter.privacy)
  const base = "/api/w/youtube/videos?" + qs.toString()
  return useSWRInfinite<YtVideoPage>(
    (i, prev: YtVideoPage | null) => {
      if (!workspaceId || !enabled || !resource) return null
      if (i === 0) return [base, workspaceId]
      if (!prev?.has_more || !prev.next_cursor) return null
      return [`${base}&cursor=${encodeURIComponent(prev.next_cursor)}`, workspaceId]
    },
    ([p, id]: [string, string]) => api<YtVideoPage>(p, { workspaceId: id }),
    { ...noRetryOn4xx, revalidateFirstPage: true },
  )
}

/** YouTube Analytics for the channel (video "") or one video (MH-442). */
export function useYtAnalytics(workspaceId: string | null | undefined, resource: string, days: number, video = "", enabled = true) {
  const q = new URLSearchParams({ resource, days: String(days) })
  if (video) q.set("video", video)
  return useSWR<YtAnalyticsOut>(
    workspaceId && enabled && resource ? [`/api/w/youtube/analytics?${q}`, workspaceId] : null,
    ([p, id]: [string, string]) => api<YtAnalyticsOut>(p, { workspaceId: id }),
    { ...noRetryOn4xx, keepPreviousData: true },
  )
}

/** One upload with its top comments. */
export function useYtVideo(workspaceId: string | null | undefined, resource: string, id: string | null) {
  return useSWR<YtVideoDetail>(
    workspaceId && id ? [`/api/w/youtube/videos/${encodeURIComponent(id)}?resource=${encodeURIComponent(resource)}`, workspaceId] : null,
    ([p, ws]: [string, string]) => api<YtVideoDetail>(p, { workspaceId: ws }),
    noRetryOn4xx,
  )
}

// ── Alerts (MH-378) ──────────────────────────────────────────────────────

/** The header bell. Revalidated by the event stream (components/alerts), not a timer. */
export const useBell = (id?: string | null) => useScoped<Bell>("/api/w/alerts", id)

/** Web push availability and the caller's own devices. */
export const usePushStatus = (id?: string | null, enabled = true) => useScoped<PushStatus>("/api/w/push", id, enabled)

// ── Overview (MH-462) ───────────────────────────────────────────────────

/** The front page. A live research run is polled until it settles (research publishes no events). */
export function useOverview(workspaceId: string | null | undefined) {
  return useSWR<Overview>(
    workspaceId ? ["/api/w/overview", workspaceId] : null,
    ([p, id]: [string, string]) => api<Overview>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => (data?.today.research?.live ? 5000 : 0),
    },
  )
}
