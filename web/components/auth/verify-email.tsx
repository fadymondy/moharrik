"use client"

import Link from "next/link"
import { useEffect, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import { CheckCircle2Icon } from "lucide-react"

import { Button } from "@fadymondy/nasaq/web"
import { AuthShell } from "@/components/auth/auth-shell"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

type State =
  { kind: "working" } | { kind: "ok"; email: string } | { kind: "failed"; message: string }

/** The landing page of the mailed confirmation link: it confirms on load and says how it went. */
export function VerifyEmail() {
  const { t, locale } = useTranslations()
  const token = useSearchParams().get("token") ?? ""
  useDocumentTitle(t("auth.verifyTitle"))
  const [state, setState] = useState<State>({ kind: "working" })
  // The token is single-use: dev's double effect must not spend it twice.
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true
    if (!token) {
      setState({ kind: "failed", message: t("auth.linkInvalid") })
      return
    }
    api<{ email: string }>("/api/auth/email/verify", { json: { token } })
      .then((res) => setState({ kind: "ok", email: res.email }))
      .catch((err) =>
        setState({
          kind: "failed",
          message:
            err instanceof ApiError
              ? err.status === 400
                ? t("auth.linkInvalid")
                : err.message
              : t("common.networkError"),
        }),
      )
  }, [token, t])

  return (
    <AuthShell title={t("auth.verifyTitle")}>
      {state.kind === "working" ? (
        <p className="text-sm text-muted-foreground" role="status">
          {t("common.working")}
        </p>
      ) : state.kind === "ok" ? (
        <div className="space-y-4">
          <p className="flex items-start gap-2 text-sm" role="status" data-verify-ok>
            <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-nq-brand" />
            <span>
              {t("auth.verifyDone")}{" "}
              <bdi dir="ltr" className="font-medium">
                {state.email}
              </bdi>
            </span>
          </p>
          <Button
            size="lg"
            className="w-full"
            nativeButton={false}
            render={<Link href={`/${locale}`} />}
          >
            {t("auth.continue")}
          </Button>
        </div>
      ) : (
        <div className="space-y-4">
          <p className="text-sm text-nq-danger-text" role="alert" data-verify-failed>
            {state.message}
          </p>
          <p className="text-sm text-muted-foreground">{t("auth.verifyFailedHint")}</p>
          <Button
            variant="secondary"
            size="lg"
            className="w-full"
            nativeButton={false}
            render={<Link href={`/${locale}/login`} />}
          >
            {t("auth.signIn")}
          </Button>
        </div>
      )}
    </AuthShell>
  )
}
