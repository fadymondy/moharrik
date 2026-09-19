"use client"

import { useEffect } from "react"

/** Every route sets its own document title: "<page> · Moharrik" (محرّك on /ar). */
export function useDocumentTitle(title: string | null | undefined) {
  useEffect(() => {
    const name = document.documentElement.lang === "ar" ? "محرّك" : "Moharrik"
    document.title = title ? `${title} · ${name}` : name
  }, [title])
}
