"use client"

import { useEffect } from "react"

/**
 * Every route sets its own document title: "<page> · Moharrik" (محرّك on /ar).
 *
 * On a dynamic route Next streams the layout's static metadata AFTER the page
 * has hydrated, and its <title> lands first in <head> — so the title set here
 * was put back to the bare site name (the invite and connect pages both showed
 * "Moharrik"). The title is therefore re-asserted whenever <head> changes,
 * for as long as the page is mounted.
 */
export function useDocumentTitle(title: string | null | undefined) {
  useEffect(() => {
    const name = document.documentElement.lang === "ar" ? "محرّك" : "Moharrik"
    const want = title ? `${title} · ${name}` : name
    const apply = () => {
      if (document.title !== want) document.title = want
    }
    apply()
    const watch = new MutationObserver(apply)
    watch.observe(document.head, { childList: true, subtree: true, characterData: true })
    return () => watch.disconnect()
  }, [title])
}
