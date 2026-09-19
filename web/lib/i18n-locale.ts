// Pure helpers with no React/client dependency, so both Server Components
// (app/layout.tsx, proxy.ts) and client code (lib/i18n.tsx) can import them.

export const LOCALES = ["en", "ar"] as const
export type Locale = (typeof LOCALES)[number]
export const DEFAULT_LOCALE: Locale = "en"
export const LOCALE_COOKIE = "NEXT_LOCALE"

const RTL_LOCALES = new Set<Locale>(["ar"])

/** Native name, for the language switcher. A language names itself. */
export const LOCALE_NAMES: Record<Locale, string> = {
  en: "English",
  ar: "العربية",
}

/** BCP-47 tag for Intl formatting. Arabic dates use ar-EG conventions. */
const INTL_TAGS: Record<Locale, string> = { en: "en-US", ar: "ar-EG" }

export function intlTag(locale: string): string {
  return INTL_TAGS[locale as Locale] ?? "en-US"
}

export function dirForLocale(locale: string): "ltr" | "rtl" {
  return RTL_LOCALES.has(locale as Locale) ? "rtl" : "ltr"
}

export function isLocale(value: string | undefined | null): value is Locale {
  return !!value && (LOCALES as readonly string[]).includes(value)
}

/** Picks the best supported locale from an Accept-Language header. */
export function localeFromAcceptLanguage(header: string | null): Locale | null {
  if (!header) return null
  const ranked = header
    .split(",")
    .map((part) => {
      const [tag, q] = part.trim().split(";q=")
      return { tag: tag.toLowerCase().split("-")[0], q: q ? Number(q) : 1 }
    })
    .sort((a, b) => b.q - a.q)
  for (const { tag } of ranked) if (isLocale(tag)) return tag
  return null
}
