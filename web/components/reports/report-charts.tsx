"use client"

/*
  The CRM report's charts (MH-394), on the shadcn `chart` registry component (Recharts).

  The dataviz rules they follow: one axis per chart (count OR value, never both); bars anchored
  to the baseline with 4px rounded ends and a 2px gap; --chart-1 for won / the single series and
  --chart-2 for lost (validated per theme in moharrik-brand.css); recessive grid; a tooltip on
  every bar; a legend whenever there are two series; and every chart has a table view, because
  the table is where a reader checks a number. In RTL the value axis and the time axis run right
  to left, like the rest of the page. Figures are drawn as given: nothing is smoothed or filled.

  Stage names are the workspace's own text, so every one is bidi-isolated (isolate / <Bdi>).
*/

import { Bar, BarChart, CartesianGrid, LabelList, XAxis, YAxis } from "recharts"
import { useSyncExternalStore, type ReactNode } from "react"

import { Bdi, isolate } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { type ChartConfig, ChartContainer, ChartTooltip, ChartTooltipContent, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@fadymondy/nasaq/web"
import { useTranslations } from "@/lib/i18n"

/** True below the sm breakpoint: narrow charts give the labels more room. */
function useNarrow() {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia("(max-width: 639px)")
      m.addEventListener("change", cb)
      return () => m.removeEventListener("change", cb)
    },
    () => window.matchMedia("(max-width: 639px)").matches,
    () => false,
  )
}

// ── horizontal bars: one value per named row (pipeline by stage, the funnel) ──

export type BarRow = {
  key: string
  name: string
  value: number
  label: string
  tooltip: ReactNode
}

export function HorizontalBars({
  rows,
  series,
  format,
  id,
}: {
  rows: BarRow[]
  series: string
  format: (n: number) => string
  id: string
}) {
  const { isRtl, dir } = useTranslations()
  const narrow = useNarrow()
  const config = { value: { label: series, color: "var(--chart-1)" } } satisfies ChartConfig
  const data = rows.map((r) => ({ ...r, name: isolate(r.name) }))
  // Row height 44px: room for the bar and its label, and a hover target taller than the bar.
  const height = Math.max(120, rows.length * 44 + 24)
  return (
    // dir="ltr" on the chart: SVG text anchors follow the document's direction, so in RTL a
    // start-anchored tick grew off the edge and vanished. The mirroring is done explicitly
    // instead (a reversed value axis, the category axis on the right).
    <ChartContainer
      id={id}
      config={config}
      className="aspect-auto w-full"
      style={{ height }}
      data-chart-id={id}
      dir="ltr"
    >
      <BarChart
        data={data}
        layout="vertical"
        margin={{ top: 4, bottom: 4, left: 8, right: 8 }}
        barCategoryGap={10}
        accessibilityLayer
      >
        <CartesianGrid horizontal={false} strokeDasharray="0" />
        {/* Head-room past the longest bar, so its label is never clipped. */}
        <XAxis
          type="number"
          hide
          reversed={isRtl}
          domain={[0, (max: number) => Math.max(1, max) * (narrow ? 1.75 : 1.3)]}
        />
        <YAxis
          type="category"
          dataKey="name"
          width={narrow ? 84 : 120}
          orientation={isRtl ? "right" : "left"}
          tickLine={false}
          axisLine={false}
          tick={{ fontSize: 12 }}
          interval={0}
        />
        <ChartTooltip
          cursor={false}
          content={
            <ChartTooltipContent config={config} hideLabel />
          }
        />
        <Bar
          dataKey="value"
          fill="var(--color-value)"
          radius={isRtl ? [4, 0, 0, 4] : [0, 4, 4, 0]}
          maxBarSize={22}
          isAnimationActive={false}
        >
          {/* The label sits past the bar's outer end: right of it in LTR, left of it in RTL. */}
          <LabelList
            dataKey="label"
            content={(p) => {
              const x = Number(p.x ?? 0)
              const w = Number(p.width ?? 0)
              const y = Number(p.y ?? 0) + Number(p.height ?? 0) / 2
              const outer = isRtl ? Math.min(x, x + w) - 8 : Math.max(x, x + w) + 8
              return (
                <text
                  x={outer}
                  y={y}
                  dy="0.35em"
                  textAnchor={isRtl ? "end" : "start"}
                  className="fill-nq-fg"
                  fontSize={12}
                >
                  {String(p.value ?? "")}
                </text>
              )
            }}
          />
        </Bar>
      </BarChart>
    </ChartContainer>
  )
}

// ── won and lost over time ──────────────────────────────────────────────────

export type TimePoint = { start: string; won: number; lost: number }

export function WonLostChart({
  points,
  format,
  formatTick,
  formatBucket,
  wonLabel,
  lostLabel,
}: {
  points: TimePoint[]
  format: (n: number) => string
  formatTick: (n: number) => string
  formatBucket: (start: string) => string
  wonLabel: string
  lostLabel: string
}) {
  const { isRtl, dir } = useTranslations()
  const config = {
    won: { label: wonLabel, color: "var(--chart-1)" },
    lost: { label: lostLabel, color: "var(--chart-2)" },
  } satisfies ChartConfig
  return (
    <>
      {/* The legend, in the series' own order (won, then lost); identity is never colour alone. */}
      <ul
        className="flex flex-wrap items-center justify-center gap-4 pb-2 text-xs text-muted-foreground"
        data-legend
      >
        {(["won", "lost"] as const).map((k) => (
          <li key={k} className="flex items-center gap-1.5">
            <span
              className="size-2.5 rounded-[2px]"
              style={{ background: config[k].color }}
              aria-hidden
            />
            {config[k].label}
          </li>
        ))}
      </ul>
      <ChartContainer
        id="won-lost"
        config={config}
        className="aspect-auto h-64 w-full"
        data-chart-id="won-lost"
        dir="ltr"
      >
        <BarChart
          data={points}
          margin={{ top: 8, bottom: 0, left: 4, right: 4 }}
          barGap={2}
          accessibilityLayer
        >
          <CartesianGrid vertical={false} />
          <XAxis
            dataKey="start"
            reversed={isRtl}
            tickLine={false}
            axisLine={false}
            tickMargin={8}
            minTickGap={24}
            tickFormatter={(v: string) => formatBucket(v)}
          />
          <YAxis
            orientation={isRtl ? "right" : "left"}
            tickLine={false}
            axisLine={false}
            width={56}
            allowDecimals={false}
            tickFormatter={(v: number) => formatTick(v)}
          />
          <ChartTooltip
            cursor={{ fillOpacity: 0.4 }}
            content={
              <ChartTooltipContent
                config={config}
                labelFormatter={(l) => formatBucket(String(l ?? ""))}
              />
            }
          />
          <Bar
            dataKey="won"
            fill="var(--color-won)"
            radius={[4, 4, 0, 0]}
            maxBarSize={28}
            isAnimationActive={false}
          />
          <Bar
            dataKey="lost"
            fill="var(--color-lost)"
            radius={[4, 4, 0, 0]}
            maxBarSize={28}
            isAnimationActive={false}
          />
        </BarChart>
      </ChartContainer>
    </>
  )
}

// ── table views ───────────────────────────────────────────────────────────────

export type Column<T> = {
  key: string
  head: string
  cell: (row: T) => ReactNode
  numeric?: boolean
}

/** A chart's data as a table: the view where a reader checks a number. */
export function DataTable<T>({
  rows,
  columns,
  rowKey,
  caption,
  id,
}: {
  rows: T[]
  columns: Column<T>[]
  rowKey: (row: T) => string
  caption: string
  id: string
}) {
  return (
    <Table data-table={id} aria-label={caption}>
      <TableHeader>
        <TableRow>
          {columns.map((c) => (
            <TableHead key={c.key} className={c.numeric ? "text-end" : undefined}>
              {c.head}
            </TableHead>
          ))}
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((r) => (
          <TableRow key={rowKey(r)}>
            {columns.map((c) => (
              <TableCell
                key={c.key}
                className={c.numeric ? "text-end tabular-nums" : "max-w-64 truncate"}
              >
                {c.cell(r)}
              </TableCell>
            ))}
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export { Bdi }
