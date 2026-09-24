"use client"

/*
  The Overview's own charts (MH-567): traffic over time (users/sessions/clicks), leads per day,
  and conversations per day by channel — on the same ChartContainer/Recharts pattern as
  components/reports/report-charts.tsx. Colours come only from --chart-1/--chart-2 (validated per
  theme in moharrik-brand.css) plus --grid-brand for a third series — already used and validated
  the same way, never a hard-coded colour. Every chart has a DataTable view behind a chart/table
  ToggleGroup, exactly like reports/page.tsx. Figures are drawn as given: nothing is smoothed.
*/

import { HandshakeIcon, InboxIcon, TrendingUpIcon } from "lucide-react"
import { useState } from "react"
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"

import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { type BarRow, type Column, DataTable, HorizontalBars } from "@/components/reports/report-charts"
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { OverviewConversationDay, OverviewCountDay, OverviewStage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

/** The three series colours this page ever draws with, in series order. Never a hard-coded hex. */
const SERIES_COLORS = ["var(--chart-1)", "var(--chart-2)", "var(--grid-brand)"] as const

export type SeriesPoint = { date: string } & Record<string, number | string | null>

/** A panel's two views: the chart, and the same numbers as a table. */
export function ViewToggle({ value, onChange, id }: { value: "chart" | "table"; onChange: (v: "chart" | "table") => void; id: string }) {
  const { t } = useTranslations()
  return (
    <ToggleGroup
      variant="outline"
      size="sm"
      spacing={0}
      className="ms-auto"
      value={[value]}
      onValueChange={(v) => v[0] && onChange(v[0] as "chart" | "table")}
      aria-label={t("reports.view")}
      data-view-toggle={id}
    >
      <ToggleGroupItem value="chart">{t("reports.chart")}</ToggleGroupItem>
      <ToggleGroupItem value="table">{t("reports.table")}</ToggleGroupItem>
    </ToggleGroup>
  )
}

export function NoChartData({ id }: { id: string }) {
  const { t } = useTranslations()
  return (
    <p className="px-5 pt-2 pb-6 text-sm text-grid-muted" data-no-data={id}>
      {t("overview.noChartData")}
    </p>
  )
}

/**
 * A line chart over daily dates, one to three series. Used for traffic over time, leads per day,
 * and conversations per day by channel — the only difference between them is the series list.
 */
export function TrendChart({
  data,
  seriesKeys,
  seriesLabels,
  formatTick,
  formatDay,
  id,
}: {
  data: SeriesPoint[]
  seriesKeys: string[]
  seriesLabels: string[]
  formatTick: (n: number) => string
  formatDay: (ymd: string) => string
  id: string
}) {
  const { isRtl, dir } = useTranslations()
  const config = Object.fromEntries(
    seriesKeys.map((k, i) => [k, { label: seriesLabels[i], color: SERIES_COLORS[i % SERIES_COLORS.length] }]),
  ) satisfies ChartConfig
  return (
    <>
      {seriesKeys.length > 1 ? (
        <ul className="flex flex-wrap items-center justify-center gap-4 pb-2 text-xs text-grid-muted" data-legend>
          {seriesKeys.map((k, i) => (
            <li key={k} className="flex items-center gap-1.5">
              <span className="size-2.5 rounded-full" style={{ background: SERIES_COLORS[i % SERIES_COLORS.length] }} aria-hidden />
              {seriesLabels[i]}
            </li>
          ))}
        </ul>
      ) : null}
      {/* dir="ltr": SVG text anchors follow the document's direction, not a CSS property, so the
          mirroring is explicit — a reversed time axis and the value axis on the right in RTL. */}
      <ChartContainer id={id} config={config} className="aspect-auto h-56 w-full" data-chart-id={id} dir="ltr">
        <LineChart data={data} margin={{ top: 8, bottom: 0, left: 4, right: 4 }} accessibilityLayer>
          <CartesianGrid vertical={false} />
          <XAxis dataKey="date" reversed={isRtl} tickLine={false} axisLine={false} tickMargin={8} minTickGap={24} tickFormatter={(v: string) => formatDay(v)} />
          <YAxis orientation={isRtl ? "right" : "left"} tickLine={false} axisLine={false} width={48} allowDecimals={false} tickFormatter={(v: number) => formatTick(v)} />
          <ChartTooltip
            cursor={{ strokeOpacity: 0.4 }}
            content={
              <ChartTooltipContent
                labelFormatter={(_l, payload) => formatDay(String(payload?.[0]?.payload?.date ?? ""))}
                formatter={(value, name, item) => (
                  <div className="flex w-full items-center justify-between gap-4" dir={dir}>
                    <span className="flex items-center gap-1.5 text-grid-muted">
                      <span className="size-2.5 rounded-full" style={{ background: item.color }} aria-hidden />
                      {config[String(name)]?.label}
                    </span>
                    <Ltr className="font-medium text-grid-fg tabular-nums">{value == null ? "—" : formatTick(Number(value))}</Ltr>
                  </div>
                )}
              />
            }
          />
          {seriesKeys.map((k, i) => (
            <Line key={k} dataKey={k} stroke={SERIES_COLORS[i % SERIES_COLORS.length]} strokeWidth={2} dot={false} connectNulls={false} isAnimationActive={false} />
          ))}
        </LineChart>
      </ChartContainer>
    </>
  )
}

/** The same series as an accessible table: date, then one numeric column per series. */
export function trendColumns(dayHead: string, seriesKeys: string[], seriesLabels: string[], formatTick: (n: number) => string, formatDay: (ymd: string) => string): Column<SeriesPoint>[] {
  return [
    { key: "date", head: dayHead, cell: (r) => <Ltr>{formatDay(r.date)}</Ltr> },
    ...seriesKeys.map((k, i) => ({
      key: k,
      head: seriesLabels[i],
      cell: (r: SeriesPoint) => (r[k] == null ? "—" : formatTick(Number(r[k]))),
      numeric: true,
    })),
  ]
}

/** One chart/table panel body: the chart above a ToggleGroup-driven table, or NoChartData. */
export function ChartOrTable({
  id,
  caption,
  data,
  seriesKeys,
  seriesLabels,
  formatTick,
  formatDay,
  dayHead,
  empty,
}: {
  id: string
  caption: string
  data: SeriesPoint[]
  seriesKeys: string[]
  seriesLabels: string[]
  formatTick: (n: number) => string
  formatDay: (ymd: string) => string
  dayHead: string
  empty: boolean
}) {
  const [view, setView] = useState<"chart" | "table">("chart")
  if (empty) return <NoChartData id={id} />
  return (
    <div className="flex flex-1 flex-col">
      <div className="flex items-center px-5 pt-3 pb-1">
        <ViewToggle id={id} value={view} onChange={setView} />
      </div>
      {view === "chart" ? (
        <div className="px-3 pb-4">
          <TrendChart data={data} seriesKeys={seriesKeys} seriesLabels={seriesLabels} formatTick={formatTick} formatDay={formatDay} id={id} />
        </div>
      ) : (
        <div className="px-3 pb-4">
          <DataTable id={id} caption={caption} rows={data} rowKey={(r) => r.date} columns={trendColumns(dayHead, seriesKeys, seriesLabels, formatTick, formatDay)} />
        </div>
      )}
    </div>
  )
}

// ── the three Overview panels ───────────────────────────────────────────────

/** Leads created per day, the last 28 days, zero-filled — a single-series trend. */
export function LeadsChartCard({ leadsDaily, base }: { leadsDaily: OverviewCountDay[]; base: string }) {
  const { t, locale } = useTranslations()
  const f = useFormat()
  const day = dateFormat(locale, { day: "numeric", month: "short", timeZone: "UTC" })
  const formatDay = (ymd: string) => day.format(new Date(`${ymd}T00:00:00Z`))
  const data: SeriesPoint[] = leadsDaily.map((d) => ({ date: d.date, leads: d.count }))
  const total = leadsDaily.reduce((sum, d) => sum + d.count, 0)
  return (
    <Panel id="leads-trend" title={t("overview.leadsChart")} icon={<TrendingUpIcon />} href={`${base}/leads`} meta={<span className="text-xs text-grid-muted">{t("overview.last28")}</span>}>
      <ChartOrTable
        id="leads-trend"
        caption={t("overview.leadsChart")}
        data={data}
        seriesKeys={["leads"]}
        seriesLabels={[t("nav.leads")]}
        formatTick={f.n}
        formatDay={formatDay}
        dayHead={t("reports.bucketHead.day")}
        empty={total === 0}
      />
    </Panel>
  )
}

/** Conversations started per day, the last 28 days, split by channel and zero-filled. */
export function ConversationsChartCard({ conversationsDaily, base }: { conversationsDaily: OverviewConversationDay[]; base: string }) {
  const { t, locale } = useTranslations()
  const f = useFormat()
  const day = dateFormat(locale, { day: "numeric", month: "short", timeZone: "UTC" })
  const formatDay = (ymd: string) => day.format(new Date(`${ymd}T00:00:00Z`))
  const data: SeriesPoint[] = conversationsDaily.map((d) => ({ date: d.date, chat: d.chat, email: d.email, whatsapp: d.whatsapp }))
  const total = conversationsDaily.reduce((sum, d) => sum + d.chat + d.email + d.whatsapp, 0)
  return (
    <Panel
      id="conversations-trend"
      title={t("overview.conversationsChart")}
      icon={<InboxIcon />}
      href={`${base}/inbox`}
      meta={<span className="text-xs text-grid-muted">{t("overview.last28")}</span>}
    >
      <ChartOrTable
        id="conversations-trend"
        caption={t("overview.conversationsChart")}
        data={data}
        seriesKeys={["chat", "email", "whatsapp"]}
        seriesLabels={[t("inbox.channel.chat"), t("inbox.channel.email"), t("inbox.channel.whatsapp")]}
        formatTick={f.n}
        formatDay={formatDay}
        dayHead={t("reports.bucketHead.day")}
        empty={total === 0}
      />
    </Panel>
  )
}

/** Open pipeline value by stage, right now — the same figures as PipelineCard, as a chart. */
export function PipelineChartCard({ stages, currency, base }: { stages: OverviewStage[]; currency: string; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const [view, setView] = useState<"chart" | "table">("chart")
  const openDeals = stages.reduce((sum, s) => sum + s.deal_count, 0)
  const rows: BarRow[] = stages.map((s) => ({
    key: s.id,
    name: s.name,
    value: s.value_minor,
    label: f.money(s.value_minor, currency),
    tooltip: (
      <span className="flex flex-col gap-0.5">
        <Bdi className="font-medium text-grid-fg">{s.name}</Bdi>
        <span className="text-grid-muted">
          <Ltr>{f.money(s.value_minor, currency)}</Ltr> · {t("overview.deals", { n: f.n(s.deal_count) })}
        </span>
      </span>
    ),
  }))
  return (
    <Panel
      id="pipeline-chart"
      title={t("reports.pipelineByStage")}
      icon={<HandshakeIcon />}
      href={`${base}/deals`}
      meta={openDeals ? <ViewToggle id="pipeline-chart" value={view} onChange={setView} /> : undefined}
    >
      {openDeals === 0 ? (
        <NoChartData id="pipeline-chart" />
      ) : view === "chart" ? (
        <div className="px-3 pb-4 pt-3">
          <HorizontalBars id="pipeline-chart" rows={rows} series={t("overview.value")} format={(n) => f.money(n, currency)} />
        </div>
      ) : (
        <div className="px-3 pb-4 pt-3">
          <DataTable
            id="pipeline-chart"
            caption={t("reports.pipelineByStage")}
            rows={stages}
            rowKey={(s) => s.id}
            columns={[
              { key: "stage", head: t("overview.stage"), cell: (s) => <Bdi>{s.name}</Bdi> },
              { key: "deals", head: t("overview.deals"), cell: (s) => f.n(s.deal_count), numeric: true },
              { key: "value", head: t("overview.value"), cell: (s) => <Ltr>{f.money(s.value_minor, currency)}</Ltr>, numeric: true },
            ]}
          />
        </div>
      )}
    </Panel>
  )
}

export { Bdi }
