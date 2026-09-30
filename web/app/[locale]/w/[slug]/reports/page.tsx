"use client"

/*
  Reports (MH-394): the CRM over a range of days — the open pipeline by stage, deals won and lost
  with the win rate and average deal, stage-to-stage conversion, wins and losses over time, where
  leads come from, and how fast the inbox answers.

  Every figure is GET /api/w/reports/crm (the same function the MCP `crm_report` tool answers
  from); nothing is computed here but formatting. A section with no data behind it says "no data
  yet" instead of drawing zeros, and a section the viewer's role cannot read is not rendered.
*/

import { Suspense, useMemo, useState, type ReactNode } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import {
  CalendarRangeIcon,
  ChartColumnIcon,
  FilterIcon,
  HandshakeIcon,
  InboxIcon,
  SignpostIcon,
} from "lucide-react"

import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { formatMoney } from "@/components/crm/shared"
import { Panel } from "@/components/overview/overview-cards"
import { SectionHeader } from "@/components/public-frame"
import {
  DataTable,
  HorizontalBars,
  WonLostChart,
  type BarRow,
} from "@/components/reports/report-charts"
import { ErrorState } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { CrmReport, ReportChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat, numberFormat } from "@/lib/i18n-locale"
import { useCrmReport } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const PRESETS = [30, 90, 365] as const

/** LRI … PDI: a money or number string that must keep its LTR order inside an RTL line. */
const ltr = (s: string) => `⁦${s}⁩`
const DAY = /^\d{4}-\d{2}-\d{2}$/

export default function ReportsPage() {
  return (
    <Suspense>
      <Reports />
    </Suspense>
  )
}

/** Formatting for this page, all through lib/i18n-locale (Western digits in both languages). */
function useFmt(currency: string) {
  const { locale } = useTranslations()
  return useMemo(() => {
    const n = numberFormat(locale)
    const pct = numberFormat(locale, { style: "percent", maximumFractionDigits: 0 })
    const compact = numberFormat(locale, { notation: "compact", maximumFractionDigits: 1 })
    const day = dateFormat(locale, { dateStyle: "medium", timeZone: "UTC" })
    const dayShort = dateFormat(locale, { day: "numeric", month: "short", timeZone: "UTC" })
    const month = dateFormat(locale, { month: "short", year: "numeric", timeZone: "UTC" })
    const unit = (u: string, digits: number) =>
      numberFormat(locale, {
        style: "unit",
        unit: u,
        unitDisplay: "short",
        maximumFractionDigits: digits,
      })
    const sec = unit("second", 0)
    const min = unit("minute", 0)
    const hr = unit("hour", 1)
    const dy = unit("day", 1)
    const ymd = (s: string) => new Date(`${s}T00:00:00Z`)
    return {
      n: (v: number) => n.format(v),
      pct: (v: number) => pct.format(v),
      money: (minor: number) => formatMoney(minor, currency, locale),
      /** Minor units as a short axis tick: 12.5K. */
      moneyTick: (minor: number) => compact.format(minor / 100),
      day: (s: string) => day.format(ymd(s)),
      bucket: (s: string, unitName: CrmReport["bucket"]) =>
        unitName === "month" ? month.format(ymd(s)) : dayShort.format(ymd(s)),
      duration: (s: number | null) => {
        if (s === null) return null
        if (s < 60) return sec.format(Math.round(s))
        if (s < 3600) return min.format(Math.round(s / 60))
        if (s < 86400) return hr.format(s / 3600)
        return dy.format(s / 86400)
      },
    }
  }, [locale, currency])
}

type Fmt = ReturnType<typeof useFmt>

function Reports() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("reports.title")} · ${ws.summary.name}`)
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()

  const from = params.get("from") ?? ""
  const to = params.get("to") ?? ""
  const custom = DAY.test(from) && DAY.test(to)
  const days = PRESETS.includes(Number(params.get("days")) as (typeof PRESETS)[number])
    ? Number(params.get("days"))
    : 90
  const query = custom ? `from=${from}&to=${to}` : `days=${days}`
  const report = useCrmReport(ws.id, query)
  const rep = report.data

  function go(q: Record<string, string>) {
    router.replace(`${pathname}?${new URLSearchParams(q)}`, { scroll: false })
  }

  const canRead = ws.can("contacts:read")

  return (
    <>
      <SectionHeader
        micro={t("reports.micro")}
        title={t("reports.title")}
        description={t("reports.hint")}
        action={
          canRead ? (
            <div className="flex w-full flex-wrap items-center gap-2 sm:w-auto" data-range-controls>
              <ToggleGroup
                variant="outline"
                size="sm"
                spacing={0}
                value={custom ? [] : [String(days)]}
                onValueChange={(v) => v[0] && go({ days: v[0] })}
                aria-label={t("reports.range")}
              >
                {PRESETS.map((d) => (
                  <ToggleGroupItem key={d} value={String(d)} data-preset={d}>
                    {d === 365 ? t("reports.last12Months") : t("reports.lastDays", { n: d })}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
              <CustomRange
                key={`${query}:${rep?.from ?? ""}`}
                from={rep?.from ?? from}
                to={rep?.to ?? to}
                active={custom}
                onApply={(f, tt) => go({ from: f, to: tt })}
              />
            </div>
          ) : null
        }
      />

      {!canRead ? (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("reports.noAccess")}</p>
      ) : report.error ? (
        <ErrorState error={report.error} />
      ) : !rep ? (
        <PageSkeleton />
      ) : (
        <Filled rep={rep} busy={report.isValidating} />
      )}
    </>
  )
}

function CustomRange({
  from,
  to,
  active,
  onApply,
}: {
  from: string
  to: string
  active: boolean
  onApply: (from: string, to: string) => void
}) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)
  const [f, setF] = useState(from)
  const [tt, setT] = useState(to)
  const valid = DAY.test(f) && DAY.test(tt) && f <= tt
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            variant={active ? "secondary" : "outline"}
            size="sm"
            data-custom-range
            aria-pressed={active}
          />
        }
      >
        <CalendarRangeIcon />
        {t("reports.custom")}
      </PopoverTrigger>
      <PopoverContent align="end" className="w-72">
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (!valid) return
            onApply(f, tt)
            setOpen(false)
          }}
        >
          <Field>
            <FieldLabel htmlFor="report-from">{t("reports.from")}</FieldLabel>
            <Input
              id="report-from"
              type="date"
              value={f}
              max={tt || undefined}
              onChange={(e) => setF(e.target.value)}
              dir="ltr"
              required
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="report-to">{t("reports.to")}</FieldLabel>
            <Input
              id="report-to"
              type="date"
              value={tt}
              min={f || undefined}
              onChange={(e) => setT(e.target.value)}
              dir="ltr"
              required
            />
          </Field>
          <p className="text-xs text-muted-foreground">{t("reports.customHint")}</p>
          <Button type="submit" size="sm" disabled={!valid}>
            {t("reports.apply")}
          </Button>
        </form>
      </PopoverContent>
    </Popover>
  )
}

/** A panel's two views: the chart, and the same numbers as a table. */
function ViewToggle({
  value,
  onChange,
  id,
}: {
  value: "chart" | "table"
  onChange: (v: "chart" | "table") => void
  id: string
}) {
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

/** A stat tile; the figure steps down a size on a phone so a long amount never overflows. */
function Stat({
  label,
  value,
  sub,
  id,
}: {
  label: string
  value: string
  sub?: string
  id: string
}) {
  return (
    <div className="min-w-0 px-4 py-4 sm:px-5" data-stat={id}>
      <p className="eyebrow truncate">{label}</p>
      <p className="mt-1.5 text-xl font-medium break-words text-foreground tabular-nums sm:text-2xl">
        {value}
      </p>
      {sub ? <p className="mt-0.5 truncate text-xs text-muted-foreground">{sub}</p> : null}
    </div>
  )
}

function NoData({ children, id }: { children: ReactNode; id: string }) {
  return (
    <p className="px-5 pt-2 pb-6 text-sm text-muted-foreground" data-no-data={id}>
      {children}
    </p>
  )
}

function Filled({ rep, busy }: { rep: CrmReport; busy: boolean }) {
  const { t } = useTranslations()
  const f = useFmt(rep.currency)
  const d = rep.deals
  const closed = d.won + d.lost

  return (
    <div
      className="space-y-4 px-4 pb-10 sm:space-y-6 sm:px-6"
      data-report
      data-from={rep.from}
      data-to={rep.to}
      aria-busy={busy}
    >
      <p className="text-sm text-muted-foreground" data-range>
        <Ltr>{`${f.day(rep.from)} – ${f.day(rep.to)}`}</Ltr> ·{" "}
        {t("reports.rangeNote", { n: f.n(rep.days), tz: rep.timezone, currency: rep.currency })}
      </p>

      <div className="cells grid-cols-2 bg-card lg:grid-cols-5" data-report-tiles>
        <Stat
          id="open-pipeline"
          label={t("reports.openPipeline")}
          value={ltr(f.money(rep.pipeline.open_value_minor))}
          sub={t("reports.openDeals", { n: f.n(rep.pipeline.open_deals) })}
        />
        <Stat
          id="won"
          label={t("reports.won")}
          value={ltr(f.money(d.won_value_minor))}
          sub={t("reports.dealsN", { n: f.n(d.won) })}
        />
        <Stat
          id="win-rate"
          label={t("reports.winRate")}
          value={d.win_rate === null ? t("reports.none") : f.pct(d.win_rate)}
          sub={
            closed
              ? t("reports.wonOfClosed", { won: f.n(d.won), closed: f.n(closed) })
              : t("reports.nothingClosed")
          }
        />
        <Stat
          id="avg-deal"
          label={t("reports.avgDeal")}
          value={
            d.avg_won_value_minor === null ? t("reports.none") : ltr(f.money(d.avg_won_value_minor))
          }
          sub={t("reports.avgDealNote")}
        />
        <Stat
          id="opened"
          label={t("reports.opened")}
          value={f.n(d.opened)}
          sub={t("reports.lostN", { n: f.n(d.lost), value: ltr(f.money(d.lost_value_minor)) })}
        />
      </div>
      {d.other_currency_won + d.other_currency_lost > 0 ? (
        <p className="-mt-2 text-xs text-muted-foreground sm:-mt-4" data-other-currency-closed>
          {t("reports.otherCurrencyClosed", {
            n: f.n(d.other_currency_won + d.other_currency_lost),
            currency: rep.currency,
          })}
        </p>
      ) : null}

      <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2">
        <PipelinePanel rep={rep} f={f} />
        <ConversionPanel rep={rep} f={f} />
      </div>
      <div className="grid grid-cols-1">
        <TimelinePanel rep={rep} f={f} />
      </div>
      {rep.lead_sources || rep.inbox ? (
        <div
          className={`grid grid-cols-1 gap-4 sm:gap-6 ${rep.lead_sources && rep.inbox ? "lg:grid-cols-2" : ""}`}
        >
          {rep.lead_sources ? <LeadSourcesPanel rep={rep} f={f} /> : null}
          {rep.inbox ? <InboxPanel inbox={rep.inbox} f={f} /> : null}
        </div>
      ) : null}
      <p className="max-w-3xl text-xs text-muted-foreground">{t("reports.method")}</p>
    </div>
  )
}

function PipelinePanel({ rep, f }: { rep: CrmReport; f: Fmt }) {
  const { t } = useTranslations()
  const [view, setView] = useState<"chart" | "table">("chart")
  const stages = rep.pipeline.stages ?? []
  const rows: BarRow[] = stages.map((s) => ({
    key: s.id,
    name: s.name,
    value: s.value_minor,
    label: f.money(s.value_minor),
    tooltip: (
      <span className="flex flex-col gap-0.5">
        <Bdi className="font-medium text-foreground">{s.name}</Bdi>
        <span className="text-muted-foreground">
          <Ltr>{f.money(s.value_minor)}</Ltr> · {t("reports.dealsN", { n: f.n(s.deal_count) })}
        </span>
      </span>
    ),
  }))
  return (
    <Panel
      id="report-pipeline"
      title={t("reports.pipelineByStage")}
      icon={<HandshakeIcon />}
      meta={
        rep.pipeline.open_deals ? (
          <ViewToggle id="pipeline" value={view} onChange={setView} />
        ) : null
      }
    >
      <p className="px-5 pt-3 pb-2 text-xs text-muted-foreground">{t("reports.pipelineNow")}</p>
      {rep.pipeline.open_deals === 0 ? (
        <NoData id="pipeline">{t("reports.noOpenDeals")}</NoData>
      ) : view === "chart" ? (
        <div className="px-3 pb-4">
          <HorizontalBars
            id="pipeline"
            rows={rows}
            series={t("reports.openValue")}
            format={f.money}
          />
        </div>
      ) : (
        <DataTable
          id="pipeline"
          caption={t("reports.pipelineByStage")}
          rows={stages}
          rowKey={(s) => s.id}
          columns={[
            { key: "stage", head: t("reports.stage"), cell: (s) => <Bdi>{s.name}</Bdi> },
            {
              key: "deals",
              head: t("reports.deals"),
              cell: (s) => f.n(s.deal_count),
              numeric: true,
            },
            {
              key: "value",
              head: t("reports.value"),
              cell: (s) => <Ltr>{f.money(s.value_minor)}</Ltr>,
              numeric: true,
            },
          ]}
        />
      )}
      {rep.pipeline.other_currency_deals > 0 ? (
        <p
          className="border-t border-nq-line px-5 py-2.5 text-xs text-muted-foreground"
          data-other-currency-open
        >
          {t("reports.otherCurrencyOpen", {
            n: f.n(rep.pipeline.other_currency_deals),
            currency: rep.currency,
          })}
        </p>
      ) : null}
    </Panel>
  )
}

function ConversionPanel({ rep, f }: { rep: CrmReport; f: Fmt }) {
  const { t } = useTranslations()
  const [view, setView] = useState<"chart" | "table">("chart")
  const c = rep.conversion
  const steps = c.steps ?? []
  const rows: BarRow[] = steps.map((s, i) => ({
    key: s.stage_id || `won-${i}`,
    name: s.name,
    value: s.reached,
    label:
      i === 0 || s.from_previous === null
        ? f.n(s.reached)
        : `${f.n(s.reached)} · ${f.pct(s.from_previous)}`,
    tooltip: (
      <span className="flex flex-col gap-0.5">
        <Bdi className="font-medium text-foreground">{s.name}</Bdi>
        <span className="text-muted-foreground">{t("reports.reachedN", { n: f.n(s.reached) })}</span>
        {i > 0 && s.from_previous !== null ? (
          <span className="text-muted-foreground">
            {t("reports.fromPrevious", { pct: f.pct(s.from_previous) })}
          </span>
        ) : null}
        {s.from_start !== null ? (
          <span className="text-muted-foreground">
            {t("reports.fromStart", { pct: f.pct(s.from_start) })}
          </span>
        ) : null}
      </span>
    ),
  }))
  return (
    <Panel
      id="report-conversion"
      title={t("reports.conversion")}
      icon={<FilterIcon />}
      meta={c.opened ? <ViewToggle id="conversion" value={view} onChange={setView} /> : null}
    >
      <p className="px-5 pt-3 pb-2 text-xs text-muted-foreground">
        {t("reports.conversionNote", { n: f.n(c.opened), lost: f.n(c.lost) })}
      </p>
      {c.opened === 0 ? (
        <NoData id="conversion">{t("reports.noOpened")}</NoData>
      ) : view === "chart" ? (
        <div className="px-3 pb-4">
          <HorizontalBars id="conversion" rows={rows} series={t("reports.reached")} format={f.n} />
        </div>
      ) : (
        <DataTable
          id="conversion"
          caption={t("reports.conversion")}
          rows={steps}
          rowKey={(s) => s.stage_id || s.name}
          columns={[
            { key: "stage", head: t("reports.stage"), cell: (s) => <Bdi>{s.name}</Bdi> },
            {
              key: "reached",
              head: t("reports.reached"),
              cell: (s) => f.n(s.reached),
              numeric: true,
            },
            {
              key: "prev",
              head: t("reports.fromPreviousHead"),
              cell: (s) => (s.from_previous === null ? "—" : f.pct(s.from_previous)),
              numeric: true,
            },
            {
              key: "start",
              head: t("reports.fromStartHead"),
              cell: (s) => (s.from_start === null ? "—" : f.pct(s.from_start)),
              numeric: true,
            },
          ]}
        />
      )}
    </Panel>
  )
}

function TimelinePanel({ rep, f }: { rep: CrmReport; f: Fmt }) {
  const { t } = useTranslations()
  const [view, setView] = useState<"chart" | "table">("chart")
  const [metric, setMetric] = useState<"count" | "value">("count")
  const points = rep.timeline ?? []
  const any = points.some((p) => p.won + p.lost > 0)
  const data = points.map((p) =>
    metric === "count"
      ? { start: p.start, won: p.won, lost: p.lost }
      : { start: p.start, won: p.won_value_minor, lost: p.lost_value_minor },
  )
  const fmt = metric === "count" ? f.n : f.money
  const tick = metric === "count" ? f.n : f.moneyTick
  const bucketLabel = (s: string) =>
    rep.bucket === "week"
      ? t("reports.weekOf", { d: f.bucket(s, "week") })
      : f.bucket(s, rep.bucket)
  return (
    <Panel
      id="report-timeline"
      title={t("reports.timeline")}
      icon={<ChartColumnIcon />}
      meta={
        any ? (
          <div className="ms-auto flex flex-wrap items-center gap-2">
            <ToggleGroup
              variant="outline"
              size="sm"
              spacing={0}
              value={[metric]}
              onValueChange={(v) => v[0] && setMetric(v[0] as "count" | "value")}
              aria-label={t("reports.metric")}
              data-metric-toggle
            >
              <ToggleGroupItem value="count">{t("reports.count")}</ToggleGroupItem>
              <ToggleGroupItem value="value">{t("reports.value")}</ToggleGroupItem>
            </ToggleGroup>
            <ViewToggle id="timeline" value={view} onChange={setView} />
          </div>
        ) : null
      }
    >
      <p className="px-5 pt-3 pb-2 text-xs text-muted-foreground">{t(`reports.bucket.${rep.bucket}`)}</p>
      {!any ? (
        <NoData id="timeline">{t("reports.noClosed")}</NoData>
      ) : view === "chart" ? (
        <div className="px-3 pb-4">
          <WonLostChart
            points={data}
            format={fmt}
            formatTick={tick}
            formatBucket={bucketLabel}
            wonLabel={t("reports.wonSeries")}
            lostLabel={t("reports.lostSeries")}
          />
        </div>
      ) : (
        <DataTable
          id="timeline"
          caption={t("reports.timeline")}
          rows={points.filter((p) => p.won + p.lost > 0)}
          rowKey={(p) => p.start}
          columns={[
            {
              key: "bucket",
              head: t(`reports.bucketHead.${rep.bucket}`),
              cell: (p) => <Ltr>{bucketLabel(p.start)}</Ltr>,
            },
            { key: "won", head: t("reports.wonSeries"), cell: (p) => f.n(p.won), numeric: true },
            {
              key: "wonv",
              head: t("reports.wonValue"),
              cell: (p) => <Ltr>{f.money(p.won_value_minor)}</Ltr>,
              numeric: true,
            },
            { key: "lost", head: t("reports.lostSeries"), cell: (p) => f.n(p.lost), numeric: true },
            {
              key: "lostv",
              head: t("reports.lostValue"),
              cell: (p) => <Ltr>{f.money(p.lost_value_minor)}</Ltr>,
              numeric: true,
            },
          ]}
        />
      )}
    </Panel>
  )
}

function LeadSourcesPanel({ rep, f }: { rep: CrmReport; f: Fmt }) {
  const { t } = useTranslations()
  const ls = rep.lead_sources!
  const sources = ls.sources ?? []
  const max = Math.max(1, ...sources.map((s) => s.leads))
  return (
    <Panel id="report-sources" title={t("reports.leadSources")} icon={<SignpostIcon />}>
      <p className="px-5 pt-3 pb-2 text-xs text-muted-foreground">
        {t("reports.leadSourcesNote", {
          n: f.n(ls.leads),
          converted: f.n(ls.converted),
          spam: f.n(ls.spam),
        })}
      </p>
      {ls.leads === 0 ? (
        <NoData id="sources">{t("reports.noLeads")}</NoData>
      ) : (
        <DataTable
          id="sources"
          caption={t("reports.leadSources")}
          rows={sources}
          rowKey={(s) => s.source}
          columns={[
            {
              key: "source",
              head: t("reports.source"),
              cell: (s) => (
                <span className="flex min-w-32 flex-col gap-1">
                  <Ltr>{s.source === "direct" ? t("reports.direct") : s.source}</Ltr>
                  <span className="h-1.5 w-full" aria-hidden>
                    <span
                      className="block h-full rounded-e-full"
                      style={{
                        width: `${Math.max(2, (s.leads / max) * 100)}%`,
                        background: "var(--chart-1)",
                      }}
                    />
                  </span>
                </span>
              ),
            },
            { key: "leads", head: t("reports.leads"), cell: (s) => f.n(s.leads), numeric: true },
            {
              key: "converted",
              head: t("reports.converted"),
              cell: (s) => f.n(s.converted),
              numeric: true,
            },
            {
              key: "won",
              head: t("reports.wonDeals"),
              cell: (s) => f.n(s.won_deals),
              numeric: true,
            },
            {
              key: "value",
              head: t("reports.wonValue"),
              cell: (s) => <Ltr>{f.money(s.won_value_minor)}</Ltr>,
              numeric: true,
            },
          ]}
        />
      )}
    </Panel>
  )
}

function InboxPanel({ inbox, f }: { inbox: NonNullable<CrmReport["inbox"]>; f: Fmt }) {
  const { t } = useTranslations()
  const total = inbox.total
  const rows: ReportChannel[] = [...(inbox.channels ?? []), total]
  return (
    <Panel id="report-inbox" title={t("reports.inbox")} icon={<InboxIcon />}>
      <p className="px-5 pt-3 pb-2 text-xs text-muted-foreground">{t("reports.inboxNote")}</p>
      {total.conversations === 0 ? (
        <NoData id="inbox">{t("reports.noConversations")}</NoData>
      ) : (
        <>
          <div className="grid grid-cols-2 border-y border-nq-line [&>*:first-child]:border-e [&>*:first-child]:border-nq-line">
            <Stat
              id="first-response"
              label={t("reports.firstResponse")}
              value={f.duration(total.median_seconds) ?? t("reports.none")}
              sub={t("reports.answeredOf", {
                n: f.n(total.answered),
                total: f.n(total.conversations),
              })}
            />
            <Stat
              id="awaiting"
              label={t("reports.awaiting")}
              value={f.n(total.awaiting)}
              sub={t("reports.awaitingNote")}
            />
          </div>
          <DataTable
            id="inbox"
            caption={t("reports.inbox")}
            rows={rows}
            rowKey={(c) => c.channel}
            columns={[
              {
                key: "channel",
                head: t("reports.channel"),
                cell: (c) =>
                  c.channel === "all" ? t("reports.allChannels") : t(`inbox.channel.${c.channel}`),
              },
              {
                key: "conv",
                head: t("reports.conversations"),
                cell: (c) => f.n(c.conversations),
                numeric: true,
              },
              {
                key: "median",
                head: t("reports.median"),
                cell: (c) => f.duration(c.median_seconds) ?? "—",
                numeric: true,
              },
              {
                key: "mean",
                head: t("reports.mean"),
                cell: (c) => f.duration(c.mean_seconds) ?? "—",
                numeric: true,
              },
              {
                key: "awaiting",
                head: t("reports.awaiting"),
                cell: (c) => f.n(c.awaiting),
                numeric: true,
              },
            ]}
          />
        </>
      )}
    </Panel>
  )
}

function PageSkeleton() {
  return (
    <div className="space-y-6 px-4 pb-10 sm:px-6" aria-busy>
      <div className="cells grid-cols-2 lg:grid-cols-5">
        {Array.from({ length: 5 }, (_, i) => (
          <div key={i} className="space-y-2 px-5 py-4">
            <Skeleton className="h-3 w-20" />
            <Skeleton className="h-7 w-24" />
          </div>
        ))}
      </div>
      <div className="grid gap-6 lg:grid-cols-2">
        <Skeleton className="h-56 w-full" />
        <Skeleton className="h-56 w-full" />
      </div>
    </div>
  )
}
