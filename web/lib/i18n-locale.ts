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

/**
 * BCP-47 tag for ALL Intl formatting (numbers, money, dates, relative times): every
 * Intl.NumberFormat / DateTimeFormat / RelativeTimeFormat in the app takes its tag from here.
 * Arabic keeps ar-EG conventions (month and am/pm words, RTL marks) but WESTERN digits
 * (-u-nu-latn), decided 2026-09-23: Arabic-Indic digits next to Latin currency codes and
 * English names read as a jumble ("٦٤٬٦٠٠٫٠٠ US$"), and their zero "٠" looks like a dot, so a
 * "0 unread" tile read as blank. With latn the separators follow too: "64,600.00 US$".
 */
const INTL_TAGS: Record<Locale, string> = { en: "en-US", ar: "ar-EG-u-nu-latn" }

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

// ── formatting ───────────────────────────────────────────────────────────
// The ONE place numbers, money and dates are formatted: every call goes through these, so
// both locales get Western digits (see INTL_TAGS) and the same clean output.
//
// Intl's Arabic output carries bidi control marks (U+200F RLM, U+061C ALM) around separators:
// "07‏/10‏/2026". The app shows figures inside an LTR isolate (<Ltr>) so they read the same in
// both directions, and inside LTR those marks reorder the pieces: that date rendered as
// "072026/10/". The isolate already does the marks' job, so they are stripped here.
const BIDI_MARKS = /[‎‏؜]/g
const clean = (s: string) => s.replace(BIDI_MARKS, "")

export function numberFormat(locale: string, options?: Intl.NumberFormatOptions) {
  const f = new Intl.NumberFormat(intlTag(locale), options)
  return { format: (n: number) => clean(f.format(n)) }
}

export function dateFormat(locale: string, options?: Intl.DateTimeFormatOptions) {
  const f = new Intl.DateTimeFormat(intlTag(locale), options)
  return { format: (d: Date | number) => clean(f.format(d)) }
}

export function relativeTimeFormat(locale: string, options?: Intl.RelativeTimeFormatOptions) {
  const f = new Intl.RelativeTimeFormat(intlTag(locale), options)
  return { format: (v: number, unit: Intl.RelativeTimeFormatUnit) => clean(f.format(v, unit)) }
}
