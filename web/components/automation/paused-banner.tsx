"use client"

import { OctagonXIcon } from "lucide-react"

import { useTranslations } from "@/lib/i18n"

/** Automation is stopped workspace-wide: said on every automation page, not just Browsers where the switch lives. */
export function PausedBanner({ reason }: { reason?: string }) {
  const { t } = useTranslations()
  return (
    <div
      className="mx-6 mb-6 flex items-start gap-2 border border-nq-danger-text/40 bg-nq-danger-text/5 px-4 py-3 text-sm"
      role="status"
      data-paused="true"
    >
      <OctagonXIcon className="mt-0.5 size-4 shrink-0 text-nq-danger-text" />
      <div>
        <p className="font-medium">{t("automation.pausedTitle")}</p>
        <p className="text-muted-foreground">{reason || t("automation.pausedBody")}</p>
      </div>
    </div>
  )
}
