"use client"

/*
The Automation group used to be one long page at this URL (MH-526 split it
into Browsers / Workflows / Presets / Rules / Runs / Schedules / Limits &
settings). Browsers led that page, so an old bookmark or link lands there.

A client redirect, not next/navigation's server redirect(): this route sits
under the workspace shell's client layout boundary, where a thrown
server-side redirect does not turn into an HTTP redirect in this app's
Next.js/Turbopack setup (confirmed by direct testing — the response comes
back 200 with the page never actually moving). useRouter().replace() is the
reliable path already used everywhere else in this codebase for this shell.
*/

import { useParams, useRouter } from "next/navigation"
import { useEffect } from "react"

export default function AutomationIndexRedirect() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}/automation/browsers`)
  }, [router, locale, slug])
  return null
}
