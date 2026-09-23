"use client"

/*
  YouTube Analytics (MH-442) for the channel, or for one video inside its sheet: totals, the daily
  trend (one metric at a time, one axis), the top videos and where the views came from, over 7, 28
  or 90 days — all as YouTube Analytics answered (GET /api/w/youtube/analytics). A day YouTube has
  not published yet is a gap in the line, never a zero.
*/

import { useState } from "react"
import { ChartLineIcon, PlayIcon, SignpostIcon } from "lucide-react"

import { BarList, TrendChart } from "@/components/charts/trend-chart"
import { DataError, Freshness, Stat, WindowToggle } from "@/components/google/data-page"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { EmptyState } from "@/components/states"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, type YtAnalytics, type YtAnalyticsOut } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useYtAnalytics } from "@/lib/queries"

type Metric = "views" | "watch_minutes" | "subscribers"

const SOURCES = ["YT_SEARCH", "SUGGESTED", "BROWSE", "EXT_URL", "NO_LINK_OTHER", "PLAYLIST", "NOTIFICATION", "SUBSCRIBER", "YT_CHANNEL", "YT_OTHER_PAGE", "SHORTS", "END_SCREEN", "ANNOTATION", "ADVERTISING", "RELATED_VIDEO", "HASHTAGS", "CAMPAIGN_CARD", "NO_LINK_EMBEDDED", "LIVE_REDIRECT", "SOUND_PAGE", "VIDEO_REMIXES", "IMMERSIVE_LIVE", "PRODUCT_PAGE"]

export function YouTubeAnalytics({ resourceId, video = "", onOpenVideo, compact = false }: { resourceId: string; video?: string; onOpenVideo?: (id: string) => void; compact?: boolean }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [days, setDays] = useState(28)
  const [refreshing, setRefreshing] = useState(false)
  const res = useYtAnalytics(ws.id, resourceId, days, video)

  async function refresh() {
    setRefreshing(true)
    try {
      const q = new URLSearchParams({ resource: resourceId, days: String(days), refresh: "true" })
      if (video) q.set("video", video)
      const out = await api<YtAnalyticsOut>(`/api/w/youtube/analytics?${q}`, { workspaceId: ws.id })
      await res.mutate(out, { revalidate: false })
    } finally {
      setRefreshing(false)
    }
  }

  const out = res.data
  const a = out?.analytics
  return (
    <section className={compact ? "border-t border-line" : "pt-6"} data-yt-analytics={video || "channel"}>
      <div className="flex flex-wrap items-center gap-3 px-6 pt-4 pb-3">
        <h2 className="me-auto flex items-center gap-2 text-sm font-medium [&_svg]:size-4 [&_svg]:text-grid-muted">
          <ChartLineIcon />
          {t("yta.title")}
        </h2>
        <WindowToggle value={days} onChange={setDays} />
      </div>
      {res.error ? null : !out ? (
        <Skeleton className="mx-6 mb-4 h-40" />
      ) : !a ? (
        <DataError message={out.error || t("gdata.noAnswer")} resource={out.resource} />
      ) : (
        <>
          <div className="px-6 pb-3">
            <Freshness fetchedAt={a.fetched_at} stale={a.stale} note={a.note} onRefresh={refresh} refreshing={refreshing} />
          </div>
          {a.empty ? (
            <div data-yta-empty>
              <EmptyState title={t("yta.emptyTitle")} body={t("yta.emptyBody")} />
            </div>
          ) : (
            <Filled a={a} compact={compact} onOpenVideo={onOpenVideo} />
          )}
        </>
      )}
    </section>
  )
}

function Filled({ a, compact, onOpenVideo }: { a: YtAnalytics; compact: boolean; onOpenVideo?: (id: string) => void }) {
  const { t, locale } = useTranslations()
  const f = useFormat()
  const [metric, setMetric] = useState<Metric>("views")
  const daily = a.daily ?? []
  const tot = a.totals
  const net = tot.subscribers_gained - tot.subscribers_lost
  const values = daily.map((d) => (metric === "subscribers" ? (d.subscribers_gained == null ? null : d.subscribers_gained - (d.subscribers_lost ?? 0)) : d[metric]))
  const pending = daily.filter((d) => d.views == null).length
  const hours = new Intl.NumberFormat(intlTag(locale), { maximumFractionDigits: 1 })
  const top = a.top_videos ?? []
  const traffic = a.traffic_sources ?? []
  const range = t("gdata.range", { from: f.date(a.start_date), to: f.date(a.end_date) })
  return (
    <>
      <div className={`grid-cells grid-cells-flush-x grid-cols-2 ${compact ? "" : "lg:grid-cols-4"}`} data-yta-totals>
        <Stat id="yt-views" label={t("yt.views")} value={f.n(tot.views)} sub={range} />
        <Stat id="yt-watch" label={t("yta.watchHours")} value={hours.format(tot.watch_minutes / 60)} sub={t("yta.avgView", { time: mmss(tot.avg_view_duration_seconds) })} />
        <Stat id="yt-subs" label={t("yta.subscribersNet")} value={new Intl.NumberFormat(intlTag(locale), { signDisplay: "exceptZero" }).format(net)} sub={t("yta.gainedLost", { g: f.n(tot.subscribers_gained), l: f.n(tot.subscribers_lost) })} />
        <Stat id="yt-engagement" label={t("yt.likes")} value={f.n(tot.likes)} sub={t("yta.commentsN", { n: f.n(tot.comments) })} />
      </div>
      <div className="px-5 py-4">
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <ToggleGroup variant="outline" size="sm" spacing={0} value={[metric]} onValueChange={(v) => v[0] && setMetric(v[0] as Metric)} aria-label={t("analytics.metric")}>
            {(["views", "watch_minutes", "subscribers"] as Metric[]).map((m) => (
              <ToggleGroupItem key={m} value={m} data-yt-metric={m}>
                {t(`yta.metric.${m}`)}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </div>
        <TrendChart title={`${t(`yta.metric.${metric}`)} · ${range}`} dates={daily.map((d) => d.day)} series={[{ key: metric, label: t(`yta.metric.${metric}`), values, slot: 1 }]} format={f.n} formatDate={f.date} height={compact ? 180 : 240} />
        {pending > 0 ? <p className="mt-2 text-xs text-grid-muted" data-yta-pending>{t("yta.pending", { n: f.n(pending) })}</p> : null}
      </div>
      <div className={`grid-cells grid-cells-flush-x grid-cols-1 ${compact ? "" : "lg:grid-cols-2"}`}>
        {!compact ? (
          <Panel id="yt-top" title={t("yta.topVideos")} icon={<PlayIcon />} meta={<span className="ms-auto text-xs text-grid-muted">{t("yt.views")}</span>}>
            {top.length === 0 ? (
              <p className="px-5 py-6 text-sm text-grid-muted">{t("yta.noTop")}</p>
            ) : (
              <BarList
                label={t("yta.topVideos")}
                format={f.n}
                rows={top.map((v) => ({
                  key: v.video_id,
                  label: onOpenVideo ? (
                    <button type="button" className="max-w-full truncate text-start hover:underline" onClick={() => onOpenVideo(v.video_id)} data-top-video={v.video_id}>
                      <bdi>{v.title || v.video_id}</bdi>
                    </button>
                  ) : (
                    <bdi>{v.title || v.video_id}</bdi>
                  ),
                  value: v.views,
                }))}
              />
            )}
          </Panel>
        ) : null}
        <Panel id="yt-traffic" title={t("yta.traffic")} icon={<SignpostIcon />} meta={<span className="ms-auto text-xs text-grid-muted">{t("yt.views")}</span>}>
          {traffic.length === 0 ? (
            <p className="px-5 py-6 text-sm text-grid-muted">{t("yta.noTraffic")}</p>
          ) : (
            <BarList label={t("yta.traffic")} format={f.n} rows={traffic.map((s) => ({ key: s.source, label: SOURCES.includes(s.source) ? t(`yta.source.${s.source}`) : s.source, value: s.views }))} />
          )}
        </Panel>
      </div>
    </>
  )
}

function mmss(sec: number) {
  const s = Math.round(sec)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
}
