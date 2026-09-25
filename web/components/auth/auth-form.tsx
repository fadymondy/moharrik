"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import useSWR, { useSWRConfig } from "swr"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Separator } from "@/components/ui/separator"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { api, ApiError, resetCsrf } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { safeNext } from "@/lib/use-require-auth"
import { useDocumentTitle } from "@/lib/title"

type OAuthProvider = { id: string; label: string; href: string }

const KNOWN: Record<string, string> = { google: "Google", github: "GitHub" }

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
      const m = /^(?:oauth[:/])?(google|github)$/i.exec(item)
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

export function AuthForm({ mode }: { mode: "login" | "register" }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const params = useSearchParams()
  const { mutate } = useSWRConfig()
  const next = safeNext(params.get("next"), `/${locale}/workspaces`)

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
    <PublicFrame
      eyebrow={mode === "login" ? t("auth.welcomeBack") : t("auth.getStarted")}
      title={mode === "login" ? t("auth.signIn") : t("auth.createAccount")}
      description={mode === "login" ? t("auth.signInHint") : t("auth.registerHint")}
    >
      <PublicPanel className="flex justify-center py-10">
        <div className="w-full max-w-sm" data-auth-form>
          <form onSubmit={onSubmit} noValidate>
            <FieldGroup>
              <Field data-invalid={!!error || undefined}>
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
              <Field data-invalid={!!error || undefined}>
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
              {error ? <FieldError>{error}</FieldError> : null}
              <Button type="submit" size="lg" disabled={busy || !email || !password}>
                {busy
                  ? t("common.working")
                  : mode === "login"
                    ? t("auth.signIn")
                    : t("auth.createAccount")}
              </Button>
            </FieldGroup>
          </form>

          {providers.length > 0 ? (
            <div className="mt-6 space-y-3">
              <div className="flex items-center gap-3">
                <Separator className="flex-1" />
                <span className="grid-micro">{t("auth.or")}</span>
                <Separator className="flex-1" />
              </div>
              {providers.map((p) => (
                <Button
                  key={p.id}
                  variant="outline"
                  size="lg"
                  className="w-full"
                  nativeButton={false}
                  render={
                    <a
                      href={`${p.href}${p.href.includes("?") ? "&" : "?"}next=${encodeURIComponent(next)}`}
                    />
                  }
                >
                  {t("auth.continueWith", { provider: p.label })}
                </Button>
              ))}
            </div>
          ) : null}
          <p className="mt-6 text-center text-sm text-grid-muted">
            {mode === "login" ? t("auth.noAccount") : t("auth.haveAccount")}{" "}
            <Link
              href={otherHref}
              className="font-medium text-grid-fg underline underline-offset-4"
            >
              {mode === "login" ? t("auth.createAccount") : t("auth.signIn")}
            </Link>
          </p>
        </div>
      </PublicPanel>
    </PublicFrame>
  )
}
