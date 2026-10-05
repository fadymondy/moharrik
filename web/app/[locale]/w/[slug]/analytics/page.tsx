"use client"

/*
  Analytics (MH-444): the workspace's GA4 properties — totals, a daily trend, top pages and
  source / medium for 7, 28 or 90 days. Every number is what GA4 answered (GET
  /api/w/analytics/report); a property with no data says so, a failing one says why, and an
  answer GA4 could not refresh is shown as the stale answer it is.
*/

import { Suspense, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { ChartLineIcon, FileTextIcon, SignpostIcon } from "lucide-react"

import { BarList, TrendChart } from "@/components/charts/trend-chart"
import { Ltr } from "@/components/copy-field"
import {
  DataError,
  Freshness,
  NoResource,
  ResourceSwitcher,
  Stat,
  useEnabled,
  WindowToggle,
} from "@/components/google/data-page"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState } from "@/components/states"
import { Skeleton, Toggle, ToggleGroup } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, type GaReport, type GaReportOut } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useGaReport } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type Metric = "users" | "sessions" | "views"

export default function AnalyticsPage() {
  return (
    <Suspense>
      <Analytics />
    </Suspense>
  )
}

function Analytics() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.analytics")} · ${ws.summary.name}`)
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const enabled = useEnabled("ga4_property")
  const days = [7, 28, 90].includes(Number(params.get("days"))) ? Number(params.get("days")) : 28
  const chosen = params.get("property") ?? ""
  const resourceId = enabled.list.some((r) => r.id === chosen)
    ? chosen
    : (enabled.list[0]?.id ?? "")
  const report = useGaReport(ws.id, resourceId, days, !!resourceId)
  const [refreshing, setRefreshing] = useState(false)
  const f = useFormat()

  function set(key: string, value: string) {
    const q = new URLSearchParams(params.toString())
    q.set(key, value)
    router.replace(`${pathname}?${q}`, { scroll: false })
  }

  async function refresh() {
    setRefreshing(true)
    try {
      const q = new URLSearchParams({ days: String(days), resource: resourceId, refresh: "true" })
      const out = await api<GaReportOut>(`/api/w/analytics/report?${q}`, { workspaceId: ws.id })
      await report.mutate(out, { revalidate: false })
    } finally {
      setRefreshing(false)
    }
  }

  const canRead = ws.can("analytics:read")
  const out = report.data
  const rep = out?.report ?? null

  return (
    <>
      <SectionHeader
        title={t("nav.analytics")}
        description={t("analytics.hint")}
        action={
          resourceId ? (
            <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto">
              <ResourceSwitcher
                kind="ga4_property"
                list={enabled.list}
                value={resourceId}
                onChange={(id) => set("property", id)}
              />
              <WindowToggle value={days} onChange={(d) => set("days", String(d))} />
            </div>
          ) : null
        }
      />

      {!canRead ? (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("gdata.noAccess")}</p>
      ) : enabled.error ? (
        <ErrorState error={enabled.error} />
      ) : !enabled.data ? (
        <PageSkeleton />
      ) : !resourceId ? (
        <NoResource kind="ga4_property" known={enabled.known} />
      ) : report.error ? (
        <ErrorState error={report.error} />
      ) : !out ? (
        <PageSkeleton />
      ) : !rep ? (
        <DataError message={out.error || t("gdata.noAnswer")} resource={out.resource} />
      ) : (
        <div className="pb-10" data-analytics data-days={rep.days} aria-busy={report.isValidating}>
          <div className="px-6 pb-4">
            <Freshness
              fetchedAt={rep.fetched_at}
              stale={rep.stale}
              note={rep.note}
              onRefresh={refresh}
              refreshing={refreshing}
            />
          </div>
          {rep.empty ? (
            <div data-ga-empty>
              <EmptyState
                title={t("analytics.emptyTitle")}
                body={t("analytics.emptyBody", {
                  from: f.date(rep.start_date),
                  to: f.date(rep.end_date),
                })}
              />
            </div>
          ) : (
            <Filled rep={rep} />
          )}
        </div>
      )}
    </>
  )
}

function Filled({ rep }: { rep: GaReport }) {
  const { t, locale } = useTranslations()
  const f = useFormat()
  const [metric, setMetric] = useState<Metric>("users")
  const tot = rep.totals
  const pct = new Intl.NumberFormat(intlTag(locale), { style: "percent", maximumFractionDigits: 1 })
  const daily = rep.daily ?? []
  const pages = rep.top_pages ?? []
  const sources = rep.sources ?? []
  const range = t("gdata.range", { from: f.date(rep.start_date), to: f.date(rep.end_date) })

  return (
    <>
      <div className="cells cells-flush-x grid-cols-2 lg:grid-cols-4" data-ga-totals>
        <Stat
          id="users"
          label={t("analytics.users")}
          value={f.n(tot.users)}
          sub={t("analytics.newUsers", { n: f.n(tot.new_users) })}
        />
        <Stat id="sessions" label={t("analytics.sessions")} value={f.n(tot.sessions)} sub={range} />
        <Stat
          id="views"
          label={t("analytics.views")}
          value={f.n(tot.views)}
          sub={
            tot.sessions
              ? t("analytics.perSession", { n: (tot.views / tot.sessions).toFixed(1) })
              : undefined
          }
        />
        <Stat
          id="engagement"
          label={t("analytics.engagementRate")}
          value={pct.format(tot.engagement_rate)}
          sub={t("analytics.engagementTime", {
            time: duration(tot.avg_engagement_seconds_per_user, t, f.n),
          })}
        />
      </div>

      <div className="cells cells-flush-x mt-6 grid-cols-1">
        <Panel
          id="trend"
          title={t("analytics.trend")}
          icon={<ChartLineIcon />}
          meta={
            <ToggleGroup
              className="ms-auto"
              value={[metric]}
              onValueChange={(v) => v[0] && setMetric(v[0] as Metric)}
              aria-label={t("analytics.metric")}
            >
              {(["users", "sessions", "views"] as Metric[]).map((m) => (
                <Toggle key={m} value={m} data-metric={m}>
                  {t(`analytics.${m}`)}
                </Toggle>
              ))}
            </ToggleGroup>
          }
        >
          <div className="px-5 py-4">
            <TrendChart
              title={`${t(`analytics.${metric}`)} · ${range}`}
              dates={daily.map((d) => d.date)}
              series={[
                {
                  key: metric,
                  label: t(`analytics.${metric}`),
                  values: daily.map((d) => d[metric]),
                  slot: 1,
                },
              ]}
              format={f.n}
              formatDate={f.date}
            />
            <p className="mt-2 text-xs text-muted-foreground">
              {t("analytics.trendNote", { tz: rep.time_zone })}
            </p>
          </div>
        </Panel>
      </div>

      <div className="cells cells-flush-x mt-6 grid-cols-1 lg:grid-cols-2">
        <Panel
          id="pages"
          title={t("analytics.topPages")}
          icon={<FileTextIcon />}
          meta={<span className="ms-auto text-xs text-muted-foreground">{t("analytics.views")}</span>}
        >
          {pages.length === 0 ? (
            <p className="px-5 py-6 text-sm text-muted-foreground">{t("analytics.noPages")}</p>
          ) : (
            <BarList
              label={t("analytics.topPages")}
              format={f.n}
              rows={pages.map((p) => ({
                key: p.path,
                label: <Ltr mono>{p.path || "(not set)"}</Ltr>,
                value: p.views,
                sub: t("analytics.usersShort", { n: f.n(p.users) }),
              }))}
            />
          )}
        </Panel>
        <Panel
          id="sources"
          title={t("analytics.sources")}
          icon={<SignpostIcon />}
          meta={<span className="ms-auto text-xs text-muted-foreground">{t("analytics.sessions")}</span>}
        >
          {sources.length === 0 ? (
            <p className="px-5 py-6 text-sm text-muted-foreground">{t("analytics.noSources")}</p>
          ) : (
            <BarList
              label={t("analytics.sources")}
              format={f.n}
              rows={sources.map((s) => ({
                key: `${s.source}/${s.medium}`,
                label: <Ltr>{`${s.source} / ${s.medium}`}</Ltr>,
                value: s.sessions,
                sub: t("analytics.usersShort", { n: f.n(s.users) }),
              }))}
            />
          )}
        </Panel>
      </div>
    </>
  )
}

/** 95 → "1m 35s", in the page's language. */
function duration(
  sec: number,
  t: (k: string, v?: Record<string, string | number>) => string,
  n: (v: number) => string,
) {
  const s = Math.round(sec)
  const m = Math.floor(s / 60)
  return m ? t("gdata.minSec", { m: n(m), s: n(s % 60) }) : t("gdata.sec", { s: n(s) })
}

function PageSkeleton() {
  return (
    <div className="space-y-6 pb-10" aria-busy>
      <div className="cells cells-flush-x grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="space-y-2 px-5 py-4">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-7 w-16" />
          </div>
        ))}
      </div>
      <div className="px-6">
        <Skeleton className="h-60 w-full" />
      </div>
    </div>
  )
}
