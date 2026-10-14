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

type OAuthProvider = { id: string; label: string; href: string }

const KNOWN: Record<string, string> = { google: "Google", github: "GitHub", apple: "Apple" }

// Redirect codes the API puts in ?oauth_error= when a provider sign-in fails.
const OAUTH_ERRORS = [
  "failed",
  "cancelled",
  "expired",
  "account_suspended",
  "unavailable",
  "no_email",
  "email_unverified",
]

/*
  GET /api/auth/methods lists what the togo auth plugin has enabled. Its exact shape is read
  defensively — strings ("password", "oauth:google") or objects ({ type, provider, url }). Only a
  listed oauth provider renders a button; with none listed, nothing renders (no dead controls).
*/
function oauthProviders(data: unknown): OAuthProvider[] {
  const list: unknown[] = Array.isArray(data)
    ? data
    : data && typeof data === "object"
      ? (((data as Record<string, unknown>).methods ??
          (data as Record<string, unknown>).oauth ??
          (data as Record<string, unknown>).providers ??
          []) as unknown[])
      : []
  if (!Array.isArray(list)) return []

  const out: OAuthProvider[] = []
  for (const item of list) {
    let id = ""
    let href = ""
    if (typeof item === "string") {
      const m = /^(?:oauth[:/])?(google|github|apple)$/i.exec(item)
      if (m) id = m[1].toLowerCase()
    } else if (item && typeof item === "object") {
      const o = item as Record<string, unknown>
      const name = String(o.provider ?? o.name ?? o.id ?? "").toLowerCase()
      const type = String(o.type ?? o.kind ?? "oauth").toLowerCase()
      if (type.includes("oauth") && KNOWN[name]) id = name
      href = typeof o.url === "string" ? o.url : typeof o.start_url === "string" ? o.start_url : ""
    }
    if (id && KNOWN[id] && !out.some((p) => p.id === id)) {
      out.push({ id, label: KNOWN[id], href: href || `/api/auth/oauth/${id}` })
    }
  }
  return out
}

function AppleMark() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className="size-4 fill-current">
      <path d="M16.37 1.43c0 1.14-.46 2.22-1.2 3-.8.84-2.1 1.5-3.16 1.41-.13-1.1.42-2.25 1.16-3.01.82-.86 2.2-1.5 3.2-1.4zM20.9 17.1c-.52 1.2-.77 1.73-1.44 2.79-.93 1.48-2.25 3.32-3.88 3.33-1.45.02-1.82-.94-3.79-.93-1.97.01-2.38.95-3.83.94-1.63-.02-2.88-1.68-3.81-3.16C1.5 16 1.23 10.9 2.84 8.42c1.14-1.76 2.95-2.79 4.65-2.79 1.73 0 2.82.95 4.25.95 1.39 0 2.23-.95 4.24-.95 1.51 0 3.11.82 4.25 2.24-3.73 2.04-3.12 7.37.67 9.23z" />
    </svg>
  )
}

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

  const methods = useSWR("/api/auth/methods", (p: string) => api<unknown>(p), {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  })
  const providers = oauthProviders(methods.data)

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

  // The API's own switches: "Forgot password?" only shows when it can actually send the mail.
  const appMethods = useSWR("/api/auth/app/methods", (p: string) => api<{ password_reset?: boolean }>(p), {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  })
  const canReset = mode === "login" && appMethods.data?.password_reset === true

  // A failed provider sign-in comes back to /login?oauth_error=<code>.
  const oauthCode = params.get("oauth_error")
  const oauthError = oauthCode
    ? t(`auth.oauthError.${OAUTH_ERRORS.includes(oauthCode) ? oauthCode : "failed"}`)
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
              {canReset ? (
                <Link
                  href={`/${locale}/forgot-password`}
                  className="text-caption text-muted-foreground underline underline-offset-4 hover:text-foreground"
                  data-forgot-link
                >
                  {t("auth.forgotPassword")}
                </Link>
              ) : null}
            </Field>
            {error || hubError || oauthError ? (
              <FieldError role="alert">{error ?? hubError ?? oauthError}</FieldError>
            ) : null}
            {mode === "login" && params.get("reset") === "1" ? (
              <p className="text-caption text-muted-foreground" role="status" data-reset-done>
                {t("auth.resetDone")}
              </p>
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
            {providers.map((p) => (
              <Button
                key={p.id}
                variant="secondary"
                size="lg"
                // Apple's guideline: a black button, white mark and label.
                className={
                  p.id === "apple"
                    ? "w-full border-black bg-black text-white hover:bg-black/85 hover:text-white"
                    : "w-full"
                }
                nativeButton={false}
                data-oauth-provider={p.id}
                render={
                  <a
                    href={`${p.href}${p.href.includes("?") ? "&" : "?"}next=${encodeURIComponent(next)}`}
                  />
                }
              >
                {p.id === "apple" ? <AppleMark /> : null}
                {p.id === "apple"
                  ? t("auth.continueWithApple")
                  : t("auth.continueWith", { provider: p.label })}
              </Button>
            ))}
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
