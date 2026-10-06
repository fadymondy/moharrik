"use client"

import type { ReactNode } from "react"
import { usePathname } from "next/navigation"
import { ThemeProvider, useTheme } from "next-themes"
import { NasaqProvider, Toaster } from "@fadymondy/nasaq/web"

import { I18nProvider } from "@/lib/i18n"
import { LOCALES, LOCALE_COOKIE, LOCALE_NAMES, isLocale } from "@/lib/i18n-locale"

const NASAQ_LOCALES = LOCALES.map((l) => ({
  value: l,
  label: LOCALE_NAMES[l],
  dir: l === "ar" ? ("rtl" as const) : ("ltr" as const),
}))

/**
 * Nasaq owns the design tokens, brand and the document's data-theme/dir/lang; the URL owns the
 * locale and next-themes owns the (persisted) theme, so both are passed in controlled.
 */
function NasaqBridge({ locale, children }: { locale: string; children: ReactNode }) {
  const { theme, setTheme } = useTheme()
  const pathname = usePathname()

  function switchLocale(next: string) {
    if (!isLocale(next) || next === locale) return
    document.cookie = `${LOCALE_COOKIE}=${next}; path=/; max-age=31536000; samesite=lax`
    const parts = pathname.split("/")
    if (isLocale(parts[1])) parts[1] = next
    else parts.splice(1, 0, next)
    // A full navigation: the root layout sets <html lang dir> on the server.
    window.location.assign(parts.join("/") + window.location.search)
  }

  return (
    <NasaqProvider
      brand="moharrik"
      // Rounded corners (MH-1115); the default "grid" expression squares them.
      expression="native"
      density="compact"
      theme={(theme as "light" | "dark" | "system" | undefined) ?? undefined}
      onThemeChange={setTheme}
      locale={locale}
      onLocaleChange={switchLocale}
      locales={NASAQ_LOCALES}
    >
      {children}
      <Toaster />
    </NasaqProvider>
  )
}

export function Providers({ locale, children }: { locale: string; children: ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <I18nProvider locale={locale}>
        <NasaqBridge locale={locale}>{children}</NasaqBridge>
      </I18nProvider>
    </ThemeProvider>
  )
}
