"use client"

/*
Customer 360 (MH-1064): the blocks the contact page adds around the panels it already had. Each one
renders real rows: the timeline from GET /api/w/contacts/{id}/timeline, the AI summary from an
existing stored context report (never written here), the journey from the contact's lifecycle and its
lifecycle history, and next actions that call the actions the page already has. A block the role may
not read is left out, never zeroed.
*/

import Link from "next/link"
import { useEffect, useState, type ReactNode } from "react"
import {
  BotIcon,
  CalendarIcon,
  CheckSquareIcon,
  CircleAlertIcon,
  FileTextIcon,
  HandshakeIcon,
  InboxIcon,
  LinkIcon,
  ListChecksIcon,
  MessageSquareIcon,
  PhoneIcon,
  RouteIcon,
  SparklesIcon,
  StickyNoteIcon,
  TrophyIcon,
  UserPlusIcon,
  UserRoundIcon,
  WorkflowIcon,
  XCircleIcon,
  WaypointsIcon,
} from "lucide-react"
import {
  AiSummary,
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Stepper,
  StepperItem,
  toast,
} from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { DealForm } from "@/components/crm/deals"
import { LifecycleBadge, ScoreMeter } from "@/components/crm/icp"
import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { Empty, Panel, useFormat } from "@/components/overview/overview-cards"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  LIFECYCLE_STAGES,
  type ContactDetail,
  type ContactReport,
  type Lifecycle,
} from "@/lib/api"
import {
  contactTimelinePath,
  useContactReports,
  useContactTimeline,
  type ContactAutomationRow,
  type ContactConversationRow,
  type ContactEvent,
  type ContactTimelinePage,
} from "@/lib/contact-timeline"
import { useTranslations } from "@/lib/i18n"
import { useCurrentWorkspace, usePipelineStages } from "@/lib/queries"
import { cn } from "@/lib/utils"
import { inboxHref } from "@/lib/inbox-link"
import { channelName, eventView, lifecycleName, type EventIcon, type Tone } from "./c360-text"

const TONE_TEXT: Record<Tone, string> = {
  success: "text-nq-success-text",
  info: "text-nq-info-text",
  violet: "text-nq-tag-violet",
  warning: "text-nq-warning-text",
  danger: "text-nq-danger-text",
  muted: "text-muted-foreground",
}
const TONE_SOFT: Record<Tone, string> = {
  success: "bg-nq-success-soft",
  info: "bg-nq-info-soft",
  violet: "bg-nq-tag-violet-soft",
  warning: "bg-nq-warning-soft",
  danger: "bg-nq-danger-soft",
  muted: "bg-nq-surface-soft",
}

function IconChip({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-md [&>svg]:size-4",
        TONE_SOFT[tone],
        TONE_TEXT[tone],
      )}
      aria-hidden
    >
      {children}
    </span>
  )
}

const EVENT_ICON: Record<EventIcon, ReactNode> = {
  contact: <UserRoundIcon />,
  note: <StickyNoteIcon />,
  call: <PhoneIcon />,
  meeting: <CalendarIcon />,
  task: <CheckSquareIcon />,
  conversation: <MessageSquareIcon />,
  deal: <HandshakeIcon />,
  won: <TrophyIcon />,
  lost: <XCircleIcon />,
  lifecycle: <WaypointsIcon />,
  lead: <UserPlusIcon />,
  rule: <WorkflowIcon />,
}

// ── header strip ─────────────────────────────────────────────────────────

/** Lifecycle, fit and every channel the contact is reachable on or has talked on. */
export function C360Header({
  contact,
  conversations,
  base,
}: {
  contact: ContactDetail
  conversations: ContactConversationRow[] | null | undefined
  base: string
}) {
  const { t, locale } = useTranslations()
  const f = useFormat()
  // Channels that are real: the email and phone on the record, each linked identity, and the channel
  // of every inbox conversation linked to the contact.
  const channels = new Set<string>()
  if (contact.email) channels.add("email")
  if (contact.phone) channels.add("phone")
  for (const i of contact.identities ?? []) channels.add(i.channel)
  for (const c of conversations ?? []) channels.add(c.channel)

  return (
    <section
      className="mx-6 mb-6 flex flex-wrap items-center gap-x-6 gap-y-3 border border-nq-line bg-card px-5 py-4"
      data-c360-header
    >
      <div className="flex items-center gap-2" data-c360-lifecycle>
        <span className="text-xs text-muted-foreground">{t("lifecycle.label")}</span>
        <LifecycleBadge stage={contact.lifecycle} />
      </div>
      <div className="flex items-center gap-2" data-c360-fit>
        <span className="text-xs text-muted-foreground">{t("icp.fit")}</span>
        <ScoreMeter score={contact.score} />
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-2" data-c360-channels>
        <span className="text-xs text-muted-foreground">{t("c360.channels")}</span>
        {channels.size === 0 ? (
          <span className="text-sm text-muted-foreground">{t("c360.noChannels")}</span>
        ) : (
          [...channels].map((c) => (
            <Badge key={c} variant="info" data-channel={c}>
              {channelName(t, c)}
            </Badge>
          ))
        )}
      </div>
      <div className="ms-auto flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        {contact.company ? (
          <Link
            className="hover:text-foreground hover:underline"
            href={`${base}/companies/${contact.company.id}`}
          >
            <Bdi>{contact.company.name}</Bdi>
          </Link>
        ) : null}
        {contact.source ? (
          <span>{t("c360.source", { source: isolate(contact.source) })}</span>
        ) : null}
        <span title={f.exact(contact.created_at)}>
          {t("c360.since", { when: dateOnly(locale, contact.created_at) })}
        </span>
      </div>
    </section>
  )
}

function dateOnly(locale: string, iso: string): string {
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(new Date(iso))
}

// ── journey ──────────────────────────────────────────────────────────────

/** subscriber → lead → qualified → customer, dated from the lifecycle history the contact page loaded. */
export function JourneyPanel({ contact }: { contact: ContactDetail }) {
  const { t, locale } = useTranslations()
  const current = contact.lifecycle ? LIFECYCLE_STAGES.indexOf(contact.lifecycle) : -1
  // The history is newest first; the date a stage was first reached is its oldest entry.
  const reached = new Map<string, string>()
  for (const e of [...(contact.lifecycle_history ?? [])].reverse()) {
    if (e.to && !reached.has(e.to)) reached.set(e.to, e.at)
  }
  return (
    <Panel id="c360-journey" title={t("c360.journey.title")} icon={<RouteIcon />}>
      <div
        className="relative overflow-x-auto px-5 py-5"
        data-c360-journey={contact.lifecycle || "none"}
      >
        <Stepper current={current} className="pointer-events-none min-w-[26rem]">
          {LIFECYCLE_STAGES.map((s) => (
            <StepperItem
              key={s}
              title={lifecycleName(t, s)}
              description={reached.has(s) ? dateOnly(locale, reached.get(s) as string) : undefined}
            />
          ))}
        </Stepper>
        {!contact.lifecycle ? (
          <p className="mt-3 text-xs text-muted-foreground">{t("c360.journey.noStage")}</p>
        ) : null}
      </div>
    </Panel>
  )
}

// ── AI summary: only ever an existing stored report ─────────────────────

/** Runs the existing "Generate report" action and refreshes the shared reports list. */
export function useGenerateReport(contactId: string, onGenerated?: () => void) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const reports = useContactReports(ws.id, contactId, ws.can("contacts:read"))
  const [generating, setGenerating] = useState(false)
  async function generate() {
    setGenerating(true)
    try {
      const res = await api<ContactReport>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/reports`,
        { method: "POST", workspaceId: ws.id },
      )
      toast.success(t("contactContext.reportGenerated", { version: res.version }))
      await reports.mutate()
      onGenerated?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setGenerating(false)
    }
  }
  return { reports, generate, generating }
}

/** The first prose line of the report's own markdown: its own words, not a new conclusion. */
function leadLine(markdown: string): string {
  for (const raw of markdown.split("\n")) {
    const line = raw.replace(/^\s*(?:[-*>]|\d+[.)])\s+/, "").trim()
    if (line && !line.startsWith("#") && !/^[-=_*]{3,}$/.test(line))
      return line.replace(/\*\*/g, "")
  }
  return ""
}

export function AiSummaryPanel({
  generate,
  generating,
  reports,
}: {
  generate: () => void
  generating: boolean
  reports: ReturnType<typeof useGenerateReport>["reports"]
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const latest = reports.data?.[0]

  let body: ReactNode
  if (reports.error)
    body = (
      <div className="p-5">
        <ErrorState error={reports.error} />
      </div>
    )
  else if (!reports.data)
    body = (
      <div className="p-5">
        <LoadingRows rows={2} />
      </div>
    )
  else if (!latest)
    body = (
      <div
        className="flex flex-1 flex-col items-start justify-center gap-1.5 px-5 py-6"
        data-empty
        data-c360-ai="empty"
      >
        <p className="text-sm font-medium text-foreground">{t("c360.ai.emptyTitle")}</p>
        <p className="max-w-md text-sm text-muted-foreground">{t("c360.ai.emptyBody")}</p>
        {canWrite ? (
          <Button
            variant="secondary"
            size="sm"
            className="mt-2"
            onClick={generate}
            disabled={generating}
            data-action="generate-report-summary"
          >
            <FileTextIcon />
            {generating ? t("contactContext.generating") : t("contactContext.generateReport")}
          </Button>
        ) : null}
      </div>
    )
  else if (latest.no_provider)
    // Built from the records without a model: shown as the report it is, never labelled AI.
    body = (
      <div className="px-5 py-4" data-c360-ai="report" data-report-version={latest.version}>
        <div className="mb-2 flex flex-wrap items-center gap-2">
          <Badge variant="neutral">
            {t("contactContext.reportVersion", { version: latest.version })}
          </Badge>
          <Badge variant="neutral">{t("contactContext.noProviderShort")}</Badge>
          {latest.created_at ? (
            <span className="text-xs text-muted-foreground">
              {dateOnly(locale, latest.created_at)}
            </span>
          ) : null}
        </div>
        <pre className="max-h-80 overflow-auto whitespace-pre-wrap font-sans text-sm">
          {latest.markdown}
        </pre>
      </div>
    )
  else
    body = (
      <div className="px-4 py-4" data-c360-ai="report" data-report-version={latest.version}>
        <AiSummary
          title={t("c360.ai.reportTitle", { version: latest.version })}
          tldr={
            leadLine(latest.markdown) ||
            t("contactContext.reportVersion", { version: latest.version })
          }
          full={latest.markdown}
          model={latest.model}
          onRegenerate={canWrite && !generating ? generate : undefined}
        />
        {latest.created_at ? (
          <p className="mt-2 text-xs text-muted-foreground">
            {t("c360.ai.storedOn", { when: dateOnly(locale, latest.created_at) })}
          </p>
        ) : null}
      </div>
    )

  return (
    <Panel id="c360-ai" title={t("c360.ai.title")} icon={<SparklesIcon />}>
      {body}
    </Panel>
  )
}

// ── next actions ─────────────────────────────────────────────────────────

function jumpTo(anchor: string, focus?: string) {
  const el = document.querySelector(anchor)
  if (!el) return
  el.scrollIntoView({ behavior: "smooth", block: "start" })
  if (focus)
    window.setTimeout(() => (document.getElementById(focus) as HTMLElement | null)?.focus(), 350)
}

/** Existing actions only: each one does what the same control lower on the page does. */
export function NextActionsPanel({
  contact,
  canWrite,
  generate,
  generating,
  onChanged,
}: {
  contact: ContactDetail
  canWrite: boolean
  generate: () => void
  generating: boolean
  onChanged: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [dealOpen, setDealOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const stages = usePipelineStages(ws.id, canWrite && dealOpen)
  const current = useCurrentWorkspace(ws.id)

  async function setStage(next: Lifecycle) {
    setBusy(true)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, {
        method: "PATCH",
        json: { lifecycle: next },
        workspaceId: ws.id,
      })
      toast.success(t("lifecycle.saved"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  if (!canWrite)
    return (
      <Panel id="c360-actions" title={t("c360.actions.title")} icon={<ListChecksIcon />}>
        <Empty title={t("c360.actions.readOnly")} />
      </Panel>
    )

  return (
    <Panel id="c360-actions" title={t("c360.actions.title")} icon={<ListChecksIcon />}>
      <div className="flex flex-col gap-2 px-5 py-4" data-c360-actions>
        <div className="grid gap-2 sm:grid-cols-2">
          <Button
            variant="secondary"
            size="sm"
            data-action="c360-log-activity"
            onClick={() => jumpTo("[data-c360-activities]", "activity-body")}
          >
            <StickyNoteIcon />
            {t("c360.actions.logActivity")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            data-action="c360-open-deal"
            onClick={() => setDealOpen(true)}
          >
            <HandshakeIcon />
            {t("c360.actions.openDeal")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            data-action="c360-link-identity"
            onClick={() => jumpTo("[data-identities-panel]", "identity-value")}
          >
            <LinkIcon />
            {t("c360.actions.linkIdentity")}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            data-action="c360-generate-report"
            onClick={generate}
            disabled={generating}
          >
            <FileTextIcon />
            {generating ? t("contactContext.generating") : t("contactContext.generateReport")}
          </Button>
        </div>
        <div className="flex items-center gap-3">
          <label htmlFor="c360-lifecycle" className="shrink-0 text-sm text-muted-foreground">
            {t("c360.actions.setLifecycle")}
          </label>
          <div className="min-w-0 flex-1">
            <PickSelect
              id="c360-lifecycle"
              value={contact.lifecycle || "__none"}
              onChange={(v) => void setStage((v === "__none" ? "" : v) as Lifecycle)}
              options={LIFECYCLE_STAGES.map((s) => ({ value: s, label: lifecycleName(t, s) }))}
              noneLabel={t("lifecycle.none")}
              disabled={busy}
            />
          </div>
        </div>
      </div>

      <Dialog open={dealOpen} onOpenChange={setDealOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t("deals.newDeal")}</DialogTitle>
            <DialogDescription>{t("deals.newDealHint")}</DialogDescription>
          </DialogHeader>
          {stages.data ? (
            <DealForm
              formId="c360-deal"
              stages={stages.data.stages ?? []}
              currency={current.data?.workspace.currency || "USD"}
              preset={{ contact_id: contact.id }}
              onSaved={() => {
                setDealOpen(false)
                onChanged()
              }}
            />
          ) : (
            <LoadingRows rows={3} />
          )}
        </DialogContent>
      </Dialog>
    </Panel>
  )
}

// ── unified timeline ─────────────────────────────────────────────────────

/** Everything that happened with the contact, newest first, read-only; older pages on demand. */
export function UnifiedTimeline({
  contactId,
  page,
  base,
}: {
  contactId: string
  page: ContactTimelinePage
  base: string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const members = useMemberNames(ws.id)
  const [older, setOlder] = useState<ContactEvent[]>([])
  const [cursor, setCursor] = useState(page.next_cursor)
  const [loading, setLoading] = useState(false)
  const first = page.events ?? []
  // A refetch of the first page (something was logged, a deal opened) replaces what was loaded after it.
  const head = `${first[0]?.kind}:${first[0]?.id}:${first[0]?.at}:${first.length}:${page.next_cursor}`
  useEffect(() => {
    setOlder([])
    setCursor(page.next_cursor)
  }, [head, page.next_cursor])

  async function loadOlder() {
    setLoading(true)
    try {
      const next = await api<ContactTimelinePage>(contactTimelinePath(contactId, cursor), {
        workspaceId: ws.id,
      })
      setOlder((o) => [...o, ...(next.events ?? [])])
      setCursor(next.next_cursor)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLoading(false)
    }
  }

  const events = [...first, ...older]
  const sources = new Set(page.sources ?? [])
  return (
    <Panel
      id="c360-timeline"
      title={t("c360.timeline.title")}
      icon={<WaypointsIcon />}
      meta={<span className="text-xs text-muted-foreground">{t("c360.timeline.readOnly")}</span>}
    >
      {events.length === 0 ? (
        <Empty title={t("c360.timeline.emptyTitle")} body={t("c360.timeline.emptyBody")} />
      ) : (
        <ul className="divide-y divide-nq-line" data-c360-timeline>
          {events.map((e, i) => {
            const v = eventView(t, e, { money: f.money, who: (id) => members.name(id) })
            const body = (
              <>
                <IconChip tone={v.tone}>{EVENT_ICON[v.icon]}</IconChip>
                <div className="min-w-0 flex-1">
                  <p className="text-sm text-foreground">
                    <bdi>{v.title}</bdi>
                  </p>
                  {v.detail ? (
                    <p className="line-clamp-2 text-xs text-muted-foreground">
                      <bdi>{v.detail}</bdi>
                    </p>
                  ) : null}
                </div>
                <time
                  dateTime={e.at}
                  title={f.exact(e.at)}
                  className="shrink-0 text-xs text-muted-foreground tabular-nums"
                >
                  {f.ago(e.at)}
                </time>
              </>
            )
            return (
              <li key={`${e.kind}-${e.id}-${e.at}-${i}`} data-event={e.kind}>
                {v.href ? (
                  <Link
                    href={v.href.startsWith("?") ? v.href : `${base}/${v.href}`}
                    className="flex items-center gap-3 px-5 py-2.5 transition-colors hover:bg-nq-surface-soft"
                  >
                    {body}
                  </Link>
                ) : (
                  <div className="flex items-center gap-3 px-5 py-2.5">{body}</div>
                )}
              </li>
            )
          })}
        </ul>
      )}
      {cursor ? (
        <div className="border-t border-nq-line px-5 py-3">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void loadOlder()}
            disabled={loading}
            data-action="c360-load-older"
          >
            {loading ? t("c360.timeline.loading") : t("c360.timeline.older")}
          </Button>
        </div>
      ) : null}
      <SourcesNote sources={sources} />
    </Panel>
  )
}

/** Says what the viewer's role cannot read, so a short timeline is not mistaken for a quiet contact. */
function SourcesNote({ sources }: { sources: Set<string> }) {
  const { t } = useTranslations()
  const hidden = (["conversations", "leads", "automations"] as const).filter((s) => !sources.has(s))
  if (hidden.length === 0) return null
  return (
    <p className="border-t border-nq-line px-5 py-3 text-xs text-muted-foreground" data-c360-hidden>
      {t("c360.timeline.hidden", { kinds: hidden.map((s) => t(`c360.source.${s}`)).join(", ") })}
    </p>
  )
}

// ── conversations ────────────────────────────────────────────────────────

export function ConversationsPanel({
  rows,
  base,
}: {
  rows: ContactConversationRow[]
  base: string
}) {
  const { t, formatNumber } = useTranslations()
  const f = useFormat()
  return (
    <Panel
      id="c360-conversations"
      title={t("c360.conversations.title")}
      icon={<InboxIcon />}
      meta={rows.length ? <Badge variant="neutral">{formatNumber(rows.length)}</Badge> : null}
    >
      {rows.length === 0 ? (
        <Empty
          title={t("c360.conversations.emptyTitle")}
          body={t("c360.conversations.emptyBody")}
        />
      ) : (
        <ul className="divide-y divide-nq-line" data-c360-conversations>
          {rows.map((c) => (
            <li key={c.id} data-conversation={c.id}>
              <Link
                href={inboxHref(c.id)}
                className="flex items-center gap-3 px-5 py-2.5 transition-colors hover:bg-nq-surface-soft"
              >
                <IconChip tone="info">
                  <MessageSquareIcon />
                </IconChip>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-foreground">
                    <bdi>{c.subject || t("c360.ev.noSubject")}</bdi>
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {channelName(t, c.channel)} ·{" "}
                    {t("c360.conversations.messages", { n: formatNumber(c.messages) })}
                    {c.last_direction
                      ? ` · ${t(`c360.conversations.last.${c.last_direction}`)}`
                      : ""}
                  </p>
                </div>
                {c.unread ? (
                  <Badge variant="warning">{t("c360.conversations.unread")}</Badge>
                ) : null}
                <Badge variant="outline">{t(`inbox.status.${c.status}`)}</Badge>
                <time
                  dateTime={c.last_message_at}
                  title={f.exact(c.last_message_at)}
                  className="shrink-0 text-xs text-muted-foreground tabular-nums"
                >
                  {f.ago(c.last_message_at)}
                </time>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

// ── automations ──────────────────────────────────────────────────────────

const RUN_VARIANT: Record<string, "success" | "danger" | "neutral"> = {
  ok: "success",
  failed: "danger",
}

export function AutomationsPanel({ rows, base }: { rows: ContactAutomationRow[]; base: string }) {
  const { t, formatNumber } = useTranslations()
  const f = useFormat()
  return (
    <Panel
      id="c360-automations"
      title={t("c360.automations.title")}
      icon={<BotIcon />}
      meta={rows.length ? <Badge variant="neutral">{formatNumber(rows.length)}</Badge> : null}
    >
      {rows.length === 0 ? (
        <Empty title={t("c360.automations.emptyTitle")} body={t("c360.automations.emptyBody")} />
      ) : (
        <ul className="divide-y divide-nq-line" data-c360-automations>
          {rows.map((a) => (
            <li key={a.rule_id} data-rule={a.rule_id}>
              <Link
                href={`${base}/automation/rules/${a.rule_id}`}
                className="flex items-center gap-3 px-5 py-2.5 transition-colors hover:bg-nq-surface-soft"
              >
                <IconChip tone={a.last_status === "failed" ? "danger" : "violet"}>
                  {a.last_status === "failed" ? <CircleAlertIcon /> : <WorkflowIcon />}
                </IconChip>
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm text-foreground">
                    <bdi>{a.rule_name}</bdi>
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {t("c360.automations.runs", { n: formatNumber(a.runs) })}
                  </p>
                </div>
                <Badge variant={RUN_VARIANT[a.last_status] ?? "neutral"}>
                  {t(`automation.status.${a.last_status}`)}
                </Badge>
                <time
                  dateTime={a.last_at}
                  title={f.exact(a.last_at)}
                  className="shrink-0 text-xs text-muted-foreground tabular-nums"
                >
                  {f.ago(a.last_at)}
                </time>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

// ── the block the page mounts ────────────────────────────────────────────

/** Header, AI summary, journey, next actions, then the timeline with conversations and automations. */
export function C360Top({
  contact,
  canWrite,
  base,
  onChanged,
  onReportChanged,
}: {
  contact: ContactDetail
  canWrite: boolean
  base: string
  onChanged: () => void
  onReportChanged: () => void
}) {
  const ws = useWorkspace()
  const timeline = useContactTimeline(ws.id, contact.id)
  const { reports, generate, generating } = useGenerateReport(contact.id, onReportChanged)
  const canRead = ws.can("contacts:read")
  return (
    <>
      <C360Header
        contact={contact}
        conversations={timeline.data?.overview?.conversations}
        base={base}
      />
      <div className="mb-6 grid gap-4 px-6 lg:grid-cols-3">
        {canRead ? (
          <div className="min-w-0 lg:col-span-2">
            <AiSummaryPanel generate={generate} generating={generating} reports={reports} />
          </div>
        ) : null}
        <div className={cn("min-w-0", canRead ? "" : "lg:col-span-3")}>
          <NextActionsPanel
            contact={contact}
            canWrite={canWrite}
            generate={generate}
            generating={generating}
            onChanged={() => {
              onChanged()
              void timeline.mutate()
            }}
          />
        </div>
      </div>
      <div className="mb-6 px-6">
        <JourneyPanel contact={contact} />
      </div>
      {timeline.error ? (
        <div className="px-6 pb-6">
          <ErrorState error={timeline.error} />
        </div>
      ) : !timeline.data ? (
        <div className="px-6 pb-6">
          <LoadingRows rows={4} />
        </div>
      ) : (
        <div className="mb-6 grid gap-4 px-6 lg:grid-cols-3">
          <div className="min-w-0 lg:col-span-2">
            <UnifiedTimeline contactId={contact.id} page={timeline.data} base={base} />
          </div>
          <div className="flex min-w-0 flex-col gap-4">
            {timeline.data.overview?.conversations ? (
              <ConversationsPanel rows={timeline.data.overview.conversations} base={base} />
            ) : null}
            {timeline.data.overview?.automations ? (
              <AutomationsPanel rows={timeline.data.overview.automations} base={base} />
            ) : null}
          </div>
        </div>
      )}
    </>
  )
}
