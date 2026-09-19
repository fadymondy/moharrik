"use client"

import type { ReactNode } from "react"
import { ThemeProvider, useTheme } from "next-themes"
import { Toaster } from "sonner"

import { I18nProvider } from "@/lib/i18n"
import { dirForLocale } from "@/lib/i18n-locale"

function ThemedToaster({ locale }: { locale: string }) {
  const { resolvedTheme } = useTheme()
  return (
    <Toaster
      theme={resolvedTheme === "dark" ? "dark" : "light"}
      dir={dirForLocale(locale)}
      position={dirForLocale(locale) === "rtl" ? "bottom-left" : "bottom-right"}
      toastOptions={{
        style: {
          background: "var(--grid-elevated)",
          color: "var(--grid-fg)",
          border: "1px solid var(--grid-elevated-line)",
        },
      }}
    />
  )
}

export function Providers({ locale, children }: { locale: string; children: ReactNode }) {
  return (
    <ThemeProvider attribute="class" defaultTheme="system" enableSystem disableTransitionOnChange>
      <I18nProvider locale={locale}>
        {children}
        <ThemedToaster locale={locale} />
      </I18nProvider>
    </ThemeProvider>
  )
}
