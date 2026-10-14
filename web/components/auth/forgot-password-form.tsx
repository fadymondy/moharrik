"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"

import { Button, Field, FieldLabel, Input } from "@fadymondy/nasaq/web"
import { AuthShell } from "@/components/auth/auth-shell"
import { FieldError, FieldGroup } from "@/components/form-layout"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export function ForgotPasswordForm() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("auth.forgotTitle"))

  const [email, setEmail] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [sent, setSent] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      await api("/api/auth/password/forgot", { json: { email: email.trim() } })
      setSent(true)
    } catch (err) {
      if (err instanceof ApiError && err.status === 503) setError(t("auth.mailUnavailable"))
      else if (err instanceof ApiError && err.status === 429) setError(t("auth.tooManyRequests"))
      else setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AuthShell title={t("auth.forgotTitle")} description={t("auth.forgotHint")}>
      {sent ? (
        <p className="text-sm" role="status" data-forgot-sent>
          {t("auth.forgotSent")}
        </p>
      ) : (
        <form onSubmit={onSubmit} noValidate>
          <FieldGroup>
            <Field invalid={!!error}>
              <FieldLabel htmlFor="email">{t("auth.email")}</FieldLabel>
              <Input
                id="email"
                type="email"
                dir="ltr"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@company.com"
              />
            </Field>
            {error ? <FieldError role="alert">{error}</FieldError> : null}
            <Button type="submit" size="lg" disabled={busy || !email.trim()}>
              {busy ? t("common.working") : t("auth.sendResetLink")}
            </Button>
          </FieldGroup>
        </form>
      )}
      <p className="mt-6 text-center text-sm">
        <Link
          href={`/${locale}/login`}
          className="font-medium text-foreground underline underline-offset-4"
        >
          {t("auth.backToSignIn")}
        </Link>
      </p>
    </AuthShell>
  )
}
