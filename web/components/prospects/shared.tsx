"use client"

import { Badge } from "@/components/ui/badge"
import type { ProspectReason, ProspectStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/** 0-100, as a number people read at a glance. */
export function ScoreBadge({ score }: { score: number }) {
  const { t, formatNumber } = useTranslations()
  const tone = score >= 70 ? "default" : score >= 40 ? "secondary" : "outline"
  return (
    <Badge
      variant={tone}
      className="tabular-nums"
      aria-label={`${t("prospects.score")} ${score}`}
      data-score={score}
    >
      {formatNumber(Math.round(score))}
    </Badge>
  )
}

export function StatusBadge({ status }: { status: ProspectStatus }) {
  const { t } = useTranslations()
  return (
    <Badge variant={status === "accepted" ? "default" : "outline"}>
      {t(`prospects.status.${status}`)}
    </Badge>
  )
}

/** A score's reasons, translated from their parts, each with its points. */
export function ReasonList({
  reasons,
  compact = false,
}: {
  reasons: ProspectReason[]
  compact?: boolean
}) {
  const { t, formatNumber, locale } = useTranslations()
  // Compact: the three that weigh most, plus anything that pulled the score
  // down or came from your own verdicts — those are what a list must show.
  const list = compact
    ? reasons.filter(
        (r, i) =>
          r.points < 0 ||
          r.dimension.startsWith("feedback") ||
          [...reasons]
            .sort((a, b) => Math.abs(b.points) - Math.abs(a.points))
            .slice(0, 3)
            .includes(reasons[i]),
      )
    : reasons
  const text = (r: ProspectReason) => {
    const items = (r.items ?? [])
      .map((i) =>
        r.dimension === "signals" || r.dimension.startsWith("feedback") ? signalLabel(t, i) : i,
      )
      .join(locale === "ar" ? "، " : ", ")
    return t(`prospects.reason.${r.dimension}`, { items, count: formatNumber(r.count) })
  }
  return (
    <ul
      className={
        compact ? "flex flex-wrap gap-x-3 gap-y-1 text-xs text-grid-muted" : "space-y-1.5 text-sm"
      }
      data-reasons
    >
      {list.map((r, i) => (
        <li key={i} className="flex items-baseline gap-2" data-reason={r.dimension}>
          <span
            className={`shrink-0 tabular-nums ${r.points < 0 ? "text-grid-danger-text" : "text-grid-ok-text"}`}
            dir="ltr"
          >
            {r.points > 0 ? "+" : ""}
            {formatNumber(Math.round(r.points))}
          </span>
          <span className={compact ? "" : "text-grid-body"}>{text(r)}</span>
        </li>
      ))}
    </ul>
  )
}

function signalLabel(t: (k: string, v?: Record<string, string | number>) => string, s: string) {
  const known = [
    "hiring",
    "funding",
    "launch",
    "expansion",
    "partnership",
    "leadership",
    "technology",
    "regulatory",
  ]
  return known.includes(s) ? t(`research.signal.${s}`) : s
}
