"use client"

import { InfoIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import type { ResearchList, ResearchStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

export const isLive = (status?: string) => status === "queued" || status === "running"

const TONE: Record<ResearchStatus, "default" | "secondary" | "outline" | "destructive"> = {
  queued: "secondary",
  running: "secondary",
  ok: "default",
  failed: "destructive",
  cancelled: "outline",
}

export function ResearchStatusBadge({ status }: { status: ResearchStatus }) {
  const { t } = useTranslations()
  return <Badge variant={TONE[status] ?? "outline"}>{t(`research.status.${status}`)}</Badge>
}

/** Date and time: a run is minutes long, so the day alone does not tell two apart. */
export function useFormatDateTime() {
  const { locale } = useTranslations()
  return (value: string | null | undefined) => {
    if (!value) return "—"
    const d = new Date(value)
    if (Number.isNaN(d.getTime())) return "—"
    return dateFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(d)
  }
}

/**
 * What this deployment cannot do, said plainly. Production has search and a
 * reader but no model, and a page that promised answers there would be lying.
 */
export function CapabilityNotes({ caps }: { caps: Pick<ResearchList, "can_search" | "can_read" | "can_summarise"> }) {
  const { t } = useTranslations()
  const notes: { key: string; text: string }[] = []
  if (!caps.can_search) notes.push({ key: "search", text: t("research.capNoSearch") })
  else {
    if (!caps.can_read) notes.push({ key: "read", text: t("research.capNoRead") })
    if (!caps.can_summarise) notes.push({ key: "summarise", text: t("research.capNoModel") })
  }
  if (notes.length === 0) return null
  return (
    <div className="space-y-2 px-6 pb-4" role="status">
      {notes.map((n) => (
        <p key={n.key} className="flex items-start gap-2 text-sm text-grid-muted" data-capability={n.key}>
          <InfoIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
          <span>{n.text}</span>
        </p>
      ))}
    </div>
  )
}
