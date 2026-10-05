"use client"

// MH-1148: the inbox is a dock now (MH-1147); this page moved to Settings. A client
// redirect for the same reason as inbox/email/page.tsx.

import { useParams, useRouter } from "next/navigation"
import { useEffect } from "react"

export default function MovedToSettings() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}/settings/canned`)
  }, [router, locale, slug])
  return null
}
