"use client"

import Link from "next/link"
import { MessageCircleWarningIcon } from "lucide-react"

import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"

/** Where to link a WhatsApp number, or who to ask — never a send that would 409. */
export function WhatsAppMissing() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  return (
    <p className="flex items-start gap-2 text-sm text-grid-muted" role="status" data-whatsapp-missing>
      <MessageCircleWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
      {ws.can("inbox:manage") ? (
        <span>
          {t("campaigns.whatsappMissing")}{" "}
          <Link className="underline underline-offset-4" href={`/${locale}/w/${ws.slug}/inbox/whatsapp`}>
            {t("campaigns.whatsappLink")}
          </Link>
        </span>
      ) : (
        <span>{t("campaigns.whatsappMissingMember")}</span>
      )}
    </p>
  )
}
