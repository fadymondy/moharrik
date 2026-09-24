"use client"

/*
Research used to be one page with in-page tabs at ?tab=runs|evidence|schedules
(MH-526 split it into its own sidebar item per tab). The tab, if any, maps
straight across; no tab meant "runs", the tab that led the old page.

A client redirect, not next/navigation's server redirect() — see
automation/page.tsx's note: a thrown server redirect does not turn into an
HTTP redirect under this route's client layout boundary in this app's
Next.js/Turbopack setup.
*/

import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect } from "react"

export default function ResearchIndexRedirect() {
  return (
    <Suspense>
      <Redirecting />
    </Suspense>
  )
}

function Redirecting() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  const params = useSearchParams()
  useEffect(() => {
    const tab = params.get("tab")
    const dest = tab === "evidence" ? "evidence" : tab === "schedules" ? "schedules" : "runs"
    router.replace(`/${locale}/w/${slug}/research/${dest}`)
  }, [router, locale, slug, params])
  return null
}
