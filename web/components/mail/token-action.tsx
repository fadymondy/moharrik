"use client"

import { Suspense, useState } from "react"
import { useSearchParams } from "next/navigation"
import { CheckCircle2Icon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

type Result = { status: string; list: string; workspace: string }

/**
 * The landing page for a link in a marketing email: double opt-in confirm, or unsubscribe.
 * It never acts on load — mail scanners open links, and a GET that confirmed would let a
 * scanner consent on someone's behalf. The visitor presses the button.
 */
export function MailTokenAction({ kind }: { kind: "confirm" | "unsubscribe" }) {
  return (
    <Suspense>
      <Inner kind={kind} />
    </Suspense>
  )
}

function Inner({ kind }: { kind: "confirm" | "unsubscribe" }) {
  const { t } = useTranslations()
  const token = useSearchParams().get("token") ?? ""
  const title = kind === "confirm" ? t("mail.confirmTitle") : t("mail.unsubscribeTitle")
  useDocumentTitle(title)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<Result | null>(null)

  async function act() {
    setBusy(true)
    setError(null)
    try {
      setDone(await api<Result>(`/api/mail/${kind}`, { json: { token } }))
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) setError(t("mail.invalid"))
      else setError(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  let body
  if (!token || token.length < 32)
    body = <p className="text-sm text-grid-danger-text">{t("mail.invalid")}</p>
  else if (done) {
    const ok = kind === "confirm" ? done.status === "subscribed" : done.status === "unsubscribed"
    body = ok ? (
      <p className="flex items-start gap-2 text-sm" role="status" data-mail-result={done.status}>
        <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-grid-brand" />
        {t(kind === "confirm" ? "mail.confirmed" : "mail.unsubscribed", {
          list: done.list,
          workspace: done.workspace,
        })}
      </p>
    ) : (
      <p className="text-sm text-grid-muted" role="status" data-mail-result={done.status}>
        {t("mail.notConfirmable", { status: t(`sub.${done.status}`) })}
      </p>
    )
  } else
    body = (
      <div className="space-y-4">
        <p className="text-sm text-grid-muted">
          {kind === "confirm" ? t("mail.confirmBody") : t("mail.unsubscribeBody")}
        </p>
        <Button onClick={act} disabled={busy} variant={kind === "confirm" ? "default" : "outline"}>
          {busy
            ? t("common.working")
            : kind === "confirm"
              ? t("mail.confirm")
              : t("mail.unsubscribe")}
        </Button>
        {error ? <p className="text-sm text-grid-danger-text">{error}</p> : null}
      </div>
    )

  return (
    <PublicFrame eyebrow={t("mail.micro")} title={title}>
      <PublicPanel className="flex justify-center py-10">
        <div className="w-full max-w-sm">{body}</div>
      </PublicPanel>
    </PublicFrame>
  )
}
