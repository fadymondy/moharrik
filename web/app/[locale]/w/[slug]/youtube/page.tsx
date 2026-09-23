"use client"

/*
  YouTube (MH-441): the enabled channel as last synced — its counters, every upload (search and
  privacy filter), and a detail sheet with the video's top comments and a link to reply on YouTube.
  Everything is from the sync (GET /api/w/youtube/…); a count YouTube does not give (likes hidden,
  comments off) is shown as not given, never as 0. The Data API quota is shown, because a refused
  sync must be explainable.
*/

import { Suspense, useEffect, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { ExternalLinkIcon, LoaderIcon, MessageSquareIcon, ThumbsUpIcon, EyeIcon, CheckIcon, TriangleAlertIcon } from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { DataError, NoResource, ResourceSwitcher, useEnabled } from "@/components/google/data-page"
import { SyncStatus } from "@/components/google/resource-picker"
import { YouTubeAnalytics } from "@/components/google/youtube-analytics"
import { YouTubeReports } from "@/components/google/youtube-reports"
import { useFormat } from "@/components/overview/overview-cards"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { YtChannelOut, YtSyncStep, YtVideo } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useYtChannel, useYtVideo, useYtVideos } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const PRIVACY = ["all", "public", "unlisted", "private", "scheduled"] as const

export default function YouTubePage() {
  return (
    <Suspense>
      <YouTube />
    </Suspense>
  )
}

function YouTube() {
  const { t, isRtl } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.youtube")} · ${ws.summary.name}`)
  const params = useSearchParams()
  const router = useRouter()
  const pathname = usePathname()
  const enabled = useEnabled("yt_channel")
  const chosen = params.get("channel") ?? ""
  const resourceId = enabled.list.some((r) => r.id === chosen) ? chosen : (enabled.list[0]?.id ?? "")
  const ch = useYtChannel(ws.id, resourceId, !!resourceId)
  const open = params.get("video")

  function set(next: Record<string, string>) {
    const u = new URLSearchParams(params.toString())
    for (const [k, v] of Object.entries(next)) {
      if (v) u.set(k, v)
      else u.delete(k)
    }
    router.replace(`${pathname}?${u}`, { scroll: false })
  }

  const out = ch.data
  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.youtube")}
        description={t("yt.hint")}
        action={resourceId ? <ResourceSwitcher kind="yt_channel" list={enabled.list} value={resourceId} onChange={(id) => set({ channel: id, video: "" })} /> : null}
      />
      {!ws.can("analytics:read") ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("gdata.noAccess")}</p>
      ) : enabled.error ? (
        <ErrorState error={enabled.error} />
      ) : !enabled.data ? (
        <Skeleton className="mx-6 h-40" />
      ) : !resourceId ? (
        <NoResource kind="yt_channel" known={enabled.known} />
      ) : ch.error ? (
        <ErrorState error={ch.error} />
      ) : !out ? (
        <Skeleton className="mx-6 h-40" />
      ) : !out.channel ? (
        out.resource?.syncing || (!out.resource?.last_error && !out.resource?.last_synced_at) ? (
          <div className="flex items-center gap-3 border-y border-line bg-grid-card px-6 py-10 text-sm text-grid-body" role="status" data-yt-syncing>
            <LoaderIcon className="size-4 animate-spin text-grid-muted" />
            {t("yt.firstSync")}
          </div>
        ) : (
          <DataError message={out.resource?.last_error || t("yt.notSynced")} resource={out.resource} />
        )
      ) : (
        <div className="pb-10" data-youtube>
          <ChannelHeader out={out} />
          <YouTubeAnalytics resourceId={resourceId} onOpenVideo={(id) => set({ video: id })} />
          <Videos resourceId={resourceId} onOpen={(id) => set({ video: id })} />
          <YouTubeReports resourceId={resourceId} />
        </div>
      )}
      <Sheet open={!!open && !!resourceId} onOpenChange={(v) => !v && set({ video: "" })}>
        <SheetContent side={isRtl ? "left" : "right"} className="w-full gap-0 overflow-y-auto p-0 sm:max-w-xl" data-video-sheet>
          {open && resourceId ? <VideoDetail resourceId={resourceId} id={open} /> : null}
        </SheetContent>
      </Sheet>
    </>
  )
}

function ChannelHeader({ out }: { out: YtChannelOut }) {
  const { t } = useTranslations()
  const f = useFormat()
  const c = out.channel!
  const q = out.quota
  return (
    <section className="border-y border-line" data-yt-channel>
      <div className="flex flex-col gap-4 px-6 py-5 sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-1 items-center gap-4">
          {c.avatar_url ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={c.avatar_url} alt="" className="size-14 shrink-0 rounded-full border border-line" referrerPolicy="no-referrer" />
          ) : null}
          <div className="min-w-0">
            <p className="truncate text-lg font-medium text-grid-fg">
              <bdi>{c.title}</bdi>
            </p>
            <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-grid-muted">
              {c.handle ? <Ltr>{c.handle}</Ltr> : null}
              <a href={c.url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 hover:text-grid-fg">
                {t("yt.openChannel")}
                <ExternalLinkIcon className="size-3.5" />
              </a>
              {out.resource ? <SyncStatus r={out.resource} /> : null}
            </p>
          </div>
        </div>
        <dl className="grid grid-cols-3 gap-6 text-sm">
          <div data-yt-stat="subscribers">
            <dt className="grid-micro">{t("yt.subscribers")}</dt>
            <dd className="mt-1 text-xl font-medium text-grid-fg tabular-nums">{c.subscribers_hidden ? <span className="text-sm text-grid-muted">{t("yt.hidden")}</span> : f.n(c.subscribers)}</dd>
          </div>
          <div data-yt-stat="views">
            <dt className="grid-micro">{t("yt.lifetimeViews")}</dt>
            <dd className="mt-1 text-xl font-medium text-grid-fg tabular-nums">{f.n(c.views)}</dd>
          </div>
          <div data-yt-stat="videos">
            <dt className="grid-micro">{t("yt.uploads")}</dt>
            <dd className="mt-1 text-xl font-medium text-grid-fg tabular-nums">{f.n(c.uploads)}</dd>
            {c.videos !== c.public_uploads ? (
              <dd className="mt-0.5 max-w-44 text-xs text-grid-muted" title={t("yt.countHint")} data-yt-count-differs>
                {t("yt.youtubeCounts", { n: f.n(c.videos), p: f.n(c.public_uploads) })}
              </dd>
            ) : null}
          </div>
        </dl>
      </div>
      <Steps steps={c.steps ?? []} />
      {out.resource?.last_error ? (
        <p className="border-t border-line px-6 py-2 text-xs break-words text-grid-danger-text" data-last-error>
          <Ltr>{out.resource.last_error}</Ltr>
        </p>
      ) : null}
      <p className="border-t border-line px-6 py-2 text-xs text-grid-muted" data-yt-quota>
        {t("yt.quota", { used: f.n(q.workspace_used), limit: f.n(q.workspace_limit), all: f.n(q.global_used), allLimit: f.n(q.global_limit) })}
      </p>
    </section>
  )
}

/** Each sync step that did not go fine, with the reason in the server's words. */
function Steps({ steps }: { steps: YtSyncStep[] }) {
  const { t } = useTranslations()
  const f = useFormat()
  const bad = steps.filter((s) => s.state !== "ok")
  if (bad.length === 0) return null
  return (
    <ul className="divide-y divide-line border-t border-line" data-yt-steps>
      {bad.map((s) => (
        <li key={s.name} className="flex items-start gap-3 px-6 py-2 text-xs" data-yt-step={s.name} data-state={s.state}>
          <TriangleAlertIcon className={s.state === "failed" ? "mt-0.5 size-3.5 shrink-0 text-grid-danger" : "mt-0.5 size-3.5 shrink-0 text-grid-warn"} />
          <p className="min-w-0 break-words">
            <span className="font-medium text-grid-fg">
              {t(`yt.step.${s.name}`)} · {t(`yt.stepState.${s.state}`)}
            </span>
            {s.error ? (
              <span className="text-grid-muted">
                {" — "}
                <Ltr>{s.error}</Ltr>
              </span>
            ) : null}
            <span className="ms-2 text-grid-muted" title={f.exact(s.at)}>
              {f.ago(s.at)}
            </span>
          </p>
        </li>
      ))}
    </ul>
  )
}

function Videos({ resourceId, onOpen }: { resourceId: string; onOpen: (id: string) => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const [q, setQ] = useState("")
  const [search, setSearch] = useState("")
  const [privacy, setPrivacy] = useState<(typeof PRIVACY)[number]>("all")
  useEffect(() => {
    const id = setTimeout(() => setSearch(q.trim()), 300)
    return () => clearTimeout(id)
  }, [q])
  const pages = useYtVideos(ws.id, resourceId, { q: search, privacy: privacy === "all" ? "" : privacy })
  const first = pages.data?.[0]
  const videos = (pages.data ?? []).flatMap((p) => p.videos ?? [])
  const last = pages.data?.[pages.data.length - 1]
  const counts = first?.counts ?? {}

  return (
    <section className="pt-6" data-yt-videos>
      <div className="flex flex-wrap items-center gap-3 px-6 pb-4">
        <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("yt.search")} aria-label={t("yt.search")} className="w-full sm:w-64" data-yt-search />
        <ToggleGroup variant="outline" size="sm" spacing={0} className="flex-wrap" value={[privacy]} onValueChange={(v) => v[0] && setPrivacy(v[0] as (typeof PRIVACY)[number])} aria-label={t("yt.privacy")}>
          {PRIVACY.map((p) => (
            <ToggleGroupItem key={p} value={p} data-privacy={p}>
              {t(`yt.privacy.${p}`)} <span className="text-grid-muted tabular-nums">{first ? f.n(p === "all" ? first.total : (counts[p] ?? 0)) : ""}</span>
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      {pages.error ? (
        <ErrorState error={pages.error} />
      ) : !pages.data ? (
        <div className="space-y-2 px-6">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-20 w-full" />
          ))}
        </div>
      ) : videos.length === 0 ? (
        <EmptyState title={search || privacy !== "all" ? t("yt.noMatch") : t("yt.noVideos")} body={search || privacy !== "all" ? undefined : t("yt.noVideosBody")} />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {videos.map((v) => (
            <VideoRow key={v.id} v={v} onOpen={() => onOpen(v.id)} />
          ))}
        </ul>
      )}
      {last?.has_more ? (
        <div className="flex justify-center py-4">
          <Button variant="outline" size="sm" onClick={() => pages.setSize(pages.size + 1)} disabled={pages.isValidating} data-load-more>
            {pages.isValidating ? t("common.working") : t("yt.more")}
          </Button>
        </div>
      ) : null}
    </section>
  )
}

function Counts({ v }: { v: YtVideo }) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-grid-muted tabular-nums">
      <span className="inline-flex items-center gap-1" title={t("yt.views")}>
        <EyeIcon className="size-3.5" aria-hidden />
        <span className="sr-only">{t("yt.views")}</span>
        {f.n(v.views)}
      </span>
      <span className="inline-flex items-center gap-1" title={t("yt.likes")}>
        <ThumbsUpIcon className="size-3.5" aria-hidden />
        <span className="sr-only">{t("yt.likes")}</span>
        {v.likes == null ? t("yt.hidden") : f.n(v.likes)}
      </span>
      <span className="inline-flex items-center gap-1" title={t("yt.comments")}>
        <MessageSquareIcon className="size-3.5" aria-hidden />
        <span className="sr-only">{t("yt.comments")}</span>
        {v.comments == null ? t("yt.commentsOff") : f.n(v.comments)}
      </span>
    </span>
  )
}

function PrivacyBadge({ v }: { v: YtVideo }) {
  const { t } = useTranslations()
  if (v.scheduled) return <Badge variant="outline">{t("yt.privacy.scheduled")}</Badge>
  if (v.privacy_status && v.privacy_status !== "public") return <Badge variant="outline">{t(`yt.privacy.${v.privacy_status}`)}</Badge>
  return null
}

function duration(sec: number) {
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = sec % 60
  return (h ? `${h}:${String(m).padStart(2, "0")}` : `${m}`) + `:${String(s).padStart(2, "0")}`
}

function VideoRow({ v, onOpen }: { v: YtVideo; onOpen: () => void }) {
  const f = useFormat()
  return (
    <li>
      <button type="button" onClick={onOpen} className="flex w-full items-start gap-4 px-6 py-3 text-start transition-colors hover:bg-grid-soft" data-video={v.id}>
        <span className="relative w-32 shrink-0 sm:w-40">
          {v.thumbnail_url ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={v.thumbnail_url} alt="" className="aspect-video w-full rounded-sm border border-line object-cover" referrerPolicy="no-referrer" loading="lazy" />
          ) : (
            <span className="block aspect-video w-full rounded-sm border border-line bg-grid-soft" />
          )}
          {v.duration_seconds ? (
            <span dir="ltr" className="absolute end-1 bottom-1 rounded-sm bg-black/75 px-1 text-[11px] text-white tabular-nums">
              {duration(v.duration_seconds)}
            </span>
          ) : null}
        </span>
        <span className="min-w-0 flex-1 space-y-1.5">
          <span className="line-clamp-2 text-sm font-medium text-grid-fg">
            <bdi>{v.title}</bdi>
          </span>
          <span className="flex flex-wrap items-center gap-2 text-xs text-grid-muted">
            {v.published_at ? <span title={f.exact(v.published_at)}>{f.ago(v.published_at)}</span> : null}
            <PrivacyBadge v={v} />
          </span>
          <Counts v={v} />
        </span>
      </button>
    </li>
  )
}

function VideoDetail({ resourceId, id }: { resourceId: string; id: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const d = useYtVideo(ws.id, resourceId, id)
  if (d.error) return <div className="p-6"><ErrorState error={d.error} /></div>
  const v = d.data
  if (!v)
    return (
      <div className="space-y-3 p-6" aria-busy>
        <SheetTitle className="sr-only">{t("yt.video")}</SheetTitle>
        <Skeleton className="aspect-video w-full" />
        <Skeleton className="h-5 w-3/4" />
        <Skeleton className="h-4 w-1/2" />
      </div>
    )
  const comments = v.top_comments ?? []
  return (
    <div data-video-detail={v.id}>
      {v.thumbnail_url ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img src={v.thumbnail_url.replace("mqdefault", "hqdefault")} alt="" className="aspect-video w-full border-b border-line object-cover" referrerPolicy="no-referrer" />
      ) : null}
      <SheetHeader className="gap-2 px-6 pt-5 pb-3">
        <SheetTitle className="text-base">
          <bdi>{v.title}</bdi>
        </SheetTitle>
        <SheetDescription className="flex flex-wrap items-center gap-2">
          {v.published_at ? <span title={f.exact(v.published_at)}>{f.exact(v.published_at)}</span> : null}
          <PrivacyBadge v={v} />
        </SheetDescription>
        <Counts v={v} />
        <div className="flex flex-wrap gap-2 pt-1">
          <Button variant="outline" size="sm" nativeButton={false} render={<a href={v.url} target="_blank" rel="noreferrer" />}>
            <ExternalLinkIcon />
            {t("yt.watch")}
          </Button>
          <Button variant="outline" size="sm" nativeButton={false} render={<a href={v.studio_url} target="_blank" rel="noreferrer" />}>
            <ExternalLinkIcon />
            {t("yt.studio")}
          </Button>
        </div>
      </SheetHeader>
      <YouTubeAnalytics resourceId={resourceId} video={v.id} compact />
      {v.description ? (
        <p className="border-t border-line px-6 py-4 text-sm whitespace-pre-wrap text-grid-body">
          <bdi>{v.description.length > 1200 ? v.description.slice(0, 1200) + "…" : v.description}</bdi>
        </p>
      ) : null}
      {v.tags && v.tags.length ? (
        <div className="flex flex-wrap gap-1 border-t border-line px-6 py-3">
          {v.tags.slice(0, 20).map((tag) => (
            <Badge key={tag} variant="secondary">
              <bdi>{tag}</bdi>
            </Badge>
          ))}
        </div>
      ) : null}
      <section className="border-t border-line" data-top-comments={comments.length}>
        <h3 className="px-6 pt-4 pb-2 text-sm font-medium">{t("yt.topComments")}</h3>
        {v.comment_note === "comments_disabled" ? (
          <p className="px-6 pb-5 text-sm text-grid-muted" data-comments-off>
            {t("yt.commentsDisabled")}
          </p>
        ) : v.comment_note === "comments_unavailable" || v.comment_note === "comments_not_public" ? (
          <p className="px-6 pb-5 text-sm text-grid-muted" data-comments-unavailable={v.comment_note}>
            {t(v.comment_note === "comments_unavailable" ? "yt.commentsUnavailable" : "yt.commentsNotPublic")}
          </p>
        ) : (
          <>
            {v.comment_note ? (
              <p className="px-6 pb-2 text-xs break-words text-grid-warn-text" data-comment-note>
                <Ltr>{v.comment_note}</Ltr>
              </p>
            ) : null}
            {comments.length === 0 ? (
              <p className="px-6 pb-5 text-sm text-grid-muted">{t("yt.noComments")}</p>
            ) : (
              <ul className="divide-y divide-line border-t border-line">
                {comments.map((c) => (
                  <li key={c.id} className="space-y-1.5 px-6 py-3" data-comment={c.id}>
                    <p className="flex flex-wrap items-center gap-x-2 text-xs text-grid-muted">
                      <span className="font-medium text-grid-fg">
                        <bdi>{c.author}</bdi>
                      </span>
                      {c.published_at ? <span title={f.exact(c.published_at)}>{f.ago(c.published_at)}</span> : null}
                      {c.answered ? (
                        <span className="inline-flex items-center gap-1 text-grid-ok-text">
                          <CheckIcon className="size-3" />
                          {t("yt.answered")}
                        </span>
                      ) : null}
                    </p>
                    <p className="text-sm whitespace-pre-wrap text-grid-body">
                      <bdi>{c.text}</bdi>
                    </p>
                    <p className="flex flex-wrap items-center gap-x-4 text-xs text-grid-muted tabular-nums">
                      <span className="inline-flex items-center gap-1">
                        <ThumbsUpIcon className="size-3" aria-hidden /> {f.n(c.likes)}
                      </span>
                      <span>{t("yt.replies", { n: f.n(c.replies) })}</span>
                      <a href={c.reply_url} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-grid-fg hover:underline" data-reply>
                        {t("yt.reply")}
                        <ExternalLinkIcon className="size-3" />
                      </a>
                    </p>
                  </li>
                ))}
              </ul>
            )}
          </>
        )}
      </section>
    </div>
  )
}
