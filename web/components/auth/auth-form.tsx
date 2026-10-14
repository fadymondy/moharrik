"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import useSWR, { useSWRConfig } from "swr"

import {
  AuthFooter,
  AuthLayout,
  Button,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  OAuthDivider,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"
import { api, ApiError, resetCsrf } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { safeNext } from "@/lib/use-require-auth"
import { useDocumentTitle } from "@/lib/title"
import { SocialButtons, useSocialError, useSocialProviders } from "@/components/auth/social-buttons"

export function AuthForm({ mode }: { mode: "login" | "register" }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const params = useSearchParams()
  const { mutate } = useSWRConfig()
  // MH-1108: /{locale} opens the last workspace's dashboard.
  const next = safeNext(params.get("next"), `/${locale}`)

  useDocumentTitle(mode === "login" ? t("auth.signIn") : t("auth.createAccount"))

  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // Sign in with Apple / Google: only the configured providers render (social-buttons.tsx).
  const { providers } = useSocialProviders()
  const socialError = useSocialError(params.get("social_error"))

  // Sign in with CircleXO: the API says whether the hub is configured; the button only exists
  // when it is (no dead control). The link is a full navigation to the API's login route.
  const hub = useSWR("/api/circlexo/config", (p: string) => api<{ enabled?: boolean }>(p), {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  })
  const hubEnabled = mode === "login" && hub.data?.enabled === true
  const hubCode = params.get("circlexo_error")
  const hubError = hubCode
    ? [
        "failed",
        "email_unverified",
        "account_conflict",
        "no_email",
        "not_member",
        "unavailable",
      ].includes(hubCode)
      ? t(`auth.circlexoError.${hubCode}`)
      : t("auth.circlexoError.failed")
    : null

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (mode === "register" && password.length < 8) {
      setError(t("auth.passwordTooShort"))
      return
    }
    setBusy(true)
    try {
      resetCsrf()
      await api(`/api/auth/${mode}`, { json: { email: email.trim(), password } })
      // A new session carries a new CSRF token.
      resetCsrf()
      await mutate("/api/auth/me")
      router.replace(next)
    } catch (err) {
      if (err instanceof ApiError) {
        if (mode === "login" && err.status === 401) setError(t("auth.invalidCredentials"))
        else if (mode === "register" && /registration failed/i.test(err.message))
          setError(t("auth.registrationFailed"))
        else setError(err.message)
      } else {
        setError(t("common.networkError"))
      }
    } finally {
      setBusy(false)
    }
  }

  const other = mode === "login" ? "register" : "login"
  const otherHref = `/${locale}/${other}${params.get("next") ? `?next=${encodeURIComponent(next)}` : ""}`

  return (
    <AuthLayout
      title={mode === "login" ? t("auth.signIn") : t("auth.createAccount")}
      description={mode === "login" ? t("auth.signInHint") : t("auth.registerHint")}
      footer={
        <AuthFooter
          end={
            <div className="flex items-center gap-1">
              <LanguageSwitcher />
              <ThemeToggle />
            </div>
          }
        />
      }
    >
      <div className="w-full" data-auth-form>
        <form onSubmit={onSubmit} noValidate>
          <FieldGroup>
            <Field invalid={!!!!error}>
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
            <Field invalid={!!!!error}>
              <FieldLabel htmlFor="password">{t("auth.password")}</FieldLabel>
              <Input
                id="password"
                type="password"
                dir="ltr"
                autoComplete={mode === "login" ? "current-password" : "new-password"}
                required
                minLength={mode === "register" ? 8 : undefined}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              {mode === "register" ? (
                <FieldDescription>{t("auth.passwordHint")}</FieldDescription>
              ) : null}
            </Field>
            {error || hubError || socialError ? (
              <FieldError role="alert" data-auth-error>
                {error ?? hubError ?? socialError}
              </FieldError>
            ) : null}
            <Button type="submit" size="lg" disabled={busy || !email || !password}>
              {busy
                ? t("common.working")
                : mode === "login"
                  ? t("auth.signIn")
                  : t("auth.createAccount")}
            </Button>
          </FieldGroup>
        </form>

        {providers.length > 0 || hubEnabled ? (
          <div className="mt-6 space-y-3">
            <OAuthDivider>{t("auth.or")}</OAuthDivider>
            <SocialButtons providers={providers} next={next} />
            {hubEnabled ? (
              <Button
                variant="secondary"
                size="lg"
                className="w-full"
                nativeButton={false}
                data-circlexo-login
                render={<a href={`/auth/circlexo/login?return_to=${encodeURIComponent(next)}`} />}
              >
                {t("auth.circlexo")}
              </Button>
            ) : null}
          </div>
        ) : null}
        <p className="mt-6 text-center text-sm text-muted-foreground">
          {mode === "login" ? t("auth.noAccount") : t("auth.haveAccount")}{" "}
          <Link
            href={otherHref}
            className="font-medium text-foreground underline underline-offset-4"
          >
            {mode === "login" ? t("auth.createAccount") : t("auth.signIn")}
          </Link>
        </p>
      </div>
    </AuthLayout>
  )
}
