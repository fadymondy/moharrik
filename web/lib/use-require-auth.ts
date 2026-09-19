"use client"

import { useEffect } from "react"
import { usePathname, useRouter } from "next/navigation"

import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"

/**
 * Client-side guard: a session cookie that the API rejects (401) sends the visitor to login and
 * back here afterwards. proxy.ts already redirects when there is no cookie at all.
 */
export function useRequireAuth() {
  const me = useMe()
  const router = useRouter()
  const pathname = usePathname()
  const { locale } = useTranslations()

  useEffect(() => {
    if (me.data === null) {
      const next = pathname + window.location.search
      router.replace(`/${locale}/login?next=${encodeURIComponent(next)}`)
    }
  }, [me.data, pathname, locale, router])

  return me
}

/** Only same-app relative paths are honoured as a post-login destination. */
export function safeNext(next: string | null | undefined, fallback: string): string {
  if (!next || !next.startsWith("/") || next.startsWith("//") || next.startsWith("/\\")) return fallback
  return next
}
