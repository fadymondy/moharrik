"use client"

// Frontend i18n: flat key dictionaries per locale under web/lang/<locale>.json.
// Adding a language: add lang/<code>.json with the same keys as en.json,
// register it in DICTIONARIES and add the code to LOCALES in ./i18n-locale.ts.
import { createContext, useContext, useMemo, type ReactNode } from "react"
import { dateFormat, dirForLocale, isLocale, numberFormat, type Locale } from "@/lib/i18n-locale"
import en from "@/lang/en.json"
import ar from "@/lang/ar.json"

export { LOCALES, dirForLocale, isLocale } from "@/lib/i18n-locale"
export type { Locale } from "@/lib/i18n-locale"

type Dict = Record<string, string>
const DICTIONARIES: Record<Locale, Dict> = { en, ar }

/** Non-reactive lookup — for use outside React. */
export function trans(key: string, locale: string = "en"): string {
  const dict = DICTIONARIES[isLocale(locale) ? locale : "en"]
  return dict[key] ?? DICTIONARIES.en[key] ?? key
}

const I18nContext = createContext<Locale | null>(null)

export function I18nProvider({ locale: raw, children }: { locale: string; children: ReactNode }) {
  const locale = isLocale(raw) ? raw : "en"
  return <I18nContext.Provider value={locale}>{children}</I18nContext.Provider>
}

export function useTranslations() {
  const locale = useContext(I18nContext)
  if (!locale) throw new Error("useTranslations must be used inside <I18nProvider>")
  const dict = DICTIONARIES[locale]

  const t = useMemo(
    () => (key: string, vars?: Record<string, string | number>) => {
      let str = dict[key] ?? DICTIONARIES.en[key] ?? key
      if (vars) for (const [k, v] of Object.entries(vars)) str = str.replaceAll(`{${k}}`, String(v))
      return str
    },
    [dict],
  )

  const formatDate = useMemo(
    () => (value: string | null | undefined) => {
      if (!value) return "—"
      const d = new Date(value)
      if (Number.isNaN(d.getTime())) return "—"
      return dateFormat(locale, { dateStyle: "medium" }).format(d)
    },
    [locale],
  )

  const formatNumber = useMemo(() => {
    const nf = numberFormat(locale)
    return (n: number) => nf.format(n)
  }, [locale])

  const dir = dirForLocale(locale)
  return { t, locale, dir, isRtl: dir === "rtl", formatDate, formatNumber }
}
