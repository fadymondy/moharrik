"use client"

import Link from "next/link"
import { useMemo, useState } from "react"
import {
  BookmarkCheckIcon,
  BookmarkIcon,
  CheckIcon,
  EyeOffIcon,
  ExternalLinkIcon,
  RotateCcwIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type FeedSource,
  type NewsItem,
  type TrendReason,
  type TrendTopic,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat, relativeTimeFormat } from "@/lib/i18n-locale"

type T = ReturnType<typeof useTranslations>["t"]

/** "3 hours ago" / "منذ 3 ساعات", and a full date past a week. */
export function useWhen() {
  const { locale } = useTranslations()
  return useMemo(() => {
    // The shared formatters: Western digits in both locales, bidi marks stripped (MH-468).
    const rel = relativeTimeFormat(locale, { numeric: "auto" })
    const abs = dateFormat(locale, { dateStyle: "medium", timeStyle: "short" })
    return (iso: string | null | undefined) => {
      if (!iso) return "—"
      const d = new Date(iso)
      if (Number.isNaN(d.getTime())) return "—"
      const secs = (d.getTime() - Date.now()) / 1000
      const a = Math.abs(secs)
      if (a < 60) return rel.format(Math.round(secs), "second")
      if (a < 3600) return rel.format(Math.round(secs / 60), "minute")
      if (a < 86400) return rel.format(Math.round(secs / 3600), "hour")
      if (a < 7 * 86400) return rel.format(Math.round(secs / 86400), "day")
      return abs.format(d)
    }
  }, [locale])
}

/** A UTC day ("2026-09-23") as the reader says it. */
export function useDayLabel() {
  const { locale } = useTranslations()
  return useMemo(() => {
    const f = dateFormat(locale, {
      weekday: "long",
      day: "numeric",
      month: "long",
      timeZone: "UTC",
    })
    return (day: string) => f.format(new Date(`${day}T00:00:00Z`))
  }, [locale])
}

/** The reason in the reader's language, from its structured fields. */
function reasonText(r: TrendReason, t: T) {
  switch (r.kind) {
    case "sources":
      return (r.count ?? r.detail.length) === 1
        ? t("trends.reason.sourcesOne")
        : t("trends.reason.sourcesMany", { n: r.count ?? r.detail.length })
    case "velocity":
      if (r.signal === "hn")
        return t("trends.reason.hn", { position: r.position ?? 0, points: r.metric ?? 0 })
      if (r.signal === "devto") return t("trends.reason.devto", { points: r.metric ?? 0 })
      return t("trends.reason.burst", { n: r.count ?? 2 })
    case "relevance":
      return t("trends.reason.relevance", { topics: r.detail.join(t("trends.listSeparator")) })
  }
  return r.label
}

/**
 * Why a topic ranks where it does: one chip per part of the score, largest
 * first, each with its points. The chips add up to the score beside the title.
 */
export function ReasonChips({ reasons }: { reasons: TrendReason[] }) {
  const { t } = useTranslations()
  return (
    <ul className="flex flex-wrap gap-1.5" aria-label={t("trends.whyRanked")} data-trend-reasons>
      {reasons.map((r) => (
        <li key={r.kind}>
          <Tooltip>
            <TooltipTrigger
              render={
                <Badge
                  variant={
                    r.kind === "relevance"
                      ? "default"
                      : r.kind === "velocity"
                        ? "secondary"
                        : "outline"
                  }
                  className="h-auto max-w-full whitespace-normal py-0.5 text-start"
                  data-reason={r.kind}
                />
              }
            >
              <span className="font-mono tabular-nums" dir="ltr">
                +{r.points}
              </span>
              <span className="break-words">
                <bdi>{reasonText(r, t)}</bdi>
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-xs">
              <p className="font-medium">{t(`trends.reasonKind.${r.kind}`)}</p>
              {r.detail.length ? <p className="mt-0.5">{r.detail.join(" · ")}</p> : null}
            </TooltipContent>
          </Tooltip>
        </li>
      ))}
    </ul>
  )
}

/** The score, as a small tabular figure. */
export function Score({ value }: { value: number }) {
  const { t } = useTranslations()
  return (
    <span
      className="mt-0.5 inline-flex h-6 min-w-9 shrink-0 items-center justify-center rounded-md bg-muted px-1.5 font-mono text-xs tabular-nums text-muted-foreground"
      title={t("trends.score")}
      aria-label={`${t("trends.score")}: ${value}`}
      dir="ltr"
    >
      {value}
    </span>
  )
}

/** Save / review / dismiss, and their undo. `onChange` gets the updated topic. */
export function TopicActions({
  topic,
  onChange,
}: {
  topic: TrendTopic
  onChange: (t: TrendTopic) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  const saved = !!topic.saved_at
  const reviewed = !!topic.reviewed_at
  const dismissed = !!topic.dismissed_at

  async function patch(body: Record<string, boolean>, done: string) {
    setBusy(true)
    try {
      const res = await api<{ topic: TrendTopic }>(
        `/api/w/trends/${encodeURIComponent(topic.id)}`,
        { method: "PATCH", json: body, workspaceId: ws.id },
      )
      onChange(res.topic)
      toast.success(done)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const size = "sm"
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-1" data-topic-actions>
      <Button
        variant={saved ? "secondary" : "ghost"}
        size={size}
        aria-pressed={saved}
        disabled={busy}
        onClick={() =>
          patch({ saved: !saved }, saved ? t("trends.done.unsaved") : t("trends.done.saved"))
        }
        data-action="save"
      >
        {saved ? <BookmarkCheckIcon className="text-primary" /> : <BookmarkIcon />}
        {saved ? t("trends.topicSaved") : t("trends.topicSave")}
      </Button>
      <Button
        variant={reviewed ? "ghost" : "outline"}
        size={size}
        aria-pressed={reviewed}
        disabled={busy}
        onClick={() =>
          patch(
            { reviewed: !reviewed },
            reviewed ? t("trends.done.unreviewed") : t("trends.done.reviewed"),
          )
        }
        data-action="review"
      >
        {reviewed ? <RotateCcwIcon /> : <CheckIcon />}
        {reviewed ? t("trends.unreview") : t("trends.review")}
      </Button>
      <Button
        variant="ghost"
        size={size}
        aria-pressed={dismissed}
        disabled={busy}
        onClick={() =>
          patch(
            { dismissed: !dismissed },
            dismissed ? t("trends.done.restored") : t("trends.done.dismissed"),
          )
        }
        data-action="dismiss"
      >
        {dismissed ? <RotateCcwIcon /> : <EyeOffIcon />}
        {dismissed ? t("trends.undismiss") : t("trends.dismiss")}
      </Button>
    </div>
  )
}

/** One item: its headline (a link out), its outlet, its API signal and its time. */
export function ItemLine({
  item,
  showSummary = false,
  titleless = false,
}: {
  item: NewsItem
  showSummary?: boolean
  titleless?: boolean
}) {
  const { t, formatNumber } = useTranslations()
  const when = useWhen()
  return (
    <div className="flex min-w-0 flex-col gap-0.5" data-item={item.id}>
      {titleless ? null : item.url ? (
        <a
          href={item.url}
          target="_blank"
          rel="noopener noreferrer"
          className="min-w-0 break-words text-sm underline-offset-2 hover:underline"
        >
          <bdi>{item.title}</bdi>
          <ExternalLinkIcon
            className="ms-1 inline size-3 align-baseline text-muted-foreground"
            aria-hidden
          />
        </a>
      ) : (
        <span className="min-w-0 break-words text-sm">
          <bdi>{item.title}</bdi>
        </span>
      )}
      {showSummary && item.summary ? (
        <p className="line-clamp-2 break-words text-sm text-muted-foreground">{item.summary}</p>
      ) : null}
      <span className="flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground">
        {titleless && item.url ? (
          // The topic's title is this item's headline already: the outlet is the link.
          <a
            href={item.url}
            target="_blank"
            rel="noopener noreferrer"
            className="font-medium underline-offset-2 hover:underline"
          >
            <bdi>{item.outlet || item.source_name || t("trends.unknownSource")}</bdi>
            <ExternalLinkIcon className="ms-1 inline size-3 align-baseline" aria-hidden />
          </a>
        ) : (
          <bdi className="font-medium">
            {item.outlet || item.source_name || t("trends.unknownSource")}
          </bdi>
        )}
        {item.provider === "hn" && item.rank > 0 ? (
          <>
            <span aria-hidden>·</span>
            <span>
              {t("trends.hnMeta", {
                rank: item.rank,
                points: formatNumber(item.points),
                comments: formatNumber(item.comments),
              })}
            </span>
          </>
        ) : null}
        {item.provider === "devto" && item.points > 0 ? (
          <>
            <span aria-hidden>·</span>
            <span>{t("trends.reason.devto", { points: formatNumber(item.points) })}</span>
          </>
        ) : null}
        <span aria-hidden>·</span>
        <time dateTime={item.published_at} title={new Date(item.published_at).toISOString()}>
          {when(item.published_at)}
        </time>
      </span>
    </div>
  )
}

/** One trending topic in the list. */
export function TopicCard({
  topic,
  href,
  onChange,
}: {
  topic: TrendTopic
  href: string
  onChange: (t: TrendTopic) => void
}) {
  const { t } = useTranslations()
  const more = topic.item_count - topic.items.length
  return (
    <li
      className="flex flex-col gap-3 px-6 py-5"
      data-trend-topic={topic.id}
      data-state={topic.state}
    >
      <div className="flex min-w-0 flex-col gap-3 lg:flex-row lg:items-start">
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div className="flex min-w-0 items-start gap-2.5">
            <Score value={topic.score} />
            <h3
              className={
                topic.state === "open"
                  ? "min-w-0 break-words text-base font-medium"
                  : "min-w-0 break-words text-base font-medium text-muted-foreground"
              }
            >
              <Link href={href} className="underline-offset-2 hover:underline" data-topic-link>
                <bdi>{topic.title}</bdi>
              </Link>
            </h3>
          </div>
          {topic.summary ? (
            <p
              className="line-clamp-3 break-words text-sm text-muted-foreground"
              data-topic-summary={topic.summary_source}
            >
              <bdi>{topic.summary}</bdi>
            </p>
          ) : null}
          <ReasonChips reasons={topic.reasons} />
        </div>
        <TopicActions topic={topic} onChange={onChange} />
      </div>
      <ul
        className="flex flex-col gap-2 border-s-2 border-border ps-3"
        aria-label={t("trends.coverage")}
      >
        {topic.items.map((item) => (
          <li key={item.id}>
            <ItemLine item={item} titleless={item.title === topic.title} />
          </li>
        ))}
      </ul>
      {more > 0 ? (
        <Link
          href={href}
          className="self-start text-xs text-muted-foreground underline-offset-2 hover:underline"
          data-topic-more
        >
          {t("trends.moreItems", { n: more })}
        </Link>
      ) : null}
    </li>
  )
}

/** A source's state as one badge. */
export function SourceStatus({ source }: { source: FeedSource }) {
  const { t } = useTranslations()
  const [variant, key] = !source.enabled
    ? (["outline", "off"] as const)
    : source.last_status === "failed"
      ? (["destructive", "failed"] as const)
      : source.last_status === "over_budget"
        ? (["secondary", "overBudget"] as const)
        : source.last_status === "ok"
          ? (["default", "ok"] as const)
          : (["outline", "waiting"] as const)
  return (
    <Badge
      variant={variant}
      data-source-status={source.enabled ? source.last_status || "waiting" : "off"}
    >
      {t(`trends.status.${key}`)}
    </Badge>
  )
}

export const INTERVALS = [360, 720, 1440] as const
