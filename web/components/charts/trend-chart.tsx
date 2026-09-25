"use client"

/*
  A time-series line chart for the Google data pages (MH-444/445/442), in plain SVG.

  Follows the dataviz method: one axis only; 2px lines; the series colours are --chart-1 / --chart-2
  (validated per theme in moharrik-brand.css); recessive gridlines; a crosshair + tooltip on hover
  and on arrow keys; one series needs no legend (the title names it), two get a legend with the
  second drawn dashed as well as in its own colour, so identity is never colour alone; and every
  chart has a table view. In RTL the time axis runs right to left, like the rest of the page.

  Values are drawn exactly as given: a null is a gap, never a zero.
*/

import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type PointerEvent,
  type ReactNode,
} from "react"

import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type TrendSeries = {
  key: string
  label: string
  values: (number | null)[]
  /** Colour slot: 1 = the primary series, 2 = the comparison. */
  slot: 1 | 2
  dashed?: boolean
  /** Dates for this series' tooltip when they differ from the axis (a previous period). */
  dates?: string[]
}

const PAD = { top: 14, bottom: 30, start: 52, end: 14 }

function niceStep(raw: number) {
  if (raw <= 0) return 1
  const p = Math.pow(10, Math.floor(Math.log10(raw)))
  const f = raw / p
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10) * p
}

export function TrendChart({
  dates,
  series,
  format,
  formatDate,
  title,
  height = 240,
  invert = false,
  className,
}: {
  dates: string[]
  series: TrendSeries[]
  format: (n: number) => string
  formatDate: (ymd: string) => string
  /** Names the chart for screen readers and the table view. */
  title: string
  height?: number
  /** Smaller is better (a search position): the axis runs top-down. */
  invert?: boolean
  className?: string
}) {
  const { t, isRtl } = useTranslations()
  const wrap = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(640)
  const [hover, setHover] = useState<number | null>(null)
  const id = useId()

  useEffect(() => {
    const el = wrap.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(240, Math.round(e.contentRect.width))))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const n = dates.length
  const { ticks, lo, hi } = useMemo(() => {
    const all = series
      .flatMap((s) => s.values)
      .filter((v): v is number => v != null && Number.isFinite(v))
    const max = all.length ? Math.max(...all) : 0
    const min = all.length ? Math.min(...all) : 0
    if (invert) {
      // Positions: from the best (top) to the worst (bottom), never below 1.
      const step = niceStep(Math.max(1, (max - Math.max(1, min)) / 4))
      const top = Math.max(1, Math.floor(Math.max(1, min) / step) * step)
      const bottom = Math.max(top + step, Math.ceil(max / step) * step)
      const tk: number[] = []
      for (let v = top; v <= bottom + 1e-9; v += step) tk.push(v)
      return { ticks: tk, lo: top, hi: bottom }
    }
    const step = niceStep(max / 4)
    const top = Math.max(step, Math.ceil(max / step) * step)
    const tk: number[] = []
    for (let v = 0; v <= top + 1e-9; v += step) tk.push(v)
    return { ticks: tk, lo: 0, hi: top }
  }, [series, invert])

  const plotW = width - PAD.start - PAD.end
  const plotH = height - PAD.top - PAD.bottom
  const xAt = (i: number) => {
    const frac = n <= 1 ? 0.5 : i / (n - 1)
    return isRtl ? width - PAD.start - frac * plotW : PAD.start + frac * plotW
  }
  const yAt = (v: number) => {
    const frac = hi === lo ? 0 : (v - lo) / (hi - lo)
    return invert ? PAD.top + frac * plotH : PAD.top + plotH - frac * plotH
  }

  const paths = series.map((s) => {
    let d = ""
    let area = ""
    let run: [number, number][] = []
    const flush = () => {
      if (run.length === 0) return
      d += run.map(([x, y], i) => `${i ? "L" : "M"}${x.toFixed(1)},${y.toFixed(1)}`).join("")
      if (!invert && s.slot === 1 && series.length === 1) {
        const base = yAt(lo)
        area +=
          `M${run[0][0].toFixed(1)},${base}` +
          run.map(([x, y]) => `L${x.toFixed(1)},${y.toFixed(1)}`).join("") +
          `L${run[run.length - 1][0].toFixed(1)},${base}Z`
      }
      run = []
    }
    s.values.forEach((v, i) => {
      if (v == null || !Number.isFinite(v)) flush()
      else run.push([xAt(i), yAt(v)])
    })
    flush()
    return { s, d, area }
  })

  // ~6 date labels, always including both ends.
  const labelIdx = useMemo(() => {
    if (n === 0) return []
    const want = Math.max(2, Math.min(6, Math.floor(width / 110)))
    const out = new Set<number>([0, n - 1])
    for (let k = 1; k < want - 1; k++) out.add(Math.round((k * (n - 1)) / (want - 1)))
    return [...out].sort((a, b) => a - b)
  }, [n, width])

  function pick(e: PointerEvent<SVGSVGElement>) {
    if (n === 0) return
    const rect = e.currentTarget.getBoundingClientRect()
    const x = ((e.clientX - rect.left) / rect.width) * width
    const frac = (isRtl ? width - PAD.start - x : x - PAD.start) / plotW
    setHover(Math.max(0, Math.min(n - 1, Math.round(frac * (n - 1)))))
  }

  function onKey(e: KeyboardEvent<SVGSVGElement>) {
    if (n === 0) return
    const fwd = isRtl ? "ArrowLeft" : "ArrowRight"
    const back = isRtl ? "ArrowRight" : "ArrowLeft"
    if (e.key === fwd) setHover((h) => Math.min(n - 1, (h ?? -1) + 1))
    else if (e.key === back) setHover((h) => Math.max(0, (h ?? n) - 1))
    else if (e.key === "Home") setHover(0)
    else if (e.key === "End") setHover(n - 1)
    else if (e.key === "Escape") setHover(null)
    else return
    e.preventDefault()
  }

  const tipLeft = hover == null ? 0 : xAt(hover)
  const flip = hover != null && tipLeft > width * 0.6

  return (
    <div className={cn("space-y-2", className)}>
      {series.length > 1 ? (
        <ul className="flex flex-wrap gap-x-5 gap-y-1 text-xs text-grid-muted" aria-hidden>
          {series.map((s) => (
            <li key={s.key} className="flex items-center gap-2">
              <svg width="22" height="8" className="shrink-0">
                <line
                  x1="1"
                  x2="21"
                  y1="4"
                  y2="4"
                  stroke={`var(--chart-${s.slot})`}
                  strokeWidth="2"
                  strokeDasharray={s.dashed ? "4 3" : undefined}
                  strokeLinecap="round"
                />
              </svg>
              <span className="text-grid-body">{s.label}</span>
            </li>
          ))}
        </ul>
      ) : null}
      <div ref={wrap} className="relative w-full select-none" data-chart={title}>
        <svg
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          role="img"
          aria-label={title}
          aria-describedby={`${id}-table`}
          tabIndex={0}
          // Geometry is mirrored by hand for RTL; SVG text must not also flip its anchors.
          direction="ltr"
          className="block max-w-full touch-pan-y outline-none focus-visible:ring-2 focus-visible:ring-grid-action/50"
          onPointerMove={pick}
          onPointerDown={pick}
          onPointerLeave={() => setHover(null)}
          onKeyDown={onKey}
          onBlur={() => setHover(null)}
        >
          {ticks.map((v) => (
            <g key={v}>
              <line
                x1={PAD.start}
                x2={width - PAD.end}
                y1={yAt(v)}
                y2={yAt(v)}
                stroke="var(--grid-line)"
                strokeWidth="1"
                strokeDasharray={v === lo && !invert ? undefined : "2 4"}
                transform={isRtl ? `translate(${PAD.end - PAD.start} 0)` : undefined}
              />
              <text
                x={isRtl ? width - PAD.start + 8 : PAD.start - 8}
                y={yAt(v)}
                dy="0.32em"
                textAnchor={isRtl ? "start" : "end"}
                className="fill-grid-muted text-[11px] tabular-nums"
              >
                {format(v)}
              </text>
            </g>
          ))}
          {labelIdx.map((i) => (
            <text
              key={i}
              x={xAt(i)}
              y={height - 8}
              textAnchor={
                i === 0
                  ? isRtl
                    ? "end"
                    : "start"
                  : i === n - 1
                    ? isRtl
                      ? "start"
                      : "end"
                    : "middle"
              }
              className="fill-grid-muted text-[11px]"
            >
              {formatDate(dates[i])}
            </text>
          ))}
          {paths.map(({ s, area }) =>
            area ? (
              <path key={s.key + "-a"} d={area} fill={`var(--chart-${s.slot})`} opacity="0.1" />
            ) : null,
          )}
          {paths.map(({ s, d }) => (
            <path
              key={s.key}
              d={d}
              fill="none"
              stroke={`var(--chart-${s.slot})`}
              strokeWidth="2"
              strokeLinejoin="round"
              strokeLinecap="round"
              strokeDasharray={s.dashed ? "5 4" : undefined}
            />
          ))}
          {hover != null ? (
            <g>
              <line
                x1={xAt(hover)}
                x2={xAt(hover)}
                y1={PAD.top}
                y2={PAD.top + plotH}
                stroke="var(--grid-muted)"
                strokeWidth="1"
                opacity="0.6"
              />
              {series.map((s) => {
                const v = s.values[hover]
                if (v == null) return null
                return (
                  <circle
                    key={s.key}
                    cx={xAt(hover)}
                    cy={yAt(v)}
                    r="4.5"
                    fill={`var(--chart-${s.slot})`}
                    stroke="var(--grid-bg)"
                    strokeWidth="2"
                  />
                )
              })}
            </g>
          ) : null}
        </svg>
        {hover != null ? (
          <div
            role="status"
            className="pointer-events-none absolute top-1 z-10 min-w-36 border border-line bg-grid-card px-3 py-2 text-xs shadow-sm"
            style={
              flip
                ? { left: Math.max(0, tipLeft - 12), transform: "translateX(-100%)" }
                : { left: Math.min(width - 12, tipLeft + 12) }
            }
            data-chart-tip
          >
            {series.map((s) => {
              const v = s.values[hover]
              const date = (s.dates ?? dates)[hover]
              return (
                <div key={s.key} className="flex items-center gap-2 py-0.5">
                  <svg width="14" height="6" className="shrink-0" aria-hidden>
                    <line
                      x1="1"
                      x2="13"
                      y1="3"
                      y2="3"
                      stroke={`var(--chart-${s.slot})`}
                      strokeWidth="2"
                      strokeDasharray={s.dashed ? "3 2" : undefined}
                    />
                  </svg>
                  <span className="text-grid-muted">{date ? formatDate(date) : ""}</span>
                  <span className="ms-auto ps-3 font-medium text-grid-fg tabular-nums">
                    {v == null ? "—" : format(v)}
                  </span>
                </div>
              )
            })}
          </div>
        ) : null}
      </div>
      <details className="text-xs text-grid-muted" id={`${id}-table`}>
        <summary className="w-fit cursor-pointer select-none hover:text-grid-fg">
          {t("charts.table")}
        </summary>
        <div className="mt-2 max-h-72 overflow-auto border border-line">
          <table className="w-full text-start text-xs">
            <caption className="sr-only">{title}</caption>
            <thead className="sticky top-0 bg-grid-card">
              <tr>
                <th className="px-3 py-1.5 text-start font-medium">{t("charts.date")}</th>
                {series.map((s) => (
                  <th key={s.key} className="px-3 py-1.5 text-end font-medium">
                    {s.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {dates.map((d, i) => (
                <tr key={d} className="border-t border-line">
                  <td className="px-3 py-1 text-grid-body">{formatDate(d)}</td>
                  {series.map((s) => (
                    <td key={s.key} className="px-3 py-1 text-end text-grid-body tabular-nums">
                      {s.values[i] == null ? "—" : format(s.values[i] as number)}
                      {s.dates && s.dates[i] ? (
                        <span className="ms-1 text-grid-muted">({formatDate(s.dates[i])})</span>
                      ) : null}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </div>
  )
}

/** A ranked list with a thin magnitude bar under each row (one hue: magnitude, not identity). */
export function BarList({
  rows,
  format,
  label,
  className,
}: {
  rows: { key: string; label: ReactNode; value: number; sub?: ReactNode }[]
  format: (n: number) => string
  label: string
  className?: string
}) {
  const max = Math.max(1, ...rows.map((r) => r.value))
  return (
    <ul className={cn("divide-y divide-line", className)} aria-label={label}>
      {rows.map((r) => (
        <li key={r.key} className="px-5 py-2.5" data-bar-row>
          <div className="flex items-baseline gap-3 text-sm">
            <span className="min-w-0 flex-1 truncate text-grid-fg">{r.label}</span>
            {r.sub ? (
              <span className="shrink-0 text-xs text-grid-muted tabular-nums">{r.sub}</span>
            ) : null}
            <span className="shrink-0 font-medium text-grid-fg tabular-nums">
              {format(r.value)}
            </span>
          </div>
          <div className="mt-1.5 h-1.5 w-full" aria-hidden>
            <div
              className="h-full rounded-e-full"
              style={{
                width: `${Math.max(1.5, (r.value / max) * 100)}%`,
                background: "var(--chart-1)",
              }}
            />
          </div>
        </li>
      ))}
    </ul>
  )
}
