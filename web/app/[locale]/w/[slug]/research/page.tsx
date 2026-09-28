"use client"

/*
The Research section's own page (MH-673). Research used to be one page with
in-page tabs at ?tab=runs|evidence|schedules (MH-526 split each tab into its
own sidebar item), so an old link that names a tab still goes to that tab.
Without one it is the section's dashboard.

A client redirect, not next/navigation's server redirect(): a thrown server
redirect does not turn into an HTTP redirect under this route's client layout
boundary in this app's Next.js/Turbopack setup.
*/

import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect } from "react"

import { SectionHome } from "@/components/workspace/section-home"

export default function ResearchSectionPage() {
  return (
    <Suspense>
      <ResearchHome />
    </Suspense>
  )
}

function ResearchHome() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  const tab = useSearchParams().get("tab")
  const dest =
    tab === "evidence" ? "evidence" : tab === "schedules" ? "schedules" : tab ? "runs" : null
  useEffect(() => {
    if (dest) router.replace(`/${locale}/w/${slug}/research/${dest}`)
  }, [router, locale, slug, dest])
  return dest ? null : <SectionHome section="research" />
}
