"use client"

/*
  The Overview's Google cards (MH-444/445/441, rebuilt for MH-567): kept apart from
  overview-cards.tsx so the front page's own cards can change without touching these.

  TrafficCard used to list one row per GA4 property with only users/sessions. It is now one
  summary over every connected source for the last 28 days — GA4 totals across every property,
  Search Console totals, YouTube — each with its % change vs the 28 days before when that
  comparison is known, a daily trend chart, and a compact "top properties" breakdown. A figure
  never fetched is a dash with the reason, never a zero; a delta with no prior period is a dash
  too, never a fake 0%.
*/

import Link from "next/link"
import { ArrowDownIcon, ArrowUpIcon, ChartLineIcon, MinusIcon, YoutubeIcon } from "lucide-react"
import type { ReactNode } from "react"

import { Bdi } from "@/components/bidi"
import { ChartOrTable, type SeriesPoint } from "@/components/overview/overview-charts"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import type {
  OverviewGA4Summary,
  OverviewSearchConsoleSummary,
  OverviewTraffic,
  OverviewYouTube,
  OverviewYouTubeSummary,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat, numberFormat } from "@/lib/i18n-locale"
import { cn } from "@/lib/utils"

function Figure({
  label,
  value,
  id,
  delta,
}: {
  label: string
  value: string
  id: string
  delta?: ReactNode
}) {
  return (
    <div className="min-w-0" data-traffic-figure={id}>
      <p className="eyebrow truncate">{label}</p>
      <p className="mt-1 text-2xl font-medium text-foreground tabular-nums">{value}</p>
      {delta ? <div className="mt-1">{delta}</div> : null}
    </div>
  )
}

/** The % change vs the previous 28 days, in the same visual language as components/google's
 * Delta (arrow + sign + word), but taking the server's precomputed percentage directly — the
 * API never sends a raw "before" figure to reconstruct one from. */
function PctBadge({ pct }: { pct: number | null }) {
  const { t, locale } = useTranslations()
  if (pct == null)
    return <span className="text-xs text-muted-foreground">{t("gdata.noCompare")}</span>
  const flat = Math.abs(pct) < 0.0005
  const good = flat ? null : pct > 0
  const Icon = flat ? MinusIcon : pct > 0 ? ArrowUpIcon : ArrowDownIcon
  const text = numberFormat(locale, {
    style: "percent",
    maximumFractionDigits: 1,
    signDisplay: "always",
  }).format(pct)
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-xs font-medium tabular-nums",
        good == null
          ? "text-muted-foreground"
          : good
            ? "text-nq-success-text"
            : "text-nq-danger-text",
      )}
      data-delta={good == null ? "flat" : good ? "better" : "worse"}
    >
      <Icon className="size-3.5" aria-hidden />
      {text}
      <span className="font-normal text-muted-foreground">
        {t(good == null ? "gdata.flat" : good ? "gdata.better" : "gdata.worse")}
      </span>
    </span>
  )
}

function useTrafficFormat() {
  const { locale } = useTranslations()
  const f = useFormat()
  const pct = numberFormat(locale, { style: "percent", maximumFractionDigits: 1 })
  const dayShort = dateFormat(locale, { day: "numeric", month: "short", timeZone: "UTC" })
  const compact = numberFormat(locale, { notation: "compact", maximumFractionDigits: 1 })
  return {
    ...f,
    pct: (v: number) => pct.format(v),
    compact: (v: number) => compact.format(v),
    dayShort: (ymd: string) => dayShort.format(new Date(`${ymd}T00:00:00Z`)),
    duration: (s: number) => {
      if (s < 60) return `${Math.round(s)}s`
      const m = Math.floor(s / 60)
      const rem = Math.round(s % 60)
      return `${m}m ${rem}s`
    },
  }
}

function Ga4Section({
  ga4,
  f,
}: {
  ga4: OverviewGA4Summary
  f: ReturnType<typeof useTrafficFormat>
}) {
  const { t } = useTranslations()
  const num = (v: number | null) => (v == null ? "—" : f.n(v))
  return (
    <div className="space-y-3 px-5 py-4" data-traffic-ga4>
      <p className="text-xs font-medium text-muted-foreground">
        {t("nav.analytics")} · {t("overview.propertiesCount", { n: f.n(ga4.properties_count) })}
      </p>
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
        <Figure
          id="ga4-users"
          label={t("analytics.users")}
          value={num(ga4.users)}
          delta={<PctBadge pct={ga4.users_delta_pct} />}
        />
        <Figure
          id="ga4-sessions"
          label={t("analytics.sessions")}
          value={num(ga4.sessions)}
          delta={<PctBadge pct={ga4.sessions_delta_pct} />}
        />
        <Figure
          id="ga4-views"
          label={t("analytics.views")}
          value={num(ga4.views)}
          delta={<PctBadge pct={ga4.views_delta_pct} />}
        />
        <Figure
          id="ga4-engagement"
          label={t("analytics.engagementRate")}
          value={ga4.engagement_rate == null ? "—" : f.pct(ga4.engagement_rate)}
        />
        <Figure
          id="ga4-avg-session"
          label={t("overview.avgSession")}
          value={ga4.avg_session_seconds == null ? "—" : f.duration(ga4.avg_session_seconds)}
        />
        <Figure id="ga4-key-events" label={t("overview.keyEvents")} value={num(ga4.key_events)} />
      </div>
    </div>
  )
}

function GscSection({
  gsc,
  f,
}: {
  gsc: OverviewSearchConsoleSummary
  f: ReturnType<typeof useTrafficFormat>
}) {
  const { t } = useTranslations()
  const num = (v: number | null) => (v == null ? "—" : f.n(v))
  return (
    <div className="space-y-3 px-5 py-4" data-traffic-gsc>
      <p className="text-xs font-medium text-muted-foreground">
        {t("nav.searchConsole")} · {t("overview.sitesCount", { n: f.n(gsc.sites_count) })}
      </p>
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Figure
          id="gsc-clicks"
          label={t("overview.searchClicks")}
          value={num(gsc.clicks)}
          delta={<PctBadge pct={gsc.clicks_delta_pct} />}
        />
        <Figure
          id="gsc-impressions"
          label={t("gsc.impressions")}
          value={num(gsc.impressions)}
          delta={<PctBadge pct={gsc.impressions_delta_pct} />}
        />
        <Figure id="gsc-ctr" label={t("gsc.ctr")} value={gsc.ctr == null ? "—" : f.pct(gsc.ctr)} />
        <Figure
          id="gsc-position"
          label={t("gsc.positionShort")}
          value={gsc.avg_position == null ? "—" : f.n(gsc.avg_position)}
        />
      </div>
    </div>
  )
}

function YoutubeSection({
  yt,
  f,
}: {
  yt: OverviewYouTubeSummary
  f: ReturnType<typeof useTrafficFormat>
}) {
  const { t } = useTranslations()
  const num = (v: number | null) => (v == null ? "—" : f.n(v))
  return (
    <div className="space-y-3 px-5 py-4" data-traffic-youtube>
      <p className="text-xs font-medium text-muted-foreground">{t("nav.youtube")}</p>
      <div className="grid grid-cols-2 gap-4">
        <Figure id="yt-views28" label={t("overview.views28")} value={num(yt.views_28)} />
        <Figure id="yt-subscribers" label={t("yt.subscribers")} value={num(yt.subscribers)} />
      </div>
    </div>
  )
}

export function TrafficCard({ traffic, base }: { traffic: OverviewTraffic; base: string }) {
  const { t } = useTranslations()
  const f = useTrafficFormat()
  const summary = traffic.summary
  const nothingConnected = !summary || (!summary.ga4 && !summary.search_console && !summary.youtube)

  const seriesKeys: string[] = []
  const seriesLabels: string[] = []
  if (summary?.ga4) {
    seriesKeys.push("users", "sessions")
    seriesLabels.push(t("analytics.users"), t("analytics.sessions"))
  }
  if (summary?.search_console) {
    seriesKeys.push("clicks")
    seriesLabels.push(t("gsc.clicks"))
  }
  const chartData: SeriesPoint[] = (summary?.daily ?? []).map((d) => ({
    date: d.date,
    users: d.users,
    sessions: d.sessions,
    clicks: d.clicks,
  }))

  return (
    <Panel
      id="traffic"
      title={t("overview.traffic")}
      icon={<ChartLineIcon />}
      href={`${base}/analytics`}
      meta={<span className="text-xs text-muted-foreground">{t("overview.last28")}</span>}
    >
      {nothingConnected ? (
        <p className="px-5 py-6 text-sm text-muted-foreground" data-empty>
          {t("overview.trafficPending")}
        </p>
      ) : (
        <div className="divide-y divide-nq-line">
          {summary?.ga4 ? <Ga4Section ga4={summary.ga4} f={f} /> : null}
          {summary?.search_console ? <GscSection gsc={summary.search_console} f={f} /> : null}
          {summary?.youtube ? <YoutubeSection yt={summary.youtube} f={f} /> : null}
          {seriesKeys.length > 0 ? (
            <div data-traffic-chart>
              <ChartOrTable
                id="traffic-trend"
                caption={t("overview.trafficChart")}
                data={chartData}
                seriesKeys={seriesKeys}
                seriesLabels={seriesLabels}
                formatTick={f.compact}
                formatDay={f.dayShort}
                dayHead={t("reports.bucketHead.day")}
                empty={chartData.length === 0}
              />
            </div>
          ) : null}
          {summary && summary.top_properties.length > 0 ? (
            <div className="px-5 py-4" data-traffic-top>
              <p className="eyebrow pb-2">{t("overview.topProperties")}</p>
              <ul className="space-y-2">
                {summary.top_properties.slice(0, 5).map((p) => (
                  <li key={p.resource_id} className="flex items-center justify-between gap-3">
                    <Link
                      href={`${base}/analytics`}
                      className="min-w-0 truncate text-sm text-foreground hover:underline"
                      data-top-property={p.name}
                    >
                      <Bdi>{p.name}</Bdi>
                    </Link>
                    <span className="shrink-0 text-sm text-muted-foreground tabular-nums">
                      {p.users == null ? "—" : f.n(p.users)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </div>
      )}
    </Panel>
  )
}

export function YouTubeCard({ channels, base }: { channels: OverviewYouTube[]; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const num = (v: number | null) => (v == null ? "—" : f.n(v))
  return (
    <Panel id="youtube" title={t("nav.youtube")} icon={<YoutubeIcon />} href={`${base}/youtube`}>
      <ul className="divide-y divide-nq-line">
        {channels.map((c) => (
          <li key={c.resource_id} className="space-y-3 px-5 py-4" data-youtube-channel={c.title}>
            <p className="truncate text-sm font-medium text-foreground">
              <bdi>{c.title}</bdi>
            </p>
            <div className="grid grid-cols-2 gap-4">
              <Figure id="subscribers" label={t("yt.subscribers")} value={num(c.subscribers)} />
              <Figure id="views28" label={t("overview.views28")} value={num(c.views_28)} />
            </div>
            <p className="text-xs text-muted-foreground">
              {c.last_error
                ? c.last_error
                : c.views_28 == null
                  ? t("overview.views28Pending")
                  : t("overview.views28Source", {
                      to: c.views_28_to ? f.date(c.views_28_to) : "—",
                    })}
            </p>
          </li>
        ))}
      </ul>
    </Panel>
  )
}
