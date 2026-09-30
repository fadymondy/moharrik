"use client"

import { Badge } from "@fadymondy/nasaq/web"
import type { Lead, LeadAttribution, LeadStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const statusVariant: Record<LeadStatus, "brand" | "neutral" | "outline" | "danger"> = {
  new: "brand",
  contacted: "neutral",
  qualified: "neutral",
  converted: "outline",
  spam: "danger",
}

export function LeadStatusBadge({ status, className }: { status: LeadStatus; className?: string }) {
  const { t } = useTranslations()
  return (
    <Badge variant={statusVariant[status]} className={className} data-lead-badge={status}>
      {t(`leads.status.${status}`)}
    </Badge>
  )
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, "")
  } catch {
    return ""
  }
}

/**
 * Where a lead came from, in one phrase: the campaign source (and medium) when
 * the snippet saw UTM parameters, "Google Ads" for a gclid alone, else the
 * referring site, else direct.
 */
/** What leadSource reads: a whole Lead, or the overview's slimmer row. */
type SourceInput = Pick<Lead, "origin"> & {
  attribution: Pick<LeadAttribution, "utm_source" | "utm_medium" | "gclid" | "referrer">
}

export function leadSource(
  lead: SourceInput,
  t: (key: string, vars?: Record<string, string | number>) => string,
): string {
  const a = lead.attribution
  if (a.utm_source) return a.utm_medium ? `${a.utm_source} / ${a.utm_medium}` : a.utm_source
  if (a.gclid) return t("leads.source.googleAds")
  const ref = hostOf(a.referrer)
  if (ref && ref !== hostOf(lead.origin)) return ref
  return t("leads.source.direct")
}
