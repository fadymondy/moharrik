"use client"

import useSWR, { type SWRConfiguration } from "swr"
import useSWRInfinite from "swr/infinite"

import {
  type Ga4ConversionsPage,
  type CollectedSourceList,
  type NotificationPage,
  api,
  ApiError,
  type AutomationJob,
  type AutomationPreset,
  type CrmReport,
  type IcpProfileOut,
  type AutomationRun,
  type AutomationSchedule,
  type AutomationWorkflow,
  type AutomationSettings,
  type ExtensionInfo,
  type WorkflowVersion,
  type BrowserAgent,
  type Mailbox,
  type ImportSourceInfo,
  type ImportItem,
  type ImportJob,
  type ChannelCount,
  type WhatsAppLink,
  type ContactWhatsApp,
  type ContextSummary,
  type WhatsAppNotify,
  type TelegramChannel,
  type SlackChannel,
  type DiscordChannel,
  type WorkflowChannel,
  type ChannelType,
  type ChannelStatus,
  type ChannelPage,
  type RunStep,
  type ResearchDetail,
  type CompanySuggestion,
  type EvidencePage,
  type ResearchBudget,
  type ResearchSchedule,
  type ProspectDetail,
  type ProspectList,
  type ProspectProfileOut,
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
  type SmtpService,
  type SmtpServiceOption,
  type Invitation,
  type McpScope,
  type McpToken,
  type OAuthGrant,
  type AuditEntry,
  type AuditFacets,
  type Member,
  type ChatWidget,
  type Conversation,
  type ConversationDetail,
  type ThreadPage,
  type ConversationRef,
  type CannedReply,
  type InboxStats,
  type InboxMessage,
  type FeedSource,
  type GoogleConnections,
  type GoogleContactsConnections,
  type GoogleContactsSyncStatus,
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
  type YtReports,
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
    () =>
      api<{ workspaces: WorkspaceSummary[] | null }>("/api/me/workspaces").then(
        (r) => r.workspaces ?? [],
      ),
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

export const useCurrentWorkspace = (id?: string | null) =>
  useScoped<CurrentWorkspace>("/api/w/current", id)

export const useMembers = (id?: string | null) =>
  useScoped<{ members: Member[] | null }>("/api/w/members", id)

export const useInvitations = (id?: string | null, enabled = true) =>
  useScoped<{ invitations: Invitation[] | null }>("/api/w/invitations", id, enabled)

export const useMcpTokens = (id?: string | null) =>
  useScoped<{ tokens: McpToken[] | null; scopes: McpScope[] | null; all?: boolean }>(
    "/api/w/mcp_tokens",
    id,
  )

/** Apps connected over OAuth (MH-391), and the URL a connector is added by. */
export const useOAuthGrants = (id?: string | null) =>
  useScoped<{ grants: OAuthGrant[] | null; all?: boolean; server_url: string }>(
    "/api/w/oauth_grants",
    id,
  )

export type AuditQuery = {
  actor?: string
  actorKind?: string
  entityType?: string
  action?: string
  from?: string
  to?: string
  cursor?: string
  limit?: number
}

/** The audit log (MH-392), a page at a time, newest first. Owners and admins only. */
export const useAuditPage = (
  workspaceId: string | null | undefined,
  q: AuditQuery,
  enabled = true,
) =>
  usePaged<Page & { entries: AuditEntry[] | null }>(
    withQuery("/api/w/audit", {
      actor: q.actor,
      actor_kind: q.actorKind,
      entity_type: q.entityType,
      action: q.action,
      from: q.from,
      to: q.to,
      cursor: q.cursor,
      limit: q.limit,
    }),
    enabled ? workspaceId : null,
  )

export const useAuditFacets = (id?: string | null, enabled = true) =>
  useScoped<AuditFacets>("/api/w/audit/facets", id, enabled)

export const useMailLists = (id?: string | null) =>
  useScoped<{ lists: MailList[] | null }>("/api/w/mail/lists", id)

export type ContactQuery = {
  listId?: string
  status?: string
  q?: string
  cursor?: string
  limit?: number
  /** MH-395: a lifecycle stage ("none" for no stage), a minimum score, and "score" to rank by it. */
  lifecycle?: string
  minScore?: number
  sort?: "" | "score"
}

export function contactsPath(p: ContactQuery): string {
  const qs = new URLSearchParams()
  if (p.listId) qs.set("list_id", p.listId)
  if (p.listId && p.status) qs.set("status", p.status)
  if (p.q?.trim()) qs.set("q", p.q.trim())
  if (p.lifecycle) qs.set("lifecycle", p.lifecycle)
  if (p.minScore) qs.set("min_score", String(p.minScore))
  if (p.sort) qs.set("sort", p.sort)
  if (p.cursor) qs.set("cursor", p.cursor)
  if (p.limit) qs.set("limit", String(p.limit))
  const s = qs.toString()
  return s ? `/api/w/contacts?${s}` : "/api/w/contacts"
}

/**
 * One page of contacts (MH-408). The list used to arrive whole and silently stop
 * at 1,000; now it is paged, and `total` says how many match.
 */
export function useContactsPage(
  workspaceId: string | null | undefined,
  p: ContactQuery,
  enabled = true,
) {
  const path = contactsPath(p)
  const res = useSWR<ContactPage & { path: string }>(
    workspaceId && enabled ? [path, workspaceId] : null,
    ([key, id]: [string, string]) =>
      api<ContactPage>(key, { workspaceId: id }).then((d) => ({ ...d, path: key })),
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
function usePaged<T>(
  path: string,
  workspaceId: string | null | undefined,
  extra?: SWRConfiguration<T & { path: string }>,
) {
  const res = useSWR<T & { path: string }>(
    workspaceId ? [path, workspaceId] : null,
    ([key, id]: [string, string]) =>
      api<T>(key, { workspaceId: id }).then((d) => ({ ...d, path: key })),
    { ...noRetryOn4xx, keepPreviousData: true, ...extra },
  )
  return { ...res, current: res.data?.path === path }
}

function withQuery(path: string, q: Record<string, string | number | undefined>): string {
  const qs = new URLSearchParams()
  for (const [k, v] of Object.entries(q))
    if (v !== undefined && v !== "" && v !== 0) qs.set(k, String(v))
  const s = qs.toString()
  return s ? `${path}?${s}` : path
}

/**
 * Campaigns a page at a time. Progress arrives as workspace events (MH-413; the
 * page listens and revalidates); the slow poll while one is sending is only a
 * fallback for a dropped stream.
 */
export function useCampaigns(
  workspaceId: string | null | undefined,
  p: { cursor?: string; limit?: number } = {},
) {
  type R = Page & {
    campaigns: Campaign[] | null
    smtp_configured: boolean
    whatsapp_linked: boolean
    max_audience: number
  }
  return usePaged<R>(
    withQuery("/api/w/campaigns", { cursor: p.cursor, limit: p.limit }),
    workspaceId,
    {
      refreshInterval: (data) =>
        (data?.campaigns ?? []).some((c) => c.status === "sending") ? 15000 : 0,
    },
  )
}

/** Who a WhatsApp broadcast to a list would reach right now (MH-320), in one language or all (MH-413). */
export const useWhatsAppAudience = (
  workspaceId?: string | null,
  listId?: string | null,
  locale = "",
) =>
  useScoped<{ reachable: number }>(
    `/api/w/mail/lists/${encodeURIComponent(listId ?? "")}/whatsapp-audience${locale ? `?locale=${locale}` : ""}`,
    workspaceId,
    !!listId,
  )

/** Who an email send to a list would reach right now, in one language or all (MH-413). `skipped` is
 *  subscribed contacts with no email (MH-561) the send would leave out. */
export const useEmailAudience = (
  workspaceId?: string | null,
  listId?: string | null,
  locale = "",
) =>
  useScoped<{ reachable: number; skipped: number }>(
    `/api/w/mail/lists/${encodeURIComponent(listId ?? "")}/email-audience${locale ? `?locale=${locale}` : ""}`,
    workspaceId,
    !!listId,
  )

export const useSubscribeForms = (id?: string | null) =>
  useScoped<{ forms: SubscribeForm[] | null }>("/api/w/subscribe-forms", id)

export const useSmtpSettings = (id?: string | null, enabled = true) =>
  useScoped<SmtpSettings>("/api/w/mail/smtp", id, enabled)

/** Every SMTP service of the workspace (MH-412): mail settings only. */
export const useSmtpServices = (id?: string | null, enabled = true) =>
  useScoped<{ services: SmtpService[] }>("/api/w/smtp-services", id, enabled)

/** The services a campaign can be sent through: names and roles, for anyone who reads campaigns. */
export const useSmtpServiceOptions = (id?: string | null, enabled = true) =>
  useScoped<{ services: SmtpServiceOption[] }>("/api/w/smtp-services/options", id, enabled)

// ── CRM ──────────────────────────────────────────────────────────────────

/** Companies a page at a time, A–Z, optionally searched (MH-467). */
export const useCompaniesPage = (
  workspaceId: string | null | undefined,
  p: { q?: string; cursor?: string; limit?: number; sort?: "" | "score"; minScore?: number },
) =>
  usePaged<Page & { companies: Company[] }>(
    withQuery("/api/w/companies", {
      q: p.q?.trim(),
      cursor: p.cursor,
      limit: p.limit,
      sort: p.sort || undefined,
      min_score: p.minScore || undefined,
    }),
    workspaceId,
  )

export const useCompany = (wsId?: string | null, id?: string) =>
  useScoped<
    { company: Company; contacts: ContactRef[]; deals: Deal[]; deals_total: number } & TimelinePage
  >(`/api/w/companies/${encodeURIComponent(id ?? "")}`, wsId, !!id)

export const useContact = (wsId?: string | null, id?: string) =>
  useScoped<{ contact: ContactDetail; deals: Deal[]; deals_total: number } & TimelinePage>(
    `/api/w/contacts/${encodeURIComponent(id ?? "")}`,
    wsId,
    !!id,
  )

export type DealsPage = Page & { deals: Deal[]; stages: Stage[]; currency: string }

/** The board: the first cards of every stage, each stage with its own next_cursor (MH-467). */
export const useDeals = (id?: string | null) => useScoped<DealsPage>("/api/w/deals", id)

/** The list view: the whole pipeline in board order, a page at a time. */
export const useDealsList = (
  workspaceId: string | null | undefined,
  p: { cursor?: string; limit?: number },
  enabled = true,
) =>
  usePaged<DealsPage>(
    withQuery("/api/w/deals", { view: "list", cursor: p.cursor, limit: p.limit }),
    enabled ? workspaceId : null,
  )

export const useDeal = (wsId?: string | null, id?: string) =>
  useScoped<{ deal: Deal } & TimelinePage>(
    `/api/w/deals/${encodeURIComponent(id ?? "")}`,
    wsId,
    !!id,
  )

// ── Leads (MH-390) ───────────────────────────────────────────────────────

export type LeadFilter = { status?: LeadStatus | ""; q?: string }

/** The Leads list. Filters vary, so this builds its own key (like useNews). */
export function useLeads(workspaceId: string | null | undefined, filter: LeadFilter = {}) {
  const qs = new URLSearchParams()
  if (filter.status) qs.set("status", filter.status)
  if (filter.q?.trim()) qs.set("q", filter.q.trim())
  const path = "/api/w/leads" + (qs.toString() ? `?${qs.toString()}` : "")
  type R = { leads: Lead[] | null; counts: Record<LeadStatus, number> }
  return useSWR<R>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<R>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}

export const useLead = (wsId?: string | null, id?: string) =>
  useScoped<{ lead: Lead }>(`/api/w/leads/${encodeURIComponent(id ?? "")}`, wsId, !!id)

// ── Notifications ────────────────────────────────────────────────────────

export const useWebhookSources = (id?: string | null) =>
  useScoped<{ sources: WebhookSource[] | null }>("/api/w/webhook_sources", id)

export type NotificationFilter = {
  unread?: boolean
  saved?: boolean
  /** A source key (NotificationItem.source_key). */
  sourceId?: string
  severity?: string
  kind?: string
  q?: string
  limit?: number
  cursor?: string
}

/** The notification feed, one page at a time. Query params vary per filter, so this bypasses useScoped's fixed path. */
export function useNotifications(
  workspaceId: string | null | undefined,
  filter: NotificationFilter = {},
) {
  const qs = new URLSearchParams()
  if (filter.unread) qs.set("unread", "true")
  if (filter.saved) qs.set("saved", "true")
  if (filter.sourceId) qs.set("source_id", filter.sourceId)
  if (filter.severity) qs.set("severity", filter.severity)
  if (filter.kind) qs.set("kind", filter.kind)
  if (filter.q?.trim()) qs.set("q", filter.q.trim())
  if (filter.limit) qs.set("limit", String(filter.limit))
  if (filter.cursor) qs.set("cursor", filter.cursor)
  const path = "/api/w/notifications" + (qs.toString() ? `?${qs.toString()}` : "")
  return useSWR<NotificationPage>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<NotificationPage>(p, { workspaceId: id }),
    { ...noRetryOn4xx, keepPreviousData: true },
  )
}

/** One notification with its raw payload (the detail sheet). */
export const useNotificationItem = (wsId?: string | null, id?: string | null) =>
  useScoped<{ item: NotificationItem }>(
    `/api/w/notifications/${encodeURIComponent(id ?? "")}`,
    wsId,
    !!id,
  )

/** Collected sources (Bluesky, YouTube, RSS), the catalog of kinds and the polling budget (MH-384). */
export const useCollectedSources = (id?: string | null) =>
  useScoped<CollectedSourceList>("/api/w/notification_sources", id)

// ── Inbox ────────────────────────────────────────────────────────────────

export type ConversationFilter = {
  status?: string
  unread?: boolean
  channel?: string
  /** Only this mailbox's email conversations (MH-555). */
  mailbox_id?: string
  /** Only this sender's conversations, case-insensitive (MH-600). Since MH-600's ingest-time
   *  fold, a workspace has at most one email conversation per sender, so this mostly matters
   *  for other channels reusing the filter. */
  customer_email?: string
  /** Full-text search (MH-556), kept in the URL by the page, not by this hook. */
  q?: string
  /** Page size; 50 when unset. The inbox page asks for 10 (MH-592). */
  limit?: number
  /** "unread_first" (MH-592): unread conversations, newest first, then the read ones. */
  order?: "recent" | "unread_first"
}

type ConversationPage = {
  conversations: Conversation[]
  unread_count: number
  next_cursor?: string
  /** One row per channel this workspace has a conversation on or a connection for (MH-568). */
  channel_counts: ChannelCount[]
}

function conversationsBase(filter: ConversationFilter) {
  const qs = new URLSearchParams()
  if (filter.status) qs.set("status", filter.status)
  if (filter.unread) qs.set("unread", "true")
  if (filter.channel) qs.set("channel", filter.channel)
  if (filter.mailbox_id) qs.set("mailbox_id", filter.mailbox_id)
  if (filter.customer_email) qs.set("customer_email", filter.customer_email)
  if (filter.q) qs.set("q", filter.q)
  if (filter.order) qs.set("order", filter.order)
  qs.set("limit", String(filter.limit ?? 50))
  return "/api/w/conversations?" + qs.toString()
}

/**
 * The inbox list, ONE page (the newest 50).
 *
 * No polling: the page subscribes to the workspace's event stream
 * (useWorkspaceEvents) and revalidates when a message actually arrives. SWR
 * still revalidates on focus and reconnect, which covers the case of a stream
 * that dropped while the tab was in the background.
 */
export function useConversations(
  workspaceId: string | null | undefined,
  filter: ConversationFilter = {},
) {
  const path = conversationsBase(filter)
  return useSWR<ConversationPage>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<ConversationPage>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}

/**
 * The inbox list, cursor-paged and growing (MH-556): the page asks for more
 * as the list scrolls into view, the same useSWRInfinite shape useNewsPages
 * and useTrendTopics already use, walking next_cursor instead of has_more.
 */
export function useConversationPages(
  workspaceId: string | null | undefined,
  filter: ConversationFilter = {},
  // Ids to keep among the unread under order=unread_first (MH-599). Read at
  // fetch time, not part of the key: a thread opened must not reset the pages.
  keep?: () => string[],
) {
  const base = conversationsBase(filter)
  return useSWRInfinite<ConversationPage>(
    (i, prev: ConversationPage | null) => {
      if (!workspaceId) return null
      if (i === 0) return [base, workspaceId]
      if (!prev?.next_cursor) return null
      return [`${base}&cursor=${encodeURIComponent(prev.next_cursor)}`, workspaceId]
    },
    ([p, id]: [string, string]) => {
      const ids = keep?.() ?? []
      return api<ConversationPage>(
        ids.length ? `${p}&keep=${encodeURIComponent(ids.join(","))}` : p,
        { workspaceId: id },
      )
    },
    { ...noRetryOn4xx, revalidateFirstPage: true },
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

/** Prepends an older page onto what is already loaded, deduping by id — a
 *  redelivered/overlapping page must never double a message in the thread. */
export function mergeMessages(older: InboxMessage[], newer: InboxMessage[]): InboxMessage[] {
  const seen = new Set(older.map((m) => m.id))
  return [...older, ...newer.filter((m) => !seen.has(m.id))]
}

/**
 * MH-648: one page of the messages BEFORE `before` (a ThreadPage.next_cursor,
 * from either ConversationDetail.messages_next_cursor or an earlier call to
 * this same function) — "load older", walking back from wherever the thread
 * is currently scrolled to. Not a hook: chat-thread.tsx calls this from a
 * scroll handler, not from render, so it is a plain async function over the
 * same `api` helper every other fetch in this file uses, not useSWR.
 */
export function fetchOlderMessages(
  workspaceId: string,
  conversationId: string,
  before: string,
  limit = 30,
) {
  const qs = new URLSearchParams({ cursor: before, limit: String(limit) })
  return api<ThreadPage>(
    `/api/w/conversations/${encodeURIComponent(conversationId)}/messages?${qs.toString()}`,
    { workspaceId },
  )
}

export const useChatWidgets = (id?: string | null) =>
  useScoped<{ widgets: ChatWidget[] | null }>("/api/w/chat_widgets", id)

/** The workspace's canned replies (MH-383), in picker order. */
export const useCannedReplies = (id?: string | null) =>
  useScoped<{ replies: CannedReply[] | null }>("/api/w/canned_replies", id)

/** Per-channel inbox stats over the last `days` days (MH-383). */
export const useInboxStats = (id: string | null | undefined, days: number) =>
  useScoped<InboxStats>(`/api/w/inbox/stats?days=${days}`, id)

/** The pipeline's stages, for opening a deal from a conversation (MH-379). */
export const usePipelineStages = (id?: string | null, enabled = true) =>
  useScoped<{ stages: Stage[] | null }>("/api/w/pipeline/stages", id, enabled)

// ── Trends ───────────────────────────────────────────────────────────────

export const useFeedSources = (id?: string | null) =>
  useScoped<{ sources: FeedSource[] | null }>("/api/w/feed_sources", id)

export const useTopics = (id?: string | null) =>
  useScoped<{ topics: Topic[] | null }>("/api/w/topics", id)

export type NewsFilter = {
  topic?: string
  source?: string
  saved?: boolean
  q?: string
  day?: string
}

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

export const useFeedCatalog = (id?: string | null) =>
  useScoped<{ entries: FeedCatalogEntry[] }>("/api/w/feed_catalog", id)

export const useTrendSettings = (id?: string | null) =>
  useScoped<{ settings: TrendSettings }>("/api/w/trend_settings", id)

// ── Automation ───────────────────────────────────────────────────────────

export const useBrowsers = (id?: string | null) =>
  useScoped<{ browsers: BrowserAgent[] | null; latest_extension_version: string }>(
    "/api/w/browsers",
    id,
  )

/** The extension this server offers for download (MH-399). Not workspace-scoped. */
export const useExtension = () =>
  useSWR<ExtensionInfo>(
    "/api/automation/extension",
    (p: string) => api<ExtensionInfo>(p),
    noRetryOn4xx,
  )

export const useWorkflow = (workspaceId?: string | null, id?: string) =>
  useScoped<{ workflow: AutomationWorkflow }>(
    `/api/w/workflows/${encodeURIComponent(id ?? "")}`,
    workspaceId,
    !!id,
  )

export const useWorkflowVersions = (workspaceId?: string | null, id?: string) =>
  useScoped<{ versions: WorkflowVersion[] }>(
    `/api/w/workflows/${encodeURIComponent(id ?? "")}/versions`,
    workspaceId,
    !!id,
  )

export const useAutomationSettings = (id?: string | null) =>
  useScoped<AutomationSettings>("/api/w/automation/settings", id)

/**
 * Recent runs. A run that is queued or going is polled: it is driven by a
 * browser out in the world, so nothing on this side can push an event when a
 * step finishes. Once everything has settled the polling stops.
 */
export type RunFilter = {
  agent?: string
  status?: string
  workflow?: string
  schedule?: string
  before?: string
  limit?: number
}

export function useRuns(workspaceId: string | null | undefined, filter: RunFilter | string = {}) {
  const f: RunFilter = typeof filter === "string" ? { agent: filter } : filter
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(f)) if (v !== undefined && v !== "") q.set(k, String(v))
  const qs = q.toString()
  const path = "/api/w/runs" + (qs ? `?${qs}` : "")
  return useSWR<{ runs: AutomationRun[]; next_before?: string }>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) =>
      api<{ runs: AutomationRun[]; next_before?: string }>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      // Something is going: follow it until it settles.
      refreshInterval: (data) =>
        data?.runs?.some((r) => r.status === "queued" || r.status === "running") ? 2000 : 0,
    },
  )
}

/** The server jobs a schedule can run (MH-478). */
export const useJobs = (id?: string | null) =>
  useScoped<{ jobs: AutomationJob[] }>("/api/w/automation/jobs", id)
export const usePresets = (id?: string | null) =>
  useScoped<{ version: number; presets: AutomationPreset[] }>("/api/w/automation/presets", id)
export const useAutomationProfile = (id?: string | null) =>
  useScoped<{ profile: Record<string, string>; fields: string[] }>("/api/w/automation/profile", id)

/** One run with its steps. Polled while it is still going, for the same reason. */
export function useRun(workspaceId: string | null | undefined, id?: string) {
  return useSWR<{ run: AutomationRun; steps: RunStep[] }>(
    workspaceId && id ? [`/api/w/runs/${encodeURIComponent(id)}`, workspaceId] : null,
    ([p, ws]: [string, string]) =>
      api<{ run: AutomationRun; steps: RunStep[] }>(p, { workspaceId: ws }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) =>
        data?.run.status === "queued" || data?.run.status === "running" ? 1500 : 0,
    },
  )
}

/** This workspace's mailboxes (MH-555). */
export const useMailboxes = (id?: string | null) =>
  useScoped<{ mailboxes: Mailbox[] }>("/api/w/mailboxes", id)

// ── Import a connection's chats/senders (MH-562) ────────────────────────

/** Every import source (whatsapp, email, ...) and this workspace's connections for each. */
export const useImportSources = (id?: string | null) =>
  useScoped<{ sources: ImportSourceInfo[] }>("/api/w/import/sources", id)

/** One connection's importable items, filtered before anything is written. */
export const useImportPreview = (
  id: string | null | undefined,
  source: string | null | undefined,
  filters: { connection_id?: string; q?: string; kind?: string } = {},
) => {
  const qs = new URLSearchParams()
  if (filters.connection_id) qs.set("connection_id", filters.connection_id)
  if (filters.q) qs.set("q", filters.q)
  if (filters.kind) qs.set("kind", filters.kind)
  const query = qs.toString()
  return useScoped<{ items: ImportItem[] }>(
    source ? `/api/w/import/${encodeURIComponent(source)}/preview${query ? `?${query}` : ""}` : "",
    id,
    !!source,
  )
}

/** One import job's progress; polled every `poll` ms while it runs. */
export const useImportJob = (id?: string | null, jobId?: string | null, poll = 0) =>
  useSWR<{ job: ImportJob }>(
    id && jobId ? ["/api/w/import/jobs/" + encodeURIComponent(jobId), id] : null,
    ([p, ws]: [string, string]) => api<{ job: ImportJob }>(p, { workspaceId: ws }),
    { ...noRetryOn4xx, refreshInterval: poll },
  )

/** The workspace's WhatsApp link; polled every `poll` ms while a QR or pairing code waits for the phone (MH-413). */
export const useWhatsApp = (id?: string | null, poll = 0) =>
  useSWR<WhatsAppLink>(
    id ? ["/api/w/whatsapp", id] : null,
    ([p, ws]: [string, string]) => api<WhatsAppLink>(p, { workspaceId: ws }),
    {
      ...noRetryOn4xx,
      refreshInterval: poll,
    },
  )

/** The signed-in member's WhatsApp notifications (MH-411). */
export const useWhatsAppNotify = (wsId?: string | null) =>
  useScoped<WhatsAppNotify>("/api/w/notify/whatsapp", wsId)

/** Whether one contact can be messaged on WhatsApp now, and why not (MH-410). */
export const useContactWhatsApp = (
  wsId: string | null | undefined,
  contactId: string,
  enabled = true,
) =>
  useScoped<ContactWhatsApp>(
    `/api/w/contacts/${encodeURIComponent(contactId)}/whatsapp`,
    wsId,
    enabled && !!contactId,
  )

/** A contact's context at a glance: notes, messages, WhatsApp imports, open deals (MH-586). */
export const useContactContext = (
  wsId: string | null | undefined,
  contactId: string,
  enabled = true,
) =>
  useScoped<ContextSummary>(
    `/api/w/contacts/${encodeURIComponent(contactId)}/context`,
    wsId,
    enabled && !!contactId,
  )

// ── Inbox channels: Telegram, Slack, Discord (MH-563), workflow (MH-564) ──

export const useTelegramChannels = (id?: string | null, enabled = true) =>
  useScoped<{ channels: TelegramChannel[] }>("/api/w/telegram-channels", id, enabled)

export const useSlackChannels = (id?: string | null, enabled = true) =>
  useScoped<{ channels: SlackChannel[] }>("/api/w/slack-channels", id, enabled)

export const useDiscordChannels = (id?: string | null, enabled = true) =>
  useScoped<{ channels: DiscordChannel[] }>("/api/w/discord-channels", id, enabled)

export const useWorkflowChannels = (id?: string | null, enabled = true) =>
  useScoped<{ channels: WorkflowChannel[] }>("/api/w/workflow-channels", id, enabled)

// ── Unified channels list (MH-571) ──────────────────────────────────────

export type ChannelFilter = {
  type?: ChannelType[]
  status?: ChannelStatus | ""
  q?: string
  cursor?: string
}

/**
 * Every inbox channel of every type in one list (MH-571: replaced seven
 * per-type pages). Filters are URL-driven by the caller; this only builds
 * the request. The backend sorts channels-in-error first — this hook does
 * not re-sort, so that ordering survives.
 */
export function useChannels(workspaceId: string | null | undefined, filter: ChannelFilter = {}) {
  const qs = new URLSearchParams()
  if (filter.type?.length) qs.set("type", filter.type.join(","))
  if (filter.status) qs.set("status", filter.status)
  if (filter.q?.trim()) qs.set("q", filter.q.trim())
  if (filter.cursor) qs.set("cursor", filter.cursor)
  const path = "/api/w/channels" + (qs.toString() ? `?${qs.toString()}` : "")
  return useSWR<ChannelPage>(
    workspaceId ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<ChannelPage>(p, { workspaceId: id }),
    noRetryOn4xx,
  )
}

export const useSchedules = (id?: string | null) =>
  useScoped<{ schedules: AutomationSchedule[] | null }>("/api/w/schedules", id)

export const useWorkflows = (id?: string | null) =>
  useScoped<{ workflows: AutomationWorkflow[] | null }>("/api/w/workflows", id)

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
      refreshInterval: (data) =>
        (data?.runs ?? []).some((r) => researchLive(r.status)) ? 3000 : 0,
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
export function useResearchContent(
  workspaceId: string | null | undefined,
  id: string | undefined,
  enabled: boolean,
) {
  return useSWR<ResearchDetail>(
    workspaceId && id && enabled
      ? [`/api/w/research/${encodeURIComponent(id)}?content=true`, workspaceId]
      : null,
    ([p, ws]: [string, string]) => api<ResearchDetail>(p, { workspaceId: ws }),
    noRetryOn4xx,
  )
}

// ── Research evidence (MH-388) ───────────────────────────────────────────

export type EvidenceFilter = {
  q?: string
  company?: string
  signal?: string
  domain?: string
  run?: string
  limit?: number
  offset?: number
}

function qs(params: Record<string, string | number | undefined>) {
  const u = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== "") u.set(k, String(v))
  const s = u.toString()
  return s ? `?${s}` : ""
}

/** find_evidence, for the page: every run's evidence, filtered. */
export const useEvidence = (id: string | null | undefined, f: EvidenceFilter) =>
  useScoped<EvidencePage>(`/api/w/evidence${qs(f)}`, id)

export const useCompanySuggestions = (
  id: string | null | undefined,
  status = "suggested",
  enabled = true,
) =>
  useScoped<{ suggestions: CompanySuggestion[]; total: number }>(
    `/api/w/company-suggestions${qs({ status })}`,
    id,
    enabled,
  )

export const useResearchSchedules = (id?: string | null) =>
  useScoped<{ schedules: ResearchSchedule[]; budget: ResearchBudget }>(
    "/api/w/research-schedules",
    id,
  )

// ── Prospect discovery (MH-397) ──────────────────────────────────────────

export const useProspectProfile = (id?: string | null) =>
  useScoped<ProspectProfileOut>("/api/w/prospect-profile", id)

export const useProspects = (id: string | null | undefined, status: string, q = "") =>
  useScoped<ProspectList>(`/api/w/prospects${qs({ status, q })}`, id)

export const useProspect = (id: string | null | undefined, prospectId?: string) =>
  useScoped<ProspectDetail>(
    `/api/w/prospects/${encodeURIComponent(prospectId ?? "")}`,
    id,
    !!prospectId,
  )

// ── Integrations ────────────────────────────────────────────────────────

export const useGoogleConnections = (id?: string | null, enabled = true) =>
  useScoped<GoogleConnections>("/api/w/google/connections", id, enabled)

/**
 * Connected Google accounts that have granted Contacts (MH-585), for the "Import from Google"
 * panel. Unlike useGoogleConnections, this is visible to any workspace member, not just an
 * admin — trimmed accordingly (no scopes, no connected-by identity).
 */
export const useGoogleContactsConnections = (id?: string | null, enabled = true) =>
  useScoped<GoogleContactsConnections>("/api/w/google/contacts/connections", id, enabled)

/** Last sync time/error for one Google Contacts connection, polled while a sync is running. */
export function useGoogleContactsSyncStatus(
  workspaceId: string | null | undefined,
  connectionId: string | null | undefined,
  opts: { refreshWhileSyncing?: boolean } = {},
) {
  return useSWR<GoogleContactsSyncStatus>(
    workspaceId && connectionId
      ? [`/api/w/google/contacts/${encodeURIComponent(connectionId)}/status`, workspaceId]
      : null,
    ([p, id]: [string, string]) => api<GoogleContactsSyncStatus>(p, { workspaceId: id }),
    { ...noRetryOn4xx, refreshInterval: opts.refreshWhileSyncing ? 3000 : 0 },
  )
}

/**
 * The properties, sites and channels (MH-440). An admin gets all of them; a reader the enabled
 * ones. Polled while one is syncing, so its "last synced" / error lands without a reload.
 */
export function useGoogleResources(
  workspaceId: string | null | undefined,
  kind?: string,
  enabled = true,
) {
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

/** GA4 conversions: the setup and the latest events (MH-449). Polled while an event is waiting to go out. */
export function useGa4Conversions(workspaceId: string | null | undefined, enabled = true) {
  return useSWR<Ga4ConversionsPage>(
    workspaceId && enabled ? ["/api/w/ga4-conversions", workspaceId] : null,
    ([p, id]: [string, string]) => api<Ga4ConversionsPage>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => (data?.recent?.some((e) => e.status === "pending") ? 3000 : 0),
    },
  )
}

/** The GA4 report for one property and window (MH-444). */
export function useGaReport(
  workspaceId: string | null | undefined,
  resource: string,
  days: number,
  enabled = true,
) {
  const q = new URLSearchParams({ days: String(days) })
  if (resource) q.set("resource", resource)
  return useSWR<GaReportOut>(
    workspaceId && enabled ? [`/api/w/analytics/report?${q}`, workspaceId] : null,
    ([p, id]: [string, string]) => api<GaReportOut>(p, { workspaceId: id }),
    { ...noRetryOn4xx, keepPreviousData: true },
  )
}

/** Search Console performance (MH-445). Polled while the first import has nothing yet. */
export function useGscPerformance(
  workspaceId: string | null | undefined,
  query: string,
  enabled = true,
) {
  return useSWR<GscPerformanceOut>(
    workspaceId && enabled ? [`/api/w/search-console/performance?${query}`, workspaceId] : null,
    ([p, id]: [string, string]) => api<GscPerformanceOut>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      keepPreviousData: true,
      refreshInterval: (data) =>
        data?.performance?.nothing_yet && data.resource?.syncing ? 4000 : 0,
    },
  )
}

/** A site's latest URL inspections and today's inspection budget (MH-446). */
export const useGscInspections = (workspaceId: string | null | undefined, resource: string) =>
  useScoped<GscInspectionsOut>(
    `/api/w/search-console/inspections?resource=${encodeURIComponent(resource)}`,
    workspaceId,
    !!resource,
  )

/** A site's sitemaps, live from Google (MH-446). */
export const useGscSitemaps = (workspaceId: string | null | undefined, resource: string) =>
  useScoped<GscSitemapsOut>(
    `/api/w/search-console/sitemaps?resource=${encodeURIComponent(resource)}`,
    workspaceId,
    !!resource,
  )

/** An enabled YouTube channel and today's quota (MH-441). Polled while its first sync runs. */
export function useYtChannel(
  workspaceId: string | null | undefined,
  resource: string,
  enabled = true,
) {
  const path = `/api/w/youtube/channel${resource ? `?resource=${encodeURIComponent(resource)}` : ""}`
  return useSWR<YtChannelOut>(
    workspaceId && enabled ? [path, workspaceId] : null,
    ([p, id]: [string, string]) => api<YtChannelOut>(p, { workspaceId: id }),
    {
      ...noRetryOn4xx,
      refreshInterval: (data) => (data?.resource?.syncing ? 3000 : 0),
    },
  )
}

/** A channel's uploads, 24 at a time. */
export function useYtVideos(
  workspaceId: string | null | undefined,
  resource: string,
  filter: { q?: string; privacy?: string },
  enabled = true,
) {
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
export function useYtAnalytics(
  workspaceId: string | null | undefined,
  resource: string,
  days: number,
  video = "",
  enabled = true,
) {
  const q = new URLSearchParams({ resource, days: String(days) })
  if (video) q.set("video", video)
  return useSWR<YtAnalyticsOut>(
    workspaceId && enabled && resource ? [`/api/w/youtube/analytics?${q}`, workspaceId] : null,
    ([p, id]: [string, string]) => api<YtAnalyticsOut>(p, { workspaceId: id }),
    { ...noRetryOn4xx, keepPreviousData: true },
  )
}

/** YouTube Reporting jobs and downloaded reports (MH-443). */
export const useYtReports = (workspaceId: string | null | undefined, resource: string) =>
  useScoped<YtReports>(
    `/api/w/youtube/reports?resource=${encodeURIComponent(resource)}`,
    workspaceId,
    !!resource,
  )

/** One upload with its top comments. */
export function useYtVideo(
  workspaceId: string | null | undefined,
  resource: string,
  id: string | null,
) {
  return useSWR<YtVideoDetail>(
    workspaceId && id
      ? [
          `/api/w/youtube/videos/${encodeURIComponent(id)}?resource=${encodeURIComponent(resource)}`,
          workspaceId,
        ]
      : null,
    ([p, ws]: [string, string]) => api<YtVideoDetail>(p, { workspaceId: ws }),
    noRetryOn4xx,
  )
}

// ── Alerts (MH-378) ──────────────────────────────────────────────────────

/** The header bell. Revalidated by the event stream (components/alerts), not a timer. */
export const useBell = (id?: string | null) => useScoped<Bell>("/api/w/alerts", id)

/** Web push availability and the caller's own devices. */
export const usePushStatus = (id?: string | null, enabled = true) =>
  useScoped<PushStatus>("/api/w/push", id, enabled)

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

// ── Reports (MH-394) ─────────────────────────────────────────────────────

/** `range` is the query string: `days=30`, or `from=YYYY-MM-DD&to=YYYY-MM-DD`. */
export const useCrmReport = (id: string | null | undefined, range: string) =>
  useScoped<CrmReport>(`/api/w/reports/crm?${range}`, id)

// ── Ideal-customer profile (MH-395) ──────────────────────────────────────

export const useIcpProfile = (id: string | null | undefined) =>
  useScoped<IcpProfileOut>("/api/w/icp", id)
