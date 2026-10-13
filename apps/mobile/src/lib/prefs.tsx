// Language and appearance. Arabic-first: a phone set to Arabic, or to no language we know, gets
// Arabic; an English phone gets English. The layout direction follows the language at once — the
// root view sets `direction` (Yoga mirrors every row under it), so switching needs no restart.
import { getLocales } from "expo-localization"
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react"
import { useColorScheme } from "react-native"

import { isRTL, palette, translator, type Locale, type Palette, type T } from "@moharrik/client"

import { getJSON, KEYS, setJSON } from "./storage"

export type ThemePref = "system" | "light" | "dark"

interface Prefs {
  ready: boolean
  locale: Locale
  rtl: boolean
  t: T
  theme: ThemePref
  scheme: "light" | "dark"
  colors: Palette
  setLocale(l: Locale): void
  setTheme(t: ThemePref): void
}

const Ctx = createContext<Prefs | null>(null)

export function deviceLocale(): Locale {
  try {
    return getLocales()[0]?.languageCode === "en" ? "en" : "ar"
  } catch {
    return "ar"
  }
}

export function PrefsProvider({ children }: { children: ReactNode }) {
  const system = useColorScheme()
  const [ready, setReady] = useState(false)
  const [locale, setLocaleState] = useState<Locale>(deviceLocale)
  const [theme, setThemeState] = useState<ThemePref>("system")

  useEffect(() => {
    void (async () => {
      const [l, th] = await Promise.all([getJSON<Locale>(KEYS.locale), getJSON<ThemePref>(KEYS.theme)])
      if (l === "en" || l === "ar") setLocaleState(l)
      if (th === "light" || th === "dark" || th === "system") setThemeState(th)
      setReady(true)
    })()
  }, [])

  const value = useMemo<Prefs>(() => {
    const scheme = theme === "system" ? (system === "dark" ? "dark" : "light") : theme
    return {
      ready,
      locale,
      rtl: isRTL(locale),
      t: translator(locale),
      theme,
      scheme,
      colors: palette(scheme),
      setLocale(l) {
        setLocaleState(l)
        void setJSON(KEYS.locale, l)
      },
      setTheme(th) {
        setThemeState(th)
        void setJSON(KEYS.theme, th)
      },
    }
  }, [ready, locale, theme, system])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function usePrefs(): Prefs {
  const v = useContext(Ctx)
  if (!v) throw new Error("usePrefs outside PrefsProvider")
  return v
}
