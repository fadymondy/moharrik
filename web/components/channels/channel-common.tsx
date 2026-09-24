"use client"

import { CheckCircle2Icon, XCircleIcon } from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { useTranslations } from "@/lib/i18n"

/** A setup page's step-by-step instructions (MH-563): plain numbered steps, en/ar. */
export function ChannelInstructions({ titleKey, steps }: { titleKey: string; steps: string[] }) {
  const { t } = useTranslations()
  return (
    <div className="mx-6 mb-6 border border-line bg-grid-card px-4 py-3 text-sm" data-setup-instructions>
      <p className="font-medium">{t(titleKey)}</p>
      <ol className="mt-2 list-decimal space-y-1 ps-5 text-grid-muted">
        {steps.map((k) => (
          <li key={k}>{t(k)}</li>
        ))}
      </ol>
    </div>
  )
}

/** The last test-connection result, the same shape the SMTP settings page uses. */
export function ChannelTestBadge({ ok, at, error }: { ok: boolean | null; at: string | null; error: string }) {
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
      {ok ? <CheckCircle2Icon className="size-3.5 text-grid-brand" /> : <XCircleIcon className="size-3.5" />}
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
