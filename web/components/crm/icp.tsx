"use client"

/*
  The lifecycle stage and the ideal-customer score as the CRM pages show them (MH-395).

  A score is never shown without what it is made of: the badge opens its reasons, and the
  reasons say, per dimension, what was checked, whether it held, and how much it moved the score
  (weight × how much of it is present). The reasons are rendered from their structured fields
  in the page's language; the API's English sentence is the fallback for a dimension this
  client does not know.
*/

import { CheckIcon, MinusIcon } from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { Bdi } from "@/components/bidi"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { Lifecycle, ScoreReason } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { numberFormat } from "@/lib/i18n-locale"
import { cn } from "@/lib/utils"

const stageVariant: Record<Exclude<Lifecycle, "">, "default" | "secondary" | "outline"> = {
  subscriber: "outline",
  lead: "secondary",
  qualified: "secondary",
  customer: "default",
}

export function LifecycleBadge({ stage }: { stage: Lifecycle }) {
  const { t } = useTranslations()
  if (!stage) return <span className="text-muted-foreground">—</span>
  return (
    <Badge variant={stageVariant[stage]} data-lifecycle={stage}>
      {t(`lifecycle.${stage}`)}
    </Badge>
  )
}

/** A signed contribution: +3, −1.5, 0. */
export function useSigned() {
  const { locale } = useTranslations()
  const f = numberFormat(locale, { maximumFractionDigits: 2, signDisplay: "exceptZero" })
  return (n: number) => f.format(n)
}

/** One reason, in the page's language. */
function reasonLine(r: ScoreReason, t: (k: string, v?: Record<string, string | number>) => string) {
  const matched = (r.matched ?? []).join(", ")
  switch (r.dimension) {
    case "industry":
    case "size":
    case "region":
      if (r.value > 0) return t("icp.reason.match", { value: r.detail })
      if (r.detail) return t("icp.reason.noMatch", { value: r.detail })
      return t("icp.reason.unknown")
    case "keywords":
      return r.value > 0 ? t("icp.reason.found", { words: matched }) : t("icp.reason.noneFound")
    default: {
      const key = `icp.signal.${r.dimension}.${r.value > 0 ? "yes" : "no"}`
      const s = t(key)
      return s === key ? r.text : s
    }
  }
}

function dimensionLabel(d: string, t: (k: string) => string) {
  const k = ["industry", "size", "region", "keywords"].includes(d)
    ? `icp.dim.${d}`
    : `icp.signal.${d}`
  const s = t(k)
  return s === k ? d : s
}

/** The reasons behind a score, biggest effect first. */
export function ScoreReasons({
  reasons,
  className,
}: {
  reasons: ScoreReason[]
  className?: string
}) {
  const { t } = useTranslations()
  const signed = useSigned()
  const sorted = [...reasons].sort((a, b) => Math.abs(b.contribution) - Math.abs(a.contribution))
  if (sorted.length === 0) return <p className="text-sm text-muted-foreground">{t("icp.noReasons")}</p>
  return (
    <ul className={cn("divide-y divide-nq-line", className)} data-score-reasons>
      {sorted.map((r) => (
        <li
          key={r.dimension}
          className="flex items-start gap-3 py-2 text-sm"
          data-reason={r.dimension}
        >
          <span
            className={cn(
              "mt-0.5 flex size-4 shrink-0 items-center justify-center [&_svg]:size-3.5",
              r.value > 0 ? "text-foreground" : "text-muted-foreground",
            )}
            aria-hidden
          >
            {r.value > 0 ? <CheckIcon /> : <MinusIcon />}
          </span>
          <span className="min-w-0 flex-1">
            <span className="block font-medium text-foreground">{dimensionLabel(r.dimension, t)}</span>
            <span className="block text-muted-foreground">
              <Bdi>{reasonLine(r, t)}</Bdi>
            </span>
          </span>
          <span
            className="shrink-0 text-end tabular-nums"
            title={t("icp.weightOf", { w: signed(r.weight) })}
          >
            <Ltr
              className={cn(
                "font-medium",
                r.contribution < 0
                  ? "text-nq-danger-text"
                  : r.contribution > 0
                    ? "text-foreground"
                    : "text-muted-foreground",
              )}
            >
              {signed(r.contribution)}
            </Ltr>
            <span className="block text-xs text-muted-foreground">
              {t("icp.ofWeight", { w: signed(r.weight) })}
            </span>
          </span>
        </li>
      ))}
    </ul>
  )
}

/** The score as a small meter; the number is the text, the bar only echoes it. */
export function ScoreMeter({ score }: { score: number | null }) {
  const { t, formatNumber } = useTranslations()
  if (score === null) return <span className="text-muted-foreground">{t("icp.unscored")}</span>
  return (
    <span className="inline-flex items-center gap-2" data-score={score}>
      <span className="w-7 text-end font-medium tabular-nums">{formatNumber(score)}</span>
      <span className="h-1.5 w-12 overflow-hidden rounded-full bg-nq-surface-soft" aria-hidden>
        <span
          className="block h-full rounded-full"
          style={{ width: `${Math.max(2, score)}%`, background: "var(--chart-1)" }}
        />
      </span>
    </span>
  )
}

/** The score in a list row: a button that opens its reasons. */
export function ScoreButton({
  score,
  reasons,
  who,
}: {
  score: number | null
  reasons: ScoreReason[] | null
  who: string
}) {
  const { t } = useTranslations()
  if (score === null) return <ScoreMeter score={null} />
  return (
    <Popover>
      <PopoverTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-1.5"
            aria-label={t("icp.whyScore", { who, score })}
            data-score-button={score}
          />
        }
      >
        <ScoreMeter score={score} />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80">
        <p className="text-sm font-medium">{t("icp.scoreTitle", { score })}</p>
        <p className="pb-1 text-xs text-muted-foreground">{t("icp.scoreHow")}</p>
        <ScoreReasons reasons={reasons ?? []} />
      </PopoverContent>
    </Popover>
  )
}
