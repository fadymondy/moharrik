"use client"

import { useCallback, useEffect, useRef } from "react"
import { Card, Skeleton } from "@fadymondy/nasaq/web"

import {
  ActivityPanel,
  AttentionPanel,
  ConnectionsPanel,
  HomeHeader,
  HomeNextSteps,
  HomeTiles,
  JourneysPanel,
  WorkingPanel,
} from "@/components/home/home-sections"
import { SetupCard, setupPending } from "@/components/overview/overview-cards"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useWorkspaceEvents, useStreamConnected } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useHome, useMe, useOverview } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

// What on this page a stream event can change. A burst (an import, a campaign) refetches once.
const REFRESH_KINDS = new Set(["message", "lead", "run", "rules", "conversation", "notification"])
const DEBOUNCE_MS = 1500

/**
 * The workspace Home (MH-1062): a greeting, four numbers for today, what needs attention, what
 * Moharrik is doing, today's activity, customer journeys and what is connected. Every figure is
 * from GET /api/w/home; a section the role cannot read is left out. The setup checklist stays
 * until a new workspace has finished it.
 */
export default function HomePage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.home")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`

  const home = useHome(ws.id)
  const ov = useOverview(ws.id)
  const me = useMe()
  const live = useStreamConnected(ws.id)

  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const refresh = home.mutate
  const refreshSoon = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => void refresh(), DEBOUNCE_MS)
  }, [refresh])
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current)
    },
    [],
  )
  useWorkspaceEvents(ws.id, (e) => {
    if (REFRESH_KINDS.has(e.kind)) refreshSoon()
  })

  const h = home.data
  const email = me.data?.email ?? ""
  const local = email.split("@")[0] ?? ""
  const name = local ? local.charAt(0).toUpperCase() + local.slice(1) : ws.summary.name
  const setup = ov.data?.setup ?? null

  if (home.error) {
    return (
      <div className="px-4 sm:px-6">
        <ErrorState error={home.error} />
      </div>
    )
  }
  if (!h) return <HomeSkeleton />

  return (
    <div className="space-y-4 pb-10 sm:space-y-6" data-home>
      <HomeHeader home={h} name={name} live={live} />
      <HomeTiles home={h} base={base} />

      {setup && setupPending(setup) ? (
        <div className="px-4 sm:px-6">
          <SetupCard
            items={setup}
            base={base}
            can={ws.can}
            className="overflow-hidden rounded-card border-border shadow-xs"
          />
        </div>
      ) : null}

      <HomeNextSteps has={h.has} base={base} can={ws.can} />

      <div
        className="grid grid-cols-1 gap-4 px-4 sm:gap-6 sm:px-6 lg:grid-cols-3"
        data-home-row="1"
      >
        {h.attention ? <AttentionPanel attention={h.attention} base={base} /> : null}
        <WorkingPanel working={h.working} base={base} />
        <ActivityPanel events={h.activity} base={base} />
      </div>

      {h.journeys || h.connections ? (
        <div
          className="grid grid-cols-1 gap-4 px-4 sm:gap-6 sm:px-6 lg:grid-cols-5"
          data-home-row="2"
        >
          {h.journeys ? (
            <div className="min-w-0 lg:col-span-3 [&>*]:h-full">
              <JourneysPanel journeys={h.journeys} base={base} />
            </div>
          ) : null}
          {h.connections ? (
            <div className="min-w-0 lg:col-span-2 [&>*]:h-full">
              <ConnectionsPanel
                connections={h.connections}
                base={base}
                canManage={ws.can("inbox:manage")}
              />
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function HomeSkeleton() {
  return (
    <div className="space-y-4 px-4 sm:space-y-6 sm:px-6" aria-busy>
      <div className="space-y-2 pt-2">
        <Skeleton className="h-8 w-72" />
        <Skeleton className="h-4 w-96 max-w-full" />
      </div>
      <div className="grid grid-cols-2 gap-3 sm:gap-4 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <Card key={i} className="gap-2 px-5 py-4 shadow-xs">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-8 w-12" />
            <Skeleton className="h-3 w-20" />
          </Card>
        ))}
      </div>
      <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-3">
        {Array.from({ length: 3 }, (_, i) => (
          <Card key={i} className="gap-3 px-5 py-4 shadow-xs">
            <Skeleton className="h-4 w-32" />
            <Skeleton className="h-4 w-full" />
            <Skeleton className="h-4 w-3/4" />
          </Card>
        ))}
      </div>
    </div>
  )
}
