"use client"

import Link from "next/link"
import type { ReactNode } from "react"
import { ArrowRightIcon, PlayIcon, SearchCheckIcon } from "lucide-react"

import { SectionHeader } from "@/components/public-frame"
import { Bdi } from "@/components/bidi"
import { RunRow as AutomationRunRow } from "@/components/automation/run-controls"
import { RunRow as ResearchRunRow } from "@/components/research/tabs"
import {
  Empty,
  InboxCard,
  LeadsCard,
  MarketingCard,
  NotificationsCard,
  Panel,
  PipelineCard,
  SetupCard,
  TrendsCard,
  setupPending,
} from "@/components/overview/overview-cards"
import { TrafficCard, YouTubeCard } from "@/components/overview/google-cards"
import {
  ConversationsChartCard,
  LeadsChartCard,
  PipelineChartCard,
} from "@/components/overview/overview-charts"
import { WorkspaceCard } from "@/components/overview/workspace-card"
import { ErrorState, LoadingRows } from "@/components/states"
import { Skeleton } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { NAV, type NavGroup } from "@/lib/nav-config"
import { useOverview, useResearchRuns, useRuns } from "@/lib/queries"
import { ResearchFlagship } from "@/components/research/flagship"
import { useDocumentTitle } from "@/lib/title"

/**
 * A sidebar section's own page (MH-673): opening CRM, Inbox, Automation… lands
 * here rather than only swapping the sidebar. The top is that section's slice
 * of the workspace overview (the same figures and cards, from GET
 * /api/w/overview, or its latest runs where the overview has none); below it,
 * one card per page in the section, filtered by the viewer's role exactly as
 * the sidebar is.
 */
export function SectionHome({ section }: { section: string }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const base = `/${locale}/w/${ws.slug}`
  const group = NAV.flatMap((e) => (e.type === "group" ? [e.group] : [])).find(
    (g) => g.key === section,
  ) as NavGroup
  const label = t(group.labelKey)
  useDocumentTitle(`${label} · ${ws.summary.name}`)

  const allowed = !group.can || ws.can(group.can)
  const pages = group.items.filter((i) => !i.can || ws.can(i.can))

  return (
    <div data-section-home={group.key}>
      <SectionHeader micro={<Bdi>{ws.summary.name}</Bdi>} title={label} />
      <div className="space-y-4 px-4 pb-10 sm:space-y-6 sm:px-6">
        {allowed ? <SectionFigures section={group.key} base={base} /> : null}
        <section aria-labelledby="section-pages" data-section-pages>
          <h2 id="section-pages" className="eyebrow mb-3">
            {t("section.pages")}
          </h2>
          <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:gap-4 xl:grid-cols-3">
            {pages.map((item) => {
              const Icon = item.icon
              return (
                <li key={item.key} className="min-w-0">
                  <Link
                    href={`${base}/${item.path}`}
                    data-section-page={item.key}
                    className="group flex h-full items-start gap-3 border border-nq-line bg-card px-5 py-4 transition-colors hover:bg-nq-surface-soft"
                  >
                    <Icon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
                    <span className="min-w-0 flex-1 space-y-1">
                      <span className="block text-sm font-medium text-foreground">
                        {t(item.labelKey)}
                      </span>
                      <span className="block text-sm text-muted-foreground">
                        {t(`section.desc.${item.key}`)}
                      </span>
                    </span>
                    <ArrowRightIcon
                      className="mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 rtl:-scale-x-100 rtl:group-hover:-translate-x-0.5"
                      aria-hidden
                    />
                  </Link>
                </li>
              )
            })}
          </ul>
        </section>
      </div>
    </div>
  )
}

/** The section's figures: the overview cards that belong to it, or its latest runs. */
function SectionFigures({ section, base }: { section: string; base: string }) {
  if (section === "automation") return <RecentAutomationRuns base={base} />
  if (section === "research") return <ResearchFlagship base={base} />
  return <OverviewSlice section={section} base={base} />
}

function OverviewSlice({ section, base }: { section: string; base: string }) {
  const ws = useWorkspace()
  const ov = useOverview(ws.id)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "lead" || e.kind === "message") void ov.mutate()
  })
  const o = ov.data
  if (ov.error) return <ErrorState error={ov.error} />
  if (!o)
    return (
      <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2" aria-busy>
        {Array.from({ length: 2 }, (_, i) => (
          <div key={i} className="space-y-3 border border-nq-line bg-card px-5 py-4">
            <Skeleton className="h-4 w-32" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-3/4" />
          </div>
        ))}
      </div>
    )

  const cards: ReactNode[] = []
  switch (section) {
    case "crm":
      if (o.pipeline)
        cards.push(
          <PipelineCard
            key="pipeline"
            p={o.pipeline}
            currency={o.currency}
            base={base}
            className="lg:col-span-2"
          />,
        )
      if (o.leads)
        cards.push(
          <LeadsCard
            key="leads"
            leads={o.leads}
            counts={o.today.leads}
            base={base}
            canAddForm={ws.can("campaigns:write")}
          />,
        )
      if (o.leads_daily)
        cards.push(<LeadsChartCard key="leads-chart" leadsDaily={o.leads_daily} base={base} />)
      if (o.pipeline)
        cards.push(
          <div key="pipeline-chart" className="min-w-0 lg:col-span-2">
            <PipelineChartCard stages={o.pipeline.stages} currency={o.currency} base={base} />
          </div>,
        )
      break
    case "marketing":
      if (o.marketing)
        cards.push(
          <MarketingCard
            key="marketing"
            m={o.marketing}
            base={base}
            canWriteContacts={ws.can("contacts:write")}
            canWriteCampaigns={ws.can("campaigns:write")}
          />,
        )
      break
    case "inbox":
      if (o.conversations)
        cards.push(
          <InboxCard
            key="inbox"
            items={o.conversations}
            unread={o.today.unread_conversations}
            base={base}
            canAddWidget={ws.can("inbox:manage")}
          />,
        )
      if (o.conversations_daily)
        cards.push(
          <ConversationsChartCard
            key="inbox-chart"
            conversationsDaily={o.conversations_daily}
            base={base}
          />,
        )
      break
    case "insights":
      if (o.trends)
        cards.push(
          <TrendsCard key="trends" tr={o.trends} base={base} canManage={ws.can("trends:manage")} />,
        )
      if (o.traffic) cards.push(<TrafficCard key="traffic" traffic={o.traffic} base={base} />)
      if (o.youtube && o.youtube.length)
        cards.push(<YouTubeCard key="youtube" channels={o.youtube} base={base} />)
      break
    case "notifications":
      if (o.notifications)
        cards.push(
          <NotificationsCard
            key="notifications"
            items={o.notifications}
            unread={o.today.unread_notifications}
            base={base}
            canAddSource={ws.can("notifications:manage")}
          />,
        )
      break
    case "settings":
      if (o.setup && setupPending(o.setup))
        cards.push(
          <SetupCard
            key="setup"
            items={o.setup}
            base={base}
            can={ws.can}
          />,
        )
      cards.push(
        <WorkspaceCard
          key="workspace"
          base={base}
          timezone={o.timezone}
          currency={o.currency}
          limited={false}
        />,
      )
      break
  }
  // A lone card, or Settings' setup list and details, read best full width.
  if (cards.length === 1 || section === "settings") return <div className="grid grid-cols-1 gap-4 sm:gap-6">{cards}</div>
  return cards.length ? (
    <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2" data-section-cards>
      {cards}
    </div>
  ) : null
}

function RecentAutomationRuns({ base }: { base: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const runs = useRuns(ws.id, { limit: 5 })
  const list = (runs.data?.runs ?? []).slice(0, 5)
  return (
    <Panel
      id="automation-runs"
      title={t("section.recentRuns")}
      icon={<PlayIcon />}
      href={`${base}/automation/runs`}
    >
      {runs.error ? (
        <ErrorState error={runs.error} />
      ) : runs.isLoading ? (
        <LoadingRows rows={3} />
      ) : list.length === 0 ? (
        <Empty
          title={t("automation.noRuns")}
          action={{ href: `${base}/automation/browsers`, label: t("automation.browsers") }}
        />
      ) : (
        <ul className="divide-y divide-nq-line">
          {list.map((r) => (
            <AutomationRunRow key={r.id} run={r} href={`${base}/automation/runs/${r.id}`} />
          ))}
        </ul>
      )}
    </Panel>
  )
}

