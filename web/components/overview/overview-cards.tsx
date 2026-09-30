"use client"

/*
  The Overview's cards (MH-462). Every card is its own panel (one hairline frame on the card
  surface, spaced from its neighbours, rows divided inside), links to the page it summarises, and turns each count
  into a link to that page already filtered to it. An empty card says plainly
  what is missing and links to the action that fills it — only when the viewer
  can take that action; otherwise it just says what is missing.
*/

import Link from "next/link"
import type { ReactNode } from "react"
import {
  ArrowRightIcon,
  BellIcon,
  CheckCircle2Icon,
  ChevronDownIcon,
  CircleIcon,
  ExternalLinkIcon,
  HandshakeIcon,
  InboxIcon,
  ListChecksIcon,
  SendIcon,
  TrendingUpIcon,
  UserPlusIcon,
} from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { formatMoney } from "@/components/crm/shared"
import { LeadStatusBadge, leadSource } from "@/components/leads/shared"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  LEAD_STATUSES,
  type NotificationSeverity,
  type OverviewConversation,
  type OverviewLead,
  type OverviewLeadCounts,
  type OverviewMarketing,
  type OverviewNotification,
  type OverviewPipeline,
  type OverviewToday,
  type OverviewTrends,
  type SetupKey,
  phoneLabel,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat, numberFormat, relativeTimeFormat } from "@/lib/i18n-locale"
import { cn } from "@/lib/utils"

// ── formatting ───────────────────────────────────────────────────────────

export function useFormat() {
  const { locale } = useTranslations()
  const num = numberFormat(locale)
  const rtf = relativeTimeFormat(locale, { numeric: "auto" })
  const full = dateFormat(locale, { dateStyle: "medium", timeStyle: "short" })
  const day = dateFormat(locale, { dateStyle: "medium", timeZone: "UTC" })
  return {
    n: (v: number) => num.format(v),
    money: (minor: number, currency: string) => formatMoney(minor, currency, locale),
    /** "3 hours ago" — with the exact time kept for the title attribute. */
    ago: (iso: string) => {
      const secs = (Date.parse(iso) - Date.now()) / 1000
      const abs = Math.abs(secs)
      if (abs < 60) return rtf.format(Math.round(secs), "second")
      if (abs < 3600) return rtf.format(Math.round(secs / 60), "minute")
      if (abs < 86400) return rtf.format(Math.round(secs / 3600), "hour")
      if (abs < 86400 * 30) return rtf.format(Math.round(secs / 86400), "day")
      return full.format(new Date(iso))
    },
    exact: (iso: string) => full.format(new Date(iso)),
    /** A YYYY-MM-DD calendar date, shown as that date whatever the viewer's zone. */
    date: (ymd: string) => day.format(new Date(`${ymd}T00:00:00Z`)),
  }
}

// ── frame ────────────────────────────────────────────────────────────────

/** One card: a titled cell with a "View all" link to the page it summarises. */
export function Panel({
  id,
  title,
  icon,
  href,
  meta,
  className,
  children,
}: {
  id: string
  title: string
  icon: ReactNode
  href?: string
  meta?: ReactNode
  className?: string
  children: ReactNode
}) {
  const { t } = useTranslations()
  return (
    <section
      aria-labelledby={`ov-${id}`}
      data-card={id}
      className={cn("flex min-w-0 flex-col border border-nq-line bg-card", className)}
    >
      <header className="flex min-h-12 flex-wrap items-center gap-x-2 gap-y-1 px-5 pt-4 pb-3 [&>svg]:size-4 [&>svg]:shrink-0 [&>svg]:text-muted-foreground">
        {icon}
        <h2 id={`ov-${id}`} className="text-base font-medium text-foreground">
          {href ? (
            <Link href={href} className="hover:underline hover:underline-offset-4">
              {title}
            </Link>
          ) : (
            title
          )}
        </h2>
        {meta}
        {href ? (
          <Link
            href={href}
            className="ms-auto inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
            data-view-all={id}
          >
            {t("overview.viewAll")}
            <ArrowRightIcon className="size-3.5 rtl:-scale-x-100" aria-hidden />
          </Link>
        ) : null}
      </header>
      <div className="flex flex-1 flex-col border-t border-nq-line">{children}</div>
    </section>
  )
}

/** What a card says when there is nothing to show, and the action that fills it. */
export function Empty({
  title,
  body,
  action,
}: {
  title: string
  body?: string
  action?: { href: string; label: string }
}) {
  return (
    <div className="flex flex-1 flex-col items-start justify-center gap-1.5 px-5 py-6" data-empty>
      <p className="text-sm font-medium text-foreground">{title}</p>
      {body ? <p className="max-w-md text-sm text-muted-foreground">{body}</p> : null}
      {action ? (
        <Button
          variant="outline"
          size="sm"
          className="mt-2"
          nativeButton={false}
          render={<Link href={action.href} />}
        >
          {action.label}
        </Button>
      ) : null}
    </div>
  )
}

function Rows({ children, label }: { children: ReactNode; label: string }) {
  return (
    <ul className="divide-y divide-nq-line" aria-label={label}>
      {children}
    </ul>
  )
}

function RowLink({
  href,
  children,
  className,
  ...rest
}: { href: string; children: ReactNode; className?: string } & Record<`data-${string}`, string>) {
  return (
    <li>
      <Link
        href={href}
        className={cn(
          "flex flex-col gap-1 px-5 py-3 transition-colors hover:bg-nq-surface-soft",
          className,
        )}
        {...rest}
      >
        {children}
      </Link>
    </li>
  )
}

// ── today ────────────────────────────────────────────────────────────────

/**
 * One stat. The label wraps (two lines on a phone, never an ellipsis: "UNREAD CONVERSA…" hid the
 * one word that says what the number is) and reserves two lines below lg, so the numbers in a row
 * still line up. Numbers are Western digits in both languages (lib/i18n-locale.ts).
 */
function Tile({
  href,
  label,
  value,
  sub,
  id,
}: {
  href: string
  label: string
  value: string
  sub?: ReactNode
  id: string
}) {
  return (
    <Link
      href={href}
      className="flex min-w-0 flex-col gap-2 border border-nq-line bg-card px-5 py-4 transition-colors hover:bg-nq-surface-soft"
      data-tile={id}
    >
      <span className="eyebrow min-h-[2lh] text-balance lg:min-h-0">{label}</span>
      <span className="text-4xl leading-none font-medium text-foreground tabular-nums" data-tile-value>
        {value}
      </span>
      <span className="truncate text-xs text-muted-foreground">{sub}</span>
    </Link>
  )
}

/** The four stat tiles. A tile the viewer's role cannot read is left out, not zeroed. */
export function TodayStrip({ today, base }: { today: OverviewToday; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const tiles: ReactNode[] = []
  if (today.leads) {
    tiles.push(
      <Tile
        key="leads"
        id="new-leads"
        href={`${base}/leads?status=new`}
        label={t("overview.newLeads")}
        value={f.n(today.leads.new)}
        sub={t("overview.receivedToday", { n: f.n(today.leads_received_today ?? 0) })}
      />,
    )
  }
  if (today.unread_conversations !== null) {
    tiles.push(
      <Tile
        key="inbox"
        id="unread-conversations"
        href={`${base}/inbox?unread=1`}
        label={t("overview.unreadConversations")}
        value={f.n(today.unread_conversations)}
        sub={today.unread_conversations === 0 ? t("overview.allClear") : t("nav.inbox")}
      />,
    )
  }
  if (today.unread_notifications !== null) {
    tiles.push(
      <Tile
        key="notifications"
        id="unread-notifications"
        href={`${base}/notifications?unread=1`}
        label={t("overview.unreadNotifications")}
        value={f.n(today.unread_notifications)}
        sub={today.unread_notifications === 0 ? t("overview.allClear") : t("nav.notifications")}
      />,
    )
  }
  if (today.research) {
    const live = today.research.latest
    tiles.push(
      <Tile
        key="research"
        id="research"
        href={live ? `${base}/research/${live.id}` : `${base}/research`}
        label={t("overview.research")}
        value={f.n(today.research.live)}
        sub={live ? <bdi>{live.topic}</bdi> : t("overview.researchIdle")}
      />,
    )
  }
  if (!tiles.length) return null
  return (
    <div
      className={cn(
        "grid grid-cols-2 gap-3 sm:gap-4",
        tiles.length >= 4 ? "lg:grid-cols-4" : tiles.length === 3 ? "lg:grid-cols-3" : "",
      )}
      role="group"
      aria-label={t("overview.today")}
      data-today
    >
      {tiles}
    </div>
  )
}

// ── pipeline ─────────────────────────────────────────────────────────────

/**
 * Open value per stage as a table with an inline bar: one series, so one hue
 * and no legend; the value is printed in the row, so the bar is decoration for
 * assistive tech (the table is the data).
 */
export function PipelineCard({
  p,
  currency,
  base,
  className,
}: {
  p: OverviewPipeline
  currency: string
  base: string
  className?: string
}) {
  const { t } = useTranslations()
  const f = useFormat()
  const max = Math.max(0, ...p.stages.map((s) => s.value_minor))
  const hasDeals = p.open_deals > 0 || p.won_this_month.count > 0
  const won = p.won_this_month

  return (
    <Panel
      id="pipeline"
      title={t("overview.pipeline")}
      icon={<HandshakeIcon />}
      href={`${base}/deals`}
      meta={
        hasDeals ? (
          <span className="text-xs text-muted-foreground">
            <Ltr>{f.money(p.open_value_minor, currency)}</Ltr> ·{" "}
            {t("overview.openDeals", { n: f.n(p.open_deals) })}
          </span>
        ) : null
      }
      className={className}
    >
      {!hasDeals ? (
        <Empty
          title={t("overview.noDeals")}
          body={t("overview.noDealsBody")}
          action={{ href: `${base}/deals`, label: t("deals.newDeal") }}
        />
      ) : (
        <div className="grid flex-1 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:divide-x lg:divide-nq-line rtl:lg:divide-x-reverse">
          <div className="min-w-0">
            <table className="w-full text-sm" data-pipeline-stages>
              <thead>
                <tr className="border-b border-nq-line text-start text-xs text-muted-foreground">
                  <th scope="col" className="w-full px-5 py-2 text-start font-normal">
                    {t("overview.stage")}
                  </th>
                  <th scope="col" className="px-2 py-2 text-end font-normal">
                    {t("overview.deals")}
                  </th>
                  <th scope="col" className="px-5 py-2 text-end font-normal">
                    {t("overview.value")}
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-nq-line">
                {p.stages.map((s) => (
                  <tr key={s.id} data-stage={s.name}>
                    <th scope="row" className="px-5 pt-2.5 pb-3 text-start align-top font-normal">
                      <Link
                        href={`${base}/deals`}
                        className="hover:underline hover:underline-offset-4"
                      >
                        <bdi className="text-foreground">{s.name}</bdi>
                      </Link>
                      {/* One hue, 8px, 4px rounded data-end, square at the baseline. */}
                      <div className="mt-2 h-2" aria-hidden title={t("overview.stageBarLabel")}>
                        <div
                          className="h-2 rounded-e-[4px] bg-nq-brand"
                          style={{
                            width:
                              max > 0 && s.value_minor > 0
                                ? `max(2px, ${(s.value_minor / max) * 100}%)`
                                : 0,
                          }}
                        />
                      </div>
                    </th>
                    <td className="px-2 pt-2.5 text-end align-top tabular-nums">
                      <Link
                        href={`${base}/deals`}
                        className="hover:underline hover:underline-offset-4"
                        data-stage-count
                      >
                        {f.n(s.deal_count)}
                      </Link>
                    </td>
                    <td
                      className="px-5 pt-2.5 text-end align-top whitespace-nowrap tabular-nums text-foreground"
                      data-stage-value
                    >
                      <Ltr>{f.money(s.value_minor, currency)}</Ltr>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {p.other_currency_deals > 0 ? (
              <p
                className="border-t border-nq-line px-5 py-2.5 text-xs text-muted-foreground"
                data-other-currency
              >
                {t("overview.otherCurrency", { n: f.n(p.other_currency_deals) })}
              </p>
            ) : null}
            <Link
              href={`${base}/deals`}
              className="flex flex-col gap-1 border-t border-nq-line px-5 py-3 transition-colors hover:bg-nq-surface-soft"
              data-won-month
            >
              <span className="eyebrow">{t("overview.wonThisMonth")}</span>
              <span className="text-2xl font-medium text-foreground">
                <Ltr>{f.money(won.value_minor, currency)}</Ltr>
              </span>
              <span className="text-xs text-muted-foreground">
                {t("overview.wonCount", { n: f.n(won.count) })}
                {won.other_currency_deals > 0
                  ? ` · ${t("overview.wonOtherCurrency", { n: f.n(won.other_currency_deals) })}`
                  : ""}
              </span>
            </Link>
          </div>

          <div className="flex min-w-0 flex-col border-t border-nq-line lg:border-t-0">
            <p className="eyebrow px-5 pt-3 pb-1">{t("overview.nextCloses")}</p>
            {p.next_closes.length === 0 ? (
              <p className="px-5 pb-4 text-sm text-muted-foreground">{t("overview.noNextCloses")}</p>
            ) : (
              <Rows label={t("overview.nextCloses")}>
                {p.next_closes.map((d) => (
                  <RowLink
                    key={d.id}
                    href={`${base}/deals/${d.id}`}
                    className="flex-row items-baseline gap-3"
                    data-next-close={d.title}
                  >
                    <span className="min-w-0 flex-1">
                      <span className="bidi-block block truncate text-sm text-foreground" dir="auto">
                        {d.title}
                      </span>
                      <span className="block truncate text-xs text-muted-foreground">
                        <bdi>{d.company_name ?? d.stage_name}</bdi> ·{" "}
                        <Ltr>{f.date(d.expected_close)}</Ltr>
                      </span>
                    </span>
                    <span className="shrink-0 text-sm tabular-nums">
                      <Ltr>{f.money(d.value_minor, d.currency)}</Ltr>
                    </span>
                  </RowLink>
                ))}
              </Rows>
            )}
          </div>
        </div>
      )}
    </Panel>
  )
}

// ── leads ────────────────────────────────────────────────────────────────

export function LeadsCard({
  leads,
  counts,
  base,
  canAddForm,
}: {
  leads: OverviewLead[]
  counts: OverviewLeadCounts | null
  base: string
  canAddForm: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <Panel
      id="leads"
      title={t("overview.latestLeads")}
      icon={<UserPlusIcon />}
      href={`${base}/leads`}
    >
      {counts && leads.length > 0 ? (
        <div
          className="flex flex-wrap gap-1.5 border-b border-nq-line px-5 py-2.5"
          role="group"
          aria-label={t("overview.leadsByStatus")}
        >
          {LEAD_STATUSES.map((s) => (
            <Link
              key={s}
              href={`${base}/leads?status=${s}`}
              className="inline-flex items-center gap-1.5 border border-nq-line px-2 py-0.5 text-xs text-nq-fg-body transition-colors hover:bg-nq-surface-soft"
              data-lead-count={s}
            >
              {t(`leads.status.${s}`)}
              <span className="font-medium text-foreground tabular-nums">{f.n(counts[s] ?? 0)}</span>
            </Link>
          ))}
        </div>
      ) : null}
      {leads.length === 0 ? (
        <Empty
          title={t("overview.noLeads")}
          body={t("overview.noLeadsBody")}
          action={
            canAddForm
              ? { href: `${base}/subscribe-forms`, label: t("forms.addInquiry") }
              : undefined
          }
        />
      ) : (
        <Rows label={t("overview.latestLeads")}>
          {leads.map((l) => (
            <RowLink key={l.id} href={`${base}/leads/${l.id}`} data-overview-lead={l.email}>
              <span className="flex min-w-0 items-center gap-2">
                <span className="bidi-block truncate text-sm font-medium text-foreground" dir="auto">
                  {l.name || l.email}
                </span>
                {l.company ? (
                  <span className="hidden truncate text-sm text-muted-foreground sm:inline">
                    ·&nbsp;<bdi>{l.company}</bdi>
                  </span>
                ) : null}
                <LeadStatusBadge status={l.status} className="ms-auto shrink-0" />
              </span>
              <span className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
                <bdi>{leadSource(l, t)}</bdi>
                <span aria-hidden>·</span>
                <time dateTime={l.created_at} title={f.exact(l.created_at)}>
                  {f.ago(l.created_at)}
                </time>
              </span>
            </RowLink>
          ))}
        </Rows>
      )}
    </Panel>
  )
}

// ── inbox ────────────────────────────────────────────────────────────────

export function InboxCard({
  items,
  unread,
  base,
  canAddWidget,
}: {
  items: OverviewConversation[]
  unread: number | null
  base: string
  canAddWidget: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <Panel
      id="inbox"
      title={t("nav.inbox")}
      icon={<InboxIcon />}
      href={`${base}/inbox`}
      meta={
        unread ? (
          <Link
            href={`${base}/inbox?unread=1`}
            className="text-xs text-muted-foreground hover:text-foreground hover:underline"
            data-unread-link
          >
            {t("overview.unreadConversations")}:{" "}
            <span className="font-medium text-foreground">{f.n(unread)}</span>
          </Link>
        ) : null
      }
    >
      {items.length === 0 ? (
        <Empty
          title={t("overview.noConversations")}
          body={t("overview.noConversationsBody")}
          action={
            canAddWidget
              ? { href: `${base}/inbox/widgets`, label: t("inbox.addWidget") }
              : undefined
          }
        />
      ) : (
        <Rows label={t("nav.inbox")}>
          {items.map((c) => (
            <RowLink
              key={c.id}
              href={`${base}/inbox?c=${encodeURIComponent(c.id)}`}
              className={c.unread ? "bg-card" : undefined}
              data-overview-conversation={c.id}
            >
              <span className="flex min-w-0 items-center gap-2">
                {c.unread ? (
                  <span
                    className="size-2 shrink-0 rounded-full bg-nq-brand"
                    role="img"
                    aria-label={t("inbox.unread")}
                  />
                ) : null}
                <span
                  className={cn(
                    "bidi-block truncate text-sm text-foreground",
                    c.unread && "font-medium",
                  )}
                  dir="auto"
                >
                  {c.customer_name ||
                    c.customer_email ||
                    phoneLabel(c.customer_phone) ||
                    t("inbox.anonymous")}
                </span>
                <Badge variant="outline" className="ms-auto shrink-0">
                  {t(`inbox.channel.${c.channel}`)}
                </Badge>
              </span>
              <span className="flex min-w-0 gap-2 text-xs text-muted-foreground">
                <span className="bidi-block min-w-0 flex-1 truncate" dir="auto">
                  {c.preview || c.subject}
                </span>
                <time
                  className="shrink-0"
                  dateTime={c.last_message_at}
                  title={f.exact(c.last_message_at)}
                >
                  {f.ago(c.last_message_at)}
                </time>
              </span>
            </RowLink>
          ))}
        </Rows>
      )}
    </Panel>
  )
}

// ── notifications ────────────────────────────────────────────────────────

const severityTone: Record<NotificationSeverity, string> = {
  info: "bg-nq-brand",
  warning: "bg-nq-warning",
  error: "bg-nq-danger",
}

export function NotificationsCard({
  items,
  unread,
  base,
  canAddSource,
}: {
  items: OverviewNotification[]
  unread: number | null
  base: string
  canAddSource: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <Panel
      id="notifications"
      title={t("nav.notifications")}
      icon={<BellIcon />}
      href={`${base}/notifications`}
      meta={
        unread ? (
          <Link
            href={`${base}/notifications?unread=1`}
            className="text-xs text-muted-foreground hover:text-foreground hover:underline"
            data-unread-link
          >
            {t("overview.unreadNotifications")}:{" "}
            <span className="font-medium text-foreground">{f.n(unread)}</span>
          </Link>
        ) : null
      }
    >
      {items.length === 0 ? (
        <Empty
          title={t("overview.noNotifications")}
          body={t("overview.noNotificationsBody")}
          action={
            canAddSource
              ? { href: `${base}/notifications/sources`, label: t("notifications.addSource") }
              : undefined
          }
        />
      ) : (
        <Rows label={t("nav.notifications")}>
          {items.map((n) => (
            <RowLink
              key={n.id}
              href={`${base}/notifications${n.read ? "" : "?unread=1"}`}
              className={cn("flex-row items-start gap-3", !n.read && "bg-card")}
              data-overview-notification={n.title}
            >
              <span
                className={cn("mt-1.5 size-2 shrink-0 rounded-full", severityTone[n.severity])}
                aria-hidden
              />
              <span className="min-w-0 flex-1">
                <span
                  className={cn(
                    "bidi-block block truncate text-sm text-foreground",
                    !n.read && "font-medium",
                  )}
                  dir="auto"
                >
                  {n.title}
                </span>
                <span className="flex flex-wrap gap-x-2 text-xs text-muted-foreground">
                  <span>{t(`notifications.severity.${n.severity}`)}</span>
                  <span aria-hidden>·</span>
                  <bdi>{n.source_name || t("notifications.unknownSource")}</bdi>
                  <span aria-hidden>·</span>
                  <time dateTime={n.occurred_at} title={f.exact(n.occurred_at)}>
                    {f.ago(n.occurred_at)}
                  </time>
                </span>
              </span>
            </RowLink>
          ))}
        </Rows>
      )}
    </Panel>
  )
}

// ── marketing ────────────────────────────────────────────────────────────

export function MarketingCard({
  m,
  base,
  canWriteContacts,
  canWriteCampaigns,
}: {
  m: OverviewMarketing
  base: string
  canWriteContacts: boolean
  canWriteCampaigns: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <Panel
      id="marketing"
      title={t("overview.marketing")}
      icon={<SendIcon />}
      href={`${base}/campaigns`}
    >
      {m.lists !== null ? (
        m.lists.length === 0 ? (
          <Empty
            title={t("overview.noLists")}
            body={t("overview.noListsBody")}
            action={
              canWriteContacts ? { href: `${base}/contacts`, label: t("lists.create") } : undefined
            }
          />
        ) : (
          <table className="w-full text-sm" data-marketing-lists>
            <thead>
              <tr className="border-b border-nq-line text-xs text-muted-foreground">
                <th scope="col" className="w-full px-5 py-2 text-start font-normal">
                  {t("lists.title")}
                </th>
                <th scope="col" className="px-2 py-2 text-end font-normal whitespace-nowrap">
                  {t("lists.subscribed")}
                </th>
                <th scope="col" className="px-5 py-2 text-end font-normal whitespace-nowrap">
                  {t("lists.pending")}
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-nq-line">
              {m.lists.map((l) => (
                <tr key={l.id} data-list={l.name}>
                  <th scope="row" className="max-w-0 truncate px-5 py-2.5 text-start font-normal">
                    <Link
                      href={`${base}/contacts`}
                      className="text-foreground hover:underline hover:underline-offset-4"
                    >
                      <bdi>{l.name}</bdi>
                    </Link>
                  </th>
                  <td className="px-2 py-2.5 text-end tabular-nums" data-subscribed>
                    <Link
                      href={`${base}/contacts`}
                      className="hover:underline hover:underline-offset-4"
                    >
                      {f.n(l.subscribed)}
                    </Link>
                  </td>
                  <td className="px-5 py-2.5 text-end tabular-nums text-muted-foreground" data-pending>
                    <Link
                      href={`${base}/contacts`}
                      className="hover:underline hover:underline-offset-4"
                    >
                      {f.n(l.pending)}
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )
      ) : null}
      <div
        className={cn(
          "flex items-center gap-2 px-5 pt-3 pb-1",
          m.lists !== null && "border-t border-nq-line",
        )}
      >
        <p className="eyebrow">{t("overview.drafts")}</p>
        <Link
          href={`${base}/campaigns`}
          className="text-xs font-medium text-foreground hover:underline"
          data-draft-count
        >
          {f.n(m.draft_count)}
        </Link>
      </div>
      {m.drafts.length === 0 ? (
        <div className="flex flex-wrap items-center gap-3 px-5 pb-4">
          <p className="text-sm text-muted-foreground">{t("overview.noDrafts")}</p>
          {canWriteCampaigns ? (
            <Button
              variant="outline"
              size="sm"
              nativeButton={false}
              render={<Link href={`${base}/campaigns`} />}
            >
              {t("overview.newCampaign")}
            </Button>
          ) : null}
        </div>
      ) : (
        <Rows label={t("overview.drafts")}>
          {m.drafts.map((d) => (
            <RowLink
              key={d.id}
              href={`${base}/campaigns`}
              className="flex-row items-center gap-2"
              data-draft={d.subject}
            >
              <span className="bidi-block min-w-0 flex-1 truncate text-sm text-foreground" dir="auto">
                {d.subject}
              </span>
              <span className="hidden shrink-0 truncate text-xs text-muted-foreground sm:inline">
                <bdi>{d.list_name}</bdi>
              </span>
              <Badge variant="outline" className="shrink-0">
                {t(`campaigns.channel.${d.channel}`)}
              </Badge>
            </RowLink>
          ))}
        </Rows>
      )}
    </Panel>
  )
}

// ── trends ───────────────────────────────────────────────────────────────

export function TrendsCard({
  tr,
  base,
  canManage,
}: {
  tr: OverviewTrends
  base: string
  canManage: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  const manage = canManage
    ? { href: `${base}/trends/sources`, label: t("trends.addTopic") }
    : undefined
  return (
    <Panel
      id="trends"
      title={t("nav.trends")}
      icon={<TrendingUpIcon />}
      href={`${base}/trends`}
      meta={
        tr.topics > 0 ? (
          <span className="text-xs text-muted-foreground">
            {t("overview.topicsFollowed", { n: f.n(tr.topics) })} ·{" "}
            {t("overview.matchedItems", { n: f.n(tr.matched) })}
          </span>
        ) : null
      }
    >
      {tr.topics === 0 ? (
        <Empty title={t("overview.noTopics")} body={t("overview.noTopicsBody")} action={manage} />
      ) : tr.items.length === 0 ? (
        <Empty title={t("overview.noMatches")} body={t("overview.noMatchesBody")} action={manage} />
      ) : (
        <ul className="divide-y divide-nq-line" aria-label={t("nav.trends")}>
          {tr.items.map((it) => (
            <li key={it.id} className="flex flex-col gap-1 px-5 py-3" data-trend={it.title}>
              {it.url ? (
                <a
                  href={it.url}
                  target="_blank"
                  rel="noreferrer"
                  className="group inline-flex items-start gap-1.5 text-sm text-foreground hover:underline hover:underline-offset-4"
                >
                  <span className="bidi-block line-clamp-2" dir="auto">
                    {it.title}
                  </span>
                  <ExternalLinkIcon
                    className="mt-0.5 size-3.5 shrink-0 text-muted-foreground"
                    aria-hidden
                  />
                </a>
              ) : (
                <span className="bidi-block line-clamp-2 text-sm text-foreground" dir="auto">
                  {it.title}
                </span>
              )}
              <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
                {it.topics.map((name) => (
                  <Badge key={name} variant="secondary">
                    <bdi>{name}</bdi>
                  </Badge>
                ))}
                <bdi>{it.source_name}</bdi>
                <span aria-hidden>·</span>
                <time dateTime={it.published_at} title={f.exact(it.published_at)}>
                  {f.ago(it.published_at)}
                </time>
              </span>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  )
}

// ── setup ────────────────────────────────────────────────────────────────

/** Where each item is set up, and the permission that may do it. */
const SETUP: Record<SetupKey, { path: string; perm: string; role: "admin" | "manager" }> = {
  inquiry_form: { path: "subscribe-forms", perm: "campaigns:write", role: "manager" },
  smtp: { path: "settings/email", perm: "mail:settings", role: "admin" },
  chat_widget: { path: "inbox/widgets", perm: "inbox:manage", role: "manager" },
  whatsapp: { path: "inbox/whatsapp", perm: "inbox:manage", role: "manager" },
  google: { path: "settings/integrations", perm: "integrations:manage", role: "admin" },
  browser: { path: "automation", perm: "automation:manage", role: "admin" },
}

/** Shown only while something this server can do is still unconfigured. */
export function setupPending(items: { done: boolean; available: boolean }[] | null) {
  return !!items?.some((i) => i.available && !i.done)
}

export function SetupCard({
  items,
  base,
  can,
  className,
}: {
  items: { key: SetupKey; done: boolean; available: boolean }[]
  base: string
  can: (p: string) => boolean
  className?: string
}) {
  const { t } = useTranslations()
  const f = useFormat()
  const doable = items.filter((i) => i.available)
  const doneItems = items.filter((i) => i.done)
  const open = items.filter((i) => !i.done)
  const done = doable.filter((i) => i.done).length
  const pct = doable.length ? Math.round((done / doable.length) * 100) : 0
  return (
    <Panel
      id="setup"
      title={t("overview.setup")}
      icon={<ListChecksIcon />}
      meta={
        <span
          className="ms-auto flex items-center gap-2 text-xs text-muted-foreground"
          data-setup-progress
        >
          {t("overview.setupProgress", { done: f.n(done), total: f.n(doable.length) })}
          <span className="h-1.5 w-20 overflow-hidden rounded-full bg-nq-surface-soft" aria-hidden>
            <span className="block h-full rounded-full bg-nq-success" style={{ width: `${pct}%` }} />
          </span>
        </span>
      }
      className={className}
    >
      {/* Only what is still to do is spelled out; finished items fold into one line. They used
          to be struck through, which read as errors rather than progress. */}
      <ul
        className="grid divide-y divide-nq-line sm:grid-cols-2 sm:divide-y-0 lg:grid-cols-3"
        aria-label={t("overview.setup")}
      >
        {open.map((it) => {
          const cfg = SETUP[it.key]
          const canFix = can(cfg.perm)
          return (
            <li
              key={it.key}
              className="flex items-start gap-3 px-5 py-4"
              data-setup={it.key}
              data-setup-done="false"
            >
              <CircleIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
              <div className="min-w-0 flex-1">
                <p
                  className={cn(
                    "text-sm",
                    it.available ? "font-medium text-foreground" : "text-muted-foreground",
                  )}
                >
                  {t(`overview.setup.${it.key}`)}
                </p>
                <p className="mt-0.5 text-xs text-muted-foreground">
                  {t(`overview.setup.${it.key}.hint`)}
                </p>
                {!it.available ? (
                  <p className="mt-1.5 text-xs text-muted-foreground" data-setup-unavailable>
                    {t("overview.unavailable")}
                  </p>
                ) : canFix ? (
                  <Link
                    href={`${base}/${cfg.path}`}
                    className="mt-1.5 inline-flex items-center gap-1 text-xs font-medium text-foreground underline underline-offset-4"
                    data-setup-link={it.key}
                  >
                    {t("overview.setUp")}
                    <ArrowRightIcon className="size-3 rtl:-scale-x-100" aria-hidden />
                  </Link>
                ) : (
                  <p className="mt-1.5 text-xs text-muted-foreground">
                    {t(cfg.role === "admin" ? "overview.needsAdmin" : "overview.needsManager")}
                  </p>
                )}
              </div>
            </li>
          )
        })}
      </ul>
      {doneItems.length ? (
        <Collapsible className="border-t border-nq-line">
          <CollapsibleTrigger
            className="group flex w-full items-center gap-2 px-5 py-3 text-start text-xs text-muted-foreground transition-colors hover:text-foreground"
            data-setup-done-toggle
          >
            <CheckCircle2Icon className="size-4 shrink-0 text-nq-success" aria-hidden />
            {t("overview.showDone", { n: f.n(doneItems.length) })}
            <ChevronDownIcon
              className="size-3.5 transition-transform group-aria-expanded:rotate-180"
              aria-hidden
            />
          </CollapsibleTrigger>
          <CollapsibleContent>
            <ul
              className="flex flex-wrap gap-x-6 gap-y-2 px-5 pb-4"
              aria-label={t("overview.done")}
            >
              {doneItems.map((it) => (
                <li
                  key={it.key}
                  className="flex items-center gap-2 text-sm text-muted-foreground"
                  data-setup={it.key}
                  data-setup-done="true"
                >
                  <CheckCircle2Icon
                    className="size-4 shrink-0 text-nq-success"
                    aria-label={t("overview.done")}
                  />
                  {t(`overview.setup.${it.key}`)}
                </li>
              ))}
            </ul>
          </CollapsibleContent>
        </Collapsible>
      ) : null}
    </Panel>
  )
}
