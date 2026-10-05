"use client"

/*
  Search Console (MH-445): clicks, impressions, CTR and average position for an enabled site over
  7, 28 or 90 days, a previous-period comparison, filters (query, page, country, device), and the
  top queries and pages — all from the daily import (GET /api/w/search-console/performance).

  Honesty: the window ends on the newest day Search Console has data for (it lags 2–3 days) and
  says so; with a query or page filter the totals are sums of query rows, which leave anonymised
  queries out, and the page says that too; a day never imported is a gap, never a zero.
*/

import { Suspense, useEffect, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import {
  ChartLineIcon,
  FileTextIcon,
  InfoIcon,
  LoaderIcon,
  NetworkIcon,
  ScanSearchIcon,
  SearchIcon,
} from "lucide-react"

import { TrendChart, type TrendSeries } from "@/components/charts/trend-chart"
import { Ltr } from "@/components/copy-field"
import {
  DataError,
  Delta,
  NoResource,
  ResourceSwitcher,
  Stat,
  useEnabled,
  WindowToggle,
} from "@/components/google/data-page"
import { InspectTab, SitemapsTab } from "@/components/google/gsc-tools"
import { SyncStatus } from "@/components/google/resource-picker"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { SectionHeader } from "@/components/public-frame"
import { RowsTable } from "@/components/nq-table"
import { EmptyState, ErrorState } from "@/components/states"
import {
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Skeleton,
  Switch,
  Tabs,
  TabsList,
  TabsPanel,
  TabsTab,
  Toggle,
  ToggleGroup,
} from "@fadymondy/nasaq/web"
import { Label } from "@/components/form-layout"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { GoogleResource, GscPerformance, GscPoint, GscRow } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useGscPerformance } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type Metric = "clicks" | "impressions" | "ctr" | "position"
const ALL = "__all"

// Search Console writes countries as ISO 3166-1 alpha-3; Intl.DisplayNames wants alpha-2.
const A3: Record<string, string> = {
  egy: "EG",
  sau: "SA",
  are: "AE",
  usa: "US",
  gbr: "GB",
  kwt: "KW",
  qat: "QA",
  bhr: "BH",
  omn: "OM",
  jor: "JO",
  lbn: "LB",
  irq: "IQ",
  mar: "MA",
  dza: "DZ",
  tun: "TN",
  lby: "LY",
  sdn: "SD",
  pse: "PS",
  syr: "SY",
  yem: "YE",
  tur: "TR",
  ind: "IN",
  pak: "PK",
  can: "CA",
  aus: "AU",
  deu: "DE",
  fra: "FR",
  nld: "NL",
  esp: "ES",
  ita: "IT",
  swe: "SE",
  che: "CH",
  bel: "BE",
  irl: "IE",
  nga: "NG",
  ken: "KE",
  zaf: "ZA",
  bra: "BR",
  mex: "MX",
  idn: "ID",
  mys: "MY",
  sgp: "SG",
  phl: "PH",
  jpn: "JP",
  kor: "KR",
  chn: "CN",
  rus: "RU",
  ukr: "UA",
  pol: "PL",
  rou: "RO",
  prt: "PT",
  aut: "AT",
  nzl: "NZ",
  arg: "AR",
  col: "CO",
}

export default function SearchConsolePage() {
  return (
    <Suspense>
      <SearchConsole />
    </Suspense>
  )
}

function SearchConsole() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.searchConsole")} · ${ws.summary.name}`)
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const enabled = useEnabled("gsc_site")
  const days = [7, 28, 90].includes(Number(params.get("days"))) ? Number(params.get("days")) : 28
  const compare = params.get("compare") !== "false"
  const chosen = params.get("site") ?? ""
  const resourceId = enabled.list.some((r) => r.id === chosen)
    ? chosen
    : (enabled.list[0]?.id ?? "")
  const filters = {
    query: params.get("query") ?? "",
    page: params.get("page") ?? "",
    country: params.get("country") ?? "",
    device: params.get("device") ?? "",
  }

  const q = new URLSearchParams({ days: String(days), compare: String(compare) })
  if (resourceId) q.set("resource", resourceId)
  for (const [k, v] of Object.entries(filters)) if (v) q.set(k, v)
  const perf = useGscPerformance(
    ws.id,
    q.toString(),
    !!resourceId && !["inspect", "sitemaps"].includes(params.get("tab") ?? ""),
  )

  function set(next: Record<string, string>) {
    const u = new URLSearchParams(params.toString())
    for (const [k, v] of Object.entries(next)) {
      if (v) u.set(k, v)
      else u.delete(k)
    }
    router.replace(`${pathname}?${u}`, { scroll: false })
  }

  const out = perf.data
  const p = out?.performance ?? null
  const tab = ["inspect", "sitemaps"].includes(params.get("tab") ?? "")
    ? (params.get("tab") as string)
    : "performance"
  const current = enabled.list.find((r) => r.id === resourceId)
  const siteName = current?.display_name ?? ""
  const siteID = current?.external_id ?? ""

  return (
    <>
      <SectionHeader
        title={t("nav.searchConsole")}
        description={t("gsc.hint")}
        action={
          resourceId ? (
            <div className="flex w-full flex-wrap items-center gap-3 sm:w-auto">
              <ResourceSwitcher
                kind="gsc_site"
                list={enabled.list}
                value={resourceId}
                onChange={(id) => set({ site: id })}
              />
              {tab === "performance" ? (
                <>
                  <WindowToggle value={days} onChange={(d) => set({ days: String(d) })} />
                  <Label className="flex items-center gap-2 text-sm font-normal text-nq-fg-body">
                    <Switch
                      checked={compare}
                      onCheckedChange={(v) => set({ compare: v ? "" : "false" })}
                      data-compare
                    />
                    {t("gsc.compare")}
                  </Label>
                </>
              ) : null}
            </div>
          ) : null
        }
      />

      {!ws.can("analytics:read") ? (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("gdata.noAccess")}</p>
      ) : enabled.error ? (
        <ErrorState error={enabled.error} />
      ) : !enabled.data ? (
        <PageSkeleton />
      ) : !resourceId ? (
        <NoResource kind="gsc_site" known={enabled.known} />
      ) : (
        <Tabs
          value={tab}
          onValueChange={(v) => set({ tab: v === "performance" ? "" : String(v) })}
          className="gap-0"
        >
          <div className="px-6 pb-4">
            <TabsList variant="underline">
              <TabsTab value="performance" data-tab="performance">
                <ChartLineIcon />
                {t("gsc.tabPerformance")}
              </TabsTab>
              <TabsTab value="inspect" data-tab="inspect">
                <ScanSearchIcon />
                {t("gsc.tabInspect")}
              </TabsTab>
              <TabsTab value="sitemaps" data-tab="sitemaps">
                <NetworkIcon />
                {t("gsc.tabSitemaps")}
              </TabsTab>
            </TabsList>
          </div>
          <TabsPanel value="performance">
            {perf.error ? (
              <ErrorState error={perf.error} />
            ) : !out || !p ? (
              <PageSkeleton />
            ) : p.nothing_yet ? (
              out.resource?.syncing ||
              (!out.resource?.last_error && !out.resource?.last_synced_at) ? (
                <div
                  className="flex items-center gap-3 border-y border-nq-line bg-card px-6 py-10 text-sm text-nq-fg-body"
                  role="status"
                  data-gsc-importing
                >
                  <LoaderIcon className="size-4 animate-spin text-muted-foreground" />
                  {t("gsc.importing")}
                </div>
              ) : (
                <DataError
                  message={out.resource?.last_error || t("gsc.notImported")}
                  resource={out.resource}
                />
              )
            ) : (
              <div className="pb-10" data-gsc data-days={p.days} aria-busy={perf.isValidating}>
                <Filters filters={filters} p={p} onChange={set} />
                <Coverage p={p} resource={out.resource} />
                {p.empty ? (
                  <div data-gsc-empty>
                    <EmptyState
                      title={t("gsc.emptyTitle")}
                      body={t(p.basis === "queries" ? "gsc.emptyFiltered" : "gsc.emptyBody")}
                    />
                  </div>
                ) : (
                  <Filled p={p} />
                )}
              </div>
            )}
          </TabsPanel>
          <TabsPanel value="inspect">
            {tab === "inspect" ? <InspectTab resourceId={resourceId} siteLabel={siteName} /> : null}
          </TabsPanel>
          <TabsPanel value="sitemaps">
            {tab === "sitemaps" ? <SitemapsTab resourceId={resourceId} site={siteID} /> : null}
          </TabsPanel>
        </Tabs>
      )}
    </>
  )
}

function Filters({
  filters,
  p,
  onChange,
}: {
  filters: Record<string, string>
  p: GscPerformance
  onChange: (n: Record<string, string>) => void
}) {
  const { t, locale } = useTranslations()
  const [query, setQuery] = useState(filters.query)
  const [page, setPage] = useState(filters.page)
  // Typing waits for a pause before it asks the server.
  useEffect(() => {
    const id = setTimeout(() => {
      if (query !== filters.query || page !== filters.page) onChange({ query, page })
    }, 400)
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query, page])
  const names = new Intl.DisplayNames([intlTag(locale)], { type: "region" })
  const countryItems = [
    { value: ALL, label: t("gsc.allCountries") },
    ...(p.countries ?? []).map((c) => ({ value: c, label: countryName(names, c) })),
  ]
  if (filters.country && !countryItems.some((i) => i.value === filters.country))
    countryItems.push({ value: filters.country, label: countryName(names, filters.country) })
  const deviceItems = [
    { value: ALL, label: t("gsc.allDevices") },
    ...["DESKTOP", "MOBILE", "TABLET"].map((d) => ({ value: d, label: t(`gsc.device.${d}`) })),
  ]
  return (
    <div className="flex flex-wrap items-end gap-3 px-6 pb-4" data-gsc-filters>
      <div className="w-full space-y-1 sm:w-56">
        <Label htmlFor="gsc-query" className="text-xs text-muted-foreground">
          {t("gsc.filterQuery")}
        </Label>
        <Input
          id="gsc-query"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("gsc.filterQueryHint")}
          data-filter-query
        />
      </div>
      <div className="w-full space-y-1 sm:w-56">
        <Label htmlFor="gsc-page" className="text-xs text-muted-foreground">
          {t("gsc.filterPage")}
        </Label>
        <Input
          id="gsc-page"
          value={page}
          onChange={(e) => setPage(e.target.value)}
          placeholder="/blog"
          dir="ltr"
          data-filter-page
        />
      </div>
      <div className="w-[calc(50%-0.375rem)] space-y-1 sm:w-44">
        <Label className="text-xs text-muted-foreground">{t("gsc.country")}</Label>
        <Select
          items={countryItems}
          value={filters.country || ALL}
          onValueChange={(v) => onChange({ country: v == null || v === ALL ? "" : String(v) })}
        >
          <SelectTrigger className="w-full" aria-label={t("gsc.country")} data-filter-country>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {countryItems.map((i) => (
              <SelectItem key={i.value} value={i.value}>
                {i.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <div className="w-[calc(50%-0.375rem)] space-y-1 sm:w-40">
        <Label className="text-xs text-muted-foreground">{t("gsc.device")}</Label>
        <Select
          items={deviceItems}
          value={filters.device || ALL}
          onValueChange={(v) => onChange({ device: v == null || v === ALL ? "" : String(v) })}
        >
          <SelectTrigger className="w-full" aria-label={t("gsc.device")} data-filter-device>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {deviceItems.map((i) => (
              <SelectItem key={i.value} value={i.value}>
                {i.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    </div>
  )
}

function countryName(names: Intl.DisplayNames, a3: string) {
  const a2 = A3[a3.toLowerCase()]
  try {
    return (a2 && names.of(a2)) || a3.toUpperCase()
  } catch {
    return a3.toUpperCase()
  }
}

function Coverage({ p, resource }: { p: GscPerformance; resource: GoogleResource | null }) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <div className="space-y-2 px-6 pb-4 text-xs text-muted-foreground">
      <p className="flex flex-wrap items-center gap-x-3 gap-y-1" data-gsc-coverage>
        <span>{t("gsc.range", { from: f.date(p.start_date), to: f.date(p.end_date) })}</span>
        <span>·</span>
        <span>{t("gsc.lag")}</span>
        {p.coverage.last_import_at ? (
          <>
            <span>·</span>
            <span title={f.exact(p.coverage.last_import_at)}>
              {t("gsc.imported", {
                when: f.ago(p.coverage.last_import_at),
                n: f.n(p.coverage.imported_days),
              })}
            </span>
          </>
        ) : null}
        {resource ? <SyncStatus r={resource} /> : null}
      </p>
      {resource?.last_error ? (
        <p className="break-words text-nq-danger-text" data-last-error>
          <Ltr>{resource.last_error}</Ltr>
        </p>
      ) : null}
      {p.basis === "queries" ? (
        <p className="flex items-start gap-2" data-basis="queries">
          <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
          {t("gsc.basisQueries")}
        </p>
      ) : null}
      {p.coverage.truncated_days > 0 ? (
        <p>{t("gsc.truncated", { n: f.n(p.coverage.truncated_days) })}</p>
      ) : null}
      {p.missing_days > 0 ? (
        <p className="text-nq-warning-text" data-missing>
          {t("gsc.missing", { n: f.n(p.missing_days) })}
        </p>
      ) : null}
      {p.previous && p.previous_missing_days > 0 ? (
        <p data-prev-missing>{t("gsc.prevMissing", { n: f.n(p.previous_missing_days) })}</p>
      ) : null}
    </div>
  )
}

function Filled({ p }: { p: GscPerformance }) {
  const { t, locale } = useTranslations()
  const f = useFormat()
  const [metric, setMetric] = useState<Metric>("clicks")
  const pct = new Intl.NumberFormat(intlTag(locale), {
    style: "percent",
    maximumFractionDigits: 1,
  })
  const dec = new Intl.NumberFormat(intlTag(locale), {
    maximumFractionDigits: 1,
    minimumFractionDigits: 1,
  })
  const fmt: Record<Metric, (n: number) => string> = {
    clicks: f.n,
    impressions: f.n,
    ctr: (n) => pct.format(n),
    position: (n) => dec.format(n),
  }
  const tot = p.totals
  // A previous period still being imported is not compared against: its totals are partial.
  const prev = p.previous_missing_days > 0 ? null : p.previous
  const daily = p.daily ?? []
  const pick = (d: GscPoint) => d[metric]
  const series: TrendSeries[] = [
    {
      key: "now",
      label: t("gsc.thisPeriod", { from: f.date(p.start_date), to: f.date(p.end_date) }),
      values: daily.map(pick),
      slot: 1,
    },
  ]
  if (prev && p.previous_daily) {
    series.push({
      key: "before",
      label: t("gsc.previousPeriod", {
        from: f.date(p.previous_start_date ?? ""),
        to: f.date(p.previous_end_date ?? ""),
      }),
      values: p.previous_daily.map(pick),
      slot: 2,
      dashed: true,
      dates: p.previous_daily.map((d) => d.date),
    })
  }
  return (
    <>
      <div className="cells cells-flush-x grid-cols-2 lg:grid-cols-4" data-gsc-totals>
        <Stat
          id="clicks"
          label={t("gsc.clicks")}
          value={f.n(tot.clicks)}
          delta={prev ? <Delta now={tot.clicks} before={prev.clicks} /> : undefined}
        />
        <Stat
          id="impressions"
          label={t("gsc.impressions")}
          value={f.n(tot.impressions)}
          delta={prev ? <Delta now={tot.impressions} before={prev.impressions} /> : undefined}
        />
        <Stat
          id="ctr"
          label={t("gsc.ctr")}
          value={pct.format(tot.ctr)}
          delta={prev ? <Delta now={tot.ctr} before={prev.ctr} mode="pp" /> : undefined}
        />
        <Stat
          id="position"
          label={t("gsc.position")}
          value={tot.position == null ? "—" : dec.format(tot.position)}
          delta={
            prev ? (
              <Delta now={tot.position} before={prev.position} lowerIsBetter mode="points" />
            ) : undefined
          }
        />
      </div>

      <div className="cells cells-flush-x mt-6 grid-cols-1">
        <Panel
          id="gsc-trend"
          title={t("analytics.trend")}
          icon={<ChartLineIcon />}
          meta={
            <ToggleGroup
              className="ms-auto flex-wrap"
              value={[metric]}
              onValueChange={(v) => v[0] && setMetric(v[0] as Metric)}
              aria-label={t("analytics.metric")}
            >
              {(["clicks", "impressions", "ctr", "position"] as Metric[]).map((m) => (
                <Toggle key={m} value={m} data-metric={m}>
                  {t(`gsc.${m}`)}
                </Toggle>
              ))}
            </ToggleGroup>
          }
        >
          <div className="px-5 py-4">
            <TrendChart
              title={t(`gsc.${metric}`)}
              dates={daily.map((d) => d.date)}
              series={series}
              format={fmt[metric]}
              formatDate={f.date}
              invert={metric === "position"}
            />
            {metric === "position" ? (
              <p className="mt-2 text-xs text-muted-foreground">{t("gsc.positionNote")}</p>
            ) : null}
          </div>
        </Panel>
      </div>

      <div className="cells cells-flush-x mt-6 grid-cols-1 xl:grid-cols-2">
        <Panel id="gsc-queries" title={t("gsc.topQueries")} icon={<SearchIcon />}>
          <Ranked rows={p.top_queries ?? []} kind="query" pct={pct} dec={dec} />
        </Panel>
        <Panel id="gsc-pages" title={t("gsc.topPages")} icon={<FileTextIcon />}>
          <Ranked rows={p.top_pages ?? []} kind="page" pct={pct} dec={dec} />
        </Panel>
      </div>
    </>
  )
}

function Ranked({
  rows,
  kind,
  pct,
  dec,
}: {
  rows: GscRow[]
  kind: "query" | "page"
  pct: Intl.NumberFormat
  dec: Intl.NumberFormat
}) {
  const { t } = useTranslations()
  const f = useFormat()
  if (rows.length === 0)
    return <p className="px-5 py-6 text-sm text-muted-foreground">{t("gsc.noRows")}</p>
  return (
    <RowsTable<GscRow>
      data={rows}
      getRowId={(r) => r.key}
      rowLabel={(r) => r.key}
      label={t(kind === "query" ? "gsc.topQueries" : "gsc.topPages")}
      frame={false}
      className="table-fixed"
      data-gsc-rows={kind}
      columns={[
        {
          id: "key",
          header: t(kind === "query" ? "gsc.query" : "gsc.page"),
          hideable: false,
          sortValue: (r) => r.key,
          cell: (r) =>
            kind === "page" ? (
              // A URL truncates at its end, whatever the page's direction.
              <span
                dir="ltr"
                className="block truncate font-mono rtl:text-right"
                title={r.key}
                data-row={r.key}
              >
                {pathOf(r.key)}
              </span>
            ) : (
              <span className="block truncate" title={r.key} data-row={r.key}>
                <bdi>{r.key}</bdi>
              </span>
            ),
        },
        {
          id: "clicks",
          header: t("gsc.clicks"),
          align: "end",
          sortValue: (r) => r.clicks,
          headerClassName: "w-20",
          className: "tabular-nums",
          cell: (r) => f.n(r.clicks),
        },
        {
          id: "impressions",
          header: t("gsc.impressions"),
          align: "end",
          sortValue: (r) => r.impressions,
          headerClassName: "hidden w-28 sm:table-cell",
          className: "hidden tabular-nums sm:table-cell",
          cell: (r) => f.n(r.impressions),
        },
        {
          id: "ctr",
          header: t("gsc.ctr"),
          align: "end",
          sortValue: (r) => r.ctr,
          headerClassName: "hidden w-20 md:table-cell",
          className: "hidden tabular-nums md:table-cell",
          cell: (r) => pct.format(r.ctr),
        },
        {
          id: "position",
          header: t("gsc.positionShort"),
          align: "end",
          sortValue: (r) => r.position ?? undefined,
          headerClassName: "w-24",
          className: "tabular-nums",
          cell: (r) => (r.position == null ? "—" : dec.format(r.position)),
        },
      ]}
    />
  )
}

function pathOf(u: string) {
  try {
    const x = new URL(u)
    return x.pathname + x.search
  } catch {
    return u
  }
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
