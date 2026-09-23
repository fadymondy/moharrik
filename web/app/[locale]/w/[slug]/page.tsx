"use client"

import Link from "next/link"
import { Building2Icon, KeyRoundIcon, UsersIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import {
  InboxCard,
  LeadsCard,
  MarketingCard,
  NotificationsCard,
  Panel,
  PipelineCard,
  SetupCard,
  TodayStrip,
  TrendsCard,
  setupPending,
} from "@/components/overview/overview-cards"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useOverview } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

/**
 * The workspace's front page (MH-462): what is happening across leads, the
 * pipeline, the inbox, notifications, marketing and trends, and what is still
 * to set up — every figure from GET /api/w/overview, every count a link to the
 * page it came from. A section the viewer's role cannot read is not rendered.
 */
export default function OverviewPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.overview")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`

  const ov = useOverview(ws.id)
  // A new lead or message changes the numbers on this page; the stream says so.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "lead" || e.kind === "message") void ov.mutate()
  })
  const o = ov.data

  const sections = o
    ? [o.pipeline, o.leads, o.conversations, o.notifications, o.marketing, o.trends].filter((s) => s !== null).length
    : 0
  const showSetup = !!o && setupPending(o.setup)

  return (
    <>
      <SectionHeader micro={t("nav.overview")} title={<Bdi>{ws.summary.name}</Bdi>} action={<Badge variant="outline">{t(`role.${ws.summary.role}`)}</Badge>} />

      {ov.error ? (
        <div className="mt-2">
          <ErrorState error={ov.error} />
        </div>
      ) : !o ? (
        <OverviewSkeleton />
      ) : (
        // Cards sit inside the page padding, each on its own frame with a gap between: the
        // first version drew every card as a cell of one hairline grid, bled 1px past the page
        // edge (grid-cells-flush-x) and read as cramped and heavy.
        <div className="space-y-4 px-4 pb-10 sm:space-y-6 sm:px-6" data-overview>
          <TodayStrip today={o.today} base={base} />

          <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2" data-overview-cards>
            {showSetup && o.setup ? <SetupCard items={o.setup} base={base} can={ws.can} className="lg:col-span-2" /> : null}
            {o.pipeline ? <PipelineCard p={o.pipeline} currency={o.currency} base={base} className="lg:col-span-2" /> : null}
            {o.leads ? <LeadsCard leads={o.leads} counts={o.today.leads} base={base} canAddForm={ws.can("campaigns:write")} /> : null}
            {o.conversations ? (
              <InboxCard items={o.conversations} unread={o.today.unread_conversations} base={base} canAddWidget={ws.can("inbox:manage")} />
            ) : null}
            {o.notifications ? (
              <NotificationsCard items={o.notifications} unread={o.today.unread_notifications} base={base} canAddSource={ws.can("notifications:manage")} />
            ) : null}
            {o.marketing ? (
              <MarketingCard m={o.marketing} base={base} canWriteContacts={ws.can("contacts:write")} canWriteCampaigns={ws.can("campaigns:write")} />
            ) : null}
            {o.trends ? <TrendsCard tr={o.trends} base={base} canManage={ws.can("trends:manage")} /> : null}
            <WorkspaceCard base={base} timezone={o.timezone} currency={o.currency} limited={sections === 0} />
          </div>
        </div>
      )}
    </>
  )
}

/** The workspace's own settings, which used to be the whole page. There is no general Settings page to move them to. */
function WorkspaceCard({ base, timezone, currency, limited }: { base: string; timezone: string; currency: string; limited: boolean }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const details = ws.current?.workspace
  return (
    <Panel id="workspace" title={t("overview.workspace")} icon={<Building2Icon />} className={limited ? "lg:col-span-2" : undefined}>
      {limited ? <p className="border-b border-line px-5 py-3 text-sm text-grid-muted">{t("overview.limitedRole")}</p> : null}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 px-5 pt-4 text-sm">
        {[
          [t("overview.slug"), <Ltr key="s" mono>/{ws.slug}</Ltr>],
          [t("overview.locale"), details?.locale ? <Ltr key="l" mono>{details.locale}</Ltr> : "—"],
          [t("overview.timezone"), <Ltr key="tz" mono>{timezone}</Ltr>],
          [t("overview.currency"), <Ltr key="c" mono>{currency}</Ltr>],
        ].map(([label, value], i) => (
          <div key={i} className="min-w-0">
            <dt className="grid-micro mb-1">{label}</dt>
            <dd className="text-grid-fg [overflow-wrap:anywhere]">{value}</dd>
          </div>
        ))}
      </dl>
      <div className="flex flex-wrap gap-2 px-5 py-4">
        {ws.can("members:read") ? (
          <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`${base}/members`} />}>
            <UsersIcon />
            {t("nav.members")}
          </Button>
        ) : null}
        <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`${base}/settings/mcp`} />}>
          <KeyRoundIcon />
          {t("nav.mcpTokens")}
        </Button>
      </div>
    </Panel>
  )
}

function OverviewSkeleton() {
  return (
    <div className="space-y-4 px-4 sm:space-y-6 sm:px-6" aria-busy>
      <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="space-y-2 border border-line bg-grid-card px-5 py-4">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-8 w-12" />
            <Skeleton className="h-3 w-20" />
          </div>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="space-y-3 border border-line bg-grid-card px-5 py-4">
            <Skeleton className="h-4 w-32" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-3/4" />
          </div>
        ))}
      </div>
    </div>
  )
}
