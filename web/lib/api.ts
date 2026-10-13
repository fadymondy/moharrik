// The one fetch wrapper for the Moharrik API. Same origin (next.config.mjs proxies /api), session
// cookie `togo_session`, CSRF token on every mutating request, X-Workspace-ID when scoped.

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
    this.name = "ApiError"
  }
}

let csrfToken: string | null = null
let csrfPending: Promise<string> | null = null

async function getCsrf(force = false): Promise<string> {
  if (csrfToken && !force) return csrfToken
  if (!csrfPending || force) {
    csrfPending = fetch("/api/auth/csrf", { credentials: "same-origin", cache: "no-store" })
      .then(async (res) => {
        if (!res.ok) throw new ApiError(res.status, await errorMessage(res))
        const data = (await res.json()) as { csrf_token?: string }
        if (!data.csrf_token) throw new ApiError(500, "The API did not return a CSRF token")
        csrfToken = data.csrf_token
        return csrfToken
      })
      .finally(() => {
        csrfPending = null
      })
  }
  return csrfPending
}

/** Forget the cached token, e.g. after sign-out (a new session gets a new token). */
export function resetCsrf() {
  csrfToken = null
}

async function errorMessage(res: Response): Promise<string> {
  try {
    const text = await res.text()
    try {
      const data = JSON.parse(text) as { error?: string; message?: string; detail?: string }
      return data.error || data.message || data.detail || text || res.statusText
    } catch {
      return text || res.statusText || `Request failed (${res.status})`
    }
  } catch {
    return res.statusText || `Request failed (${res.status})`
  }
}

export type ApiOptions = {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE"
  /** JSON body. */
  json?: unknown
  /** Multipart body (the browser sets the boundary). */
  form?: FormData
  /** Sends X-Workspace-ID for workspace-scoped endpoints. */
  workspaceId?: string | null
  /** Extra request headers, e.g. the connect page's X-Connect-Key (MH-478). */
  headers?: Record<string, string>
  signal?: AbortSignal
}

export async function api<T = unknown>(path: string, opts: ApiOptions = {}): Promise<T> {
  const method = opts.method ?? (opts.json !== undefined || opts.form ? "POST" : "GET")
  const mutating = method !== "GET"

  const send = async (forceCsrf: boolean) => {
    const headers: Record<string, string> = { Accept: "application/json", ...(opts.headers ?? {}) }
    if (opts.json !== undefined) headers["Content-Type"] = "application/json"
    if (opts.workspaceId) headers["X-Workspace-ID"] = opts.workspaceId
    if (mutating) headers["X-CSRF-Token"] = await getCsrf(forceCsrf)
    return fetch(path, {
      method,
      headers,
      body: opts.form ?? (opts.json !== undefined ? JSON.stringify(opts.json) : undefined),
      credentials: "same-origin",
      cache: "no-store",
      signal: opts.signal,
    })
  }

  let res = await send(false)
  if (!res.ok && mutating && res.status === 403) {
    const msg = await errorMessage(res.clone())
    // A stale token (new session, server restart): fetch a fresh one and retry exactly once.
    if (/csrf/i.test(msg)) res = await send(true)
  }

  if (!res.ok) throw new ApiError(res.status, await errorMessage(res))
  if (res.status === 204) return undefined as T
  const type = res.headers.get("content-type") ?? ""
  return (type.includes("json") ? await res.json() : await res.text()) as T
}

/**
 * Download a file the API streams (a CSV export) and hand it to the browser as a
 * download. A plain link cannot do this: the workspace travels in a header.
 *
 * Read as a Blob, never as text: Response.text() decodes UTF-8 and silently drops
 * the byte-order mark, and the BOM is what makes Excel read Arabic correctly.
 */
export async function downloadFile(
  path: string,
  opts: { workspaceId?: string | null; fallbackName: string },
): Promise<void> {
  const headers: Record<string, string> = { Accept: "text/csv, */*" }
  if (opts.workspaceId) headers["X-Workspace-ID"] = opts.workspaceId
  const res = await fetch(path, { headers, credentials: "same-origin", cache: "no-store" })
  if (!res.ok) throw new ApiError(res.status, await errorMessage(res))
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  try {
    const a = document.createElement("a")
    a.href = url
    a.download = fileNameFrom(res.headers.get("content-disposition")) ?? opts.fallbackName
    document.body.appendChild(a)
    a.click()
    a.remove()
  } finally {
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }
}

/** The name in Content-Disposition, preferring the UTF-8 filename* form (an Arabic list name). */
function fileNameFrom(header: string | null): string | null {
  if (!header) return null
  const star = /filename\*=UTF-8''([^;]+)/i.exec(header)
  if (star) {
    try {
      return decodeURIComponent(star[1])
    } catch {
      /* fall through to the ASCII name */
    }
  }
  const plain = /filename="([^"]+)"/i.exec(header)
  return plain ? plain[1] : null
}

/**
 * MH-659: reply to a conversation with one attachment — a file (image/video/
 * document/voice note) or a location — via the multipart endpoint added for
 * the composer. Text-only replies keep using the plain JSON `/reply` above;
 * this is only reached once there is a file/blob or a location to send.
 */
export type ReplyAttachmentInput =
  | { kind: "image" | "video" | "document"; file: File | Blob; filename?: string }
  | { kind: "voice"; file: Blob; filename: string }
  | { kind: "location"; lat: number; lng: number; name?: string; address?: string }

export async function replyWithAttachment(
  workspaceId: string,
  conversationId: string,
  body: string,
  subject: string,
  attachment: ReplyAttachmentInput,
  /** MH-664: quote this earlier message of the same conversation. */
  replyToMessageId?: string | null,
): Promise<{ message: InboxMessage }> {
  const form = new FormData()
  if (body) form.set("body", body)
  if (subject) form.set("subject", subject)
  if (replyToMessageId) form.set("reply_to_message_id", replyToMessageId)
  form.set("kind", attachment.kind)
  if (attachment.kind === "location") {
    form.set("lat", String(attachment.lat))
    form.set("lng", String(attachment.lng))
    if (attachment.name) form.set("location_name", attachment.name)
    if (attachment.address) form.set("location_address", attachment.address)
  } else {
    const filename =
      "filename" in attachment && attachment.filename
        ? attachment.filename
        : attachment.file instanceof File
          ? attachment.file.name
          : "attachment"
    form.set("file", attachment.file, filename)
  }
  return api(`/api/w/conversations/${encodeURIComponent(conversationId)}/reply/attachment`, {
    form,
    workspaceId,
  })
}

// ── Contract types ───────────────────────────────────────────────────────

export type User = { id: string | number; email: string; roles?: string[] | null }

export type Role = "owner" | "admin" | "manager" | "member" | "client"
export const INVITABLE_ROLES = ["admin", "manager", "member", "client"] as const

export type WorkspaceSummary = {
  id: string
  slug: string
  name: string
  role: Role
  locale?: string
  /** The uploaded picture (MH-679); "" draws the initial instead. */
  avatar_url?: string
  /** Active members (MH-679). */
  member_count?: number
  /** When I last acted here, or joined (MH-1108). Picks the workspace opened after sign-in. */
  last_active_at?: string
}

/** The rename/picture endpoints' response (MH-679). */
export type WorkspaceProfile = { id: string; slug: string; name: string; avatar_url: string }

export type CurrentWorkspace = {
  workspace: {
    id: string
    slug: string
    name: string
    locale?: string
    timezone?: string
    currency?: string
    /** ISO 3166-1 alpha-2, or "" if unset (MH-646). */
    default_country?: string
    avatar_url?: string
  }
  role: Role
  can: string[]
}

/** PUT /api/w/settings/default-country's response (MH-646). */
export type DefaultCountryOut = { default_country: string }

export type Member = {
  user_id: string
  email: string
  role: Role
  status: string
  joined_at: string | null
}

export type Invitation = {
  id: string
  email: string
  role: Role
  expires_at: string
  created_at: string
}

export type InvitationPreview = {
  workspace_name: string
  email: string
  role: Role
  expired: boolean
  accepted: boolean
}

export type McpToken = {
  id: string
  /** Who the token acts as (MH-396): owners and admins see every member's. */
  user_id: string
  user_email: string
  mine: boolean
  name: string
  prefix: string
  scopes: string[]
  expires_at: string | null
  last_used_at: string | null
  revoked_at: string | null
  created_at: string
}

export type McpScope = { scope: string; description: string; min_role: Role; allowed: boolean }

/** A connected app (MH-391): a client that signed in over OAuth, bound to one workspace. */
export type OAuthGrant = {
  id: string
  client_id: string
  client_name: string
  client_uri: string
  user_id: string
  user_email: string
  mine: boolean
  scopes: string[]
  last_used_at: string | null
  revoked_at: string | null
  created_at: string
}

/** What the OAuth consent page is asked to approve (MH-391). */
export type ConsentRequest = {
  client: { id: string; name: string; uri: string; metadata_document: boolean }
  redirect_uri: string
  redirect_host: string
  loopback_redirect: boolean
  requested_scopes: { scope: string; description: string; min_role: Role }[]
  workspaces: { id: string; slug: string; name: string; role: Role; allowed_scopes: string[] }[]
  resource: string
  access_ttl_seconds: number
}

/** One row of the audit log (MH-392). */
export type AuditEntry = {
  id: string
  occurred_at: string
  kind: "change" | "request"
  request_id: string
  actor_kind: "member" | "token" | "oauth" | "agent" | "admin" | ""
  actor_id: string
  actor_name: string
  user_id: string
  via: string
  operation: string
  action: string
  entity_type: string
  entity_id: string
  /** {field: {before?, after?}}; secrets are "[redacted]", message bodies "[hidden]". */
  changes: Record<string, { before?: unknown; after?: unknown }>
  status: number
  ip: string
  user_agent: string
}

export type AuditFacets = {
  actors: { kind: AuditEntry["actor_kind"]; id: string; name: string }[]
  entity_types: string[]
  actions: string[]
  retention_days: number
}

export type AuthMethods = { methods?: unknown; oauth?: unknown; providers?: unknown }

// ── Email marketing ──────────────────────────────────────────────────────

export type SubscriptionStatus = "pending" | "subscribed" | "unsubscribed" | "bounced"

export type MailList = {
  id: string
  name: string
  description: string
  double_opt_in: boolean
  subscribed: number
  pending: number
  created_at: string
}

export type Subscription = {
  id: string
  list_id: string
  list_name: string
  status: SubscriptionStatus
  confirmed_at: string | null
  unsubscribed_at: string | null
  /** A member took the address off this list (MH-408); who is on the contact's timeline. */
  unsubscribed_by_workspace: boolean
}

export type StatusCounts = Record<SubscriptionStatus | "all", number>

/** One page of GET /api/w/contacts (MH-408): keyset-paged, with the filter's total. */
export type ContactPage = {
  contacts: Contact[] | null
  /** Every contact matching the filter, across all pages. */
  total: number
  limit: number
  /** Fetches the next page; empty on the last one. */
  next_cursor: string
  /** One list's contacts by status, under the same search. Null without a list. */
  counts: StatusCounts | null
}

export type Contact = {
  id: string
  email: string
  name: string
  locale: "en" | "ar"
  source: string
  phone: string
  /** A messaging handle/username (MH-561) — the third identity, alongside email and phone. */
  handle: string
  title: string
  company_id: string | null
  company_name: string
  created_at: string
  subscriptions: Subscription[] | null
  /** WhatsApp consent (MH-320) — its own, never implied by a list. */
  whatsapp_opt_in_at: string | null
  /** Set when the contact said STOP. Only an explicit reversal clears it. */
  whatsapp_opt_out_at: string | null
  /** MH-395: the lifecycle stage ("" = none yet) and the ideal-customer score with its reasons. */
  lifecycle: Lifecycle
  score: number | null
  score_reasons: ScoreReason[] | null
  /** MH-588: best-available photo (upload > provider-cached > none); empty means render initials. */
  avatar_url: string
}

/** A contact's best display identity, email > phone > handle (MH-561: at least one is always present). */
export function contactIdentity(c: { email: string; phone: string; handle: string }): string {
  return c.email || c.phone || c.handle
}

/**
 * A phone as a name stands in for a thread with none: WhatsApp filing stores
 * digits only, so it gets its "+" back. "" when there is no phone.
 */
export function phoneLabel(phone: string | null | undefined): string {
  const p = (phone ?? "").trim()
  if (!p) return ""
  return /^\d+$/.test(p) ? `+${p}` : p
}

export type CampaignStatus = "draft" | "sending" | "sent" | "failed"

export type CampaignChannel = "email" | "whatsapp"

export type Campaign = {
  id: string
  list_id: string
  list_name: string
  channel: CampaignChannel
  subject: string
  body: string
  locale: "en" | "ar"
  status: CampaignStatus
  recipient_count: number
  sent_count: number
  failed_count: number
  /** Subscribed contacts this send skipped for having no email (MH-561). Always 0 for WhatsApp. */
  skipped_count: number
  last_error: string
  sent_at: string | null
  created_at: string
  /** The SMTP service it names (MH-412); null = the campaign default. */
  smtp_service_id: string | null
  /** The service it goes out through now: its own, else the campaign default, else the default. */
  smtp_service_name: string
  /** Only contacts in this language receive it (MH-413); "" = everyone on the list. */
  audience_locale: "" | "en" | "ar"
}

export type FormKind = "subscribe" | "inquiry"

export type SubscribeForm = {
  id: string
  /** subscribe puts an address on a list; inquiry files a lead (MH-390). */
  kind: FormKind
  /** Empty for an inquiry form. */
  list_id: string
  list_name: string
  name: string
  public_key: string
  /** Empty allows NO site to submit. */
  allowed_origins: string[] | null
  enabled: boolean
  thanks_en: string
  thanks_ar: string
  created_at: string
  /** Where a customer's page posts. */
  endpoint: string
  /** A minimal HTML form, built by the API (it knows its public URL). */
  embed: string
}

export type SmtpSettings = {
  configured: boolean
  host: string
  port: number
  username: string
  has_password: boolean
  from_name: string
  from_email: string
  tls_mode: "none" | "starttls" | "tls"
  updated_at: string | null
}

/** One of the workspace's SMTP services (MH-412). The password is never returned. */
export type SmtpService = {
  id: string
  name: string
  host: string
  port: number
  username: string
  has_password: boolean
  from_name: string
  from_email: string
  tls_mode: "none" | "starttls" | "tls"
  /** Confirmations, inbox replies and tests go through this one. */
  is_default: boolean
  /** A campaign that names no service goes through this one. */
  is_campaign_default: boolean
  last_test_at: string | null
  last_test_ok: boolean | null
  last_test_error: string
  /** How many campaigns name it explicitly. */
  campaigns: number
  created_at: string
  updated_at: string
}

/** What a campaign's service picker may know: names and roles only. */
export type SmtpServiceOption = Pick<
  SmtpService,
  "id" | "name" | "is_default" | "is_campaign_default"
>

// ── CRM ──────────────────────────────────────────────────────────────────

export type Ref = { id: string; name: string }
export type ContactRef = { id: string; email: string; name: string; title: string }

export type CompanySize = "" | "1-10" | "11-50" | "51-200" | "201-1000" | "1000+"
export const COMPANY_SIZES: CompanySize[] = ["1-10", "11-50", "51-200", "201-1000", "1000+"]

export type Company = {
  id: string
  name: string
  domain: string
  industry: string
  size: CompanySize
  country: string
  notes: string
  owner_user_id: string | null
  contact_count: number
  open_deals: number
  created_at: string
  score: number | null
  score_reasons: ScoreReason[] | null
}

export type StageKind = "open" | "won" | "lost"

/**
 * A pipeline stage. deal_count and value_minor cover the WHOLE stage (value in the workspace
 * currency), not just the cards sent; next_cursor (board view) fetches the stage's next cards.
 */
export type Stage = {
  id: string
  name: string
  position: number
  kind: StageKind
  deal_count: number
  value_minor: number
  next_cursor?: string
}

/** One page of a keyset-paged list (MH-408, MH-467). */
export type Page = { total: number; limit: number; next_cursor: string }

/** The first page of a record's timeline, carried on the record's own page. */
export type TimelinePage = {
  activities: Activity[]
  activities_total: number
  activities_next_cursor: string
  /** Linked inbox conversations in the first page's stretch of time (MH-379); older pages carry the rest. */
  conversations: ConversationRef[]
}

export type Deal = {
  id: string
  title: string
  value_minor: number
  currency: string
  stage_id: string
  stage_name: string
  stage_kind: StageKind
  position: number
  contact: ContactRef | null
  company: Ref | null
  owner_user_id: string | null
  expected_close: string | null
  closed_at: string | null
  created_at: string
}

export type ActivityKind = "note" | "call" | "meeting" | "task"
export const ACTIVITY_KINDS: ActivityKind[] = ["note", "call", "meeting", "task"]

export type Activity = {
  id: string
  kind: ActivityKind
  body: string
  due_at: string | null
  done_at: string | null
  contact: ContactRef | null
  company: Ref | null
  deal: Ref | null
  created_by: string
  created_at: string
}

/** One linked account on one channel (MH-583) — internal/contacts.Identity on the wire. */
export type ContactChannel =
  | "email"
  | "phone"
  | "whatsapp"
  | "handle"
  | "telegram"
  | "slack"
  | "discord"
  | "messenger"
  | "instagram"
  | "linkedin"
  | "x"
  | "widget"
  | "sip"
  | "other"

export const CONTACT_CHANNELS: ContactChannel[] = [
  "email",
  "phone",
  "whatsapp",
  "handle",
  "telegram",
  "slack",
  "discord",
  "messenger",
  "instagram",
  "linkedin",
  "x",
  "widget",
  "sip",
  "other",
]

export type ContactIdentity = {
  id: string
  channel: ContactChannel
  external_id: string
  label: string
  is_primary: boolean
  position: number
}

export type ContactDetail = {
  id: string
  email: string
  name: string
  locale: "en" | "ar"
  source: string
  phone: string
  /** A messaging handle/username (MH-561) — the third identity, alongside email and phone. */
  handle: string
  title: string
  tags: string[]
  company: Ref | null
  owner_user_id: string | null
  created_at: string
  subscriptions: Subscription[]
  lifecycle: Lifecycle
  lifecycle_at: string | null
  lifecycle_history: LifecycleEvent[] | null
  lifecycle_history_total: number
  score: number | null
  score_reasons: ScoreReason[] | null
  /** MH-583: Google-People-parity scalar fields. All empty-string-means-absent. */
  given_name?: string
  family_name?: string
  middle_name?: string
  name_prefix?: string
  name_suffix?: string
  phonetic_given_name?: string
  phonetic_family_name?: string
  nickname?: string
  birthday?: string | null
  biography?: string
  photo_url?: string
  /** MH-602: what the UI should actually render — an uploaded photo, else a
   *  cached provider (WhatsApp) photo, else "" so the client falls back to
   *  initials. Never derive this from photo_url yourself; the API already
   *  picked the right source. */
  avatar_url?: string
  /** Free-form key/value pairs Fady asked for by name. */
  meta: Record<string, string>
  /** Every linked account on every channel, including the ones denormalised above. */
  identities: ContactIdentity[]
  /** MH-662: the team's own free text on this contact — separate from
   *  biography, which is what the contact said about themselves. */
  notes: string
}

// ── Notification center ─────────────────────────────────────────────────

export type WebhookSourceKind = "generic" | "github" | "sentry"

export type WebhookSource = {
  id: string
  name: string
  kind: WebhookSourceKind
  enabled: boolean
  url: string
  delivery_count: number
  last_delivery_at: string | null
  last_error: string
  last_error_at: string | null
  /** Predates MH-381: still accepts the body-only signature, which is replayable. */
  legacy_signing: boolean
  /** Last delivery that verified only by the legacy scheme. */
  last_legacy_at: string | null
  /** False for a Sentry source whose Client Secret has not been pasted yet. */
  has_secret: boolean
  rotated_at: string
  created_at: string
}

export type NotificationSeverity = "info" | "warning" | "error"

export type NotificationItem = {
  id: string
  source_id: string | null
  /** The collected source (MH-384) that found it. */
  feed_source_id: string | null
  origin: NotificationOrigin
  /** What the list's source filter and mark-all-read take for this item's source. */
  source_key: string
  source_name: string
  kind: string
  severity: NotificationSeverity
  title: string
  body: string
  actor: string
  url: string
  payload: string
  occurred_at: string
  read_at: string | null
  saved_at: string | null
  /** MH-1069: set when someone marked it handled. */
  resolved_at: string | null
  /** MH-1069: hidden from the default list until this time. */
  snoozed_until: string | null
  /** actionable = open and not snoozed (the default list and every badge). */
  state: NotificationState
}

export type NotificationState = "actionable" | "snoozed" | "resolved"
export type NotificationView = "actionable" | "snoozed" | "resolved" | "all"

export type NotificationOrigin = "webhook" | "collected" | "internal"

/** One source in the feed's source filter, with its counts (MH-384). */
export type NotificationSourceFacet = {
  key: string
  name: string
  origin: NotificationOrigin
  total: number
  unread: number
}
export type NotificationKindFacet = { value: string; total: number; unread: number }

/** One page of the notification feed. */
export type NotificationPage = {
  items: NotificationItem[]
  unread_count: number
  saved_count: number
  /** MH-1069: how many items each view holds, whatever view is shown. */
  actionable_count: number
  snoozed_count: number
  resolved_count: number
  total: number
  next_cursor: string
  sources: NotificationSourceFacet[]
  kinds: NotificationKindFacet[]
}

export type CollectedKind = "bluesky" | "youtube" | "rss"

/** A polled notification source: Bluesky, YouTube or an RSS/Atom feed (MH-384). */
export type CollectedSource = {
  id: string
  kind: CollectedKind
  name: string
  target: string
  target_url: string
  enabled: boolean
  interval_minutes: number
  push_enabled: boolean
  push_kinds: string
  last_run_at: string | null
  last_collected_at: string | null
  next_run_at: string | null
  last_status: "" | "ok" | "failed"
  last_error: string
  last_item_count: number
  item_count: number
  unread_count: number
  created_at: string
}

/** A kind of source, and whether this server can collect it. */
export type NotificationKindEntry = {
  kind: string
  available: boolean
  target_hint?: "handle" | "channel" | "url"
  needs?: "google_oauth" | "oauth_app" | "browser_plugin"
}

export type CollectedSourceList = {
  sources: CollectedSource[]
  catalog: NotificationKindEntry[]
  budget: { runs_per_hour: number; limit: number }
  intervals: number[]
}

// ── Inbox ───────────────────────────────────────────────────────────────

// Not a closed union on purpose (MH-568): chat, email and whatsapp are the
// channels this build knows about, but the API accepts and lists whatever a
// workspace actually has — telegram/slack/discord and workflow-fed channels
// from a parallel batch included — so a hardcoded union here would make the
// frontend the thing standing in their way once they exist.
export type ConversationChannel = string
export type ConversationStatus = "open" | "snoozed" | "closed"

/** One channel's conversation counts (MH-568) — ConversationPage.channel_counts. */
export type ChannelCount = { channel: ConversationChannel; total: number; unread: number }

export type Conversation = {
  id: string
  channel: ConversationChannel
  subject: string
  status: ConversationStatus
  customer_name: string
  customer_email: string
  contact_id: string | null
  assignee_user_id: string | null
  unread: boolean
  last_message_at: string
  created_at: string
  preview: string
  message_count: number
  /** Where a chat visitor opened the thread from; "" for other channels. */
  page_url: string
  user_agent: string
  locale: string
  /** A WhatsApp customer's number; "" for other channels. */
  customer_phone: string
  /** The CRM contact it is linked to (MH-379). */
  contact: { id: string; name: string; email: string; company: Ref | null } | null
  /** MH-602: the linked contact's photo when there is one, else this
   *  conversation's own cached WhatsApp photo, else "" for initials. */
  avatar_url: string
  /** auto: matched on the exact email/phone when filed; manual: a member linked (or unlinked) it. */
  contact_link: "" | "auto" | "manual"
  /** The deal it was turned into. */
  deal: { id: string; title: string } | null
  /** When a snoozed conversation wakes (MH-383); null otherwise. */
  snoozed_until: string | null
  closed_at: string | null
  /** Which mailbox (MH-555) an email thread arrived on; null for chat/WhatsApp or a pre-MH-555 thread. */
  mailbox_id: string | null
  /** MH-604: a Telegram/Slack/Discord group thread with more than one possible
   *  sender — never true for chat/email/WhatsApp. Drives the per-message
   *  sender avatar; a 1:1 thread never shows one. */
  is_group: boolean
  /** MH-657 — the signed-in member's own view of the chat. */
  pinned: boolean
  archived: boolean
  /** Alerts are muted until then; null when not muted. "always" is 9999-12-31. */
  muted_until: string | null
  favourite: boolean
  /** MH-657 — the team's: a tag colour name ("" for none), a lucide icon name ("" for none), its labels. */
  color: TagColor | ""
  icon: string
  labels: ConversationLabelRef[]
  /** The customer is blocked: their messages are dropped and the chat cannot be answered. */
  blocked: boolean
  cleared_at: string | null
}

/** MH-657: the tag palette a chat's colour and a label's colour come from. */
export const TAG_COLORS = [
  "gray",
  "red",
  "orange",
  "amber",
  "green",
  "teal",
  "blue",
  "violet",
  "pink",
] as const
export type TagColor = (typeof TAG_COLORS)[number]
export type ConversationLabelRef = { id: string; name: string; color: TagColor }
export type ConversationLabel = ConversationLabelRef & { count: number; created_at: string }

export type VisitorGuess = { name: string; email: string; phone: string; locale: "en" | "ar" }

export type ConversationDetail = {
  conversation: Conversation
  /** The newest page of the thread, oldest first (MH-471). */
  messages: InboxMessage[]
  /** Messages in the whole thread. */
  messages_total: number
  /** "Load earlier": GET /api/w/conversations/{id}/messages?cursor=. Empty when the whole thread is here. */
  messages_next_cursor: string
  /** What the visitor gave, to prefill a new contact. */
  visitor: VisitorGuess
  /** The language to answer in; canned replies insert this variant. */
  reply_language: "en" | "ar"
}

/** One page of a thread, walking back from the newest (MH-471). */
export type ThreadPage = { messages: InboxMessage[]; total: number; next_cursor: string }

/** An inbox conversation as a CRM timeline shows it (MH-379). */
export type ConversationRef = {
  id: string
  channel: ConversationChannel
  subject: string
  status: ConversationStatus
  customer_name: string
  preview: string
  message_count: number
  contact: ContactRef | null
  deal_id: string | null
  last_message_at: string
  created_at: string
}

export type CannedReply = {
  id: string
  title: string
  body_en: string
  body_ar: string
  position: number
  created_at: string
  updated_at: string
}

/** An email reply template (MH-661): bilingual subject + Markdown body, with placeholders. */
export type EmailTemplate = {
  id: string
  name: string
  subject_en: string
  subject_ar: string
  body_en: string
  body_ar: string
  position: number
  created_at: string
  updated_at: string
}

export type ChannelStats = {
  channel: ConversationChannel | "all"
  conversations: number
  answered: number
  awaiting: number
  first_response_median_seconds: number | null
  first_response_mean_seconds: number | null
  resolved: number
  resolution_rate: number | null
  resolution_median_seconds: number | null
}

export type InboxStats = {
  days: number
  since: string
  channels: ChannelStats[]
  total: ChannelStats
  open_now: number
  snoozed_now: number
  unread_now: number
}

export type InboxAttachment = {
  id: string
  filename: string
  content_type: string
  size_bytes: number
  inline: boolean
  content_id?: string
  /** A signed, time-limited download URL — usable directly as <img src> or a link (MH-556). Absent for a location (renders from meta) or a failed download. */
  url?: string
  /** MH-658: how to render this attachment. Missing/"" on rows filed before this
   *  column existed — the client treats that the same as "document". */
  kind?:
    "image" | "video" | "audio" | "voice" | "document" | "sticker" | "location" | "contact" | ""
  /** MH-658: "failed" when Evolution's media download didn't come back in time
   *  or over the size cap — the message still filed, just with no bytes. */
  status?: "ready" | "failed" | ""
  /** MH-658: kind-specific data carried outside the file bytes — lat/lng/name/address
   *  for a location, name/number for a contact card. */
  meta?: {
    lat?: number
    lng?: number
    name?: string
    address?: string
    number?: string
    seconds?: number
  }
}

export type InboxMessage = {
  id: string
  direction: "in" | "out"
  author_user_id: string | null
  author_email: string
  /** Plain text: Markdown source for chat/WhatsApp, or the email's text/plain part. */
  body: string
  /** Sanitised HTML (MH-556): an email's own HTML part, or body's Markdown rendered. Empty when there is nothing beyond body. */
  body_html?: string
  mailbox_id?: string | null
  /** MH-600: an email keeps its own subject even after every sender's email
   *  folds into one conversation. "" for chat/WhatsApp/other channels — the
   *  client shows it only when it differs from the previous message's. */
  subject?: string
  attachments?: InboxAttachment[]
  created_at: string
  /** MH-604: who wrote THIS message in a group thread (Conversation.is_group);
   *  empty outside a group thread, where the conversation's own customer_name
   *  already says who it's with. */
  sender_name?: string
  sender_contact_id?: string | null
  /** The sender's cached photo, if any; "" so the client renders initials. */
  sender_avatar_url?: string
  /** MH-656: an outbound message's delivery tick — "sent" when the channel
   *  took it, then "delivered" / "read" as WhatsApp's receipts arrive. Empty
   *  for inbound messages and for channels that report no receipts. */
  status?: "" | "sent" | "delivered" | "read" | "failed"
  /** MH-662: starred by the SIGNED-IN member, not the team — the same
   *  per-member shape as pinned/archived/muted on a conversation. */
  starred?: boolean
  /** MH-664: the message this one quotes (a reply-to), when it quotes one. */
  reply_to?: InboxReplyTo
  /** MH-664: emoji reactions, one chip per emoji; omitted when there are none. */
  reactions?: InboxReaction[]
}

/** MH-664: what a reply quotes. message_id is null when the quoted message
 *  never reached Moharrik (an old WhatsApp message quoted on the phone). */
export type InboxReplyTo = {
  message_id: string | null
  body: string
  direction?: "in" | "out"
  author_user_id?: string | null
  attachment?: boolean
}

/** MH-664: one emoji chip under a bubble. `mine` = clicking it takes it back
 *  (on WhatsApp the number's reaction — one per number — not only this member's). */
export type InboxReaction = {
  emoji: string
  count: number
  mine: boolean
  reactors: { kind: "customer" | "member" | "phone"; user_id?: string }[]
}

export type ReactionResult = {
  emoji: string
  /** true when it went out on the channel (WhatsApp); false = shown in Moharrik only. */
  delivered: boolean
  reactions: InboxReaction[]
}

/** MH-662: one page of find-in-chat matches, oldest first — the same
 *  "load older" cursor shape as ThreadPage. */
export type SearchResult = { messages: InboxMessage[]; next_cursor: string }

/** MH-662: the Contact info panel's "Media, links and docs" tab counts. */
export type GalleryCounts = { media: number; links: number; docs: number }

/** MH-662: one row in a Media/Links/Docs tab — an attachment in the first
 *  two, a bare URL in the third. */
export type GalleryItem = {
  message_id: string
  created_at: string
  attachment?: InboxAttachment
  link?: string
}

/** MH-662: the whole gallery in one call — counts plus the newest rows of
 *  each kind, capped at the request's limit (default 60 per tab). */
export type GalleryPage = {
  counts: GalleryCounts
  media: GalleryItem[]
  links: GalleryItem[]
  docs: GalleryItem[]
}

/** MH-656: the card under a message that carries a URL. Only "ok" is shown. */
export type LinkPreview = {
  url: string
  status: "ok" | "none" | "error"
  title?: string
  description?: string
  image_url?: string
  site_name?: string
}

export type ChatWidget = {
  id: string
  name: string
  public_key: string
  allowed_origins: string[]
  enabled: boolean
  embed: string
  created_at: string
} & ChatWidgetSettings

/** A quick action: an opener the widget offers in an empty panel (MH-382). */
export type ChatQuickAction = {
  label: string
  message: string
  /** Only on pages whose path starts with this. */
  path?: string
  locale?: "" | "en" | "ar"
}

/** What a chat widget shows and does on the customer's page (MH-382). */
export type ChatWidgetSettings = {
  greeting: string
  greeting_ar: string
  title: string
  title_ar: string
  /** #RRGGBB, or "" for the widget's default. */
  accent: string
  theme: "auto" | "light" | "dark"
  position: "start" | "end"
  invite: boolean
  invite_after_seconds: number
  quick_actions: ChatQuickAction[]
  lead_event: boolean
  /** Ask for a name and an email or phone before the first message. */
  require_contact: boolean
  /** Who the visitor is talking to: the panel's header and "… is typing". "" = the workspace's name. */
  display_name: string
  /** An https:// picture next to the display name. "" = the workspace's picture. */
  display_avatar_url: string
}

// ── Trends ──────────────────────────────────────────────────────────────

export type FeedProvider = "feed" | "hn" | "devto"

export type FeedSource = {
  id: string
  name: string
  url: string
  kind: "rss" | "atom" | "api"
  /** feed = RSS/Atom; hn and devto are read through their public APIs. */
  provider: FeedProvider
  params: Record<string, string>
  catalog_key: string
  enabled: boolean
  interval_minutes: 360 | 720 | 1440
  /** "" until the first fetch. */
  last_status: "" | "ok" | "failed" | "over_budget"
  last_added: number
  last_fetched_at: string | null
  next_run_at: string | null
  last_error: string
  last_error_at: string | null
  item_count: number
  created_at: string
}

export type Topic = {
  id: string
  name: string
  keywords: string[]
  enabled: boolean
  item_count: number
  created_at: string
}

export type NewsItem = {
  id: string
  source_id: string | null
  source_name: string
  outlet: string
  provider: FeedProvider
  title: string
  url: string
  summary: string
  author: string
  topic_ids: string[]
  cluster_id: string | null
  rank: number
  points: number
  comments: number
  published_at: string
  saved_at: string | null
}

export type NewsPage = { items: NewsItem[]; total: number; has_more: boolean; next_cursor?: string }

/** One part of a trending topic's score. The score is the sum of its parts. */
export type TrendReason = {
  kind: "sources" | "velocity" | "relevance"
  points: number
  /** English, for API clients; the page renders kind + the structured fields. */
  label: string
  detail: string[]
  count?: number
  signal?: "burst" | "hn" | "devto"
  position?: number
  metric?: number
}

export type TrendState = "open" | "saved" | "reviewed" | "dismissed"

/** A trending topic: items from any number of sources about one story. */
export type TrendTopic = {
  id: string
  day: string
  title: string
  summary: string
  summary_source: string
  cluster_method: string
  source_count: number
  item_count: number
  outlets: string[]
  source_ids: string[]
  score: number
  reasons: TrendReason[]
  first_seen_at: string
  last_seen_at: string
  state: TrendState
  reviewed_at: string | null
  dismissed_at: string | null
  saved_at: string | null
  items: NewsItem[]
}

export type TrendCounts = {
  open: number
  saved: number
  reviewed: number
  dismissed: number
  all: number
}

export type TrendTopicPage = {
  topics: TrendTopic[]
  has_more: boolean
  next_cursor?: string
  counts: TrendCounts
  days: { day: string; count: number }[]
}

export type TrendTopicDetail = { topic: TrendTopic; outlets: { outlet: string; count: number }[] }

export type FeedCatalogEntry = {
  key: string
  name: string
  description: string
  category: "technology" | "business" | "world" | "industry"
  language: "en" | "ar"
  provider: FeedProvider
  url: string
  params?: Record<string, string>
  interval_minutes: number
  /** This workspace's source for the entry, when it has added it. */
  source_id: string | null
}

export type TrendSettings = {
  fetch_budget_per_day: number
  fetches_today: number
  budget_day: string
  scheduled_per_day: number
  max_budget: number
}

// ── Automation ──────────────────────────────────────────────────────────

export type BrowserAgent = {
  id: string
  name: string
  platform: string
  browser_version: string
  extension_version: string
  /** Older than the extension this server offers (MH-399). */
  update_available: boolean
  /** Live, from the socket registry — not the last value written to the row. */
  status: "online" | "offline"
  /** Empty allows nothing. */
  allowed_hosts: string[]
  /**
   * Chrome's side, live while connected (MH-483): "all" — a site added here
   * works at once — or "sites", each waiting for an OK in the extension.
   * Empty when offline or not reported.
   */
  site_access: "all" | "sites" | ""
  /** Allowed sites still waiting for that OK. */
  pending_hosts: string[]
  token_prefix: string
  last_seen_at: string
  revoked_at: string | null
  created_at: string
}

/** A workspace the signed-in member could connect a browser to (MH-478). */
export type ConnectWorkspace = {
  id: string
  slug: string
  name: string
  role: Role
  can_approve: boolean
}

/** A browser asking to connect, as the approval page sees it (MH-478). */
export type ConnectRequest = {
  code: string
  /** Everything below this line is what the browser SAYS about itself, shown only with the page's key. */
  requested_name: string
  platform: string
  browser_version: string
  extension_version: string
  ip: string
  status: "pending" | "approved" | "claimed" | "denied" | "expired"
  expires_at: string
  /** Whether this page holds the browser key the extension put in its address. */
  key_ok: boolean
  workspaces: ConnectWorkspace[]
  /** Only for whoever decided it. */
  workspace?: ConnectWorkspace
}

export type AutomationSettings = {
  paused: boolean
  reason: string
  paused_at: string | null
  paused_by: string
  /** The workspace's ceilings (MH-234, MH-478), and how much of the parallel one is in use. */
  max_parallel_runs: number
  retention_days: number
  runs_per_hour: number
  live_runs: number
  /** A run adds its workflow's sites to its browser (MH-517), and whether an unwatched run may add sites from values. */
  auto_add_sites: boolean
  auto_add_input_sites: boolean
}

/** A server job a schedule can run (MH-478). */
export type AutomationJob = { key: string; params: WorkflowParam[]; allowed: boolean }

export type RunStep = {
  /** A screenshot was kept for this step (MH-478). */
  has_screenshot?: boolean
  position: number
  command: string
  args: string
  status: "pending" | "ok" | "failed" | "skipped"
  summary: string
  result: string
  error: string
  duration_ms: number
}

export type AutomationRun = {
  /** Lines about the run itself, e.g. the sites it added before its first step (MH-517). */
  notes?: string[]
  rerun_of?: string
  id: string
  agent_id: string
  agent_name: string
  trigger: string
  requested_by: string
  status: "queued" | "running" | "ok" | "failed" | "cancelled"
  error: string
  step_count: number
  ok_count: number
  started_at: string | null
  finished_at: string | null
  created_at: string
  /** Which workflow, which version of it, and which schedule the run came from (MH-478). */
  workflow_id: string
  workflow_name: string
  workflow_version: number
  schedule_id: string
  schedule_name: string
  screenshots: number
  extracts: number
}

// ── Inbox: the email channel (MH-555: several mailboxes per workspace) ───

export type Mailbox = {
  id: string
  name: string
  is_primary: boolean
  host: string
  port: number
  username: string
  /** The password is write-only; this only says whether one is stored. */
  has_password: boolean
  tls_mode: "tls" | "starttls" | "none"
  folder: string
  /** Which SMTP service (MH-412) a reply on this mailbox goes out through; "" = the workspace default. */
  smtp_service_id: string
  smtp_service_name?: string
  enabled: boolean
  last_polled_at: string | null
  last_error: string
  last_test_at: string | null
  last_test_ok: boolean | null
  last_test_error: string
  message_count: number
  created_at: string
}

export type MailboxInput = {
  name?: string
  host: string
  port?: number
  username: string
  /** Empty on an update keeps the stored password. */
  password?: string
  tls_mode?: "tls" | "starttls" | "none"
  folder?: string
  smtp_service_id?: string
  enabled?: boolean
  is_primary?: boolean
}

/** @deprecated superseded by Mailbox[] (MH-555: GET /api/w/mailboxes). */
export type MailboxSettings = {
  configured: boolean
  host: string
  port: number
  username: string
  has_password: boolean
  tls_mode: "tls" | "starttls" | "none"
  folder: string
  enabled: boolean
  last_polled_at: string | null
  last_error: string
  message_count: number
}

export type WhatsAppLink = {
  linked: boolean
  /** Read live from the WhatsApp server, not from our row. */
  state: "open" | "connecting" | "close" | string
  phone: string
  qr_base64?: string
  qr_code?: string
  last_error: string
  linked_at: string | null
  /** false when this deployment has no WhatsApp server configured at all. */
  available: boolean
  /** Type this on the phone instead of scanning, when a number was given (MH-413). */
  pairing_code?: string
  /** Whether the WhatsApp server will deliver inbound messages here. */
  webhook: {
    status: "ok" | "not_set" | "wrong_url" | "disabled" | "unknown" | "not_linked"
    detail: string
    last_delivery_at: string | null
  }
  /** This number's sends since midnight in the workspace's time zone. */
  sent_today: number
  failed_today: number
}

// ── Import a connection's chats/senders (MH-562) ────────────────────────

/** One instance of an import source (a linked WhatsApp number, a mailbox). */
export type ImportConnection = {
  id: string
  label: string
  ready: boolean
  note?: string
}

export type ImportSourceInfo = {
  source: string
  connections: ImportConnection[]
}

export type ImportParticipant = { identity: string; name?: string }

/** One importable chat/sender, with its last message. */
export type ImportItem = {
  id: string
  kind: "direct" | "group"
  name: string
  identity: string
  last_message?: string
  last_message_at?: string | null
  message_count?: number
  participants?: ImportParticipant[]
  flags?: string[]
  already_imported: boolean
}

export type ImportJob = {
  id: string
  source: string
  connection_id: string
  status: "running" | "done" | "failed"
  total: number
  done: number
  contacts_created: number
  contacts_reused: number
  conversations_created: number
  conversations_reused: number
  messages_imported: number
  skipped: number
  last_error?: string
  created_at: string
  updated_at: string
}

/** Where one contact stands for a WhatsApp message (MH-410). */
export type ContactWhatsApp = {
  can_send: boolean
  reason?: "unavailable" | "not_linked" | "no_phone" | "not_opted_in" | "opted_out" | "limit"
  detail?: string
  phone: string
  whatsapp_opt_in_at: string | null
  whatsapp_opt_out_at: string | null
  /** How the contact agreed and the proof, when the opt-in was recorded with evidence. */
  whatsapp_opt_in?: {
    at: string | null
    source: "public_request" | "inbound_message" | "verbal" | "form" | ""
    evidence_url: string
    note: string
    /** The email of the member who recorded it (theirs, or their token's). */
    by: string
  }
  /** The contact's WhatsApp thread; empty when the first message would open one. */
  conversation_id: string
  limits?: {
    hour_used: number
    hour_max: number
    day_used: number
    day_max: number
    workspace_used: number
    workspace_max: number
    blocked?: string
  }
  recent: {
    at: string
    kind: "reply" | "broadcast" | "direct" | string
    ok: boolean
    error: string
  }[]
}

// ── Contact context: WhatsApp export, notes, ask, reports (MH-586) ────────

/** One retrieved/timeline item — a note, a message, a deal, or an activity. */
export type ContextPassage = {
  kind: "message" | "note" | "deal" | "activity" | "field"
  id: string
  text: string
  when?: string
  rank?: number
  conversation_id?: string
}

export type ContextImportRow = {
  id: string
  filename: string
  contact_sender: string
  message_count: number
  media_count: number
  skipped_count: number
  date_from?: string
  date_to?: string
  created_at: string
}

/** GET /api/w/contacts/{id}/context — a contact's context at a glance (MH-586). */
export type ContextSummary = {
  contact: { id: string; name: string; email: string; phone: string }
  recent_notes: ContextPassage[]
  recent_messages: ContextPassage[]
  imports: ContextImportRow[]
  open_deals: ContextPassage[]
}

export type ContextCitation = {
  kind: string
  id: string
  conversation_id?: string
  when?: string
  snippet: string
}

/** POST .../context/ask — grounded Q&A, works with zero AI provider configured. */
export type ContextAnswer = {
  text: string
  citations: ContextCitation[]
  no_provider: boolean
  provider?: string
  model?: string
}

/** POST/GET .../context/reports — a versioned, cited relationship report. */
export type ContactReport = {
  id: string
  version: number
  markdown: string
  no_provider: boolean
  provider?: string
  model?: string
  /** When the report was stored; absent on the response to a fresh POST. */
  created_at?: string
}

export type WhatsAppExportPreview = {
  filename: string
  message_count: number
  system_count: number
  media_count: number
  senders: string[]
  date_from?: string
  date_to?: string
  sample: { time: string; sender?: string; text: string; system: boolean }[]
  needs_sender_pick: boolean
  guessed_contact?: string
  already_imported_estimate: number
}

export type WhatsAppImportResult = {
  import_id: string
  conversation_id: string
  messages_added: number
  messages_skipped_duplicate: number
  media_stored: number
}

// ── Inbox channels: Telegram, Slack, Discord (MH-563), workflow (MH-564) ──

/** One of the workspace's Telegram bot connections. The bot token is never returned. */
export type TelegramChannel = {
  id: string
  name: string
  bot_username: string
  has_token: boolean
  allowed_chat_ids: string
  enabled: boolean
  last_delivery_at: string | null
  last_error: string
  last_error_at: string | null
  last_test_at: string | null
  last_test_ok: boolean | null
  webhook_url: string
  created_at: string
  updated_at: string
}

/** One of the workspace's Slack app connections. Tokens/secrets are never returned. */
export type SlackChannel = {
  id: string
  name: string
  has_bot_token: boolean
  has_signing_secret: boolean
  team_id: string
  team_name: string
  bot_user_id: string
  allowed_channel_ids: string
  enabled: boolean
  last_delivery_at: string | null
  last_error: string
  last_error_at: string | null
  last_test_at: string | null
  last_test_ok: boolean | null
  webhook_url: string
  created_at: string
  updated_at: string
}

/** One of the workspace's Discord bot connections. The bot token is never returned. */
export type DiscordChannel = {
  id: string
  name: string
  has_token: boolean
  application_id: string
  public_key: string
  bot_username: string
  allowed_guild_ids: string
  allowed_channel_ids: string
  enabled: boolean
  gateway_state: "disconnected" | "connecting" | "connected"
  last_delivery_at: string | null
  last_error: string
  last_error_at: string | null
  last_test_at: string | null
  last_test_ok: boolean | null
  interactions_url: string
  created_at: string
  updated_at: string
}

/** A browser-workflow inbox channel (MH-564): LinkedIn, Facebook, Instagram or X. */
export type WorkflowChannel = {
  id: string
  name: string
  network: "linkedin" | "facebook" | "instagram" | "x"
  workflow_id: string | null
  allow_replies: boolean
  enabled: boolean
  last_run_at: string | null
  last_error: string
  last_error_at: string | null
  last_pushed_count: number
  created_at: string
  updated_at: string
}

// ── Unified channels list (MH-571) ──────────────────────────────────────

export type ChannelType =
  "email" | "whatsapp" | "chat" | "telegram" | "slack" | "discord" | "workflow"
export type ChannelStatus = "connected" | "error" | "paused"

/** One row of GET /api/w/channels: every inbox channel of every type, in one shape. */
export type ChannelRow = {
  id: string
  type: ChannelType
  /** Only set on `type: "workflow"` rows — which social network it reads. */
  network?: "linkedin" | "facebook" | "instagram" | "x"
  name: string
  status: ChannelStatus
  last_error: string
  last_message_at: string | null
  conversations_30d: number
  unread: number
  enabled: boolean
}

export type ChannelPage = {
  channels: ChannelRow[] | null
  next_cursor?: string
  counts?: Record<ChannelType, number>
}

export type NotifyKind = "automation_failed" | "lead" | "notification"

/** The signed-in member's WhatsApp notifications in this workspace (MH-411). */
export type WhatsAppNotify = {
  available: boolean
  workspace_linked: boolean
  phone: string
  verified: boolean
  verified_at: string | null
  code_pending: boolean
  enabled: boolean
  kinds: NotifyKind[]
  all_kinds: { kind: NotifyKind; allowed: boolean }[]
  quiet_start: number
  quiet_end: number
  timezone: string
  effective_timezone: string
  daily_cap: number
  batch_minutes: number
  locale: "en" | "ar"
  pending: number
  sent_today: number
  recent: { at: string; kind: "batch" | "test"; events: number; ok: boolean; error: string }[]
}

/** POST /api/w/contacts/{id}/whatsapp/send. */
export type DirectSendResult = {
  confirmation_required: boolean
  code?: string
  expires_at?: string
  summary: string
  sent: boolean
  new_thread: boolean
  conversation_id?: string
  contact_id: string
}

export type AutomationSchedule = {
  id: string
  name: string
  agent_id: string
  agent_name: string
  cron: string
  timezone: string
  catch_up: "skip" | "once"
  enabled: boolean
  steps: { command: string; args?: Record<string, unknown>; continue_on_error?: boolean }[]
  /** The saved workflow this schedule runs, when it runs one (MH-403). */
  workflow_id: string
  workflow_name: string
  /** Only the plain values; secret ones are never sent back. */
  values: Record<string, string>
  /** Which secret values are set. */
  secret_params: string[]
  next_run_at: string | null
  last_run_at: string | null
  /** running | ok | failed | missed | waiting — "missed" and "waiting" are first-class. */
  last_status: string
  last_error: string
  last_run_id: string
  created_at: string
  /** A server job instead of a browser run (MH-478). */
  job_key: string
  failure_count: number
  missed_count: number
  /** Five consecutive failures. */
  failing: boolean
}

/** The downloadable extension (MH-399). */
export type ExtensionInfo = {
  available: boolean
  reason?: string
  version?: string
  file?: string
  bytes?: number
  sha256?: string
  site?: string
  dev?: boolean
  built_at?: string
  commit?: string
  download_url?: string
  /** A signed, self-updating package is offered to policy-managed Chrome (MH-483). */
  signed?: boolean
  extension_id?: string
  update_url?: string
}

export type WorkflowParam = {
  name: string
  label?: string
  required?: boolean
  /** Redacted from the run record. */
  secret?: boolean
  default?: string
  /** A line under the field when someone runs or schedules it (MH-478). */
  help?: string
}

export type WorkflowStep = {
  command: string
  args?: Record<string, unknown>
  continue_on_error?: boolean
}

export type AutomationWorkflow = {
  id: string
  name: string
  description: string
  steps: WorkflowStep[]
  params: WorkflowParam[]
  /** Every {{placeholder}} the steps actually use. */
  placeholders: string[]
  run_count: number
  /** The current version; every save adds one (MH-400). */
  version: number
  /** How a tool call names it, whether it may run, and when it last did (MH-478). */
  slug: string
  enabled: boolean
  last_run_at: string | null
  /** From a preset (MH-478): the sites it goes to, its icon, whether it publishes, and which preset. */
  hosts?: string[]
  icon?: string
  writes?: boolean
  preset?: string
  /** Its declared sites plus the written-out hosts its steps go to (MH-517). */
  required_sites?: string[]
  created_at: string
  updated_at: string
}

/** One preset of the library (MH-478): fadymondy.com's plugins, ready to add. */
export type AutomationPreset = {
  slug: string
  name: string
  group: string
  icon: string
  colour: string
  description: string
  hosts: string[]
  inputs: { name: string; label?: string; required?: boolean; default?: string; help?: string }[]
  steps: { command: string; args?: Record<string, unknown> }[]
  writes: boolean
  dry_run?: string
  dry_run_of?: string
  /** Plain words for people, by language (MH-518); name and description above are the technical ones. */
  plain?: Record<string, { name: string; summary: string }>
  needs_profile: string[]
  needs_whatsapp: boolean
  /** The workflow it became in this workspace, once added. */
  workflow_id?: string
}

export type WorkflowVersion = {
  version: number
  name: string
  description: string
  steps: WorkflowStep[]
  params: WorkflowParam[]
  note: "created" | "recorded" | "edited" | "restored"
  restored_from?: number
  created_at: string
}

/** The element picker (MH-400). */
export type ElementPick = {
  id: string
  agent_id: string
  status: "waiting" | "picked" | "failed"
  result?: { selector: string; fragile?: boolean; tag?: string; text?: string; host?: string }
  error?: string
  started_at: string
}

export type SchedulePreview = { timezone: string; valid: boolean; error?: string; times: string[] }

// ── Research (MH-227) ───────────────────────────────────────────────────

export type ResearchStatus = "queued" | "running" | "ok" | "failed" | "cancelled"

export type ResearchRun = {
  id: string
  topic: string
  query: string
  status: ResearchStatus
  /** Trimmed to 400 characters in the list; whole on the detail endpoint. */
  summary: string
  /** The provider that wrote the summary, empty when nothing did. */
  model: string
  /** On an "ok" run this is why there is no summary (no model, nothing readable). */
  error: string
  /** Written when the run finishes; 0 while it is still going. */
  found_count: number
  read_count: number
  requested_by: string
  started_at: string | null
  finished_at: string | null
  created_at: string
  mode: "search" | "urls"
  /** URL mode: the pages it was given. */
  urls: string[]
  origin: "manual" | "schedule" | "discovery"
  schedule_id: string
  /** The stage it is in while it runs; empty once settled. */
  stage: string
  stages: ResearchStage[]
  cost: ResearchCost
  evidence_count: number
}

export type ResearchSource = {
  id: string
  /** 0-based rank in the search results; the summary cites it as [position + 1]. */
  position: number
  url: string
  title: string
  snippet: string
  /** Only present when the run is fetched with ?content=true. */
  content?: string
  fetched: boolean
  /** Why the page could not be read, when it could not. */
  error?: string
}

export type ResearchList = {
  runs: ResearchRun[]
  can_search: boolean
  can_read: boolean
  can_summarise: boolean
  /** Today's allowance for automatic runs (schedules, discovery). */
  budget: ResearchBudget
}

export type ResearchDetail = {
  run: ResearchRun
  sources: ResearchSource[]
  /** Everything the run extracted (MH-388). */
  evidence: Evidence[]
  /** The companies its evidence names, whatever their review status. */
  companies: CompanySuggestion[]
}

// ── Research evidence (MH-388) ──────────────────────────────────────────

export const SIGNAL_TYPES = [
  "hiring",
  "funding",
  "launch",
  "expansion",
  "partnership",
  "leadership",
  "technology",
  "regulatory",
] as const
export type SignalType = (typeof SIGNAL_TYPES)[number]

export type ResearchStage = {
  stage: "plan" | "search" | "urls" | "read" | "extract" | "summarise"
  status: "pending" | "running" | "done" | "skipped" | "failed"
  count: number
  note: string
  started_at: string | null
  finished_at: string | null
}

/** Usage units, never a price. */
export type ResearchCost = {
  search_queries: number
  pages_tried: number
  pages_read: number
  model_calls: number
  paid_calls: number
  input_tokens: number
  output_tokens: number
  providers: string[]
}

export type Evidence = {
  id: string
  run_id: string
  run_topic: string
  source_id: string
  kind: "quote" | "claim"
  /** Verbatim page text. */
  quote: string
  /** A model's one-line reading; empty when the heuristic extracted it. */
  claim: string
  source_url: string
  source_title: string
  source_domain: string
  company_name: string
  company_domain: string
  company_key: string
  signal_type: SignalType | ""
  confidence: number
  published_at: string | null
  captured_at: string
  /** "heuristic" or "model:<provider>". */
  extractor: string
}

export type EvidencePage = { evidence: Evidence[]; total: number; limit: number; offset: number }

export type SuggestionStatus = "suggested" | "accepted" | "dismissed" | "linked"

export type CompanySuggestion = {
  id: string
  company_key: string
  name: string
  domain: string
  status: SuggestionStatus
  crm_company_id: string
  evidence_count: number
  strongest: number
  signal_types: string[]
  first_run_id: string
  last_run_id: string
  first_seen_at: string
  last_seen_at: string
  reviewed_at: string | null
  evidence: Evidence[]
}

export type ResearchBudget = { daily_run_budget: number; runs_today: number; day: string }

export type ResearchSchedule = {
  id: string
  name: string
  topic: string
  mode: "search" | "urls"
  urls: string[]
  cadence: "daily" | "weekly"
  enabled: boolean
  next_run_at: string
  last_run_id: string
  last_run_at: string | null
  last_status: "" | "started" | "over_budget" | "busy" | "not_configured" | "failed"
  last_note: string
  created_at: string
}

// ── Prospect discovery (MH-397) ─────────────────────────────────────────

export type ProspectProfile = {
  offer: string
  // industries/sizes/regions/keywords (MH-514): read-only here — they are the
  // workspace's CRM ICP (IcpProfile). Change them on the ICP settings page;
  // discovery follows.
  industries: string[]
  sizes: string[]
  keywords: string[]
  regions: string[]
  icp_configured: boolean
  exclude_keywords: string[]
  signal_weights: Record<string, number>
  queries: string[]
  enabled: boolean
  cadence: "daily" | "weekly"
  next_run_at: string
  last_run_id: string
  last_run_at: string | null
  last_status: "" | "started" | "over_budget" | "busy" | "not_configured" | "failed" | "no_queries"
  last_note: string
  configured: boolean
  effective_queries: string[]
}

export type ProspectProfileOut = {
  profile: ProspectProfile
  can_search: boolean
  can_summarise: boolean
  budget: ResearchBudget
  /** Who does the research: the server, or the caller's AI assistant over MCP. */
  provider?: ResearchProvider
}

// ── Bring your own agent: research done by the caller's AI assistant over MCP ──

export type ProviderMode = "auto" | "server" | "agent"

export type ResearchProvider = {
  mode: ProviderMode
  /** Who actually does it, given what this deployment can do. */
  effective: "server" | "agent"
  can_server: boolean
  can_summarise: boolean
  pending_tasks: number
  claimed_tasks: number
}

export type AgentTask = {
  id: string
  kind: "discovery" | "research" | "summary"
  status: "pending" | "claimed" | "done" | "failed" | "cancelled"
  topic: string
  mode: string
  origin: string
  run_id: string
  agent_name: string
  agent_model: string
  lease_until: string | null
  attempts: number
  received: number
  stored: number
  note: string
  error: string
  created_at: string
  finished_at: string | null
}

export type AgentProviderOut = {
  provider: ResearchProvider
  tasks: AgentTask[] | null
  /** The exact prompt to paste into Claude, Codex or any MCP client. */
  prompt: string
  mcp_url: string
  tools: string[]
}

export type DiscoverResult = {
  provider: "server" | "agent"
  run_id?: string
  task_id?: string
  query: string
}

export type ProspectReason = {
  dimension:
    | "signals"
    | "fit"
    | "region"
    | "evidence"
    | "recency"
    | "excluded"
    | "feedback_down"
    | "feedback_up"
  points: number
  items: string[] | null
  count: number
  text: string
}

export type ProspectStatus = "new" | "accepted" | "rejected"

export type Prospect = {
  id: string
  suggestion_id: string
  name: string
  domain: string
  company_key: string
  status: ProspectStatus
  score: number
  reasons: ProspectReason[]
  signal_types: string[]
  evidence_count: number
  contact: { name: string; title: string; email: string; source_url: string }
  crm_company_id: string
  crm_contact_id: string
  crm_deal_id: string
  reject_reason: string
  first_run_id: string
  decided_at: string | null
  scored_at: string
  created_at: string
}

export type ProspectDraft = {
  id: string
  locale: "en" | "ar"
  subject: string
  body: string
  generator: string
  evidence_ids: string[]
  created_at: string
}

export type ProspectDetail = { prospect: Prospect; timeline: Evidence[]; drafts: ProspectDraft[] }

export type ProspectList = { prospects: Prospect[]; counts: Record<ProspectStatus, number> }

// ── Leads (MH-390) ──────────────────────────────────────────────────────

export type LeadStatus = "new" | "contacted" | "qualified" | "converted" | "spam"
export const LEAD_STATUSES: LeadStatus[] = ["new", "contacted", "qualified", "converted", "spam"]

export type LeadRef = { id: string; name: string }

export type LeadAttribution = {
  utm_source: string
  utm_medium: string
  utm_campaign: string
  utm_term: string
  utm_content: string
  referrer: string
  gclid: string
  landing_page: string
}

export type Lead = {
  id: string
  status: LeadStatus
  name: string
  email: string
  phone: string
  /** What the visitor typed; the CRM record is converted_company. */
  company: string
  message: string
  /** What the visitor typed as a budget, as typed ("" when left out). */
  budget: string
  locale: string
  form_id: string | null
  form_name: string
  /** The allowed site the form was posted from. */
  origin: string
  attribution: LeadAttribution
  converted_contact: LeadRef | null
  converted_company: LeadRef | null
  converted_deal: LeadRef | null
  converted_at: string | null
  converted_by: string | null
  created_at: string
  updated_at: string
}

export type LeadConversion = {
  lead: Lead
  contact_id: string
  contact_created: boolean
  company_id: string
  company_created: boolean
  /** The email is free mail, so no company was made. */
  free_mail: boolean
  deal_id: string
  activity_id: string
}

// ── Integrations: Google (MH-439) ───────────────────────────────────────

// "contacts" (MH-585) is a valid product a connection can carry (it drives
// GET /api/w/google/connect?product=contacts and shows up in a connection's
// `products`), but it is deliberately NOT in GOOGLE_PRODUCTS: that list
// drives the Integrations page's generic "connect a product" rows, and
// Contacts has its own panel on the vCard import page instead (no
// property/site/channel picker, and "Sync now" belongs next to the import
// flow it feeds).
export type GoogleProduct = "analytics" | "search_console" | "youtube" | "contacts"
export const GOOGLE_PRODUCTS: GoogleProduct[] = ["analytics", "search_console", "youtube"]

/** A connected Google account. No token of any kind is ever in it. */
export type GoogleConnection = {
  id: string
  email: string
  /** Derived from what Google actually granted, not from what was asked for. */
  products: GoogleProduct[] | null
  scopes: string[] | null
  /** YouTube revenue reporting was granted. */
  revenue: boolean
  /** "" = working; "reauth" = Google refused the stored grant. */
  status: "" | "reauth"
  auth_error: string
  connected_by: string
  connected_by_email: string
  created_at: string
  updated_at: string
}

export type GoogleConnections = {
  /** false = this server has no Google OAuth client configured. */
  configured: boolean
  redirect_url: string
  connections: GoogleConnection[] | null
}

export type GoogleDisconnected = { revoked: boolean; shared: boolean; revoke_error?: string }

// ── Google Contacts import (MH-585) ──────────────────────────────────────

/** A connected Google account that has granted Contacts, trimmed for any workspace member (not just an admin). */
export type GoogleContactsConnection = {
  id: string
  email: string
  /** "" = working; "reauth" = an admin needs to reconnect it. */
  status: "" | "reauth"
}

export type GoogleContactsConnections = { connections: GoogleContactsConnection[] | null }

export type GoogleContactsSyncStatus = {
  connection_id: string
  last_synced_at: string | null
  last_error: string
  has_token: boolean
}

export type GoogleContactsSyncResult = {
  added: number
  updated: number
  flagged_deleted: number
  invalid: number
  full_resync: boolean
  /** Country actually used to resolve local phones, stored or inferred (MH-646). */
  default_country?: string
  /** Contacts synced with a phone kept raw because no country could be resolved (MH-646). */
  needs_country: number
}

/** POST /api/w/contacts/vcard/reparse-phones's response (MH-646). */
export type ReparsePhonesResult = { default_country: string; resolved: number }

// ── Google resources (MH-440) ────────────────────────────────────────────

export type GoogleKind = "ga4_property" | "gsc_site" | "yt_channel"
export const GOOGLE_KINDS: GoogleKind[] = ["ga4_property", "gsc_site", "yt_channel"]

/** A GA4 property, Search Console site or YouTube channel a connected account can see. */
export type GoogleResource = {
  id: string
  connection_id: string
  /** The account it is read through — only for integrations:manage. */
  connection_email?: string
  kind: GoogleKind
  /** As Google names it: properties/123, sc-domain:example.com, UC… */
  external_id: string
  display_name: string
  /** The GA4 account, or the site's permission level. */
  parent_name: string
  details: {
    handle?: string
    avatar_url?: string
    permission_level?: string
    account?: string
    property_type?: string
  } | null
  /** Still returned by the latest discovery. */
  available: boolean
  enabled: boolean
  auto_enabled: boolean
  last_synced_at: string | null
  next_sync_at: string
  syncing: boolean
  /** Google asked to slow down; nothing is fetched on demand before this. */
  backoff_until: string | null
  /** The last sync's failure, in plain words ("" = fine). */
  last_error: string
  /** The account needs reconnecting. */
  reauth: boolean
}

export type GoogleDiscovery = {
  connection_id: string
  product: GoogleProduct
  discovered_at: string
  found: number
  error: string
}

export type GoogleResources = {
  resources: GoogleResource[] | null
  discoveries: GoogleDiscovery[] | null
  needs_choice: GoogleKind[] | null
  available: GoogleKind[] | null
  can_manage: boolean
}

// ── GA4 conversions (MH-449) ─────────────────────────────────────────────

export type Ga4ConversionSettings = {
  /** The GA4 web stream's measurement id (G-…); "" = not set up. */
  measurement_id: string
  /** Whether a Measurement Protocol API secret is stored. The secret itself is never returned. */
  api_secret_set: boolean
  events_enabled: boolean
  tag_in_embeds: boolean
  debug_mode: boolean
  updated_at: string | null
}

export type Ga4ConversionEvent = {
  id: string
  name: string
  subject_kind: "lead" | "deal"
  subject_id: string
  status: "pending" | "sent" | "failed" | "skipped"
  attempts: number
  error: string
  /** true: the visitor's GA client id; false: a pseudonymous one; null: not sent yet. */
  attributed: boolean | null
  debug: boolean
  occurred_at: string
  sent_at: string | null
}

export type Ga4ConversionsPage = {
  settings: Ga4ConversionSettings
  events: {
    name: string
    when: "lead_created" | "lead_qualified" | "lead_converted" | "deal_won"
  }[]
  recent: Ga4ConversionEvent[] | null
}

export type Ga4Stream = {
  property: string
  property_name: string
  name: string
  measurement_id: string
  uri: string
}
export type Ga4Streams = { streams: Ga4Stream[] | null; errors: string[] | null }

export type Ga4TestResult = {
  valid: boolean
  messages: { fieldPath: string; description: string; validationCode: string }[] | null
  sent: boolean
  error?: string
}

// ── Analytics: GA4 (MH-444) ──────────────────────────────────────────────

export type GaTotals = {
  /** activeUsers over the whole window — GA4's own total, not a sum of days. */
  users: number
  new_users: number
  sessions: number
  views: number
  engaged_sessions: number
  /** 0..1 */
  engagement_rate: number
  engagement_seconds: number
  avg_engagement_seconds_per_user: number
}

export type GaReport = {
  days: number
  start_date: string
  end_date: string
  time_zone: string
  /** GA4 has no data for this property in this window. */
  empty: boolean
  totals: GaTotals
  daily: { date: string; users: number; sessions: number; views: number }[] | null
  top_pages: { path: string; views: number; users: number }[] | null
  sources: { source: string; medium: string; sessions: number; users: number }[] | null
  fetched_at: string
  stale: boolean
  note?: string
}

export type GaReportOut = {
  resource: GoogleResource | null
  report: GaReport | null
  error?: string
}

// ── Search Console (MH-445) ──────────────────────────────────────────────

export type GscTotals = {
  clicks: number
  impressions: number
  ctr: number
  position: number | null
}
/** One day. Null fields: the day was never imported (unknown), unlike 0 (imported, no data). */
export type GscPoint = {
  date: string
  clicks: number | null
  impressions: number | null
  ctr: number | null
  position: number | null
}
export type GscRow = {
  key: string
  clicks: number
  impressions: number
  ctr: number
  position: number | null
}

export type GscPerformance = {
  days: number
  start_date: string
  end_date: string
  /** site = the site's own totals; queries = sums of query rows (anonymised queries left out). */
  basis: "site" | "queries"
  totals: GscTotals
  previous: GscTotals | null
  previous_start_date?: string
  previous_end_date?: string
  daily: GscPoint[] | null
  previous_daily: GscPoint[] | null
  top_queries: GscRow[] | null
  top_pages: GscRow[] | null
  countries: string[] | null
  devices: string[] | null
  coverage: {
    imported_days: number
    first_date: string | null
    last_data_date: string | null
    last_import_at: string | null
    truncated_days: number
  }
  empty: boolean
  nothing_yet: boolean
  /** Days of the window not imported yet: totals over them are partial. */
  missing_days: number
  previous_missing_days: number
}

export type GscPerformanceOut = {
  resource: GoogleResource | null
  performance: GscPerformance | null
}

export type GscInspection = {
  url: string
  /** PASS, PARTIAL, FAIL or NEUTRAL, as Google says. */
  verdict: string
  /** Google's own words, e.g. "Submitted and indexed". */
  coverage_state: string
  indexing_state: string
  robots_txt_state: string
  page_fetch_state: string
  last_crawl_time: string | null
  google_canonical: string
  user_canonical: string
  mobile_verdict: string
  rich_results_verdict: string
  result_link: string
  inspected_at: string
}
export type GscInspectionsOut = {
  resource: GoogleResource | null
  inspections: GscInspection[] | null
  quota: { day: string; used: number; limit: number }
  can_write: boolean
}
export type GscSitemap = {
  path: string
  last_submitted: string | null
  last_downloaded: string | null
  is_pending: boolean
  is_sitemaps_index: boolean
  warnings: number | null
  errors: number | null
  submitted: number | null
  indexed: number | null
}
export type GscSitemapsOut = {
  resource: GoogleResource | null
  sitemaps: GscSitemap[] | null
  error?: string
  can_write: boolean
}

// ── YouTube (MH-441) ─────────────────────────────────────────────────────

export type YtChannel = {
  channel_id: string
  title: string
  handle: string
  avatar_url: string
  subscribers: number
  /** The channel hides its subscriber count. */
  subscribers_hidden: boolean
  /** YouTube's own channel counter (statistics.videoCount): public videos only; it can lag or leave some out. */
  videos: number
  /** The uploads Moharrik read from the uploads playlist, every privacy included. */
  uploads: number
  /** Of those, the public ones. */
  public_uploads: number
  /** How each part of the last sync went. */
  steps: YtSyncStep[] | null
  views: number
  uploads_complete: boolean
  updated_at: string
  comments_seen_until: string | null
  url: string
}

export type YtSyncStep = {
  name: "channel" | "uploads" | "comments" | "analytics" | "reports"
  /** unavailable: this server cannot do it (the error says why). */
  state: "ok" | "failed" | "unavailable" | "skipped"
  at: string
  error?: string
}

export type YtQuota = {
  day: string
  workspace_used: number
  workspace_limit: number
  global_used: number
  global_limit: number
}

export type YtChannelOut = {
  resource: GoogleResource | null
  channel: YtChannel | null
  quota: YtQuota
}

export type YtVideo = {
  id: string
  title: string
  description: string
  tags: string[] | null
  thumbnail_url: string
  published_at: string | null
  scheduled_at: string | null
  scheduled: boolean
  privacy_status: string
  duration_seconds: number
  views: number
  /** Null when the video hides its likes. */
  likes: number | null
  /** Null when comments are turned off. */
  comments: number | null
  comments_disabled: boolean
  synced_at: string
  url: string
  studio_url: string
}

export type YtVideoPage = {
  videos: YtVideo[] | null
  has_more: boolean
  next_cursor?: string
  total: number
  counts: Record<string, number> | null
}

export type YtComment = {
  id: string
  video_id: string
  author: string
  author_avatar_url: string
  text: string
  likes: number
  replies: number
  answered: boolean
  published_at: string | null
  reply_url: string
}

export type YtVideoDetail = YtVideo & { top_comments: YtComment[] | null; comment_note?: string }

// ── YouTube Analytics (MH-442) ───────────────────────────────────────────

export type YtAnalytics = {
  scope: string
  days: number
  start_date: string
  end_date: string
  empty: boolean
  totals: {
    views: number
    watch_minutes: number
    avg_view_duration_seconds: number
    subscribers_gained: number
    subscribers_lost: number
    likes: number
    comments: number
  }
  /** Null values: YouTube has not published that day yet. */
  daily:
    | {
        day: string
        views: number | null
        watch_minutes: number | null
        subscribers_gained: number | null
        subscribers_lost: number | null
      }[]
    | null
  top_videos:
    | {
        video_id: string
        title: string
        thumbnail_url: string
        views: number
        watch_minutes: number
      }[]
    | null
  traffic_sources: { source: string; views: number }[] | null
  fetched_at: string
  stale: boolean
  note?: string
}

export type YtAnalyticsOut = {
  resource: GoogleResource | null
  analytics: YtAnalytics | null
  error?: string
}

// ── YouTube Reporting (MH-443) ───────────────────────────────────────────

export type YtReports = {
  jobs:
    | {
        job_id: string
        report_type_id: string
        name: string
        created_at: string | null
        created_here: boolean
        reports: number
        latest_day: string | null
      }[]
    | null
  reports:
    | {
        report_id: string
        report_type_id: string
        start_time: string | null
        end_time: string | null
        row_count: number
        bytes: number
        downloaded_at: string
      }[]
    | null
  /** The last reporting pass; null when none has run. */
  checked_at: string | null
}

// ── Alerts and web push (MH-378) ─────────────────────────────────────────

export type BellItem = {
  kind: "conversation" | "notification"
  id: string
  /** The customer (conversation) or the item's title (notification). */
  title: string
  /** The latest message (conversation) or the source's name (notification). */
  snippet: string
  channel?: "chat" | "email" | "whatsapp"
  severity?: NotificationSeverity
  unread: boolean
  at: string
  assignee_user_id?: string | null
  /** MH-588: same signed photo link the inbox list/thread use; unset for a notification item. */
  avatar_url?: string
}

export type Bell = {
  unread_conversations: number
  unread_notifications: number
  can_inbox: boolean
  can_notifications: boolean
  items: BellItem[] | null
}

export type PushDevice = {
  id: string
  endpoint: string
  user_agent: string
  locale: "en" | "ar"
  created_at: string
  last_ok_at: string | null
  last_error: string
  last_error_at: string | null
}

export type PushStatus = { configured: boolean; public_key: string; devices: PushDevice[] | null }

export type PushResult = { sent: number; removed: number; failed: number; devices: number }

// ── Overview (MH-462) ───────────────────────────────────────────────────
// The workspace's front page in one read. A null section is one the viewer's
// role cannot read (hidden), never "empty": an empty list is [].

export type OverviewLeadCounts = Record<LeadStatus, number>

export type OverviewToday = {
  leads: OverviewLeadCounts | null
  /** Since midnight in the workspace's time zone. */
  leads_received_today: number | null
  unread_conversations: number | null
  unread_notifications: number | null
  research: {
    live: number
    latest: { id: string; topic: string; status: "queued" | "running" } | null
  } | null
}

export type OverviewStage = { id: string; name: string; deal_count: number; value_minor: number }

export type OverviewDeal = {
  id: string
  title: string
  value_minor: number
  currency: string
  stage_name: string
  company_name: string | null
  /** YYYY-MM-DD */
  expected_close: string
}

export type OverviewPipeline = {
  /** Open stages in board order. Values are in the workspace currency only. */
  stages: OverviewStage[]
  open_deals: number
  open_value_minor: number
  /** Open deals in another currency: counted, never summed. */
  other_currency_deals: number
  won_this_month: {
    since: string
    count: number
    value_minor: number
    other_currency_deals: number
  }
  next_closes: OverviewDeal[]
}

export type OverviewLead = {
  id: string
  name: string
  email: string
  company: string
  status: LeadStatus
  origin: string
  attribution: Pick<LeadAttribution, "utm_source" | "utm_medium" | "referrer" | "gclid">
  created_at: string
}

export type OverviewConversation = {
  id: string
  channel: ConversationChannel
  status: ConversationStatus
  customer_name: string
  customer_email: string
  customer_phone: string
  subject: string
  unread: boolean
  preview: string
  last_message_at: string
}

export type OverviewNotification = {
  id: string
  severity: NotificationSeverity
  title: string
  source_name: string
  url: string
  read: boolean
  occurred_at: string
}

export type OverviewMarketing = {
  /** null without contacts:read. */
  lists: { id: string; name: string; subscribed: number; pending: number }[] | null
  draft_count: number
  drafts: {
    id: string
    subject: string
    channel: CampaignChannel
    list_name: string
    created_at: string
  }[]
}

export type OverviewTrends = {
  topics: number
  matched: number
  items: {
    id: string
    title: string
    url: string
    source_name: string
    topics: string[]
    published_at: string
  }[]
}

export type SetupKey = "inquiry_form" | "smtp" | "chat_widget" | "whatsapp" | "google" | "browser"

/** One day of a zero-filled count series (MH-567). */
export type OverviewCountDay = { date: string; count: number }

/** One day of conversations started, split by channel (MH-567). */
export type OverviewConversationDay = {
  date: string
  chat: number
  email: number
  whatsapp: number
}

export type Overview = {
  currency: string
  timezone: string
  today: OverviewToday
  pipeline: OverviewPipeline | null
  leads: OverviewLead[] | null
  conversations: OverviewConversation[] | null
  notifications: OverviewNotification[] | null
  marketing: OverviewMarketing | null
  trends: OverviewTrends | null
  /** Only the items the viewer's role can see. available=false: this server cannot do it at all. */
  setup: { key: SetupKey; done: boolean; available: boolean }[] | null
  /** Null without analytics:read, or when no GA4 property / Search Console site is enabled. */
  traffic?: OverviewTraffic | null
  /** Each enabled YouTube channel (MH-441/442). */
  youtube?: OverviewYouTube[] | null
  /** Leads created per day, the last 28 days ending today, zero-filled. Null without leads:read (MH-567). */
  leads_daily?: OverviewCountDay[] | null
  /** Conversations started per day by channel, the last 28 days ending today, zero-filled. Null without inbox:read (MH-567). */
  conversations_daily?: OverviewConversationDay[] | null
}

export type OverviewYouTube = {
  resource_id: string
  title: string
  subscribers: number | null
  videos: number | null
  /** YouTube Analytics views over the last 28 days; null until fetched. */
  views_28: number | null
  views_28_to: string | null
  synced_at: string | null
  last_error: string
}

/** The last 28 days of each enabled GA4 property (MH-444). A null figure was never fetched. */
export type OverviewTraffic = {
  properties:
    | {
        resource_id: string
        name: string
        users: number | null
        sessions: number | null
        start_date: string | null
        end_date: string | null
        fetched_at: string | null
        last_error: string
      }[]
    | null
  /** Each enabled Search Console site's clicks over the 28 days ending on its newest day with data (MH-445). */
  sites?:
    | {
        resource_id: string
        name: string
        clicks: number | null
        impressions: number | null
        start_date: string | null
        end_date: string | null
        last_error: string
      }[]
    | null
  /**
   * The cross-source summary that replaced one row per property (MH-567):
   * totals over the last 28 days, each with its % change vs the 28 days
   * before that when the comparison is known. Never null when traffic
   * itself is not null.
   */
  summary: OverviewTrafficSummary | null
}

/** GA4's part of the rollup (MH-567). A total never fetched is null; a delta
 * that cannot be computed (no prior history) is null too — never a fake 0%. */
export type OverviewGA4Summary = {
  properties_count: number
  users: number | null
  users_delta_pct: number | null
  sessions: number | null
  sessions_delta_pct: number | null
  views: number | null
  views_delta_pct: number | null
  /** engaged sessions / sessions, 0..1, summed across properties. */
  engagement_rate: number | null
  avg_session_seconds: number | null
  key_events: number | null
}

/** Search Console's part of the rollup (MH-567), impression-weighted CTR/position. */
export type OverviewSearchConsoleSummary = {
  sites_count: number
  clicks: number | null
  clicks_delta_pct: number | null
  impressions: number | null
  impressions_delta_pct: number | null
  ctr: number | null
  avg_position: number | null
}

/** YouTube's part of the rollup (MH-567). Never a delta: no history to compare against. */
export type OverviewYouTubeSummary = {
  channels_count: number
  views_28: number | null
  subscribers: number | null
}

/** One row of the top-properties breakdown (MH-567), up to 5, by users. */
export type OverviewTopProperty = {
  resource_id: string
  name: string
  kind: "ga4_property"
  users: number | null
}

/** One day of the traffic-over-time chart (MH-567). A source with nothing
 * fetched for that day is null that day, never 0. */
export type OverviewTrafficDay = {
  date: string
  users: number | null
  sessions: number | null
  clicks: number | null
}

export type OverviewTrafficSummary = {
  /** YYYY-MM-DD, workspace time zone. */
  period_start: string
  period_end: string
  /** Null with no enabled GA4 property. */
  ga4: OverviewGA4Summary | null
  /** Null with no enabled Search Console site. */
  search_console: OverviewSearchConsoleSummary | null
  /** Null with no enabled YouTube channel. */
  youtube: OverviewYouTubeSummary | null
  daily: OverviewTrafficDay[]
  top_properties: OverviewTopProperty[]
}

// ── CRM report (MH-394) ────────────────────────────────────────────────────
// GET /api/w/reports/crm: every figure is the API's SQL; money is minor units of `currency`.

export type ReportStage = { id: string; name: string; deal_count: number; value_minor: number }
export type ReportConversionStep = {
  stage_id: string
  name: string
  kind: "open" | "won"
  reached: number
  from_previous: number | null
  from_start: number | null
}
export type ReportBucket = {
  start: string
  won: number
  lost: number
  won_value_minor: number
  lost_value_minor: number
}
export type ReportLeadSource = {
  source: string
  leads: number
  converted: number
  won_deals: number
  won_value_minor: number
}
export type ReportChannel = {
  channel: ConversationChannel | "all"
  conversations: number
  answered: number
  awaiting: number
  median_seconds: number | null
  mean_seconds: number | null
}
export type CrmReport = {
  from: string
  to: string
  days: number
  timezone: string
  currency: string
  bucket: "day" | "week" | "month"
  generated_at: string
  pipeline: {
    stages: ReportStage[] | null
    open_deals: number
    open_value_minor: number
    other_currency_deals: number
  }
  deals: {
    opened: number
    won: number
    lost: number
    win_rate: number | null
    won_value_minor: number
    lost_value_minor: number
    avg_won_value_minor: number | null
    other_currency_won: number
    other_currency_lost: number
  }
  conversion: { opened: number; lost: number; steps: ReportConversionStep[] | null }
  timeline: ReportBucket[] | null
  lead_sources: {
    leads: number
    spam: number
    converted: number
    sources: ReportLeadSource[] | null
  } | null
  inbox: { channels: ReportChannel[] | null; total: ReportChannel } | null
}

// ── Lifecycle + ideal-customer profile (MH-395) ────────────────────────────

export const LIFECYCLE_STAGES = ["subscriber", "lead", "qualified", "customer"] as const
export type Lifecycle = "" | (typeof LIFECYCLE_STAGES)[number]

export type LifecycleEvent = {
  id: string
  from: Lifecycle
  to: Lifecycle
  source: "subscribed" | "lead_converted" | "deal_opened" | "deal_won" | "manual" | "backfill"
  actor_user_id: string | null
  at: string
}

export const ICP_SIGNALS = [
  "open_deal",
  "won_deal",
  "lost_deal",
  "inbound_lead",
  "conversation",
  "subscribed",
  "whatsapp_opt_in",
  "recent_activity",
] as const
export type IcpSignal = (typeof ICP_SIGNALS)[number]

/** One scored dimension: its weight, how much of it is present (0..1), and weight × value. */
export type ScoreReason = {
  dimension: "industry" | "size" | "region" | "keywords" | IcpSignal
  weight: number
  value: number
  contribution: number
  matched: string[] | null
  detail: string
  text: string
}

export type IcpCriterion = { values: string[] | null; weight: number }

export type IcpProfile = {
  industries: IcpCriterion
  sizes: IcpCriterion
  regions: IcpCriterion
  keywords: IcpCriterion
  signals: Record<string, number>
}

export type IcpProfileOut = {
  profile: IcpProfile
  configured: boolean
  updated_at: string | null
  updated_by: string | null
  signals: IcpSignal[] | null
  sizes: string[] | null
  can_edit: boolean
  scored: number
  contacts: number
}
