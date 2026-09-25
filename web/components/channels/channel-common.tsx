"use client"

import { CheckCircle2Icon, XCircleIcon } from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { useTranslations } from "@/lib/i18n"

/** The last test-connection result, the same shape the SMTP settings page uses. */
export function ChannelTestBadge({
  ok,
  at,
  error,
}: {
  ok: boolean | null
  at: string | null
  error: string
}) {
  const { t, formatDate } = useTranslations()
  if (ok === null) {
    return <span className="text-xs text-grid-muted">{t("channels.neverTested")}</span>
  }
  return (
    <span
      className={`inline-flex items-center gap-1 text-xs ${ok ? "text-grid-muted" : "text-grid-danger-text"}`}
      title={error || undefined}
      data-test-ok={ok ? "yes" : "no"}
    >
      {ok ? (
        <CheckCircle2Icon className="size-3.5 text-grid-brand" />
      ) : (
        <XCircleIcon className="size-3.5" />
      )}
      {ok ? t("channels.testOk") : t("channels.testFailed")}
      {at ? (
        <>
          {" · "}
          <Ltr>{formatDate(at)}</Ltr>
        </>
      ) : null}
    </span>
  )
}
