"use client"

/*
  Sign in with Apple / Google (internal/social on the API).

  GET /api/auth/methods lists what the server has configured; only a listed provider renders a
  button (no dead controls). Apple comes first and both buttons are the same size — Apple's
  guideline 4.8 asks for Sign in with Apple to be at least as prominent as any other provider,
  and the web follows the apps. Each button is a full navigation to the API's start route, which
  redirects to the provider and comes back to `next` (any local path, e.g. the apps'
  /app-signin page).
*/

import useSWR from "swr"
import { siApple, siGoogle, type SimpleIcon } from "simple-icons"

import { Button } from "@fadymondy/nasaq/web"
import { api } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export type SocialProvider = "apple" | "google"

export const SOCIAL_LABEL: Record<SocialProvider, string> = { apple: "Apple", google: "Google" }

const ICON: Record<SocialProvider, SimpleIcon> = { apple: siApple, google: siGoogle }

/** The providers this server offers on the web, Apple first. */
export function useSocialProviders(): { providers: SocialProvider[]; isLoading: boolean } {
  const methods = useSWR("/api/auth/methods", (p: string) => api<unknown>(p), {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  })
  const list = (methods.data as { methods?: unknown } | undefined)?.methods
  const found = new Set<SocialProvider>()
  if (Array.isArray(list)) {
    for (const m of list) {
      if (!m || typeof m !== "object") continue
      const o = m as Record<string, unknown>
      const name = String(o.name ?? "").toLowerCase()
      if (String(o.type ?? "") === "oauth" && (name === "apple" || name === "google"))
        found.add(name)
    }
  }
  return {
    providers: (["apple", "google"] as const).filter((p) => found.has(p)),
    isLoading: methods.isLoading,
  }
}

/** The start URL for a provider round trip. */
export function socialHref(
  provider: SocialProvider,
  next: string,
  intent: "login" | "link" | "reauth" = "login",
): string {
  const q = new URLSearchParams({ next })
  if (intent !== "login") q.set("intent", intent)
  return `/api/auth/oauth/${provider}?${q.toString()}`
}

export function ProviderGlyph({ provider }: { provider: SocialProvider }) {
  const icon = ICON[provider]
  return (
    <svg viewBox="0 0 24 24" aria-hidden className="size-4 shrink-0 fill-current">
      <path d={icon.path} />
    </svg>
  )
}

const ERROR_CODES = [
  "failed",
  "cancelled",
  "no_email",
  "email_unverified",
  "account_conflict",
  "identity_taken",
  "already_linked",
  "reauth_mismatch",
  "suspended",
  "unavailable",
  "not_configured",
]

/** The message for ?social_error=<code> (null when there is none). */
export function useSocialError(code: string | null): string | null {
  const { t } = useTranslations()
  if (!code) return null
  return t(`auth.socialError.${ERROR_CODES.includes(code) ? code : "failed"}`)
}

export function SocialButtons({ providers, next }: { providers: SocialProvider[]; next: string }) {
  const { t } = useTranslations()
  if (providers.length === 0) return null
  return (
    <div className="space-y-3" data-social-buttons>
      {providers.map((p) => (
        <Button
          key={p}
          variant="secondary"
          size="lg"
          className="w-full"
          nativeButton={false}
          data-social-login={p}
          render={<a href={socialHref(p, next)} />}
        >
          <ProviderGlyph provider={p} />
          {p === "apple" ? t("auth.withApple") : t("auth.withGoogle")}
        </Button>
      ))}
    </div>
  )
}
