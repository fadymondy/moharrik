"use client"

/*
The Insights page's business questions (MH-1069). Each card answers one question
from the same report the Reports page reads (GET /api/w/reports/crm) or from the
same signal Opportunities and Home use for "at risk" (GET
/api/w/opportunities/signals), so a number here is a number there. Every card
states the window and what the figure is out of, and says plainly when there is
nothing to show. Nothing is estimated or concluded by a model: the one derived
line, the biggest drop between stages, is a division of two counts in the response.
*/

import Link from "next/link"
import { useMemo, useState, type ReactNode } from "react"
import { FilterIcon, TimerIcon, TriangleAlertIcon, UserPlusIcon } from "lucide-react"
import { Toggle, ToggleGroup } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { Empty, Panel } from "@/components/overview/overview-cards"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { formatMoney } from "@/components/crm/shared"
import type { CrmReport } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { numberFormat } from "@/lib/i18n-locale"
import { useCrmReport, useOpportunitySignals } from "@/lib/queries"

const RANGES = [30, 90, 365] as const

/** A duration in the largest sensible unit, in the viewer's locale. */
function useDuration() {
  const { locale } = useTranslations()
  return useMemo(() => {
    const unit = (u: string, d: number) =>
      numberFormat(locale, { style: "unit", unit: u, unitDisplay: "short", maximumFractionDigits: d })
    const sec = unit("second", 0)
    const min = unit("minute", 0)
    const hr = unit("hour", 1)
    const dy = unit("day", 1)
    return (s: number) => {
      if (s < 60) return sec.format(Math.round(s))
      if (s < 3600) return min.format(Math.round(s / 60))
      if (s < 86400) return hr.format(s / 3600)
      return dy.format(s / 86400)
    }
  }, [locale])
}

export function InsightQuestions({ base }: { base: string }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [days, setDays] = useState<number>(90)
  const can = ws.can("contacts:read")
  const report = useCrmReport(can ? ws.id : null, `days=${days}`)
  const signals = useOpportunitySignals(ws.id, can)
  const n = useMemo(() => numberFormat(locale), [locale])
  const pct = useMemo(() => numberFormat(locale, { style: "percent", maximumFractionDigits: 0 }), [locale])
  const dur = useDuration()
  if (!can) return null
  const rep = report.data

  return (
    <section aria-labelledby="insq-title" data-insight-questions className="space-y-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <h2 id="insq-title" className="eyebrow">
            {t("insq.title")}
          </h2>
          <p className="text-sm text-muted-foreground" data-insq-window>
            {rep
              ? t("insq.window", { n: n.format(rep.days), from: rep.from, to: rep.to, tz: rep.timezone })
              : t("insq.windowLoading")}
          </p>
        </div>
        <ToggleGroup
          value={[String(days)]}
          onValueChange={(v) => v[0] && setDays(Number(v[0]))}
          aria-label={t("reports.range")}
        >
          {RANGES.map((d) => (
            <Toggle key={d} value={String(d)} data-insq-range={d}>
              {d === 365 ? t("reports.last12Months") : t("reports.lastDays", { n: d })}
            </Toggle>
          ))}
        </ToggleGroup>
      </div>

      {report.error ? (
        <ErrorState error={report.error} />
      ) : !rep ? (
        <LoadingRows rows={3} />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2">
          <LeadSourcesCard rep={rep} base={base} n={n} pct={pct} />
          <FirstResponseCard rep={rep} base={base} n={n} dur={dur} />
          <DropOffCard rep={rep} base={base} n={n} pct={pct} />
          <AtRiskCard base={base} signals={signals} currencyFallback={rep.currency} locale={locale} />
        </div>
      )}
    </section>
  )
}

type NF = { format: (n: number) => string }

function Basis({ children }: { children: ReactNode }) {
  return (
    <p className="px-5 pb-2 text-xs text-muted-foreground" data-insq-basis>
      {children}
    </p>
  )
}

function LeadSourcesCard({ rep, base, n, pct }: { rep: CrmReport; base: string; n: NF; pct: NF }) {
  const { t } = useTranslations()
  const ls = rep.lead_sources
  // Null means the viewer cannot read leads: the card is left out, not zeroed.
  if (!ls) return null
  const sources = ls.sources ?? []
  const top = sources.slice(0, 6)
  const rest = sources.slice(6)
  const max = Math.max(1, ...top.map((s) => s.leads))
  return (
    <Panel id="insq-leads" title={t("insq.leads.q")} icon={<UserPlusIcon />} href={`${base}/reports`}>
      <Basis>
        {t("insq.leads.basis", {
          n: n.format(ls.leads),
          converted: n.format(ls.converted),
          spam: n.format(ls.spam),
        })}
      </Basis>
      {sources.length === 0 ? (
        <Empty title={t("insq.leads.empty")} body={t("insq.leads.emptyBody")} />
      ) : (
        <ul className="divide-y divide-nq-line border-t border-nq-line" data-insq-sources>
          {top.map((s) => (
            <li key={s.source} className="space-y-1.5 px-5 py-2.5" data-insq-source={s.source} data-leads={s.leads} data-converted={s.converted}>
              <div className="flex items-baseline justify-between gap-3 text-sm">
                <Bdi className="min-w-0 truncate font-medium">{s.source}</Bdi>
                <span className="shrink-0 tabular-nums text-muted-foreground">
                  {t("insq.leads.row", {
                    leads: n.format(s.leads),
                    share: pct.format(ls.leads ? s.leads / ls.leads : 0),
                  })}
                </span>
              </div>
              <div className="h-1.5 bg-nq-surface-soft" aria-hidden>
                <div className="h-full bg-nq-info" style={{ width: `${(s.leads / max) * 100}%` }} />
              </div>
              <p className="text-xs text-muted-foreground">
                {t("insq.leads.sub", { converted: n.format(s.converted), won: n.format(s.won_deals) })}
              </p>
            </li>
          ))}
          {rest.length ? (
            <li className="px-5 py-2.5 text-xs text-muted-foreground">
              {t("insq.leads.more", {
                n: n.format(rest.length),
                leads: n.format(rest.reduce((a, s) => a + s.leads, 0)),
              })}
            </li>
          ) : null}
        </ul>
      )}
    </Panel>
  )
}

function FirstResponseCard({
  rep,
  base,
  n,
  dur,
}: {
  rep: CrmReport
  base: string
  n: NF
  dur: (s: number) => string
}) {
  const { t } = useTranslations()
  const inbox = rep.inbox
  if (!inbox) return null
  const tot = inbox.total
  return (
    <Panel id="insq-reply" title={t("insq.reply.q")} icon={<TimerIcon />} href={`${base}/inbox/stats`}>
      <Basis>{t("insq.reply.basis", { n: n.format(tot.conversations) })}</Basis>
      {tot.conversations === 0 ? (
        <Empty title={t("insq.reply.empty")} body={t("insq.reply.emptyBody")} />
      ) : tot.median_seconds === null ? (
        <Empty
          title={t("insq.reply.noneAnswered")}
          body={t("insq.reply.awaiting", { n: n.format(tot.awaiting) })}
        />
      ) : (
        <div className="grid grid-cols-2 border-t border-nq-line [&>*:first-child]:border-e [&>*:first-child]:border-nq-line">
          <div className="space-y-1 px-5 py-4" data-insq-median data-value={tot.median_seconds}>
            <p className="text-xs text-muted-foreground">{t("insq.reply.median")}</p>
            <p className="text-2xl font-medium tabular-nums">{dur(tot.median_seconds)}</p>
            <p className="text-xs text-muted-foreground">
              {t("insq.reply.answered", { n: n.format(tot.answered), total: n.format(tot.conversations) })}
            </p>
          </div>
          <div className="space-y-1 px-5 py-4" data-insq-awaiting data-value={tot.awaiting}>
            <p className="text-xs text-muted-foreground">{t("insq.reply.waiting")}</p>
            <p
              className={`text-2xl font-medium tabular-nums ${tot.awaiting > 0 ? "text-nq-warning-text" : "text-nq-success-text"}`}
            >
              {n.format(tot.awaiting)}
            </p>
            <p className="text-xs text-muted-foreground">{t("insq.reply.waitingNote")}</p>
          </div>
        </div>
      )}
    </Panel>
  )
}

type Worst = { from: string; to: string; rate: number }

function DropOffCard({ rep, base, n, pct }: { rep: CrmReport; base: string; n: NF; pct: NF }) {
  const { t } = useTranslations()
  const c = rep.conversion
  const steps = c.steps ?? []
  // The biggest drop is the lowest from_previous among steps that had someone to lose.
  let worst: Worst | null = null as Worst | null
  steps.forEach((s, i) => {
    const prev = steps[i - 1]
    if (!prev || prev.reached === 0 || s.from_previous === null) return
    if (worst === null || s.from_previous < worst.rate) {
      worst = { from: prev.name, to: s.name, rate: s.from_previous }
    }
  })
  const w: Worst | null = worst
  const max = Math.max(1, ...steps.map((s) => s.reached))
  return (
    <Panel id="insq-dropoff" title={t("insq.drop.q")} icon={<FilterIcon />} href={`${base}/reports`}>
      <Basis>{t("insq.drop.basis", { n: n.format(c.opened), lost: n.format(c.lost) })}</Basis>
      {c.opened === 0 || steps.length === 0 ? (
        <Empty title={t("insq.drop.empty")} body={t("insq.drop.emptyBody")} />
      ) : (
        <>
          <ul className="divide-y divide-nq-line border-t border-nq-line" data-insq-steps>
            {steps.map((s) => (
              <li key={s.stage_id} className="space-y-1.5 px-5 py-2.5" data-insq-step={s.name} data-reached={s.reached}>
                <div className="flex items-baseline justify-between gap-3 text-sm">
                  <Bdi className="min-w-0 truncate font-medium">{s.name}</Bdi>
                  <span className="shrink-0 tabular-nums text-muted-foreground">
                    {n.format(s.reached)}
                    {s.from_previous !== null ? ` · ${pct.format(s.from_previous)}` : ""}
                  </span>
                </div>
                <div className="h-1.5 bg-nq-surface-soft" aria-hidden>
                  <div
                    className={`h-full ${w && w.to === s.name ? "bg-nq-warning" : "bg-nq-info"}`}
                    style={{ width: `${(s.reached / max) * 100}%` }}
                  />
                </div>
              </li>
            ))}
          </ul>
          {w ? (
            <p className="border-t border-nq-line px-5 py-3 text-sm text-nq-warning-text" data-insq-worst>
              {t("insq.drop.worst", { from: w.from, to: w.to, rate: pct.format(w.rate) })}
            </p>
          ) : null}
        </>
      )}
    </Panel>
  )
}

function AtRiskCard({
  base,
  signals,
  currencyFallback,
  locale,
}: {
  base: string
  signals: ReturnType<typeof useOpportunitySignals>
  currencyFallback: string
  locale: string
}) {
  const { t } = useTranslations()
  const s = signals.data
  const nf = numberFormat(locale)
  // The kind is absent when the viewer cannot read deals: omit, never zero.
  if (s && s.counts.at_risk_deals === null) return null
  const items = (s?.items ?? []).filter((i) => i.kind === "at_risk_deal").slice(0, 5)
  const total = s?.counts.at_risk_deals ?? 0
  return (
    <Panel
      id="insq-atrisk"
      title={t("insq.risk.q")}
      icon={<TriangleAlertIcon />}
      href={`${base}/deals?view=signals`}
    >
      {signals.error ? (
        <ErrorState error={signals.error} />
      ) : !s ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          <Basis>{t("insq.risk.basis", { days: nf.format(s.thresholds.quiet_days) })}</Basis>
          {total === 0 ? (
            <Empty title={t("insq.risk.empty")} body={t("insq.risk.emptyBody")} />
          ) : (
            <>
              <p
                className="px-5 pb-2 text-2xl font-medium tabular-nums text-nq-warning-text"
                data-insq-risk-count
                data-value={total}
              >
                {t("insq.risk.count", { n: nf.format(total) })}
              </p>
              <ul className="divide-y divide-nq-line border-t border-nq-line" data-insq-risk-items>
                {items.map((i) => {
                  const quiet = i.why.find((x) => x.code === "quiet_days")
                  return (
                    <li key={i.id} className="px-5 py-2.5 text-sm" data-insq-risk={i.id}>
                      <div className="flex items-baseline justify-between gap-3">
                        <Link
                          href={`${base}/deals/${i.id}`}
                          className="min-w-0 truncate font-medium hover:underline hover:underline-offset-4"
                        >
                          <Bdi>{i.title}</Bdi>
                        </Link>
                        {i.value ? (
                          <span className="shrink-0 tabular-nums text-muted-foreground">
                            {formatMoney(i.value.minor, i.value.currency || currencyFallback, locale)}
                          </span>
                        ) : null}
                      </div>
                      {quiet ? (
                        <p className="text-xs text-muted-foreground">
                          {t("insq.risk.quiet", { n: nf.format(quiet.n ?? 0) })}
                        </p>
                      ) : null}
                    </li>
                  )
                })}
              </ul>
              {total > items.length ? (
                <p className="border-t border-nq-line px-5 py-2.5 text-xs text-muted-foreground">
                  {t("insq.risk.more", { n: nf.format(total - items.length) })}
                </p>
              ) : null}
            </>
          )}
        </>
      )}
    </Panel>
  )
}
