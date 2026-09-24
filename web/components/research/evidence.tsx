"use client"

import Link from "next/link"
import { CheckIcon, CircleDashedIcon, ExternalLinkIcon, LoaderCircleIcon, MinusIcon, TriangleAlertIcon } from "lucide-react"

import { Bdi } from "@/components/bidi"
import { useFormatDateTime } from "@/components/research/shared"
import { Badge } from "@/components/ui/badge"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { Evidence, ResearchCost, ResearchStage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/** The kind of news a quote reports. */
export function SignalBadge({ signal }: { signal: string }) {
  const { t } = useTranslations()
  if (!signal) return null
  return (
    <Badge variant="secondary" data-signal={signal}>
      {t(`research.signal.${signal}`)}
    </Badge>
  )
}

function hostOf(url: string) {
  try {
    return new URL(url).host.replace(/^www\./, "")
  } catch {
    return url
  }
}

/**
 * One piece of evidence: the page's own sentence first, then who it is about,
 * where it came from and how it was read. The source link opens the real page.
 */
export function EvidenceItem({ e, showRun = false }: { e: Evidence; showRun?: boolean }) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const byModel = e.extractor.startsWith("model:")
  return (
    <li className="space-y-2 px-6 py-4" data-evidence={e.id} data-evidence-signal={e.signal_type || "none"}>
      {e.claim ? (
        <p className="text-sm font-medium" dir="auto">
          {e.claim}
        </p>
      ) : null}
      <blockquote className="border-s-2 border-line ps-3 text-sm leading-relaxed text-grid-body" dir="auto" data-quote>
        {e.quote}
      </blockquote>
      <div className="flex flex-wrap items-center gap-2 text-xs text-grid-muted">
        {e.company_name ? (
          <Badge variant="outline" data-evidence-company={e.company_name}>
            <Bdi>{e.company_name}</Bdi>
          </Badge>
        ) : null}
        <SignalBadge signal={e.signal_type} />
        <span>{t("research.confidence", { n: formatNumber(Math.round(e.confidence * 100)) })}</span>
        <span aria-hidden>·</span>
        <span>{byModel ? t("research.byModel", { model: e.extractor.slice(6) }) : t("research.byHeuristic")}</span>
      </div>
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-grid-muted">
        <a
          href={e.source_url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex min-w-0 items-center gap-1 font-medium text-grid-brand hover:underline"
          data-evidence-source
          title={e.source_url}
        >
          <ExternalLinkIcon className="size-3.5 shrink-0" aria-hidden />
          <bdi className="truncate">{e.source_title || hostOf(e.source_url)}</bdi>
        </a>
        <span dir="ltr">({hostOf(e.source_url)})</span>
        {e.published_at ? (
          <>
            <span aria-hidden>·</span>
            <span>{t("research.publishedOn", { date: fmt(e.published_at) })}</span>
          </>
        ) : null}
        <span aria-hidden>·</span>
        <span>{t("research.capturedOn", { date: fmt(e.captured_at) })}</span>
        {showRun && e.run_topic ? (
          <>
            <span aria-hidden>·</span>
            <Link href={`/${locale}/w/${ws.slug}/research/${encodeURIComponent(e.run_id)}`} className="hover:underline">
              {t("research.fromRun", { topic: e.run_topic })}
            </Link>
          </>
        ) : null}
      </div>
    </li>
  )
}

const STAGE_ICON = {
  pending: <CircleDashedIcon className="text-grid-muted" />,
  running: <LoaderCircleIcon className="animate-spin text-grid-brand" />,
  done: <CheckIcon className="text-grid-brand" />,
  skipped: <MinusIcon className="text-grid-muted" />,
  failed: <TriangleAlertIcon className="text-grid-danger" />,
} as const

/** A run's stages, in order, each with its count and what it said. */
export function StageList({ stages }: { stages: ResearchStage[] }) {
  const { t, formatNumber } = useTranslations()
  if (!stages.length) return null
  return (
    <ol className="grid gap-px border-y border-line bg-line sm:grid-cols-5" data-stages>
      {stages.map((s) => (
        <li key={s.stage} className="bg-background px-4 py-3 text-sm" data-stage={s.stage} data-stage-status={s.status}>
          <p className="flex items-center gap-1.5 font-medium [&>svg]:size-4 [&>svg]:shrink-0">
            {STAGE_ICON[s.status] ?? STAGE_ICON.pending}
            <span>{t(`research.stage.${s.stage}`)}</span>
            {s.status === "done" ? <span className="ms-auto tabular-nums text-grid-muted">{formatNumber(s.count)}</span> : null}
          </p>
          <p className="mt-1 text-xs text-grid-muted">{t(`research.stageStatus.${s.status}`)}</p>
          {s.note ? (
            <p className="mt-1 text-xs break-words text-grid-muted" dir="auto">
              {s.note}
            </p>
          ) : null}
        </li>
      ))}
    </ol>
  )
}

/** What the run used — units, never a price. */
export function CostLine({ cost }: { cost: ResearchCost }) {
  const { t, formatNumber } = useTranslations()
  const n = (v: number | undefined) => formatNumber(v ?? 0)
  return (
    <p className="px-6 pt-3 text-xs text-grid-muted" data-cost>
      <span className="font-medium text-foreground">{t("research.cost")}: </span>
      {t("research.costLine", {
        queries: n(cost.search_queries),
        read: n(cost.pages_read),
        tried: n(cost.pages_tried),
        calls: n(cost.model_calls),
        paid: n(cost.paid_calls),
        tin: n(cost.input_tokens),
        tout: n(cost.output_tokens),
      })}
      <span className="block">{t("research.costNote")}</span>
    </p>
  )
}
