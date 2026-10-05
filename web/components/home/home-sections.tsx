"use client"

/*
The workspace Home (MH-1062). Each section renders one block of GET /api/w/home; a block the role may
not read arrives as null and is left out, never zeroed. Tones are semantic only: green = healthy or
active, info = a channel, violet = Moharrik's own work, warning = needs a look, danger = failed.
*/

import Link from "next/link"
import type { ComponentType, ReactNode } from "react"
import {
  ActivityIcon,
  ArrowRightIcon,
  BellRingIcon,
  BotIcon,
  CableIcon,
  CircleAlertIcon,
  CircleCheckIcon,
  EarIcon,
  HandshakeIcon,
  LightbulbIcon,
  MessageSquareIcon,
  PlayIcon,
  RadarIcon,
  SearchIcon,
  SparklesIcon,
  TrendingUpIcon,
  WorkflowIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"

import { useFormat } from "@/components/overview/overview-cards"
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
  EmptyState,
  PageHeader,
  Sparkline,
  Stepper,
  StepperItem,
} from "@fadymondy/nasaq/web"
import type { Home, HomeAttentionItem, HomeEvent, HomeLane } from "@/lib/home-types"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"
import {
  channelLabel,
  entityRoute,
  eventText,
  greetingKey,
  laneItemText,
  reasonText,
} from "./home-text"

type Tone = "success" | "info" | "violet" | "warning" | "danger"

const TONE_TEXT: Record<Tone, string> = {
  success: "text-nq-success-text",
  info: "text-nq-info-text",
  violet: "text-nq-tag-violet",
  warning: "text-nq-warning-text",
  danger: "text-nq-danger-text",
}
const TONE_SOFT: Record<Tone, string> = {
  success: "bg-nq-success-soft",
  info: "bg-nq-info-soft",
  violet: "bg-nq-tag-violet-soft",
  warning: "bg-nq-warning-soft",
  danger: "bg-nq-danger-soft",
}
const TONE_DOT: Record<Tone, string> = {
  success: "bg-nq-success",
  info: "bg-nq-info",
  violet: "bg-nq-tag-violet",
  warning: "bg-nq-warning",
  danger: "bg-nq-danger",
}

function IconChip({ tone, children }: { tone: Tone; children: ReactNode }) {
  return (
    <span
      className={cn(
        "flex size-8 shrink-0 items-center justify-center rounded-control [&>svg]:size-4",
        TONE_SOFT[tone],
        TONE_TEXT[tone],
      )}
      aria-hidden
    >
      {children}
    </span>
  )
}

// ── card ─────────────────────────────────────────────────────────────────

/** One Home section on Nasaq's rounded Card (MH-1122): icon, title, meta and "View all", then its rows. */
function HomeCard({
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
    <Card
      role="region"
      aria-labelledby={`ov-${id}`}
      data-card={id}
      className={cn("min-w-0 gap-0 overflow-hidden py-0 shadow-xs", className)}
    >
      <CardHeader className="flex min-h-14 flex-wrap items-center gap-x-2 gap-y-1 px-5 py-3">
        <span className="text-muted-foreground [&>svg]:size-4" aria-hidden>
          {icon}
        </span>
        <CardTitle as="h2" id={`ov-${id}`} className="text-base font-medium">
          {title}
        </CardTitle>
        {meta}
        {href ? (
          <Button
            variant="ghost"
            size="sm"
            className="ms-auto"
            nativeButton={false}
            render={<Link href={href} data-view-all={id} />}
          >
            {t("overview.viewAll")}
            <ArrowRightIcon className="rtl:-scale-x-100" />
          </Button>
        ) : null}
      </CardHeader>
      <CardContent className="flex flex-1 flex-col border-t border-border px-0">{children}</CardContent>
    </Card>
  )
}

/** A section with nothing in it yet: Nasaq's EmptyState, with the one action that fills it. */
function HomeEmpty({
  icon,
  title,
  body,
  action,
}: {
  icon: LucideIcon
  title: string
  body?: string
  action?: { href: string; label: string }
}) {
  return (
    <EmptyState
      icon={icon}
      title={title}
      description={body}
      className="m-4 flex-1 py-8"
      data-empty
      actions={
        action ? (
          <Button variant="secondary" size="sm" nativeButton={false} render={<Link href={action.href} />}>
            {action.label}
          </Button>
        ) : undefined
      }
    />
  )
}

// ── header ───────────────────────────────────────────────────────────────

export function HomeHeader({ home, name, live }: { home: Home; name: string; live: boolean }) {
  const { t } = useTranslations()
  const active = live || home.working.lanes.some((l) => l.active)
  return (
    <PageHeader
      className="px-4 pt-2 pb-2 sm:px-6"
      title={<span data-home-greeting>{t(greetingKey(home.timezone), { name })}</span>}
      description={t("home.subtitle")}
      actions={
        <Badge
          variant="outline"
          className="h-8 gap-2 rounded-full px-3"
          title={live ? t("home.watchingLive") : active ? t("home.watchingLanes") : undefined}
          data-watching={active ? "on" : "off"}
        >
          <span className="relative flex size-2" aria-hidden>
            {active ? (
              <span
                className={cn(
                  "absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:animate-none",
                  TONE_DOT.success,
                )}
              />
            ) : null}
            <span
              className={cn(
                "relative inline-flex size-2 rounded-full",
                active ? TONE_DOT.success : "bg-nq-fg-muted",
              )}
            />
          </span>
          {active ? t("home.watching") : t("home.idle")}
        </Badge>
      }
    />
  )
}

// ── tiles ────────────────────────────────────────────────────────────────

function Tile({
  id,
  label,
  value,
  sub,
  href,
  icon: Icon,
  tone,
}: {
  id: string
  label: string
  value: string
  sub?: string
  href: string
  icon: ComponentType
  tone: Tone
}) {
  return (
    <Link
      href={href}
      data-home-tile={id}
      className="group min-w-0 rounded-card outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-nq-focus"
    >
      <Card className="h-full gap-3 px-4 py-4 shadow-xs transition-[background-color,box-shadow] group-hover:bg-nq-surface-soft group-hover:shadow-sm sm:px-5">
        <div className="flex items-start justify-between gap-2">
          <CardDescription className="min-h-[2lh] text-balance lg:min-h-0">{label}</CardDescription>
          <IconChip tone={tone}>
            <Icon />
          </IconChip>
        </div>
        <span
          className={cn(
            "text-xl leading-none font-medium tabular-nums sm:text-3xl xl:text-4xl",
            tone === "warning" ? TONE_TEXT.warning : "text-foreground",
          )}
          data-tile-value
        >
          {value}
        </span>
        <span className="min-h-4 text-xs text-muted-foreground">{sub}</span>
      </Card>
    </Link>
  )
}

export function HomeTiles({ home, base }: { home: Home; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const { signals, opportunities, actions } = home.today

  const sigSub = (
    [
      ["leads", signals.leads],
      ["prospects", signals.prospects],
      ["companies", signals.companies],
      ["trends", signals.trends],
    ] as const
  )
    .filter(([, v]) => v != null && v > 0)
    .map(([k, v]) => t(`home.sig.${k}`, { n: f.n(v as number) }))
    .join(" · ")

  const actSub = (
    [
      ["rule_runs", actions.rule_runs],
      ["browser_runs", actions.browser_runs],
      ["campaign_sends", actions.campaign_sends],
    ] as const
  )
    .filter(([, v]) => v != null && v > 0)
    .map(([k, v]) => t(`home.act.${k}`, { n: f.n(v as number) }))
    .join(" · ")

  // Money is never added across currencies: the first (largest) currency is the headline and the
  // rest are listed beside it.
  const money = opportunities.value
  const oppValue = money.length ? f.money(money[0].minor, money[0].currency) : null
  const oppSub =
    opportunities.open == null
      ? undefined
      : [
          opportunities.open > 0
            ? t("home.opp.open", { n: f.n(opportunities.open) })
            : t("home.opp.none"),
          ...money.slice(1).map((m) => f.money(m.minor, m.currency)),
        ].join(" · ")

  const attn = home.today.attention
  return (
    <div
      className="grid grid-cols-2 gap-3 px-4 sm:gap-4 sm:px-6 lg:grid-cols-4"
      role="group"
      aria-label={t("home.today")}
      data-home-tiles
    >
      <Tile
        id="signals"
        label={t("home.tile.signals")}
        value={f.n(signals.total)}
        sub={sigSub}
        href={`${base}/leads`}
        icon={RadarIcon}
        tone="violet"
      />
      <Tile
        id="opportunities"
        label={t("home.tile.opportunities")}
        value={oppValue ?? f.n(opportunities.open ?? 0)}
        sub={oppSub}
        href={`${base}/deals`}
        icon={TrendingUpIcon}
        tone="success"
      />
      <Tile
        id="actions"
        label={t("home.tile.actions")}
        value={f.n(actions.total)}
        sub={actSub}
        href={`${base}/automation/runs`}
        icon={ZapIcon}
        tone="info"
      />
      <Tile
        id="attention"
        label={t("home.tile.attention")}
        value={f.n(attn)}
        sub={attn > 0 ? t("home.attention.items", { n: f.n(attn) }) : t("home.attention.clear")}
        href="#ov-home-attention"
        icon={attn > 0 ? BellRingIcon : CircleCheckIcon}
        tone={attn > 0 ? "warning" : "success"}
      />
    </div>
  )
}

// ── attention ────────────────────────────────────────────────────────────

const ATTN_ICON: Record<HomeAttentionItem["kind"], ReactNode> = {
  hot_lead: <SparklesIcon />,
  at_risk: <HandshakeIcon />,
  new_prospects: <SearchIcon />,
  failed_automation: <CircleAlertIcon />,
  unanswered: <MessageSquareIcon />,
}

export function AttentionPanel({
  attention,
  base,
}: {
  attention: NonNullable<Home["attention"]>
  base: string
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <HomeCard
      id="home-attention"
      title={t("home.attention.title")}
      icon={<CircleAlertIcon />}
      meta={
        attention.total > 0 ? <Badge variant="warning">{f.n(attention.total)}</Badge> : undefined
      }
      className="lg:col-span-1"
    >
      {attention.items.length === 0 ? (
        <HomeEmpty
          icon={CircleCheckIcon}
          title={t("home.attention.emptyTitle")}
          body={t("home.attention.emptyBody")}
        />
      ) : (
        <ul className="divide-y divide-border" data-home-attention-list>
          {attention.items.map((it, i) => (
            <AttentionRow key={`${it.kind}-${it.entity?.id ?? i}`} it={it} base={base} />
          ))}
        </ul>
      )}
      {attention.items.length > 0 && attention.total > attention.items.length ? (
        <p className="border-t border-border px-5 py-3 text-xs text-muted-foreground" data-home-attention-more>
          {t("home.attention.showing", {
            shown: f.n(attention.items.length),
            total: f.n(attention.total),
          })}
        </p>
      ) : null}
    </HomeCard>
  )
}

function AttentionRow({ it, base }: { it: HomeAttentionItem; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const title = it.title ? it.title : t(`home.attn.${it.kind}${it.count === 1 ? ".one" : ""}`, { n: f.n(it.count ?? 0) })
  const lead = it.title ? t(`home.attn.${it.kind}`, { n: "" }).trim() : null
  const money = it.value && it.value.minor > 0 ? f.money(it.value.minor, it.value.currency) : null
  return (
    <li className="flex gap-3 px-5 py-3" data-attention={it.kind}>
      <IconChip tone={it.tone}>{ATTN_ICON[it.kind]}</IconChip>
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3">
          <p className="text-sm font-medium text-foreground">
            {lead ? <span className={cn("me-1.5 text-xs", TONE_TEXT[it.tone])}>{lead}</span> : null}
            <bdi>{title}</bdi>
          </p>
          {money ? (
            <span className="text-sm font-medium text-foreground tabular-nums">{money}</span>
          ) : null}
        </div>
        {it.why.length ? (
          <ul className="space-y-0.5 text-xs text-muted-foreground">
            {it.why.map((r, i) => (
              <li key={i}>
                <bdi>{reasonText(t, r, f.ago)}</bdi>
              </li>
            ))}
          </ul>
        ) : null}
        {it.actions.length ? (
          <div className="flex flex-wrap gap-x-4 gap-y-1 pt-0.5">
            {it.actions.map((a) => (
              <Link
                key={a.key + a.route}
                href={`${base}/${a.route}`}
                className="inline-flex items-center gap-1 text-xs font-medium text-nq-action hover:underline hover:underline-offset-4"
              >
                {t(`home.action.${a.key}`)}
                <ArrowRightIcon className="size-3 rtl:-scale-x-100" aria-hidden />
              </Link>
            ))}
          </div>
        ) : null}
      </div>
    </li>
  )
}

// ── working ──────────────────────────────────────────────────────────────

const LANE: Record<HomeLane["key"], { icon: ReactNode; tone: Tone; route: string }> = {
  discover: { icon: <SearchIcon />, tone: "violet", route: "research" },
  listen: { icon: <EarIcon />, tone: "info", route: "inbox" },
  understand: { icon: <LightbulbIcon />, tone: "violet", route: "insights" },
  act: { icon: <PlayIcon />, tone: "success", route: "automation/runs" },
}

export function WorkingPanel({ working, base }: { working: Home["working"]; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const pulse = working.pulse
  return (
    <HomeCard id="home-working" title={t("home.working.title")} icon={<BotIcon />}>
      <ul className="divide-y divide-border" data-home-lanes>
        {working.lanes.map((lane) => {
          const m = LANE[lane.key]
          const subs = lane.subs
            .map((s) =>
              s.n == null && s.key !== "prospect_profile" ? null : laneItemText(t, s, f.n, f.ago),
            )
            .filter((s): s is string => !!s)
          return (
            <li key={lane.key} data-lane={lane.key} data-active={lane.active ? "true" : "false"}>
              <Link
                href={`${base}/${m.route}`}
                className="flex gap-3 px-5 py-3 transition-colors hover:bg-nq-surface-soft"
              >
                <IconChip tone={lane.active ? m.tone : "info"}>{m.icon}</IconChip>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-foreground">
                      {t(`home.lane.${lane.key}`)}
                    </span>
                    <span
                      className={cn(
                        "size-1.5 rounded-full",
                        lane.active ? TONE_DOT.success : "bg-nq-fg-muted",
                      )}
                      aria-hidden
                    />
                  </div>
                  <p className="text-sm text-foreground">
                    {lane.headline == null
                      ? t("home.lane.noAccess")
                      : t(`home.lane.${lane.key}.head${lane.headline === 1 ? ".one" : ""}`, { n: f.n(lane.headline) })}
                  </p>
                  {subs.length ? (
                    <p className="text-xs text-muted-foreground">{subs.join(" · ")}</p>
                  ) : null}
                </div>
              </Link>
            </li>
          )
        })}
      </ul>
      <div className="mt-auto flex items-center gap-3 border-t border-border px-5 py-3">
        <Sparkline
          data={pulse.buckets}
          color="var(--nq-tag-violet)"
          label={t("home.working.pulseLabel")}
          className="h-8 w-32 shrink-0"
        />
        <span className="text-xs text-muted-foreground" data-home-pulse>
          {pulse.total > 0
            ? t("home.working.pulse", { n: f.n(pulse.total) })
            : t("home.working.pulseNone")}
        </span>
      </div>
    </HomeCard>
  )
}

// ── activity ─────────────────────────────────────────────────────────────

function eventTone(e: HomeEvent): { tone: Tone; icon: ReactNode } {
  if (e.status === "failed") return { tone: "danger", icon: <CircleAlertIcon /> }
  if (e.kind.startsWith("message.")) return { tone: "info", icon: <MessageSquareIcon /> }
  if (e.kind.startsWith("rule.") || e.kind.startsWith("run.") || e.kind.startsWith("research."))
    return { tone: "violet", icon: <WorkflowIcon /> }
  if (e.kind.startsWith("deal.")) return { tone: "success", icon: <HandshakeIcon /> }
  return { tone: "success", icon: <ActivityIcon /> }
}

export function ActivityPanel({ events, base }: { events: HomeEvent[]; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <HomeCard id="home-activity" title={t("home.activity.title")} icon={<ActivityIcon />}>
      {events.length === 0 ? (
        <HomeEmpty
          icon={ActivityIcon}
          title={t("home.activity.emptyTitle")}
          body={t("home.activity.emptyBody")}
        />
      ) : (
        <ul className="max-h-[32rem] divide-y divide-border overflow-y-auto" data-home-activity>
          {events.map((e, i) => {
            const { tone, icon } = eventTone(e)
            const { text, by } = eventText(t, e, f.n)
            const route = e.link ? entityRoute(e.link) : null
            const body = (
              <>
                <IconChip tone={tone}>{icon}</IconChip>
                <div className="min-w-0 flex-1">
                  <p className="text-sm text-foreground">
                    <bdi>{text}</bdi>
                    {by ? <span className="text-muted-foreground"> {by}</span> : null}
                  </p>
                  {e.snippet ? (
                    <p className="truncate text-xs text-muted-foreground">
                      <bdi>{e.snippet}</bdi>
                    </p>
                  ) : null}
                </div>
                <time
                  dateTime={e.occurred_at}
                  title={f.exact(e.occurred_at)}
                  className="shrink-0 text-xs text-muted-foreground tabular-nums"
                >
                  {f.ago(e.occurred_at)}
                </time>
              </>
            )
            return (
              <li key={`${e.kind}-${e.occurred_at}-${i}`} data-event={e.kind}>
                {route ? (
                  <Link
                    href={`${base}/${route}`}
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
    </HomeCard>
  )
}

// ── journeys ─────────────────────────────────────────────────────────────

export function JourneysPanel({
  journeys,
  base,
}: {
  journeys: NonNullable<Home["journeys"]>
  base: string
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <HomeCard
      id="home-journeys"
      title={t("home.journeys.title")}
      icon={<HandshakeIcon />}
      href={`${base}/deals`}
    >
      {journeys.deals.length === 0 ? (
        <HomeEmpty
          icon={HandshakeIcon}
          title={t("home.journeys.emptyTitle")}
          body={t("home.journeys.emptyBody")}
          action={{ href: `${base}/deals`, label: t("home.journeys.emptyAction") }}
        />
      ) : (
        <ul className="divide-y divide-border" data-home-journeys>
          {journeys.deals.map((d) => (
            <li key={d.id} data-journey={d.id}>
              <Link
                href={`${base}/deals/${d.id}`}
                className="block space-y-3 px-5 py-3.5 transition-colors hover:bg-nq-surface-soft"
              >
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium text-foreground">
                      <bdi>{d.title}</bdi>
                    </p>
                    {d.contact || d.company ? (
                      <p className="truncate text-xs text-muted-foreground">
                        <bdi>{[d.contact, d.company].filter(Boolean).join(" · ")}</bdi>
                      </p>
                    ) : null}
                  </div>
                  <div className="flex items-center gap-2">
                    {d.quiet ? (
                      <Badge variant="warning">
                        {t("home.journeys.quiet", { n: f.n(d.days_since_activity) })}
                      </Badge>
                    ) : null}
                    {d.value.minor > 0 ? (
                      <span className="text-sm font-medium text-foreground tabular-nums">
                        {f.money(d.value.minor, d.value.currency)}
                      </span>
                    ) : null}
                  </div>
                </div>
                <div className="relative overflow-x-auto">
                  <Stepper current={d.stage_index} className="pointer-events-none min-w-[26rem]">
                    {journeys.stages.map((s) => (
                      <StepperItem key={s.id} title={s.name} />
                    ))}
                  </Stepper>
                </div>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </HomeCard>
  )
}

// ── connections ──────────────────────────────────────────────────────────

export function ConnectionsPanel({
  connections,
  base,
  canManage,
}: {
  connections: NonNullable<Home["connections"]>
  base: string
  canManage: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  const { channels, google, circlexo } = connections
  const empty = channels.length === 0 && !google && !(circlexo && circlexo.linked)
  const statusTone = (s: string, err: boolean): Tone =>
    err || s === "error" ? "danger" : s === "paused" ? "warning" : "success"
  return (
    <HomeCard
      id="home-connections"
      title={t("home.connections.title")}
      icon={<CableIcon />}
      href={canManage ? `${base}/inbox/channels` : `${base}/settings/integrations`}
    >
      {empty ? (
        <HomeEmpty
          icon={CableIcon}
          title={t("home.connections.emptyTitle")}
          body={t("home.connections.emptyBody")}
          action={
            canManage
              ? { href: `${base}/inbox/channels`, label: t("home.connections.connectWhatsapp") }
              : undefined
          }
        />
      ) : (
        <ul className="divide-y divide-border" data-home-connections>
          {channels.map((c) => {
            const tone = statusTone(c.status, c.has_error)
            return (
              <li key={c.id} className="flex items-center gap-3 px-5 py-3" data-channel={c.type}>
                <span
                  className={cn("size-2 shrink-0 rounded-full", TONE_DOT[tone])}
                  role="img"
                  aria-label={t(`home.connections.status.${c.has_error ? "error" : c.status}`)}
                />
                <div className="min-w-0 flex-1">
                  <p className="truncate text-sm font-medium text-foreground">
                    <bdi>{c.name}</bdi>{" "}
                    <span className="text-xs font-normal text-muted-foreground">
                      {channelLabel(t, c.type)}
                    </span>
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {t("home.connections.conversations", { n: f.n(c.conversations_30d) })}
                    {" · "}
                    {c.last_message_at
                      ? t("home.connections.last", { when: f.ago(c.last_message_at) })
                      : t("home.connections.never")}
                  </p>
                </div>
                {tone !== "success" ? (
                  <Badge variant={tone === "violet" ? "info" : tone}>
                    {t(`home.connections.status.${c.has_error ? "error" : c.status}`)}
                  </Badge>
                ) : null}
              </li>
            )
          })}
          {google ? (
            <li className="flex items-center gap-3 px-5 py-3" data-channel="google">
              <span
                className={cn(
                  "size-2 shrink-0 rounded-full",
                  TONE_DOT[google.has_error ? "danger" : "success"],
                )}
                aria-hidden
              />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-foreground">
                  {t("home.connections.google")}
                </p>
                <p className="text-xs text-muted-foreground">
                  {[
                    t("home.connections.googleBody", { n: f.n(google.connections) }),
                    ...google.services.map((s) =>
                      t("home.connections.googleServices", { n: f.n(s.count), kind: s.kind }),
                    ),
                  ].join(" · ")}
                </p>
              </div>
              {google.has_error ? (
                <Badge variant="danger">{t("home.connections.status.error")}</Badge>
              ) : null}
            </li>
          ) : null}
          {circlexo ? (
            <li className="flex items-center gap-3 px-5 py-3" data-channel="circlexo">
              <span
                className={cn(
                  "size-2 shrink-0 rounded-full",
                  circlexo.active ? TONE_DOT.success : "bg-nq-fg-muted",
                )}
                aria-hidden
              />
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-foreground">
                  {t("home.connections.circlexo")}
                </p>
                <p className="text-xs text-muted-foreground">
                  {circlexo.linked
                    ? [
                        t("home.connections.circlexoLinked"),
                        circlexo.plan
                          ? t("home.connections.circlexoPlan", { plan: circlexo.plan })
                          : "",
                      ]
                        .filter(Boolean)
                        .join(" · ")
                    : t("home.connections.circlexoNot")}
                </p>
              </div>
            </li>
          ) : null}
        </ul>
      )}
      <div className="mt-auto border-t border-border px-5 py-2.5">
        <Link
          href={canManage ? `${base}/inbox/channels` : `${base}/settings/integrations`}
          className="inline-flex items-center gap-1 text-xs font-medium text-nq-action hover:underline hover:underline-offset-4"
        >
          {t("home.connections.manage")}
          <ArrowRightIcon className="size-3 rtl:-scale-x-100" aria-hidden />
        </Link>
      </div>
    </HomeCard>
  )
}

// ── first-run guidance ───────────────────────────────────────────────────

type Step = { title: string; body: string; actions: { href: string; label: string }[] }

/** What to do next, from what is actually missing. The first missing thing leads. */
export function HomeNextSteps({
  has,
  base,
  can,
}: {
  has: Home["has"]
  base: string
  can: (p: string) => boolean
}) {
  const { t } = useTranslations()
  const steps: Step[] = []
  if (has.contacts === false) {
    const actions = []
    if (can("research:write"))
      actions.push({ href: `${base}/research`, label: t("home.empty.startResearch") })
    if (can("contacts:write"))
      actions.push({ href: `${base}/contacts/import`, label: t("home.empty.importCustomers") })
    if (can("inbox:manage"))
      actions.push({ href: `${base}/inbox/widgets`, label: t("home.empty.connectWebsite") })
    steps.push({
      title: t("home.empty.customersTitle"),
      body: t("home.empty.customersBody"),
      actions,
    })
  }
  if (has.conversations === false) {
    const actions = can("inbox:manage")
      ? [
          { href: `${base}/inbox/whatsapp`, label: t("home.connections.connectWhatsapp") },
          { href: `${base}/inbox/email`, label: t("home.connections.connectEmail") },
        ]
      : []
    steps.push({
      title: t("home.empty.conversationsTitle"),
      body: t("home.empty.conversationsBody"),
      actions,
    })
  }
  if (has.automations === false) {
    steps.push({
      title: t("home.empty.automationsTitle"),
      body: t("home.empty.automationsBody"),
      actions: [{ href: `${base}/automation/presets`, label: t("home.empty.browsePresets") }],
    })
  }
  if (!steps.length) return null
  return (
    <div className="grid grid-cols-1 gap-4 px-4 sm:gap-6 sm:px-6 lg:grid-cols-3" data-home-next>
      {steps.map((s) => (
        <Card key={s.title} className="gap-2 px-5 py-4 shadow-xs">
          <CardTitle as="h2" className="text-sm font-medium">
            {s.title}
          </CardTitle>
          <CardDescription>{s.body}</CardDescription>
          {s.actions.length ? (
            <div className="mt-1 flex flex-wrap gap-2">
              {s.actions.map((a, i) => (
                <Button
                  key={a.href}
                  size="sm"
                  variant={i === 0 ? "primary" : "secondary"}
                  nativeButton={false}
                  render={<Link href={a.href} />}
                >
                  {a.label}
                </Button>
              ))}
            </div>
          ) : null}
        </Card>
      ))}
    </div>
  )
}
