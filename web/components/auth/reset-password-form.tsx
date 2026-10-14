"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { useRouter, useSearchParams } from "next/navigation"

import { Button, Field, FieldDescription, FieldLabel, Input } from "@fadymondy/nasaq/web"
import { AuthShell } from "@/components/auth/auth-shell"
import { FieldError, FieldGroup } from "@/components/form-layout"
import { api, ApiError, resetCsrf } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export function ResetPasswordForm() {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const token = useSearchParams().get("token") ?? ""
  useDocumentTitle(t("auth.resetTitle"))

  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [linkBad, setLinkBad] = useState(false)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (password.length < 8) {
      setError(t("auth.passwordTooShort"))
      return
    }
    if (password !== confirm) {
      setError(t("auth.passwordMismatch"))
      return
    }
    setBusy(true)
    try {
      await api("/api/auth/password/reset", { json: { token, password } })
      // A reset signs the account out everywhere, so the old CSRF token is gone too.
      resetCsrf()
      router.replace(`/${locale}/login?reset=1`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 400) setLinkBad(true)
      else if (err instanceof ApiError && err.status === 422)
        setError(
          /too_long/.test(err.message) ? t("auth.passwordTooLong") : t("auth.passwordTooShort"),
        )
      else setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  if (!token || linkBad) {
    return (
      <AuthShell title={t("auth.resetTitle")}>
        <p className="text-sm text-nq-danger-text" role="alert">
          {t("auth.linkInvalid")}
        </p>
        <p className="mt-6 text-center text-sm">
          <Link
            href={`/${locale}/forgot-password`}
            className="font-medium text-foreground underline underline-offset-4"
          >
            {t("auth.requestNewLink")}
          </Link>
        </p>
      </AuthShell>
    )
  }

  return (
    <AuthShell title={t("auth.resetTitle")} description={t("auth.resetHint")}>
      <form onSubmit={onSubmit} noValidate>
        <FieldGroup>
          <Field invalid={!!error}>
            <FieldLabel htmlFor="password">{t("auth.newPassword")}</FieldLabel>
            <Input
              id="password"
              type="password"
              dir="ltr"
              autoComplete="new-password"
              required
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <FieldDescription>{t("auth.passwordHint")}</FieldDescription>
          </Field>
          <Field invalid={!!error}>
            <FieldLabel htmlFor="confirm">{t("auth.confirmPassword")}</FieldLabel>
            <Input
              id="confirm"
              type="password"
              dir="ltr"
              autoComplete="new-password"
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </Field>
          {error ? <FieldError role="alert">{error}</FieldError> : null}
          <Button type="submit" size="lg" disabled={busy || !password || !confirm}>
            {busy ? t("common.working") : t("auth.resetPassword")}
          </Button>
        </FieldGroup>
      </form>
    </AuthShell>
  )
}
