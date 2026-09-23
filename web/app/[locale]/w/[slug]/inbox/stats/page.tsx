"use client"

import { useMemo, useState } from "react"

import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { ChannelStats } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag, isLocale } from "@/lib/i18n-locale"
import { useInboxStats } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const WINDOWS = [7, 30, 90] as const

/** Seconds as the largest unit that reads naturally: 45 sec, 12 min, 3.5 hr, 2.1 days. */
function useDuration() {
  const { locale } = useTranslations()
  return useMemo(() => {
    const tag = intlTag(isLocale(locale) ? locale : "en")
    const unit = (u: string, digits: number) =>
      new Intl.NumberFormat(tag, { style: "unit", unit: u, unitDisplay: "short", maximumFractionDigits: digits })
    const sec = unit("second", 0)
    const min = unit("minute", 0)
    const hr = unit("hour", 1)
    const day = unit("day", 1)
    return (s: number | null) => {
      if (s === null) return null
      if (s < 60) return sec.format(Math.round(s))
      if (s < 3600) return min.format(Math.round(s / 60))
      if (s < 86400) return hr.format(s / 3600)
      return day.format(s / 86400)
    }
  }, [locale])
}

/**
 * Inbox stats (MH-383): conversations per channel, first-response time and
 * resolution over a window. Every number comes from the API's SQL; nothing is
 * computed here but formatting, and an empty window says so instead of drawing
 * zeros that look like a measurement.
 */
export default function InboxStatsPage() {
  const { t, formatNumber, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("inbox.stats.title")} · ${ws.summary.name}`)
  const [days, setDays] = useState<number>(30)
  const stats = useInboxStats(ws.id, days)
  const dur = useDuration()
  const percent = useMemo(() => new Intl.NumberFormat(intlTag(isLocale(locale) ? locale : "en"), { style: "percent", maximumFractionDigits: 0 }), [locale])

  const s = stats.data
  const total = s?.total

  return (
    <>
      <SectionHeader micro={t("nav.inbox")} title={t("inbox.stats.title")} description={t("inbox.stats.hint")} />

      <div className="flex flex-wrap items-center gap-3 px-6 pb-4">
        <span className="text-sm text-grid-muted">{t("inbox.stats.window")}</span>
        <ToggleGroup
          variant="outline"
          size="sm"
          spacing={0}
          value={[String(days)]}
          onValueChange={(v) => {
            const next = Number(v[0])
            if (next) setDays(next)
          }}
          aria-label={t("inbox.stats.window")}
        >
          {WINDOWS.map((d) => (
            <ToggleGroupItem key={d} value={String(d)} data-window={d}>
              {t("inbox.stats.days", { n: formatNumber(d) })}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        {s ? (
          <span className="text-sm text-grid-muted sm:ms-auto" data-stats-now>
            {t("inbox.stats.now", { open: formatNumber(s.open_now), snoozed: formatNumber(s.snoozed_now), unread: formatNumber(s.unread_now) })}
          </span>
        ) : null}
      </div>

      {stats.error ? (
        <ErrorState error={stats.error} />
      ) : !s || !total ? (
        <LoadingRows rows={4} />
      ) : total.conversations === 0 ? (
        <EmptyState title={t("inbox.stats.noData")} body={t("inbox.stats.noDataBody", { n: formatNumber(days) })} />
      ) : (
        <>
          <dl className="grid grid-cols-2 border-y border-line lg:grid-cols-4" data-stats-tiles>
            <Tile label={t("inbox.stats.conversations")} value={formatNumber(total.conversations)} stat="conversations" />
            <Tile
              label={t("inbox.stats.firstResponse")}
              value={dur(total.first_response_median_seconds) ?? t("inbox.stats.none")}
              note={t("inbox.stats.answeredOf", { n: formatNumber(total.answered), total: formatNumber(total.conversations) })}
              stat="first-response"
            />
            <Tile
              label={t("inbox.stats.resolution")}
              value={total.resolution_rate === null ? t("inbox.stats.none") : percent.format(total.resolution_rate)}
              note={t("inbox.stats.ofN", { n: formatNumber(total.resolved), total: formatNumber(total.conversations) })}
              stat="resolved"
            />
            <Tile label={t("inbox.stats.resolutionTime")} value={dur(total.resolution_median_seconds) ?? t("inbox.stats.none")} stat="resolution-time" />
          </dl>

          <div className="px-6 pt-6">
            <Table data-stats-table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t("inbox.stats.channel")}</TableHead>
                  <TableHead className="text-end">{t("inbox.stats.conversations")}</TableHead>
                  <TableHead className="text-end">{t("inbox.stats.firstResponse")}</TableHead>
                  <TableHead className="text-end">{t("inbox.stats.firstResponseMean")}</TableHead>
                  <TableHead className="text-end">{t("inbox.stats.awaiting")}</TableHead>
                  <TableHead className="text-end">{t("inbox.stats.resolution")}</TableHead>
                  <TableHead className="text-end">{t("inbox.stats.resolutionTime")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {[...s.channels, total].map((c) => (
                  <Row key={c.channel} c={c} dur={dur} percent={percent} />
                ))}
              </TableBody>
            </Table>
          </div>
          <p className="max-w-3xl px-6 pt-4 text-xs text-grid-muted">{t("inbox.stats.method")}</p>
        </>
      )}
      <div className="pb-8" />
    </>
  )
}

function Tile({ label, value, note, stat }: { label: string; value: string; note?: string; stat: string }) {
  return (
    <div className="flex flex-col gap-1 border-line px-6 py-5 not-last:border-e max-lg:nth-2:border-e-0 max-lg:nth-[-n+2]:border-b" data-stat={stat}>
      <dt className="text-sm text-grid-muted">{label}</dt>
      <dd className="text-2xl font-medium tabular-nums">{value}</dd>
      {note ? <dd className="text-xs text-grid-muted">{note}</dd> : null}
    </div>
  )
}

function Row({ c, dur, percent }: { c: ChannelStats; dur: (s: number | null) => string | null; percent: Intl.NumberFormat }) {
  const { t, formatNumber } = useTranslations()
  const none = t("inbox.stats.none")
  const isTotal = c.channel === "all"
  const empty = c.conversations === 0
  return (
    <TableRow data-stats-row={c.channel} className={isTotal ? "font-medium" : undefined}>
      <TableCell>{isTotal ? t("inbox.stats.total") : t(`inbox.channel.${c.channel}`)}</TableCell>
      <TableCell className="text-end tabular-nums">{formatNumber(c.conversations)}</TableCell>
      <TableCell className="text-end tabular-nums">{empty ? none : (dur(c.first_response_median_seconds) ?? none)}</TableCell>
      <TableCell className="text-end tabular-nums">{empty ? none : (dur(c.first_response_mean_seconds) ?? none)}</TableCell>
      <TableCell className="text-end tabular-nums">{empty ? none : formatNumber(c.awaiting)}</TableCell>
      <TableCell className="text-end tabular-nums">
        {empty || c.resolution_rate === null ? none : `${percent.format(c.resolution_rate)} (${formatNumber(c.resolved)})`}
      </TableCell>
      <TableCell className="text-end tabular-nums">{empty ? none : (dur(c.resolution_median_seconds) ?? none)}</TableCell>
    </TableRow>
  )
}
