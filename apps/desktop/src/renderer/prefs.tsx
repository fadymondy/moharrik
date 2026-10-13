// The app's preferences (main process prefs.json): theme, language and the notification toggles.
// Language sets the document's dir, so the whole window flips for Arabic without a restart.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react"

import { isRTL, palette, translator, type Locale, type T } from "@moharrik/client"

import type { Prefs } from "../shared/types"
import { invoke, on } from "./bridge"

interface Ctx {
  prefs: Prefs
  set: (p: Partial<Prefs>) => Promise<void>
  t: T
  locale: Locale
  rtl: boolean
}

const PrefsCtx = createContext<Ctx | null>(null)

export function PrefsProvider({ initial, children }: { initial: Prefs; children: ReactNode }) {
  const [prefs, setPrefs] = useState(initial)
  useEffect(() => on<[Prefs]>("prefs", setPrefs), [])
  const dark = useDark(prefs.theme)
  useEffect(() => {
    const el = document.documentElement
    el.lang = prefs.locale
    el.dir = isRTL(prefs.locale) ? "rtl" : "ltr"
    el.dataset.theme = dark ? "dark" : "light"
    // The brand palette (shared/brand.ts, the same values as the phone) as CSS variables.
    for (const [k, v] of Object.entries(palette(dark ? "dark" : "light"))) el.style.setProperty(`--${k}`, v)
  }, [prefs.locale, dark])
  const value = useMemo<Ctx>(
    () => ({
      prefs,
      set: async (p) => setPrefs(await invoke<Prefs>("app:setPrefs", p)),
      t: translator(prefs.locale),
      locale: prefs.locale,
      rtl: isRTL(prefs.locale),
    }),
    [prefs],
  )
  return <PrefsCtx.Provider value={value}>{children}</PrefsCtx.Provider>
}

function useDark(theme: Prefs["theme"]): boolean {
  const mq = useMemo(() => window.matchMedia("(prefers-color-scheme: dark)"), [])
  const [system, setSystem] = useState(mq.matches)
  useEffect(() => {
    const f = () => setSystem(mq.matches)
    mq.addEventListener("change", f)
    return () => mq.removeEventListener("change", f)
  }, [mq])
  return theme === "system" ? system : theme === "dark"
}

export function usePrefs(): Ctx {
  const c = useContext(PrefsCtx)
  if (!c) throw new Error("usePrefs outside PrefsProvider")
  return c
}
